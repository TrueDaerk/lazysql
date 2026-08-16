package ui

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"lazysql/internal/config"
	"lazysql/internal/db"
	"lazysql/internal/secrets"
	"lazysql/internal/sshtunnel"
)

// dialTimeout bounds every connect/test attempt so a black-holed host cannot
// leave a tea.Cmd running forever.
const dialTimeout = 15 * time.Second

// ---------- connection status ----------

// connState is the per-connection UI state the [1] Connections panel colors
// itself by: green when connected, red after a failure, default when idle.
type connState struct {
	status  itemStatus
	lastErr string
}

// ---------- color tags ----------

// connTagColor resolves a connection profile's optional environment color
// tag ("red", "#ff8800", a 256-color index, …) via the same resolveColor
// [theme] uses. An empty or invalid value reports ok=false: the invalid
// case is already flagged once at startup by validateConnectionColors, so
// every other call site just treats it like "no tag" without erroring
// again.
func connTagColor(c config.Connection) (color.Color, bool) {
	if strings.TrimSpace(c.Color) == "" {
		return nil, false
	}
	col, err := resolveColor(c.Color)
	if err != nil {
		return nil, false
	}
	return col, true
}

// validateConnectionColors checks every profile's color tag at startup,
// returning one warning line per invalid value. Unlike [keys]/[theme] this
// never fails config load — an invalid tag just means no tag, same as an
// absent one — so the warnings are only ever logged, never returned as an
// error.
func validateConnectionColors(conns []config.Connection) []string {
	var warnings []string
	for _, c := range conns {
		if strings.TrimSpace(c.Color) == "" {
			continue
		}
		if _, err := resolveColor(c.Color); err != nil {
			warnings = append(warnings, fmt.Sprintf("connection %q: %v (no tag applied)", c.Name, err))
		}
	}
	return warnings
}

// activeTagColor is the live connection's color tag, if any.
func (m Model) activeTagColor() (color.Color, bool) {
	if m.active == "" {
		return nil, false
	}
	c, ok := m.cfg.Find(m.active)
	if !ok {
		return nil, false
	}
	return connTagColor(c)
}

// taggedConnName renders a connection name in bold plus its tag color, for
// extra salience in confirm modals guarding a destructive action against
// the wrong environment. A connection without a (valid) tag renders plain.
func (m Model) taggedConnName(name string) string {
	c, ok := m.cfg.Find(name)
	if !ok {
		return name
	}
	tc, ok := connTagColor(c)
	if !ok {
		return name
	}
	return lipgloss.NewStyle().Bold(true).Foreground(tc).Render(name)
}

// tagMarkerFor renders the ● environment marker for a connection name
// (trailing space included), or "" when it carries no (valid) color tag —
// the main view's counterpart to the panel [1] list marker.
func (m Model) tagMarkerFor(name string) string {
	c, ok := m.cfg.Find(name)
	if !ok {
		return ""
	}
	tc, ok := connTagColor(c)
	if !ok {
		return ""
	}
	return lipgloss.NewStyle().Foreground(tc).Render(tagMarker) + " "
}

// ---------- messages ----------

// connTestedMsg reports the outcome of `t` (test connection).
type connTestedMsg struct {
	name string
	dsn  string
	took time.Duration
	req  dialRequest
	err  error
}

// connectedMsg reports the outcome of `enter` (connect), carrying the live
// driver, the tunnel it runs through (nil when direct) and the namespaces it
// exposes.
type connectedMsg struct {
	name      string
	driver    db.Driver
	tunnel    *sshtunnel.Tunnel
	databases []string
	dsn       string
	req       dialRequest
	err       error
}

// connPersistedMsg reports the outcome of writing config.toml and the keyring
// entry after a create/edit/delete.
type connPersistedMsg struct {
	verb string // "save" / "remove"
	name string
	err  error
}

// ---------- password resolution ----------

// dialRequest is everything one dial attempt needs. It travels back in the
// result message so an attempt that stopped on a prompt — an unknown SSH host
// key, a missing key passphrase — can be repeated verbatim once the user has
// answered.
type dialRequest struct {
	conn config.Connection
	test bool

	// restore marks the one dial the startup session restore issues, so
	// the connectedMsg handler can route it into the restore chain
	// instead of the interactive connect flow.
	restore bool

	// form is the connection form a ctrl+t test was fired from, so the
	// connTestedMsg handler can write the result into that form (and only
	// while it is still the open modal) instead of the panel row status.
	form *formModal

	password    string
	hasPassword bool

	// sshSecret is the jump host password or key passphrase. It is only set
	// when a prompt supplied one; otherwise the keyring is consulted.
	sshSecret    string
	hasSSHSecret bool
}

