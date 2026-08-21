package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/config"
	"lazysql/internal/db"
)

// An ephemeral connection is a local file opened for this run only:
// `lazysql mydb.db` on the command line, or `o` in panel [1]. Nothing about
// it is written to config.toml, no keyring entry belongs to it and no
// session restore brings it back — it lives in this one Model and
// disappears with the disconnect. See
// wiki/design/ephemeral-file-connections.md.

// ephemeralTag is what panel [1] appends to the ephemeral row, so a
// connection that is not in the config never looks like one that is.
const ephemeralTag = "(ephemeral)"

// ephemeralConn is the unsaved profile plus what it took to build it: the
// file it came from, what that file turned out to be, and the statements
// its session has to be prepared with (a Parquet file's view).
type ephemeralConn struct {
	conn   config.Connection
	format db.FileFormat
	path   string
	setup  []string
}

// newEphemeralConn resolves a file path into an unsaved connection
// profile. The file must exist and be recognizable — a driver that would
// happily create a fresh database at a typo'd path never gets the chance
// (see db.SniffFile). taken reports the names already on the panel, so an
// ephemeral file never collides with a saved profile of the same name.
func newEphemeralConn(path string, taken func(string) bool) (*ephemeralConn, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("no file given")
	}
	abs, err := config.ResolveFilePath(strings.TrimSpace(path))
	if err != nil {
		return nil, err
	}
	format, err := db.SniffFile(abs)
	if err != nil {
		return nil, err
	}
	base := filepath.Base(abs)
	e := &ephemeralConn{
		format: format,
		path:   abs,
		conn: config.Connection{
			Name:   uniqueEphemeralName(strings.TrimSuffix(base, filepath.Ext(base)), taken),
			Engine: format.Engine(),
			File:   abs,
		},
	}
	if format == db.FormatParquet {
		// A Parquet file is not a database: the session is an in-memory
		// DuckDB (hence no File) with the file exposed as a view, and it
		// is read-only because nothing could be written back through it.
		e.conn.File = ""
		e.conn.ReadOnly = true
		sql, err := db.ParquetViewSQL(db.ParquetViewName(abs), abs)
		if err != nil {
			return nil, err
		}
		e.setup = []string{sql}
	}
	return e, nil
}

// uniqueEphemeralName picks the first free display name for the panel:
// the file's own name, then "name (2)", "name (3)", … A name is only ever
// a label here — nothing keys config or keyring entries by it.
func uniqueEphemeralName(base string, taken func(string) bool) string {
	if base == "" {
		base = "file"
	}
	if taken == nil || !taken(base) {
		return base
	}
	for n := 2; ; n++ {
		name := fmt.Sprintf("%s (%d)", base, n)
		if !taken(name) {
			return name
		}
	}
}

// engineLabel is how the ephemeral row describes itself in the log and in
// the main view: the file's format rather than the engine, so a Parquet
// file does not claim to be a DuckDB database.
func (e *ephemeralConn) engineLabel() string { return string(e.format) }

// ---------- model integration ----------

// isEphemeral reports whether a panel [1] row is the unsaved one.
func (m Model) isEphemeral(name string) bool {
	return m.ephem != nil && name != "" && m.ephem.conn.Name == name
}

// findConn looks a connection up by panel name: the ephemeral entry first
// — it is not in the config and never will be — then the saved profiles.
// Every lookup that asks "what is the connection called X" goes through
// here; m.cfg.Find stays for the flows that are about the config file
// itself (the wizard's name collisions, the schema diff's other side).
func (m Model) findConn(name string) (config.Connection, bool) {
	if m.isEphemeral(name) {
		return m.ephem.conn, true
	}
	return m.cfg.Find(name)
}

// connNames is what panel [1] lists: the ephemeral connection first — it
// is this run's subject — then the saved profiles in config order.
func (m Model) connNames() []string {
	saved := m.cfg.Names()
	if m.ephem == nil {
		return saved
	}
	return append([]string{m.ephem.conn.Name}, saved...)
}

// nameTaken reports whether a name is already on the panel, for
// uniqueEphemeralName.
func (m Model) nameTaken(name string) bool {
	if m.isEphemeral(name) {
		return true
	}
	_, ok := m.cfg.Find(name)
	return ok
}

// dialRequestFor builds the dial for a connection, carrying the session
// setup an ephemeral Parquet connection needs. Every connect of a profile
// goes through it so a reconnect prepares the session exactly like the
// first connect did.
func (m Model) dialRequestFor(c config.Connection, test bool) dialRequest {
	req := dialRequest{conn: c, test: test}
	if m.isEphemeral(c.Name) {
		req.setup = m.ephem.setup
	}
	return req
}

// openEphemeral adopts a resolved file connection and connects to it. It
// is the one path both entry points take: the CLI's positional argument
// and `o` in panel [1].
func (m *Model) openEphemeral(e *ephemeralConn) tea.Cmd {
	// Only one ephemeral connection exists at a time: opening a second
	// file replaces the first, whose session (if live) is closed by the
	// connect that follows, exactly like switching saved profiles.
	if m.ephem != nil {
		delete(m.connState, m.ephem.conn.Name)
	}
	m.ephem = e
	m.refreshConnections(e.conn.Name)
	m.setConnStatus(e.conn.Name, statusPending, "")
	return tea.Batch(
		logCmd("-- open %s (%s, ephemeral)", e.path, e.engineLabel()),
		redialCmd(m.dialRequestFor(e.conn, false)),
	)
}

// dropEphemeral removes the unsaved entry from the panel, which is what a
// disconnect of it means: back to the plain saved-connections view.
func (m *Model) dropEphemeral() {
	if m.ephem == nil {
		return
	}
	delete(m.connState, m.ephem.conn.Name)
	m.ephem = nil
	m.refreshConnections("")
}

// OpenFileOnStart is the `lazysql <file>` entry point: it resolves the
// path before the TUI starts — a missing or unrecognizable file is a
// plain command-line error, not a modal over an empty screen — and marks
// the connection to dial from Init, so the connect itself reports through
// the normal UI.
func (m Model) OpenFileOnStart(path string) (Model, error) {
	e, err := newEphemeralConn(path, m.nameTaken)
	if err != nil {
		return m, err
	}
	m.ephem = e
	// A CLI-opened file is this run's subject: it takes over from any
	// session that would otherwise have been restored.
	m.restoreSess = nil
	m.refreshConnections(e.conn.Name)
	m.setConnStatus(e.conn.Name, statusPending, "")
	return m, nil
}

// ---------- the open-file prompt ----------

// newOpenFileModal is `o` on panel [1]: one path field with the same
// completion the connection form's file field has. Submitting opens the
// file; a bad path reports in the form instead of closing it.
func newOpenFileModal() *formModal {
	field := newTextField("file", "File", "", "path/to/db.sqlite, .duckdb or .parquet").
		withSuggest().
		withHelp("opened for this session only — nothing is saved to config.toml")
	f := newFormModal("Open file", []*formField{field}, func(m *Model, f *formModal) (bool, tea.Cmd) {
		e, err := newEphemeralConn(f.value("file"), m.nameTaken)
		if err != nil {
			f.err = err.Error()
			return false, nil
		}
		return true, m.openEphemeral(e)
	})
	f.footer = "tab complete · enter open · esc cancel"
	return f
}
