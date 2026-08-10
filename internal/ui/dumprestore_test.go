package ui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/config"
	"lazysql/internal/db"
	"lazysql/internal/secrets"
	"lazysql/internal/sshtunnel"
	"lazysql/internal/sshtunnel/sshtest"
)

// fillBackupForm sets the open dump/restore form's fields and submits it.
// The values are set directly rather than typed: a textinput answers every
// keystroke with a cursor-blink command the synchronous driver would block
// on.
func fillBackupForm(t *testing.T, m Model, values map[string]string) Model {
	t.Helper()
	f, ok := m.modal.(*formModal)
	if !ok {
		t.Fatalf("modal = %T, want the backup form", m.modal)
	}
	for name, value := range values {
		fl := f.field(name)
		if fl == nil {
			t.Fatalf("form has no %q field", name)
		}
		fl.input.SetValue(value)
	}
	return send(t, m, special(tea.KeyEnter, 0))
}

// confirmWord is the database name the open restore form insists on. It
// comes from the form itself so the tests never re-derive it.
func confirmWord(t *testing.T, m Model) string {
	t.Helper()
	fl := backupForm(t, m).field("confirm")
	if fl == nil {
		t.Fatal("the restore form has no confirmation field")
	}
	return fl.input.Placeholder
}

func backupForm(t *testing.T, m Model) *formModal {
	t.Helper()
	f, ok := m.modal.(*formModal)
	if !ok {
		t.Fatalf("modal = %T, want the backup form", m.modal)
	}
	return f
}

// openBackup presses `B` and picks one of the two menu entries.
func openBackup(t *testing.T, m Model, entry rune) Model {
	t.Helper()
	m = send(t, m, press('B'))
	if _, ok := m.modal.(*menuModal); !ok {
		t.Fatalf("B opened %T, want the backup menu", m.modal)
	}
	return send(t, m, press(entry))
}

// ---------- SQLite ----------

// The acceptance criterion: a SQLite dump produces a file that is a valid,
// complete database — VACUUM INTO writes a real copy, not a script.
func TestSQLiteDumpProducesAValidDatabase(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "backup.db")

	m := metaBrowsing(t)
	m = send(t, m, press('1'))
	m = openBackup(t, m, 'd')
	m = fillBackupForm(t, m, map[string]string{"path": out})

	if m.backup.running {
		t.Error("the dump is still marked as running after it finished")
	}
	if !logContains(m, "dump of") || !logContains(m, "wrote "+out) {
		t.Fatalf("command log = %v", backupLogText(m))
	}
	// The statement itself went through the Driver's logger, which is
	// what puts every executed statement in the log exactly once.
	if !logContains(m, "VACUUM INTO") {
		t.Fatalf("the dump statement is not in the command log: %v", backupLogText(m))
	}

	assertSQLiteRowCount(t, out, "orders", 1)
	assertSQLiteRowCount(t, out, "people", 1)
}

// A restore copies the dump back over the database file. It closes the
// session first: overwriting the file under an open connection risks a
// stale journal being replayed onto the new database.
func TestSQLiteRestoreCopiesBackAndClosesTheSession(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "backup.db")

	m := metaBrowsing(t)
	dbFile := m.cfg.Connections[0].File
	m = send(t, m, press('1'))
	m = openBackup(t, m, 'd')
	m = fillBackupForm(t, m, map[string]string{"path": out})

	// Diverge the live database from the dump, then restore.
	if _, err := m.driver.Exec(context.Background(),
		`INSERT INTO orders (id, person_id, status) VALUES (99, 1, 'later')`); err != nil {
		t.Fatal(err)
	}

	m = openBackup(t, m, 'r')
	m = fillBackupForm(t, m, map[string]string{"path": out, "confirm": confirmWord(t, m)})

	if m.driver != nil {
		t.Fatal("the restore left the connection open over the replaced file")
	}
	if !logContains(m, "restore of") || !logContains(m, "applied "+out) {
		t.Fatalf("command log = %v", backupLogText(m))
	}
	// The row added after the dump is gone: the file really was replaced.
	assertSQLiteRowCount(t, dbFile, "orders", 1)
}