// resolvePassword picks the password for a dial attempt: an explicitly
// prompted one wins, otherwise the keyring is consulted. A missing keyring
// entry is not an error — plenty of connections need no password.
func resolvePassword(c config.Connection, explicit string, hasExplicit bool) (string, error) {
	if !c.NeedsPassword() {
		return "", nil
	}
	if hasExplicit {
		return explicit, nil
	}
	if c.AskPassword {
		return "", nil
	}
	return keyringSecret(c.Name)
}

// resolveSSHSecret is resolvePassword for the jump host: the SSH password or
// the private key passphrase, kept in its own keyring slot.
func resolveSSHSecret(req dialRequest) (string, error) {
	if !req.conn.NeedsSSHSecret() {
		return "", nil
	}
	if req.hasSSHSecret {
		return req.sshSecret, nil
	}
	return keyringSecret(secrets.SSHKey(req.conn.Name))
}

// keyringSecret reads one keyring entry, treating "nothing stored" and "no
// keyring on this machine" as an empty secret rather than a failure.
func keyringSecret(key string) (string, error) {
	pw, err := secrets.Get(key)
	switch {
	case err == nil:
		return pw, nil
	case errors.Is(err, secrets.ErrNotFound), errors.Is(err, secrets.ErrUnsupported):
		return "", nil
	default:
		return "", err
	}
}

// ---------- commands ----------

// testConnCmd dials and immediately closes. All work happens off the update
// loop; the result comes back as a message.
func testConnCmd(req dialRequest) tea.Cmd {
	req.test = true
	return func() tea.Msg {
		start := time.Now()
		drv, tunnel, dsn, err := dial(req)
		if drv != nil {
			drv.Close()
		}
		if tunnel != nil {
			tunnel.Close()
		}
		return connTestedMsg{name: req.conn.Name, dsn: dsn, took: time.Since(start), req: req, err: err}
	}
}

