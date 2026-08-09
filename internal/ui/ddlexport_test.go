package ui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `E` on the DDL tab writes exactly the TableDDL output to a `.sql`
// file — no header, no wrapping — so the file is a drop-in CREATE
// statement.
func TestExportDDLWritesExactTableDDL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "orders-ddl.sql")

	m := send(t, metaBrowsing(t), press(']'), press(']'), press(']'))
	if m.tab != mainTabDDL {
		t.Fatalf("tab = %v, want DDL", m.tab)
	}
	want := m.meta.ddl
	if want == "" {
		t.Fatal("fixture has no DDL to export")
	}

	m = send(t, m, press('E'))
	m = typePath(t, m, path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("file = %q, want exactly TableDDL's %q", data, want)
	}
	if !logContains(m, "export DDL of orders wrote") {
		t.Fatalf("command log = %v", m.commandLog)
	}
}

// A path without a `.sql` extension is refused before any file is
// created.
func TestExportDDLRequiresSQLExtension(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "orders-ddl.txt")

	m := send(t, metaBrowsing(t), press(']'), press(']'), press(']'), press('E'))
	m = typePath(t, m, path)

	if !logContains(m, "needs a .sql path") {
		t.Fatalf("command log = %v", m.commandLog)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("a file was created anyway: %v", err)
	}
}

// `E` on any other tab still exports the table's data, unaffected by the
// DDL tab's own export.
func TestExportOnDataTabStillExportsData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "orders.csv")

	m := send(t, metaBrowsing(t), press('E'))
	m = typePath(t, m, path)

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("no CSV was written: %v", err)
	}
	if !logContains(m, "export wrote") {
		t.Fatalf("command log = %v", m.commandLog)
	}
}

// ddlDatabaseBrowsing opens a fixture database with a foreign-key chain
// (line_items → orders → people) plus an independent table, so a
// whole-database export exercises both dependency ordering and the
// alphabetical fallback for tables with nothing between them.
func ddlDatabaseBrowsing(t *testing.T) Model {
	t.Helper()
	m := browsing(t)
	ctx := context.Background()
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS line_items`,
		`DROP TABLE IF EXISTS orders`,
		`DROP TABLE IF EXISTS people`,
		`DROP TABLE IF EXISTS zebras`,
		`CREATE TABLE people (id INTEGER PRIMARY KEY, email TEXT NOT NULL)`,
		`CREATE TABLE orders (id INTEGER PRIMARY KEY, person_id INTEGER REFERENCES people (id))`,
		`CREATE TABLE line_items (id INTEGER PRIMARY KEY, order_id INTEGER REFERENCES orders (id))`,
		`CREATE TABLE zebras (id INTEGER PRIMARY KEY)`,
	} {
		if _, err := m.driver.Exec(ctx, stmt); err != nil {
			t.Fatalf("fixture %q: %v", stmt, err)
		}
	}
	// The fixture database is one SQLite file shared by the whole test
	// binary; line_items' foreign key into "orders" would otherwise leak
	// a phantom incoming reference into every later test that reuses that
	// table name.
	t.Cleanup(func() {
		for _, stmt := range []string{
			`DROP TABLE IF EXISTS line_items`,
			`DROP TABLE IF EXISTS orders`,
			`DROP TABLE IF EXISTS people`,
			`DROP TABLE IF EXISTS zebras`,
		} {
			m.driver.Exec(context.Background(), stmt)
		}
	})
	return send(t, m, press('R'))
}

// `E` on the Tables panel exports every relation's DDL of the browsed
// database into one file, referenced tables ordered before the tables
// that reference them.
func TestExportDatabaseDDLOrdersByForeignKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "db-ddl.sql")

	m := ddlDatabaseBrowsing(t)
	if m.focus != panelTables {
		t.Fatalf("focus = %v, want the Tables panel", m.focus)
	}
	m = send(t, m, press('E'))
	m = typePath(t, m, path)

	if !logContains(m, "export DDL of") || !logContains(m, "wrote") {
		t.Fatalf("command log = %v", m.commandLog)
	}
	if logContains(m, "cycle") {
		t.Fatalf("an acyclic graph reported a cycle: %v", m.commandLog)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{
		"CREATE TABLE", "-- table: people", "-- table: orders",
		"-- table: line_items", "-- table: zebras", "foreign-key dependency order",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("exported file is missing %q:\n%s", want, content)
		}
	}
	// people before orders before line_items: each references the one
	// before it.
	pos := func(needle string) int { return strings.Index(content, needle) }
	if !(pos("-- table: people") < pos("-- table: orders") &&
		pos("-- table: orders") < pos("-- table: line_items")) {
		t.Fatalf("dependency order not respected:\n%s", content)
	}
}

// A foreign-key cycle cannot be ordered; the export falls back to
// alphabetical and says so in the file and the log.
func TestExportDatabaseDDLFallsBackOnCycle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cycle-ddl.sql")

	m := browsing(t)
	ctx := context.Background()
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS b_table`,
		`DROP TABLE IF EXISTS a_table`,
		// SQLite does not check a foreign key's target for existence at
		// CREATE TABLE time, so b_table can reference the not-yet-created
		// a_table, which then references b_table right back.
		`CREATE TABLE b_table (id INTEGER PRIMARY KEY, a_id INTEGER REFERENCES a_table (id))`,
		`CREATE TABLE a_table (id INTEGER PRIMARY KEY, b_id INTEGER REFERENCES b_table (id))`,
	} {
		if _, err := m.driver.Exec(ctx, stmt); err != nil {
			t.Fatalf("fixture %q: %v", stmt, err)
		}
	}
	t.Cleanup(func() {
		m.driver.Exec(context.Background(), `DROP TABLE IF EXISTS b_table`)
		m.driver.Exec(context.Background(), `DROP TABLE IF EXISTS a_table`)
	})
	m = send(t, m, press('R'))

	m = send(t, m, press('E'))
	m = typePath(t, m, path)

	if !logContains(m, "cycle") {
		t.Fatalf("command log = %v", m.commandLog)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "alphabetical") {
		t.Errorf("file does not note the alphabetical fallback:\n%s", data)
	}
}

