package ui

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

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

// newConnectionForm builds the create/edit popup. oldName is empty when
// creating; otherwise it is the profile being replaced.
func newConnectionForm(title string, c config.Connection, oldName string) *formModal {
	var labels, values []string
	for _, e := range db.Engines() {
		d, err := db.DialectFor(e)
		if err != nil {
			continue
		}
		labels = append(labels, d.DisplayName())
		values = append(values, string(e))
	}
	if c.Engine == "" && len(values) > 0 {
		c.Engine = db.Engine(values[0])
	}
	port := ""
	if c.Port > 0 {
		port = strconv.Itoa(c.Port)
	}

	fields := []*formField{
		newTextField("name", "Name", c.Name, "my-database"),
		newSelectField("engine", "Engine", labels, values, string(c.Engine)),
		newTextField("host", "Host", c.Host, "localhost").
			withHelp("or an absolute path for a unix socket").
			withVisible(isServerEngine),
		newTextField("port", "Port", port, "default").withVisible(isServerEngine),
		newTextField("user", "User", c.User, "").withVisible(isServerEngine),
		newTextField("database", "Database", c.Database, "").withVisible(isServerEngine),
		newTextField("file", "File", c.File, "path/to/db").
			withHelp("empty = in-memory (DuckDB)").
			withVisible(isFileEngine),
		newPasswordField("password", "Password", passwordPlaceholder(c, oldName)).
			withHelp("stored in the OS keyring, never in config.toml").
			withVisible(isServerEngine),
		newBoolField("ask", "Ask on connect", c.AskPassword).
			withHelp("prompt instead of using the keyring").
			withVisible(isServerEngine),
		newTextField("options", "Options", formatOptions(c.Options), "sslmode=disable, k=v"),
		// Read-only applies to every engine, so it sits outside the
		// server-only block above.
		newBoolField("read_only", "Read-only", c.ReadOnly).
			withHelp("block all writes on this connection"),
	}
	fields = append(fields, sshFields(c, oldName)...)

	return newFormModal(title, fields, func(m *Model, f *formModal) (bool, tea.Cmd) {
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
		newPasswordField("secret", "Password", ""),
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