// connectCmd dials and, on success, lists the namespaces so the [2] Databases
// panel can be filled in the same round trip.
func connectCmd(req dialRequest) tea.Cmd {
	req.test = false
	return func() tea.Msg {
		drv, tunnel, dsn, err := dial(req)
		if err != nil {
			return connectedMsg{name: req.conn.Name, dsn: dsn, req: req, err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
		defer cancel()
		dbs, err := drv.ListDatabases(ctx)
		if err != nil {
			drv.Close()
			if tunnel != nil {
				tunnel.Close()
			}
			return connectedMsg{name: req.conn.Name, dsn: dsn, req: req, err: err}
		}
		return connectedMsg{
			name: req.conn.Name, driver: drv, tunnel: tunnel,
			databases: dbs, dsn: dsn, req: req,
		}
	}
}

// dial resolves the secrets, opens the SSH tunnel when the profile asks for
// one, builds the DSN and opens the driver. The returned DSN is always
// redacted — it ends up in the command log. On any failure past the tunnel,
// the tunnel is closed here: a caller only ever receives one it must close.
func dial(req dialRequest) (db.Driver, *sshtunnel.Tunnel, string, error) {
	c := req.conn
	password, err := resolvePassword(c, req.password, req.hasPassword)
	if err != nil {
		return nil, nil, "", err
	}
	params := c.Params(password)
	redacted, err := db.RedactDSN(c.Engine, params)
	if err != nil {
		return nil, nil, "", err
	}
	dsn, err := db.BuildDSN(c.Engine, params)
	if err != nil {
		return nil, nil, redacted, err
	}

	var (
		tunnel *sshtunnel.Tunnel
		dialFn db.DialFunc
	)
	if c.UsesSSH() {
		tunnel, err = openTunnel(req)
		if err != nil {
			return nil, nil, redacted, err
		}
		dialFn = tunnel.DialContext
		redacted += " " + tunnelSuffix(c)
	}

	drv, err := db.OpenOpts(c.Engine, db.Options{Dial: dialFn, ReadOnly: c.ReadOnly})
	if err != nil {
		closeTunnel(tunnel)
		return nil, nil, redacted, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()
	if err := drv.Connect(ctx, dsn); err != nil {
		drv.Close()
		closeTunnel(tunnel)
		return nil, nil, redacted, err
	}
	return drv, tunnel, redacted, nil
}

// openTunnel builds the transport config and dials the jump host.
func openTunnel(req dialRequest) (*sshtunnel.Tunnel, error) {
	secret, err := resolveSSHSecret(req)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()
	return sshtunnel.Open(ctx, req.conn.TunnelConfig(secret))
}

func closeTunnel(t *sshtunnel.Tunnel) {
	if t != nil {
		t.Close()
	}
}

// closeSessionCmd tears a connection down off the update loop: the driver
// first, so its handles are gone before the transport under them is, then the
// tunnel that carried it.
func closeSessionCmd(d db.Driver, t *sshtunnel.Tunnel) tea.Cmd {
	if d == nil && t == nil {
		return nil
	}
	return func() tea.Msg {
		if d != nil {
			d.Close()
		}
		closeTunnel(t)
		return nil
	}
}

// formSecrets is what the connection form wants written to the keyring. A
// secret is only touched when its field was typed into: an untouched password
// leaves the stored one alone.
type formSecrets struct {
	password    string
	setPassword bool
	ssh         string
	setSSH      bool
}

// persistCmd writes the config file and, when the form supplied them, the
// keyring entries. The config is cloned by the caller so the goroutine never
// races the model.
func persistCmd(cfg *config.Config, oldName, name string, sec formSecrets) tea.Cmd {
	return func() tea.Msg {
		fail := func(err error) tea.Msg {
			return connPersistedMsg{verb: "save", name: name, err: err}
		}
		if oldName != "" && oldName != name {
			if err := secrets.Rename(oldName, name); err != nil && !errors.Is(err, secrets.ErrUnsupported) {
				return fail(err)
			}
		}
		if sec.setPassword {
			if err := storeSecret(name, sec.password); err != nil {
				return fail(err)
			}
		}
		if sec.setSSH {
			if err := storeSecret(secrets.SSHKey(name), sec.ssh); err != nil {
				return fail(err)
			}
		}
		if err := cfg.Save(); err != nil {
			return fail(err)
		}
		return connPersistedMsg{verb: "save", name: name}
	}
}

// storeSecret writes a keyring entry, or removes it when the value is empty.
func storeSecret(key, value string) error {
	if value == "" {
		return secrets.Delete(key)
	}
	return secrets.Set(key, value)
}

// forgetCmd removes a deleted connection's config entry and both of its
// keyring secrets together, so no orphan password or passphrase survives.
func forgetCmd(cfg *config.Config, name string) tea.Cmd {
	return func() tea.Msg {
		if err := secrets.Forget(name); err != nil && !errors.Is(err, secrets.ErrUnsupported) {
			return connPersistedMsg{verb: "remove", name: name, err: err}
		}
		if err := cfg.Save(); err != nil {
			return connPersistedMsg{verb: "remove", name: name, err: err}
		}
		return connPersistedMsg{verb: "remove", name: name}
	}
}

// reorderCmd persists the config file after moving a connection up/down in
// panel [1]. Only the order changes — no secret is touched — so this is
// just cfg.Save(), the same whole-file rewrite add/edit already use.
func reorderCmd(cfg *config.Config, name string) tea.Cmd {
	return func() tea.Msg {
		if err := cfg.Save(); err != nil {
			return connPersistedMsg{verb: "reorder", name: name, err: err}
		}
		return connPersistedMsg{verb: "reorder", name: name}
	}
}

// ---------- options encoding ----------

// formatOptions renders driver options as the `k=v, k=v` text the form edits.
func formatOptions(opts map[string]string) string {
	if len(opts) == 0 {
		return ""
	}
	keys := make([]string, 0, len(opts))
	for k := range opts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+opts[k])
	}
	return strings.Join(parts, ", ")
}

// parseOptions is the inverse of formatOptions.
func parseOptions(s string) (map[string]string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	out := map[string]string{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			return nil, fmt.Errorf("option %q must be key=value", part)
		}
		out[k] = strings.TrimSpace(v)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// ---------- the connection form ----------

// isFileEngine is the visibility predicate shared by every field that only
// applies to one family of engines.
func isFileEngine(f *formModal) bool {
	return db.FileBased(db.Engine(f.rawValue("engine")))
}

func isServerEngine(f *formModal) bool { return !isFileEngine(f) }

// namedTagColors are the picker's fixed choices — the six colors the issue
// calls out by name. Anything else (another ANSI name, a 256-color index,
// a hex value) goes through the "custom" choice's free-text field instead.
var namedTagColors = []string{"red", "yellow", "green", "blue", "magenta", "cyan"}

// isCustomColor reports whether the picker should show its "custom" choice
// selected, i.e. the stored value is neither empty nor one of namedTagColors.
func isCustomColor(color string) bool {
	if color == "" {
		return false
	}
	for _, n := range namedTagColors {
		if color == n {
			return false
		}
	}
	return true
}

// colorFormFields builds the "Color tag" picker: a select field over
// none/the six named colors/custom, plus a text field that only shows up
// for "custom" and carries the raw value (any string resolveColor accepts —
// another ANSI name, a 256-color index, or hex).
func colorFormFields(c config.Connection) []*formField {
	values := append([]string{""}, namedTagColors...)
	values = append(values, "custom")
	labels := append([]string{"none"}, namedTagColors...)
	labels = append(labels, "custom…")

	selected, custom := c.Color, ""
	if isCustomColor(c.Color) {
		selected, custom = "custom", c.Color
	}

	return []*formField{
		newSelectField("color", "Color tag", labels, values, selected).
			withHelp("environment tag: panel [1] marker + main view border"),
		newTextField("color_hex", "Custom color", custom, "#ff8800 or an ANSI name/index").
			withVisible(func(f *formModal) bool { return f.rawValue("color") == "custom" }).
			withValidate(func(_ *formModal, v string) string {
				if v == "" {
					return ""
				}
				if _, err := resolveColor(v); err != nil {
					return err.Error()
				}
				return ""
			}),
	}
}

// portHint is the port field's placeholder: the number the engine defaults
// to, so leaving the field empty is a visible choice rather than a guess.
func portHint(e db.Engine) string {
	if p := db.DefaultPort(e); p > 0 {
		return strconv.Itoa(p)
	}
	return "default"
}

// userHint is the user field's placeholder — the account the engine ships
// with, as a starting point, not a default the form fills in.
func userHint(e db.Engine) string {
	switch e {
	case db.EngineMySQL, db.EngineMariaDB:
		return "e.g. root"
	case db.EnginePostgres:
		return "e.g. postgres"
	}
	return ""
}

// connDraft is everything the user has typed into a connection form so
// far: the profile plus the raw, possibly mid-edit texts that do not
// round-trip through config.Connection (a half-typed port, an invalid
// options string) and the secrets a rebuild must not lose. It is what the
// engine picker and the form hand each other, so switching engines never
// discards input.
type connDraft struct {
	conn      config.Connection
	port      string
	options   string
	sshPort   string
	password  string
	sshSecret string
}

// draftFrom seeds a draft from a stored profile — the edit path.
func draftFrom(c config.Connection) connDraft {
	d := connDraft{conn: c, options: formatOptions(c.Options)}
	if c.Port > 0 {
		d.port = strconv.Itoa(c.Port)
	}
	if c.SSH != nil && c.SSH.Port > 0 {
		d.sshPort = strconv.Itoa(c.SSH.Port)
	}
	return d
}

// snapshotDraft reads the form back into a draft, starting from base so
// values this engine's form does not carry (a typed host while a file
// engine is up) survive a round trip through the picker.
func (f *formModal) snapshotDraft(base connDraft) connDraft {
	d := base
	c := &d.conn
	c.Engine = db.Engine(f.rawValue("engine"))
	if fl := f.field("name"); fl != nil {
		c.Name = fl.value()
	}
	if fl := f.field("host"); fl != nil {
		c.Host = fl.value()
	}
	if fl := f.field("user"); fl != nil {
		c.User = fl.value()
	}
	if fl := f.field("database"); fl != nil {
		c.Database = fl.value()
	}
	if fl := f.field("file"); fl != nil {
		c.File = fl.value()
	}
	if fl := f.field("ask"); fl != nil {
		c.AskPassword = fl.value() == "true"
	}
	if fl := f.field("read_only"); fl != nil {
		c.ReadOnly = fl.value() == "true"
	}
	if fl := f.field("port"); fl != nil {
		d.port = fl.value()
	}
	if fl := f.field("options"); fl != nil {
		d.options = fl.value()
	}
	if fl := f.field("password"); fl != nil {
		d.password = fl.value()
	}
	if fl := f.field("color"); fl != nil {
		color := fl.value()
		if color == "custom" {
			color = strings.TrimSpace(f.rawValue("color_hex"))
		}
		c.Color = color
	}
	if fl := f.field("ssh"); fl != nil {
		s := config.SSH{
			Enabled: fl.value() == "true",
			Host:    strings.TrimSpace(f.rawValue("ssh_host")),
			User:    strings.TrimSpace(f.rawValue("ssh_user")),
			Auth:    f.rawValue("ssh_auth"),
			KeyFile: strings.TrimSpace(f.rawValue("ssh_key")),
		}
		d.sshPort = f.rawValue("ssh_port")
		d.sshSecret = f.rawValue("ssh_secret")
		if s.Enabled || s.Host != "" || s.User != "" || s.KeyFile != "" || d.sshPort != "" {
			c.SSH = &s
		} else {
			c.SSH = nil
		}
	}
	return d
}

// newConnectionWizard opens step one of the create flow: the engine
// picker. Picking an engine builds that engine's form; esc here cancels
// the flow.
func newConnectionWizard() *enginePickerModal {
	var d connDraft
	return newEnginePicker("New connection", "", func(m *Model, e db.Engine) {
		d.conn.Engine = e
		m.modal = newConnectionFormDraft("New connection", d, "")
	})
}

// changeEnginePicker is the picker reopened from inside a form (ctrl+e, or
// esc while creating): it carries the draft, preselects the current engine
// and rebuilds the form on pick. backToForm keeps esc an excursion — the
// ctrl+e case returns to the form, while the create-flow esc case falls
// through to cancelling.
func changeEnginePicker(title string, d connDraft, oldName string, backToForm bool) *enginePickerModal {
	p := newEnginePicker(title, d.conn.Engine, func(m *Model, e db.Engine) {
		d.conn.Engine = e
		m.modal = newConnectionFormDraft(title, d, oldName)
	})
	if backToForm {
		p.onBack = func(m *Model) {
			m.modal = newConnectionFormDraft(title, d, oldName)
		}
	}
	return p
}

// newConnectionForm builds the create/edit popup for the profile's engine.
// oldName is empty when creating; otherwise it is the profile being
// replaced.
func newConnectionForm(title string, c config.Connection, oldName string) *formModal {
	return newConnectionFormDraft(title, draftFrom(c), oldName)
}

// newConnectionFormDraft is step two of the connection flow: the form for
// exactly one engine. The engine is fixed (a hidden field carries it for
// the predicates and toConnection); ctrl+e reopens the picker with the
// draft, and while creating, esc backs out to the picker rather than
// throwing typed values away.
func newConnectionFormDraft(title string, d connDraft, oldName string) *formModal {
	c := d.conn
	engine := c.Engine
	if engine == "" {
		if ch := engineChoices(); len(ch) > 0 {
			engine = ch[0].engine
		}
	}
	create := oldName == ""
	display := string(engine)
	if dl, err := db.DialectFor(engine); err == nil {
		display = dl.DisplayName()
	}
	fileEngine := db.FileBased(engine)

	fields := []*formField{newHiddenField("engine", string(engine))}

	nameField := newTextField("name", "Name", c.Name, "my-database")
	if fileEngine {
		// The file's own name is the obvious profile name, so it is the
		// default: submit derives it (toConnection), and only a profile
		// with neither a name nor a file has nothing to go by.
		nameField.input.Placeholder = "empty = named after the file"
		nameField.withValidate(func(f *formModal, v string) string {
			if v == "" && f.rawValue("file") == "" {
				return "name is required"
			}
			return ""
		})
	} else {
		nameField.withValidate(requiredField("name"))
	}
	fields = append(fields, nameField.withSection("Profile"))
	for _, fl := range colorFormFields(c) {
		fields = append(fields, fl.withSection("Profile"))
	}

	if fileEngine {
		file := newTextField("file", "File", c.File, "path/to/db").
			withSection("Storage").
			withSuggest()
		if engine == db.EngineSQLite {
			file.withValidate(requiredField("file path"))
		} else {
			file.withHelp("empty = in-memory")
		}
		fields = append(fields, file)
	} else {
		host := c.Host
		if create && host == "" {
			// The common case is the dev database on this machine, so
			// localhost is filled in as a real value — host is required
			// anyway, and a prefill the cursor never visits is the
			// cheapest field there is. Anything else overtypes it.
			host = "localhost"
		}
		fields = append(fields,
			newTextField("host", "Host", host, "db.example.com").
				withSection("Server").
				withHelp("or an absolute path for a unix socket").
				withValidate(requiredField("host")),
			newTextField("port", "Port", d.port, portHint(engine)).
				withSection("Server").
				withHelp("empty = the engine's default").
				withValidate(validPort),
			newTextField("database", "Database", c.Database, "optional").
				withSection("Server"),
			newTextField("user", "User", c.User, userHint(engine)).
				withSection("Credentials"),
			newPasswordField("password", "Password", d.password, passwordPlaceholder(c, oldName)).
				withSection("Credentials").
				withHelp("stored in the OS keyring, never in config.toml"),
			newBoolField("ask", "Ask on connect", c.AskPassword).
				withSection("Credentials").
				withHelp("prompt instead of using the keyring"),
		)
		for _, fl := range sshFields(d, oldName) {
			fields = append(fields, fl.withSection("SSH tunnel"))
		}
	}

	// A server engine's example option is the one people actually reach
	// for; a file engine gets the neutral spelling.
	optionsHint := "sslmode=disable, k=v"
	if fileEngine {
		optionsHint = "key=value, key=value"
	}
	fields = append(fields,
		newTextField("options", "Options", d.options, optionsHint).
			withSection("Advanced").
			withValidate(func(_ *formModal, v string) string {
				if _, err := parseOptions(v); err != nil {
					return err.Error()
				}
				return ""
			}),
		newBoolField("read_only", "Read-only", c.ReadOnly).
			withSection("Advanced").
			withHelp("block all writes on this connection"),
	)

	fm := newFormModal(title+" · "+display, fields, func(m *Model, f *formModal) (bool, tea.Cmd) {
		conn, sec, err := f.toConnection()
		if err != nil {
			f.err = err.Error()
			return false, nil
		}
		if err := m.cfg.Upsert(oldName, conn); err != nil {
			f.err = err.Error()
			return false, nil
		}
		m.renameConnState(oldName, conn.Name)
		m.refreshConnections(conn.Name)
		return true, tea.Batch(
			persistCmd(m.cfg.Clone(), oldName, conn.Name, sec),
			logCmd("-- save connection %s (%s)", conn.Name, conn.Engine),
		)
	})
	if create && fileEngine {
		// The path is the one thing a file profile needs — the name can
		// derive from it — so that is where the cursor opens.
		fm.focusField("file")
	}
	fm.footer = "tab/↑↓ field · ctrl+t test · ctrl+e engine · enter save · esc cancel"
	if create {
		// While creating, esc retreats one step — back to the engine
		// picker, draft intact — and esc there cancels the flow. An edit
		// has no step one to go back to.
		fm.footer = "tab/↑↓ field · ctrl+t test · ctrl+e engine · enter save · esc back"
		fm.onCancel = func(m *Model) {
			m.modal = changeEnginePicker(title, fm.snapshotDraft(d), oldName, false)
		}
	}
	fm.bar = func(k keyMap) []key.Binding { return k.connFormKeys() }
	fm.onKey = func(m *Model, f *formModal, key string) (bool, tea.Cmd) {
		switch key {
		case "ctrl+e":
			m.modal = changeEnginePicker(title, f.snapshotDraft(d), oldName, true)
			return true, nil
		case "ctrl+t":
			f.err, f.info = "", ""
			conn, sec, err := f.toConnection()
			if err != nil {
				f.err = err.Error()
				return true, nil
			}
			// The test dials exactly what the form shows, without persisting
			// anything. Untouched secret fields fall back to the keyring entries
			// of the profile being edited — looked up under oldName, because the
			// form may be renaming it.
			req := dialRequest{conn: conn, form: f}
			if sec.setPassword {
				req.password, req.hasPassword = sec.password, true
			} else if oldName != "" {
				if pw, err := keyringSecret(oldName); err == nil && pw != "" {
					req.password, req.hasPassword = pw, true
				}
			}
			if sec.setSSH {
				req.sshSecret, req.hasSSHSecret = sec.ssh, true
			} else if oldName != "" {
				if s, err := keyringSecret(secrets.SSHKey(oldName)); err == nil && s != "" {
					req.sshSecret, req.hasSSHSecret = s, true
				}
			}
			f.info = "testing …"
			return true, testConnCmd(req)
		}
		return false, nil
	}
	return fm
}

func passwordPlaceholder(c config.Connection, oldName string) string {
	if oldName != "" {
		return "unchanged"
	}
	return ""
}

// toConnection validates the form and turns it into a profile plus the
// secrets to file in the keyring.
func (f *formModal) toConnection() (config.Connection, formSecrets, error) {
	var sec formSecrets
	engine := db.Engine(f.rawValue("engine"))
	c := config.Connection{
		Name:   strings.TrimSpace(f.rawValue("name")),
		Engine: engine,
	}
	if db.FileBased(engine) {
		c.File = f.value("file")
		// An unnamed file profile borrows the file's own name — the same
		// default the Name field's placeholder promises. Derive it before
		// resolving to an absolute path: the basename is the same either
		// way, and the placeholder promise is about what was typed.
		if c.Name == "" && c.File != "" {
			base := filepath.Base(c.File)
			c.Name = strings.TrimSuffix(base, filepath.Ext(base))
		}
		// Persist an absolute path so the connection keeps resolving to the
		// same file regardless of lazysql's working directory at a later
		// start — a relative path only worked by accident of cwd.
		if c.File != "" {
			abs, err := config.ResolveFilePath(c.File)
			if err != nil {
				return c, sec, err
			}
			c.File = abs
		}
	} else {
		c.Host = f.value("host")
		c.User = f.value("user")
		c.Database = f.value("database")
		c.AskPassword = f.value("ask") == "true"
		if p := f.value("port"); p != "" {
			n, err := strconv.Atoi(p)
			if err != nil {
				return c, sec, fmt.Errorf("port %q is not a number", p)
			}
			if n < 1 || n > 65535 {
				return c, sec, fmt.Errorf("port %d out of range 1-65535", n)
			}
			c.Port = n
		} else {
			c.Port = db.DefaultPort(engine)
		}
		ssh, err := f.toSSH()
		if err != nil {
			return c, sec, err
		}
		c.SSH = ssh
	}
	opts, err := parseOptions(f.rawValue("options"))
	if err != nil {
		return c, sec, err
	}
	c.Options = opts
	c.ReadOnly = f.value("read_only") == "true"
	color := f.value("color")
	if color == "custom" {
		color = strings.TrimSpace(f.value("color_hex"))
	}
	if color != "" {
		if _, err := resolveColor(color); err != nil {
			return c, sec, fmt.Errorf("color: %w", err)
		}
	}
	c.Color = color
	if err := c.Validate(); err != nil {
		return c, sec, err
	}
	// An untouched secret field leaves its keyring entry alone.
	sec.password = f.value("password")
	sec.setPassword = sec.password != ""
	sec.ssh = f.value("ssh_secret")
	sec.setSSH = sec.ssh != ""
	return c, sec, nil
}

// newSecretPrompt is the one-field popup every dial-time secret goes through:
// the "ask on connect" database password and the SSH password/passphrase
// alike. The typed value is handed straight to the dial command and never
// stored.
func newSecretPrompt(title string, then func(secret string) tea.Cmd) *formModal {
	fields := []*formField{
		newPasswordField("secret", "Password", "", ""),
	}
	f := newFormModal(title, fields, func(m *Model, f *formModal) (bool, tea.Cmd) {
		return true, then(f.rawValue("secret"))
	})
	f.footer = "enter connect · esc cancel"
	return f
}

// newPasswordPrompt is the "ask on connect" fallback for the database
// password.
func newPasswordPrompt(c config.Connection, then func(pw string) tea.Cmd) *formModal {
	return newSecretPrompt(fmt.Sprintf("Password for %s", c.Name), then)
}