// Restore is the one action in lazysql that overwrites data without
// staging, so it does not run until the database name has been typed.
func TestRestoreRequiresTypingTheDatabaseName(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "backup.db")

	m := metaBrowsing(t)
	m = send(t, m, press('1'))
	m = openBackup(t, m, 'd')
	m = fillBackupForm(t, m, map[string]string{"path": out})

	m = openBackup(t, m, 'r')
	want := confirmWord(t, m)
	// Wrong word: the form stays open, says why, and nothing runs.
	m = fillBackupForm(t, m, map[string]string{"path": out, "confirm": "yes"})
	if m.modal == nil {
		t.Fatal("the restore form closed without the typed confirmation")
	}
	if f := backupForm(t, m); !strings.Contains(f.err, want) {
		t.Fatalf("form error = %q, want it to name %q", f.err, want)
	}
	if m.driver == nil {
		t.Fatal("the unconfirmed restore closed the session anyway")
	}
	if logContains(m, "restore of") && logContains(m, "applied") {
		t.Fatalf("an unconfirmed restore ran: %v", backupLogText(m))
	}

	// The right word runs it.
	m = fillBackupForm(t, m, map[string]string{"confirm": want})
	if m.modal != nil {
		t.Fatalf("the confirmed restore left %T open", m.modal)
	}
}

// A read-only connection refuses a restore outright: it is a write, and
// the whole point of the flag is that writes never happen.
func TestRestoreRefusedOnAReadOnlyConnection(t *testing.T) {
	m := metaBrowsing(t)
	m.cfg.Connections[0].ReadOnly = true
	m = send(t, m, press('1'))
	m = openBackup(t, m, 'r')

	if m.modal != nil {
		t.Fatalf("a read-only connection opened %T", m.modal)
	}
	if !logContains(m, "restore skipped") || !logContains(m, "read-only") {
		t.Fatalf("command log = %v", backupLogText(m))
	}
}

// ---------- DuckDB ----------

// DuckDB's EXPORT DATABASE writes a directory of CSVs plus the schema and
// load scripts; IMPORT DATABASE reads that layout back.
func TestDuckDBDumpAndRestoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "export")

	m := duckBrowsing(t)
	m = send(t, m, press('1'))
	m = openBackup(t, m, 'd')
	m = fillBackupForm(t, m, map[string]string{"path": out})

	if !logContains(m, "EXPORT DATABASE") {
		t.Fatalf("the export statement is not in the command log: %v", backupLogText(m))
	}
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatalf("EXPORT DATABASE wrote no directory: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	for _, want := range []string{"schema.sql", "load.sql"} {
		if !containsName(names, want) {
			t.Fatalf("export directory = %v, want it to contain %q", names, want)
		}
	}

	// Drop the table and import it back.
	if _, err := m.driver.Exec(context.Background(), `DROP TABLE widgets`); err != nil {
		t.Fatal(err)
	}
	m = openBackup(t, m, 'r')
	m = fillBackupForm(t, m, map[string]string{"path": out, "confirm": confirmWord(t, m)})

	if !logContains(m, "IMPORT DATABASE") {
		t.Fatalf("the import statement is not in the command log: %v", backupLogText(m))
	}
	res, err := m.driver.Query(context.Background(), `SELECT count(*) FROM widgets`)
	if err != nil {
		t.Fatalf("the imported table is not queryable: %v", err)
	}
	if got := res.Rows[0][0]; !isCount(got, 2) {
		t.Fatalf("restored row count = %v, want 2", got)
	}
}

// duckBrowsing connects the DuckDB fixture and gives it one table.
func duckBrowsing(t *testing.T) Model {
	t.Helper()
	m := sized(120, 40)
	m.cfg.Connections[3].File = filepath.Join(t.TempDir(), "analytics.duckdb")
	m.refreshConnections("")
	m.panels[panelConnections].selectByName("analytics")
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.driver == nil {
		t.Fatal("the DuckDB fixture did not connect")
	}
	for _, stmt := range []string{
		`CREATE TABLE widgets (id INTEGER, name VARCHAR)`,
		`INSERT INTO widgets VALUES (1, 'first'), (2, 'second')`,
	} {
		if _, err := m.driver.Exec(context.Background(), stmt); err != nil {
			t.Fatalf("fixture %q: %v", stmt, err)
		}
	}
	return send(t, m, press('1'))
}

