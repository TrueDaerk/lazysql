package ui

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/config"
	"lazysql/internal/db"
	"lazysql/internal/session"
)

// sqliteFile writes a real, non-empty SQLite database and returns its
// path — the file both entry points are pointed at.
func sqliteFile(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	drv, err := db.Open(db.EngineSQLite)
	if err != nil {
		t.Fatal(err)
	}
	if err := drv.Connect(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	if _, err := drv.Exec(context.Background(), "CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT)"); err != nil {
		t.Fatal(err)
	}
	drv.Close()
	return path
}

// parquetFile writes a real Parquet file through DuckDB.
func parquetFile(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	drv, err := db.Open(db.EngineDuckDB)
	if err != nil {
		t.Fatal(err)
	}
	defer drv.Close()
	if err := drv.Connect(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	_, err = drv.Exec(context.Background(),
		"COPY (SELECT 1 AS id, 'ada' AS name) TO '"+path+"' (FORMAT PARQUET)")
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// The CLI's `lazysql <file>`: the file becomes the panel's connected entry
// and nothing about it reaches config.toml.
func TestOpenFileOnStartConnectsAndPersistsNothing(t *testing.T) {
	path := sqliteFile(t, "notes.sqlite")
	m := sized(120, 40)
	m, err := m.OpenFileOnStart(path)
	if err != nil {
		t.Fatalf("OpenFileOnStart: %v", err)
	}
	m = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	for _, msg := range drain(m.Init()) {
		m = send(t, m, msg)
	}

	if m.active != "notes" {
		t.Fatalf("active = %q, want notes", m.active)
	}
	if m.driver == nil {
		t.Fatal("no live driver after opening a file")
	}
	if got := m.panels[panelConnections].items; !slices.Contains(got, "notes") {
		t.Fatalf("connections panel = %v, want to contain the ephemeral entry", got)
	}
	if _, ok := m.cfg.Find("notes"); ok {
		t.Fatal("the ephemeral connection was added to the config")
	}
	// The panel row says so, and so does the main view.
	if got := m.panels[panelConnections].suffix["notes"]; got != ephemeralTag {
		t.Fatalf("panel suffix = %q, want %q", got, ephemeralTag)
	}
	m = send(t, m, press('1'))
	if view := m.connectionDetail(80, 20); !strings.Contains(view, ephemeralTag) {
		t.Fatalf("connection detail does not mark the entry ephemeral:\n%s", view)
	}
	// Nothing lands on disk either: config.Save is never called, so the
	// file the temp XDG home would hold does not exist.
	cfgPath, err := config.Path()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cfgPath); err == nil {
		saved, err := config.LoadFrom(cfgPath)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := saved.Find("notes"); ok {
			t.Fatal("the ephemeral connection was written to config.toml")
		}
	}
}

// A path that does not exist (or is not a database) fails before anything
// is opened — and, crucially, without creating the file the SQLite driver
// would happily have made.
func TestOpenFileOnStartRejectsBadPaths(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "nope.sqlite")
	garbage := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(garbage, []byte("just some text\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{missing, garbage} {
		m := sized(120, 40)
		got, err := m.OpenFileOnStart(path)
		if err == nil {
			t.Fatalf("OpenFileOnStart(%s) succeeded, want an error", path)
		}
		if got.ephem != nil {
			t.Fatalf("OpenFileOnStart(%s) left an ephemeral connection behind", path)
		}
	}
	if _, err := os.Stat(missing); err == nil {
		t.Fatal("the missing file was created by the open attempt")
	}
}

// `o` in panel [1] produces the same session as the CLI variant.
func TestOpenFileModalMatchesCLIVariant(t *testing.T) {
	path := sqliteFile(t, "notes.sqlite")

	cli := sized(120, 40)
	cli, err := cli.OpenFileOnStart(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range drain(cli.Init()) {
		cli = send(t, cli, msg)
	}

	tui := sized(120, 40)
	tui = send(t, tui, press('o'))
	form, ok := tui.modal.(*formModal)
	if !ok {
		t.Fatalf("modal = %T, want the open-file form", tui.modal)
	}
	form.field("file").input.SetValue(path)
	tui = send(t, tui, special(tea.KeyEnter, 0))

	if tui.modal != nil {
		t.Fatalf("modal still open after opening a file: %T", tui.modal)
	}
	if tui.active != cli.active {
		t.Fatalf("active = %q, want %q (the CLI variant's)", tui.active, cli.active)
	}
	if tui.ephem == nil || tui.ephem.path != cli.ephem.path {
		t.Fatalf("ephemeral path = %v, want %s", tui.ephem, cli.ephem.path)
	}
	if got, want := tui.panels[panelConnections].items, cli.panels[panelConnections].items; !slices.Equal(got, want) {
		t.Fatalf("connections panel = %v, want %v", got, want)
	}
	if got := tui.panels[panelObjects].items; len(got) == 0 {
		t.Fatal("objects panel is empty — the file was not browsed")
	}
}

// A bad path typed into the prompt reports in the form and leaves it open,
// rather than closing onto a half-open state.
func TestOpenFileModalReportsBadPath(t *testing.T) {
	m := sized(120, 40)
	m = send(t, m, press('o'))
	form := m.modal.(*formModal)
	form.field("file").input.SetValue(filepath.Join(t.TempDir(), "nope.sqlite"))
	m = send(t, m, special(tea.KeyEnter, 0))

	if m.modal != form {
		t.Fatalf("modal = %T, want the open-file form to stay open", m.modal)
	}
	if form.err == "" {
		t.Fatal("the form reports no error for a missing file")
	}
	if m.ephem != nil {
		t.Fatal("a failed open left an ephemeral connection behind")
	}
}

// Disconnecting the ephemeral connection drops it from the panel: the view
// is the plain saved-connections one again, with the app still running.
func TestDisconnectDropsEphemeralEntry(t *testing.T) {
	path := sqliteFile(t, "notes.sqlite")
	m := sized(120, 40)
	m, err := m.OpenFileOnStart(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range drain(m.Init()) {
		m = send(t, m, msg)
	}
	saved := m.cfg.Names()

	m = send(t, m, press('1'))
	m = send(t, m, press('x'))

	if m.ephem != nil {
		t.Fatal("the ephemeral connection survived its disconnect")
	}
	if m.active != "" || m.driver != nil {
		t.Fatalf("still connected after disconnect: active = %q", m.active)
	}
	if got := m.panels[panelConnections].items; !slices.Equal(got, saved) {
		t.Fatalf("connections panel = %v, want the saved list %v", got, saved)
	}
	if _, ok := m.connState["notes"]; ok {
		t.Fatal("the ephemeral connection's status outlived it")
	}
	if !logContains(m, "-- disconnect notes") {
		t.Fatalf("command log = %v", m.commandLog)
	}
}

// An ephemeral session is never restored on the next start: saveSession
// must leave whatever real session was recorded alone.
func TestEphemeralSessionIsNotSaved(t *testing.T) {
	path := sqliteFile(t, "notes.sqlite")
	m := sized(120, 40)
	m, err := m.OpenFileOnStart(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range drain(m.Init()) {
		m = send(t, m, msg)
	}
	if m.active != "notes" {
		t.Fatalf("active = %q, want notes", m.active)
	}

	sessPath, err := session.Path()
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(sessPath)
	t.Cleanup(func() { os.Remove(sessPath) })
	m.saveSession()
	if _, err := os.Stat(sessPath); err == nil {
		t.Fatal("an ephemeral session was written to the session file")
	}
}

// The profile-editing actions have nothing to act on for a connection that
// is not in the config: they say so instead of opening a form.
func TestEphemeralRejectsProfileActions(t *testing.T) {
	path := sqliteFile(t, "notes.sqlite")
	m := sized(120, 40)
	m, err := m.OpenFileOnStart(path)
	if err != nil {
		t.Fatal(err)
	}
	m = send(t, m, press('1'))
	m.panels[panelConnections].selectByName("notes")

	for _, key := range []rune{'e', 'd', 'y'} {
		next := send(t, m, press(key))
		if next.modal != nil {
			t.Fatalf("%c opened a %T for the ephemeral connection", key, next.modal)
		}
		if !logContains(next, "notes is ephemeral") {
			t.Fatalf("%c logged nothing about the ephemeral connection: %v", key, next.commandLog)
		}
	}
	// Reordering only moves saved profiles; the config order is untouched.
	before := m.cfg.Names()
	m = send(t, m, press('J'))
	if got := m.cfg.Names(); !slices.Equal(got, before) {
		t.Fatalf("config order = %v, want %v", got, before)
	}
}

// A Parquet file is browsable through the DuckDB view, and the session it
// is browsed in refuses writes, so nothing can be staged against it.
func TestOpenParquetFile(t *testing.T) {
	path := parquetFile(t, "sales.parquet")
	m := sized(120, 40)
	m, err := m.OpenFileOnStart(path)
	if err != nil {
		t.Fatalf("OpenFileOnStart: %v", err)
	}
	for _, msg := range drain(m.Init()) {
		m = send(t, m, msg)
	}

	if m.driver == nil {
		t.Fatal("no live driver after opening a Parquet file")
	}
	if got := m.driver.Engine(); got != db.EngineDuckDB {
		t.Fatalf("engine = %q, want duckdb", got)
	}
	if !m.readOnly() {
		t.Fatal("the Parquet session is not read-only")
	}
	// The view is in the objects tree, under Views.
	names := db.RelationNames(m.relations)
	if !slices.Contains(names, "sales") {
		t.Fatalf("relations = %v, want to contain the sales view", names)
	}
	if !logContains(m, "read_parquet(") {
		t.Fatalf("the view statement never reached the command log: %v", m.commandLogEntries())
	}
}

// A second file replaces the first: only one ephemeral connection exists.
func TestOpeningASecondFileReplacesTheFirst(t *testing.T) {
	first := sqliteFile(t, "one.sqlite")
	second := sqliteFile(t, "two.sqlite")
	m := sized(120, 40)
	m, err := m.OpenFileOnStart(first)
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range drain(m.Init()) {
		m = send(t, m, msg)
	}
	m = send(t, m, press('1'))
	m = send(t, m, press('o'))
	form := m.modal.(*formModal)
	form.field("file").input.SetValue(second)
	m = send(t, m, special(tea.KeyEnter, 0))

	if m.active != "two" {
		t.Fatalf("active = %q, want two", m.active)
	}
	items := m.panels[panelConnections].items
	if slices.Contains(items, "one") {
		t.Fatalf("connections panel = %v, still holds the replaced file", items)
	}
	if _, ok := m.connState["one"]; ok {
		t.Fatal("the replaced connection's status survived")
	}
}

// The panel name of an ephemeral file never collides with a saved profile
// of the same name — the two rows have to stay distinguishable.
func TestEphemeralNameAvoidsSavedProfile(t *testing.T) {
	path := sqliteFile(t, "local-sqlite.sqlite")
	m := sized(120, 40)
	m, err := m.OpenFileOnStart(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.ephem.conn.Name; got != "local-sqlite (2)" {
		t.Fatalf("ephemeral name = %q, want local-sqlite (2)", got)
	}
	if c, ok := m.findConn("local-sqlite"); !ok || c.File == path {
		t.Fatal("the saved profile was shadowed by the ephemeral one")
	}
}