// Two whole-database exports never run at once.
func TestOnlyOneDatabaseDDLExportRunsAtATime(t *testing.T) {
	m := ddlDatabaseBrowsing(t)
	m.dbDDLExport = dbDDLExportState{running: true}
	next, cmd := m.runAction(actExportDatabaseDDL)
	if next.modal != nil {
		t.Error("a second export opened a prompt")
	}
	var lines []string
	for _, msg := range drain(cmd) {
		if l, ok := msg.(commandLogMsg); ok {
			lines = append(lines, l.line)
		}
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "already running") {
		t.Fatalf("log = %v", lines)
	}
}

// A reply from an export superseded by a newer one — the connection was
// reset and a second export started before the first's reply landed —
// is ignored rather than clobbering the newer export's in-flight state.
func TestStaleDatabaseDDLExportReplyIsIgnored(t *testing.T) {
	m := ddlDatabaseBrowsing(t)
	m.dbDDLExport = dbDDLExportState{running: true, id: 2}
	m = send(t, m, databaseDDLExportedMsg{id: 1, database: "stale", path: "/tmp/stale.sql"})
	if !m.dbDDLExport.running || m.dbDDLExport.id != 2 {
		t.Fatalf("a stale reply changed the running export's state: %+v", m.dbDDLExport)
	}
	if logContains(m, "stale") {
		t.Fatalf("the stale reply was logged: %v", m.commandLog)
	}
}

// Disconnecting mid-export cancels it, the same way it cancels a
// streaming table export: both read through the driver that is about to
// close.
func TestResetBrowseCancelsRunningDatabaseDDLExport(t *testing.T) {
	cancelled := false
	m := ddlDatabaseBrowsing(t)
	m.dbDDLExport = dbDDLExportState{running: true, id: 1, cancel: func() { cancelled = true }}
	m.resetBrowse()
	if !cancelled {
		t.Fatal("resetBrowse did not cancel the running database DDL export")
	}
}

// A cancelled scan reports itself as cancelled rather than writing a
// partial file — the file is only ever written once, at the very end,
// so cancellation before that point leaves nothing on disk.
func TestDatabaseDDLScanRespectsCancellation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cancelled.sql")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	msg := runDatabaseDDLScan(ctx, nil, "app", []string{"orders"}, path, 7)
	done, ok := msg.(databaseDDLExportedMsg)
	if !ok {
		t.Fatalf("msg = %T", msg)
	}
	if done.id != 7 || !errors.Is(done.err, context.Canceled) {
		t.Fatalf("msg = %+v, want id 7 and a context.Canceled err", done)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("a file was created despite the cancellation: %v", err)
	}
}