// ---------- server engines ----------

// A missing tool is reported by name before any prompt appears, so the
// user learns what to install rather than watching a form fail.
func TestMissingToolNamesTheBinary(t *testing.T) {
	m := sized(120, 40)
	m.cfg.Connections[1].Database = "shop"
	m.panels[panelConnections].selectByName("local-postgres")
	withEmptyPath(t)

	m = openBackup(t, m, 'd')
	if m.modal != nil {
		t.Fatalf("a missing tool still opened %T", m.modal)
	}
	if !logContains(m, "pg_dump") || !logContains(m, "PATH") {
		t.Fatalf("command log = %v", backupLogText(m))
	}
}

// The previewed command is the whole invocation, and it never carries the
// password — neither in argv nor in the environment prefix.
func TestPreviewShowsTheCommandWithoutThePassword(t *testing.T) {
	const password = "sup3rs3cret"
	if err := secrets.Set("local-postgres", password); err != nil {
		t.Skipf("no usable keyring: %v", err)
	}
	defer secrets.Delete("local-postgres")

	m := sized(120, 40)
	m.cfg.Connections[1].Database = "shop"
	m.panels[panelConnections].selectByName("local-postgres")
	withPath(t, fakeToolDir(t, "pg_dump", "psql"))

	m = openBackup(t, m, 'd')
	f := backupForm(t, m)
	preview := strings.Join(f.body(f), "\n")

	for _, want := range []string{"pg_dump", "--dbname=shop", "--no-password", "PGPASSFILE="} {
		if !strings.Contains(preview, want) {
			t.Fatalf("preview %q missing %q", preview, want)
		}
	}
	if strings.Contains(preview, password) {
		t.Fatalf("the preview leaks the password: %q", preview)
	}
	if strings.Contains(preview, "--password") {
		t.Fatalf("the preview puts a password flag in argv: %q", preview)
	}

	// The rendered modal is what the user actually sees.
	view := f.view(m.style, 120, 40)
	if strings.Contains(view, password) {
		t.Fatal("the rendered modal leaks the password")
	}
}

// The arguments line is editable and its contents reach the command.
func TestExtraArgumentsReachTheCommand(t *testing.T) {
	m := sized(120, 40)
	m.cfg.Connections[1].Database = "shop"
	m.panels[panelConnections].selectByName("local-postgres")
	withPath(t, fakeToolDir(t, "pg_dump", "psql"))

	m = openBackup(t, m, 'd')
	f := backupForm(t, m)
	f.field("args").input.SetValue("--data-only --schema=public")
	if preview := strings.Join(f.body(f), "\n"); !strings.Contains(preview, "--data-only") {
		t.Fatalf("preview %q does not reflect the arguments line", preview)
	}
}

// A dump of a running tool reports its non-zero exit with the tail of what
// it wrote to stderr, and the failed dump's file is removed.
func TestFailingToolReportsStderrAndRemovesThePartialDump(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "shop.sql")
	withPath(t, failingToolDir(t, "pg_dump"))

	m := sized(120, 40)
	m.cfg.Connections[1].Database = "shop"
	m.panels[panelConnections].selectByName("local-postgres")

	m = openBackup(t, m, 'd')
	m = fillBackupForm(t, m, map[string]string{"path": out})

	if m.backup.running {
		t.Error("the failed dump is still marked as running")
	}
	if !logContains(m, "could not connect to server") {
		t.Fatalf("the stderr tail is not in the log: %v", backupLogText(m))
	}
	if !logContains(m, "FAILED") {
		t.Fatalf("command log = %v", backupLogText(m))
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("the partial dump was left behind")
	}
}

