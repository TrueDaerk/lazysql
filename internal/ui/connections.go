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
	err  error
}

// connectedMsg reports the outcome of `enter` (connect), carrying the live
// driver and the namespaces it exposes.
type connectedMsg struct {
	name      string
	driver    db.Driver
	databases []string
	dsn       string
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
	pw, err := secrets.Get(c.Name)
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
func testConnCmd(c config.Connection, explicit string, hasExplicit bool) tea.Cmd {
	return func() tea.Msg {
		start := time.Now()
		drv, dsn, err := dial(c, explicit, hasExplicit)
		if drv != nil {
			drv.Close()
		}
		return connTestedMsg{name: c.Name, dsn: dsn, took: time.Since(start), err: err}
	}
}

// connectCmd dials and, on success, lists the namespaces so the [2] Databases
// panel can be filled in the same round trip.
func connectCmd(c config.Connection, explicit string, hasExplicit bool) tea.Cmd {
	return func() tea.Msg {
		drv, dsn, err := dial(c, explicit, hasExplicit)
		if err != nil {
			return connectedMsg{name: c.Name, dsn: dsn, err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
		defer cancel()
		dbs, err := drv.ListDatabases(ctx)
		if err != nil {
			drv.Close()
			return connectedMsg{name: c.Name, dsn: dsn, err: err}
		}
		return connectedMsg{name: c.Name, driver: drv, databases: dbs, dsn: dsn}
	}
}

// dial resolves the password, builds the DSN and opens the driver. The
// returned DSN is always redacted — it ends up in the command log.
func dial(c config.Connection, explicit string, hasExplicit bool) (db.Driver, string, error) {
	password, err := resolvePassword(c, explicit, hasExplicit)
	if err != nil {
		return nil, "", err
	}
	params := c.Params(password)
	redacted, err := db.RedactDSN(c.Engine, params)
	if err != nil {
		return nil, "", err
	}
	dsn, err := db.BuildDSN(c.Engine, params)
	if err != nil {
		return nil, redacted, err
	}
	drv, err := db.Open(c.Engine)
	if err != nil {
		return nil, redacted, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()
	if err := drv.Connect(ctx, dsn); err != nil {
		drv.Close()
		return nil, redacted, err
	}
	return drv, redacted, nil
}

// closeDriverCmd closes a superseded driver without blocking Update.
func closeDriverCmd(d db.Driver) tea.Cmd {
	if d == nil {
		return nil
	}
	return func() tea.Msg {
		d.Close()
		return nil
	}
}

// persistCmd writes the config file and, when the form supplied one, the
// keyring entry. The config is cloned by the caller so the goroutine never
// races the model.
func persistCmd(cfg *config.Config, oldName, name, password string, setPassword bool) tea.Cmd {
	return func() tea.Msg {
		if oldName != "" && oldName != name {
			if err := secrets.Rename(oldName, name); err != nil && !errors.Is(err, secrets.ErrUnsupported) {
				return connPersistedMsg{verb: "save", name: name, err: err}
			}
		}
		if setPassword {
			var err error
			if password == "" {
				err = secrets.Delete(name)
			} else {
				err = secrets.Set(name, password)
			}
			if err != nil {
				return connPersistedMsg{verb: "save", name: name, err: err}
			}
		}
		if err := cfg.Save(); err != nil {
			return connPersistedMsg{verb: "save", name: name, err: err}
		}
		return connPersistedMsg{verb: "save", name: name}
	}
}

// forgetCmd removes a deleted connection's config entry and its keyring
// secret together, so no orphan password survives the delete.
func forgetCmd(cfg *config.Config, name string) tea.Cmd {
	return func() tea.Msg {
		if err := secrets.Delete(name); err != nil && !errors.Is(err, secrets.ErrUnsupported) {
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
	}

	return newFormModal(title, fields, func(m *Model, f *formModal) (bool, tea.Cmd) {
		conn, password, setPassword, err := f.toConnection()
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
			persistCmd(m.cfg.Clone(), oldName, conn.Name, password, setPassword),
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
// password to file in the keyring.
func (f *formModal) toConnection() (config.Connection, string, bool, error) {
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
				return c, "", false, fmt.Errorf("port %q is not a number", p)
			}
			if n < 1 || n > 65535 {
				return c, "", false, fmt.Errorf("port %d out of range 1-65535", n)
			}
			c.Port = n
		} else {
			c.Port = db.DefaultPort(engine)
		}
	}
	opts, err := parseOptions(f.rawValue("options"))
	if err != nil {
		return c, "", false, err
	}
	c.Options = opts
	if err := c.Validate(); err != nil {
		return c, "", false, err
	}
	// An untouched password field leaves the keyring entry alone.
	password := f.value("password")
	return c, password, password != "", nil
}

// newPasswordPrompt is the "ask on connect" fallback: a one-field form that
// hands the typed password straight to the dial command.
func newPasswordPrompt(c config.Connection, then func(pw string) tea.Cmd) *formModal {
	fields := []*formField{
		newPasswordField("password", "Password", ""),
	}
	f := newFormModal(fmt.Sprintf("Password for %s", c.Name), fields, func(m *Model, f *formModal) (bool, tea.Cmd) {
		return true, then(f.rawValue("password"))
	})
	f.footer = "enter connect · esc cancel"
	return f
}