// Progress lines arrive in the command log as the tool writes them.
func TestToolProgressReachesTheCommandLog(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "shop.sql")
	withPath(t, chattyToolDir(t, "pg_dump"))

	m := sized(120, 40)
	m.cfg.Connections[1].Database = "shop"
	m.panels[panelConnections].selectByName("local-postgres")

	m = openBackup(t, m, 'd')
	m = fillBackupForm(t, m, map[string]string{"path": out})

	for _, want := range []string{"dumping table people", "dumping table orders"} {
		if !logContains(m, want) {
			t.Fatalf("progress line %q missing from %v", want, backupLogText(m))
		}
	}
	if !logContains(m, "$ pg_dump") {
		t.Fatalf("the command that ran is not in the log: %v", backupLogText(m))
	}
}

// ---------- guards and keys ----------

func TestBackupSkippedWithoutAConnection(t *testing.T) {
	m := sized(120, 40)
	m.cfg = &config.Config{}
	m.refreshConnections("")
	m = send(t, m, press('B'))
	if m.modal != nil {
		t.Fatalf("B opened %T with no connections", m.modal)
	}
	if !logContains(m, "no connection selected") {
		t.Fatalf("command log = %v", backupLogText(m))
	}
}

// A file engine's dump runs through the open session, so it needs one.
func TestFileEngineBackupNeedsAnOpenConnection(t *testing.T) {
	m := sized(120, 40)
	m = openBackup(t, m, 'd')
	if m.modal != nil {
		t.Fatalf("an unconnected file engine opened %T", m.modal)
	}
	if !logContains(m, "connect to local-sqlite first") {
		t.Fatalf("command log = %v", backupLogText(m))
	}
}

// `B` is offered on both panels the issue names, and the cancel key is
// hidden until there is something to cancel.
func TestBackupBindings(t *testing.T) {
	m := sized(120, 40)
	for _, id := range []panelID{panelConnections, panelObjects} {
		var found, cancel bool
		for _, a := range m.keys.panelActions(id) {
			switch a.id {
			case actBackup:
				found = true
			case actCancelBackup:
				cancel = a.binding.Enabled()
			}
		}
		if !found {
			t.Fatalf("panel %v does not offer the backup action", id)
		}
		if cancel {
			t.Fatalf("panel %v shows the cancel key with nothing running", id)
		}
	}
	// Every binding is documented in `?`.
	if !helpMentions(m, panelConnections, "dump / restore…") {
		t.Fatal("the backup key is missing from the help")
	}
}

// The browsed namespace decides what a dump targets. Only PostgreSQL has a
// schema narrower than the database.
func TestBackupTargetPerEngine(t *testing.T) {
	cases := []struct {
		name             string
		conn             config.Connection
		browsed          string
		database, schema string
	}{
		{"postgres schema",
			config.Connection{Engine: db.EnginePostgres, Database: "shop"}, "reporting", "shop", "reporting"},
		{"postgres default schema",
			config.Connection{Engine: db.EnginePostgres, Database: "shop"}, "shop", "shop", ""},
		{"mysql namespace is the database",
			config.Connection{Engine: db.EngineMySQL, Database: "shop"}, "other", "other", ""},
		{"mysql without a browsed namespace",
			config.Connection{Engine: db.EngineMySQL, Database: "shop"}, "", "shop", ""},
		{"sqlite falls back to the file name",
			config.Connection{Engine: db.EngineSQLite, File: "/data/app.db"}, "", "app", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := Model{database: tc.browsed}
			database, schema := m.backupTarget(tc.conn)
			if database != tc.database || schema != tc.schema {
				t.Fatalf("target = (%q, %q), want (%q, %q)",
					database, schema, tc.database, tc.schema)
			}
		})
	}
}

func TestDefaultBackupPath(t *testing.T) {
	if got := defaultBackupPath(db.EnginePostgres, "shop"); got != "shop.sql" {
		t.Fatalf("path = %q", got)
	}
	// DuckDB exports a directory, so the default has no `.sql` suffix.
	if got := defaultBackupPath(db.EngineDuckDB, "analytics"); got != "analytics-export" {
		t.Fatalf("path = %q", got)
	}
	// A namespace with a separator in it stays one path component.
	if got := defaultBackupPath(db.EngineMySQL, "a/b"); got != "a-b.sql" {
		t.Fatalf("path = %q", got)
	}
}

// ---------- helpers ----------

// backupLogText is the whole merged log — the UI's own notes plus the
// Driver's statements — which is what a dump's assertions need.
func backupLogText(m Model) []string {
	entries := m.commandLogEntries()
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.text)
	}
	return out
}

func helpMentions(m Model, id panelID, want string) bool {
	for _, group := range m.keys.helpGroups(id) {
		for _, b := range group {
			if b.Help().Desc == want {
				return true
			}
		}
	}
	return false
}

// withPath puts a fixture tool directory ahead of the real PATH for the
// duration of one test, so lookup finds the fixture whatever the developer
// happens to have installed — while the fixtures themselves can still call
// touch, sleep and friends.
func withPath(t *testing.T, dir string) {
	t.Helper()
	old := os.Getenv("PATH")
	os.Setenv("PATH", dir+string(os.PathListSeparator)+old)
	t.Cleanup(func() { os.Setenv("PATH", old) })
}

// withEmptyPath removes every directory from PATH, which is how the
// "the tool is not installed" case is reproduced.
func withEmptyPath(t *testing.T) {
	t.Helper()
	old := os.Getenv("PATH")
	os.Setenv("PATH", t.TempDir())
	t.Cleanup(func() { os.Setenv("PATH", old) })
}

// fakeToolDir creates executables that do nothing, so command
// construction can be exercised without a database server.
func fakeToolDir(t *testing.T, names ...string) string {
	t.Helper()
	return toolDir(t, "#!/bin/sh\nexit 0\n", names...)
}

// failingToolDir creates a tool that fails the way a real one does: a
// message on stderr and a non-zero exit, after having created its output
// file.
func failingToolDir(t *testing.T, names ...string) string {
	t.Helper()
	return toolDir(t, `#!/bin/sh
for arg in "$@"; do
  case "$arg" in --file=*) : > "${arg#--file=}" ;; esac
done
echo "pg_dump: error: connection to server failed: could not connect to server" 1>&2
exit 1
`, names...)
}

// chattyToolDir creates a tool that reports progress on stderr, the way
// `pg_dump --verbose` does.
func chattyToolDir(t *testing.T, names ...string) string {
	t.Helper()
	return toolDir(t, `#!/bin/sh
echo "pg_dump: dumping table people" 1>&2
echo "pg_dump: dumping table orders" 1>&2
for arg in "$@"; do
  case "$arg" in --file=*) echo "-- dump" > "${arg#--file=}" ;; esac
done
exit 0
`, names...)
}

func toolDir(t *testing.T, script string, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		path := filepath.Join(dir, n)
		if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func isCount(v any, want int64) bool {
	switch n := v.(type) {
	case int64:
		return n == want
	case float64:
		return int64(n) == want
	}
	return false
}

// assertSQLiteRowCount opens a dumped database directly — not through the
// model — and checks a table's contents survived the round trip.
func assertSQLiteRowCount(t *testing.T, path, table string, want int) {
	t.Helper()
	drv, err := db.Open(db.EngineSQLite)
	if err != nil {
		t.Fatal(err)
	}
	defer drv.Close()
	dsn, err := db.BuildDSN(db.EngineSQLite, db.ConnParams{File: path})
	if err != nil {
		t.Fatal(err)
	}
	if err := drv.Connect(context.Background(), dsn); err != nil {
		t.Fatalf("the dump is not a valid SQLite database: %v", err)
	}
	res, err := drv.Query(context.Background(), `SELECT count(*) FROM `+table)
	if err != nil {
		t.Fatalf("query the dump: %v", err)
	}
	if got := res.Rows[0][0]; !isCount(got, int64(want)) {
		t.Fatalf("%s in the dump has %v rows, want %d", table, got, want)
	}
}

// ---------- cancellation ----------

// `X` while a tool runs kills it and removes the half-written dump. The
// run is driven by hand here rather than through send: the fixture tool
// blocks until it is killed, so the cancel has to come from another
// goroutine while the cascade is still in flight.
func TestCancelKillsTheRunningToolAndRemovesTheDump(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "shop.sql")
	started := filepath.Join(dir, "started")
	withPath(t, slowToolDir(t, started, "pg_dump"))

	m := sized(120, 40)
	m.cfg.Connections[1].Database = "shop"
	m.panels[panelConnections].selectByName("local-postgres")
	m = openBackup(t, m, 'd')
	backupForm(t, m).field("path").input.SetValue(out)

	next, cmd := m.Update(special(tea.KeyEnter, 0))
	m = next.(Model)
	if !m.backup.running {
		t.Fatal("submitting the form did not start the job")
	}
	if !m.keys.CancelBackup.Enabled() {
		t.Fatal("the cancel key is not offered while a job runs")
	}

	cancel := m.backup.cancel
	go func() {
		waitForFile(started, 10*time.Second)
		cancel()
	}()

	// Drive the message cascade the way the runtime would.
	queue := drain(cmd)
	for round := 0; len(queue) > 0; round++ {
		if round > 10_000 {
			t.Fatal("the cancelled run did not settle")
		}
		head := queue[0]
		queue = queue[1:]
		nxt, c := m.Update(head)
		m = nxt.(Model)
		queue = append(queue, drain(c)...)
	}

	if m.backup.running {
		t.Fatal("the job is still marked as running after the cancel")
	}
	if m.keys.CancelBackup.Enabled() {
		t.Fatal("the cancel key is still offered with nothing running")
	}
	if !logContains(m, "cancelled") {
		t.Fatalf("command log = %v", backupLogText(m))
	}
	// A half-written dump looks complete, so it is removed.
	if _, err := os.Stat(out); err == nil {
		t.Fatal("the cancelled dump was left behind")
	}
}

// slowToolDir creates a tool that writes its output file, says it started
// and then blocks until it is killed.
func slowToolDir(t *testing.T, marker string, names ...string) string {
	t.Helper()
	return toolDir(t, `#!/bin/sh
for arg in "$@"; do
  case "$arg" in --file=*) echo "-- partial" > "${arg#--file=}" ;; esac
done
echo "pg_dump: dumping table people" 1>&2
touch `+marker+`
sleep 30
`, names...)
}

func waitForFile(path string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// ---------- tunnels ----------

// A tunnelled connection cannot hand the tool lazysql's Go dialer, so the
// job opens a local forward and points the tool at it. The fixture tool
// records the endpoint it was given and connects to it, which proves both
// halves: the arguments name the forward, and the forward really reaches
// the remote.
func TestTunnelledDumpRunsThroughTheForward(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "shop.sql")
	seen := filepath.Join(dir, "endpoint")

	// The "database server" behind the jump host: anything that accepts a
	// connection and echoes, so the fixture tool can prove it got through.
	if _, err := exec.LookPath("nc"); err != nil {
		t.Skip("the fixture tool needs nc to prove it reached the forward")
	}
	remote := sshtest.NewEcho(t)
	srv := sshtest.NewPassword(t, "jump", "hunter2")

	tun, err := sshtunnel.Open(context.Background(), sshtunnel.Config{
		Enabled:        true,
		Host:           srv.Host(),
		Port:           srv.Port(),
		User:           "jump",
		Auth:           sshtunnel.AuthPassword,
		Secret:         "hunter2",
		KnownHostsFile: sshtest.WriteKnownHosts(t, dir, srv.KnownHostsLine()),
		SSHConfigFile:  filepath.Join(dir, "no-such-config"),
	})
	if err != nil {
		t.Fatalf("open tunnel: %v", err)
	}
	defer tun.Close()

	withPath(t, endpointToolDir(t, seen, "pg_dump"))

	m := sized(120, 40)
	m.cfg.Connections[1].Database = "shop"
	m.cfg.Connections[1].Host = remote.Host()
	m.cfg.Connections[1].Port = remote.Port()
	m.cfg.Connections[1].SSH = &config.SSH{Enabled: true, Host: srv.Host(), Auth: "password"}
	// Pretend this profile is the live connection, so the job finds the
	// tunnel the way it would after a real dial.
	m.active = "local-postgres"
	m.tunnel = tun
	m.panels[panelConnections].selectByName("local-postgres")

	m = openBackup(t, m, 'd')
	m = fillBackupForm(t, m, map[string]string{"path": out})

	if !logContains(m, "wrote "+out) {
		t.Fatalf("command log = %v", backupLogText(m))
	}
	data, err := os.ReadFile(seen)
	if err != nil {
		t.Fatalf("the tool recorded no endpoint: %v", err)
	}
	host, port, reached := parseEndpointRecord(string(data))
	if host != "127.0.0.1" {
		t.Fatalf("the tool was pointed at %q, want the local forward", host)
	}
	if port == remote.Port() {
		t.Fatal("the tool was pointed straight at the remote, bypassing the tunnel")
	}
	if !reached {
		t.Fatalf("the forward did not carry the tool's connection through: %q", data)
	}
	// The command log records the forwarded endpoint, never the jump host
	// password.
	if !logContains(m, "--host=127.0.0.1") {
		t.Fatalf("the logged command does not name the forward: %v", backupLogText(m))
	}
	if logContains(m, "hunter2") {
		t.Fatal("the log leaks the SSH secret")
	}
}

// endpointToolDir creates a tool that writes down the --host/--port it was
// given, connects to it, and records whether that worked.
func endpointToolDir(t *testing.T, record string, names ...string) string {
	t.Helper()
	return toolDir(t, `#!/bin/sh
host=""
port=""
for arg in "$@"; do
  case "$arg" in
    --host=*) host="${arg#--host=}" ;;
    --port=*) port="${arg#--port=}" ;;
    --file=*) echo "-- dump" > "${arg#--file=}" ;;
  esac
done
reached=no
if echo ping | nc -w 2 "$host" "$port" >/dev/null 2>&1; then reached=yes; fi
echo "$host $port $reached" > `+record+`
exit 0
`, names...)
}

func parseEndpointRecord(s string) (host string, port int, reached bool) {
	fields := strings.Fields(strings.TrimSpace(s))
	if len(fields) != 3 {
		return "", 0, false
	}
	p, err := strconv.Atoi(fields[1])
	if err != nil {
		return fields[0], 0, false
	}
	return fields[0], p, fields[2] == "yes"
}

// ---------- which connection a backup acts on ----------

// From panel [1] the cursor decides: a dump does not need the profile to
// be connected, so picking one there and pressing `B` has to mean it —
// even while a different connection is live.
func TestBackupFromPanelOneUsesTheSelectedProfile(t *testing.T) {
	withPath(t, fakeToolDir(t, "pg_dump", "psql"))

	m := metaBrowsing(t) // connected to the SQLite fixture
	m.cfg.Connections[1].Database = "shop"
	m = send(t, m, press('1'))
	m.panels[panelConnections].selectByName("local-postgres")

	m = openBackup(t, m, 'd')
	f := backupForm(t, m)
	if !strings.Contains(f.title, "local-postgres") {
		t.Fatalf("form title = %q, want the selected profile", f.title)
	}
	if preview := strings.Join(f.body(f), "\n"); !strings.Contains(preview, "pg_dump") {
		t.Fatalf("preview %q is not the selected profile's tool", preview)
	}
}

// A file engine dumps through its own open session, so selecting an
// unconnected one while another connection is live is refused rather than
// silently backing up the wrong database.
func TestFileEngineBackupRefusesAnotherConnectionsSession(t *testing.T) {
	m := metaBrowsing(t) // connected to local-sqlite
	m.cfg.Connections = append(m.cfg.Connections, config.Connection{
		Name: "other-sqlite", Engine: db.EngineSQLite,
		File: filepath.Join(t.TempDir(), "other.db"),
	})
	m.refreshConnections("")
	m = send(t, m, press('1'))
	m.panels[panelConnections].selectByName("other-sqlite")

	m = openBackup(t, m, 'd')
	if m.modal != nil {
		t.Fatalf("a foreign session opened %T", m.modal)
	}
	if !logContains(m, "connect to other-sqlite first") {
		t.Fatalf("command log = %v", backupLogText(m))
	}
}
