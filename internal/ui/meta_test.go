package ui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/db"
)

// metaBrowsing opens a fixture relation that has everything the three
// introspection tabs render: a primary key, a nullable column, a
// default, a unique and a plain index, and a foreign key.
func metaBrowsing(t *testing.T) Model {
	t.Helper()
	m := browsing(t)
	ctx := context.Background()
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS orders`,
		`DROP TABLE IF EXISTS people`,
		`CREATE TABLE people (id INTEGER PRIMARY KEY AUTOINCREMENT, email TEXT NOT NULL)`,
		`CREATE UNIQUE INDEX idx_people_email ON people (email)`,
		`CREATE TABLE orders (
			id INTEGER PRIMARY KEY,
			person_id INTEGER NOT NULL REFERENCES people (id) ON DELETE CASCADE,
			status TEXT DEFAULT 'new'
		)`,
		`CREATE INDEX idx_orders_status ON orders (status)`,
		`INSERT INTO people (email) VALUES ('a@example.com')`,
		`INSERT INTO orders (id, person_id, status) VALUES (1, 1, 'new')`,
	} {
		if _, err := m.driver.Exec(ctx, stmt); err != nil {
			t.Fatalf("fixture %q: %v", stmt, err)
		}
	}
	m = send(t, m, press('3'), press('R'))
	if !m.panels[panelTables].selectByName("orders") {
		t.Fatalf("fixture table not listed: %v", m.panels[panelTables].items)
	}
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.focus != panelMain {
		t.Fatalf("focus = %v, want the main view", m.focus)
	}
	return m
}

// fakeClipboard replaces the real clipboard for the duration of a test,
// so a test run can never overwrite the developer's own.
func fakeClipboard(t *testing.T) *string {
	t.Helper()
	var got string
	prev := clipboardWrite
	clipboardWrite = func(text string) error {
		got = text
		return nil
	}
	t.Cleanup(func() { clipboardWrite = prev })
	return &got
}

// fakeSpill replaces the temp-file fallback, so a test that exercises a
// missing clipboard leaves nothing behind on disk.
func fakeSpill(t *testing.T) *string {
	t.Helper()
	var got string
	prev := spillFile
	spillFile = func(name, text string) (string, error) {
		got = text
		return "/tmp/fake-spill-" + name, nil
	}
	t.Cleanup(func() { spillFile = prev })
	return &got
}

// `]` walks Data → Structure → Indexes → DDL and wraps; `[` walks back.
func TestMainTabCycling(t *testing.T) {
	m := metaBrowsing(t)
	if m.tab != mainTabData {
		t.Fatalf("initial tab = %v, want Data", m.tab)
	}
	for _, want := range []mainTab{mainTabStructure, mainTabIndexes, mainTabDDL, mainTabData} {
		m = send(t, m, press(']'))
		if m.tab != want {
			t.Fatalf("after ] tab = %v, want %v", m.tab, want)
		}
	}
	m = send(t, m, press('['))
	if m.tab != mainTabDDL {
		t.Fatalf("after [ tab = %v, want DDL", m.tab)
	}
}

// The tab bar names all four tabs whatever the selected one is.
func TestTabBarRendersEveryTab(t *testing.T) {
	m := metaBrowsing(t)
	for range int(mainTabCount) {
		out := m.View().Content
		for _, name := range mainTabNames {
			if !strings.Contains(out, name) {
				t.Errorf("tab %v: view is missing the %q tab", m.tab, name)
			}
		}
		m = send(t, m, press(']'))
	}
}

// The Structure tab lists every column with its type, nullability,
// default, key info and extra.
func TestStructureTabRendersColumns(t *testing.T) {
	m := send(t, metaBrowsing(t), press(']'))
	if m.tab != mainTabStructure {
		t.Fatalf("tab = %v, want Structure", m.tab)
	}
	if !m.meta.loaded {
		t.Fatalf("metadata not loaded: %+v", m.meta)
	}
	if len(m.meta.cols) != 3 {
		t.Fatalf("columns = %d, want 3", len(m.meta.cols))
	}
	out := m.View().Content
	for _, want := range []string{"name", "type", "null", "default", "key", "extra",
		"person_id", "status", "'new'", "PK", "FK"} {
		if !strings.Contains(out, want) {
			t.Errorf("Structure tab is missing %q", want)
		}
	}
	if !logContains(m, "-- introspect") {
		t.Errorf("command log = %v", m.commandLog)
	}
}

// The Indexes tab lists the indexes and, under them, the foreign keys
// with the table and columns they reference.
func TestIndexesTabRendersIndexesAndForeignKeys(t *testing.T) {
	m := send(t, metaBrowsing(t), press(']'), press(']'))
	if m.tab != mainTabIndexes {
		t.Fatalf("tab = %v, want Indexes", m.tab)
	}
	out := m.View().Content
	for _, want := range []string{"idx_orders_status", "Foreign keys", "people (id)", "CASCADE"} {
		if !strings.Contains(out, want) {
			t.Errorf("Indexes tab is missing %q:\n%s", want, out)
		}
	}
}

// The DDL tab shows the CREATE statement the engine reports.
func TestDDLTabRendersStatement(t *testing.T) {
	m := send(t, metaBrowsing(t), press(']'), press(']'), press(']'))
	if m.tab != mainTabDDL {
		t.Fatalf("tab = %v, want DDL", m.tab)
	}
	if !strings.Contains(m.meta.ddl, "CREATE TABLE") {
		t.Fatalf("DDL = %q", m.meta.ddl)
	}
	out := m.View().Content
	for _, want := range []string{"CREATE TABLE", "person_id", "y opens the copy menu"} {
		if !strings.Contains(out, want) {
			t.Errorf("DDL tab is missing %q", want)
		}
	}
}

// A long DDL scrolls a line at a time and never runs past its end.
func TestDDLTabScrolls(t *testing.T) {
	m := send(t, metaBrowsing(t), press(']'), press(']'), press(']'))
	m = send(t, m, press('j'))
	if m.meta.row[mainTabDDL] != 1 {
		t.Fatalf("offset after j = %d, want 1", m.meta.row[mainTabDDL])
	}
	for range 50 {
		m = send(t, m, press('j'))
	}
	if got, max := m.meta.row[mainTabDDL], m.metaRowCount()-1; got != max {
		t.Fatalf("offset = %d, want it clamped to %d", got, max)
	}
	m = send(t, m, press('k'), press('k'), press('k'), press('k'), press('k'), press('k'))
	if m.meta.row[mainTabDDL] < 0 {
		t.Fatalf("offset = %d, want it clamped at 0", m.meta.row[mainTabDDL])
	}
}

// `y` opens the copy menu; its DDL entry copies the CREATE statement,
// from the DDL tab and from the Data tab alike — on the Data tab it
// fetches the metadata first.
func TestCopyDDLToClipboard(t *testing.T) {
	got := fakeClipboard(t)

	m := send(t, metaBrowsing(t), press(']'), press(']'), press(']'), press('y'), press('d'))
	if !strings.Contains(*got, "CREATE TABLE") {
		t.Fatalf("clipboard = %q", *got)
	}
	if !logContains(m, "-- copy DDL of orders to clipboard") {
		t.Fatalf("command log = %v", m.commandLog)
	}

	*got = ""
	m = send(t, metaBrowsing(t), press('y'), press('d'))
	if m.tab != mainTabData {
		t.Fatalf("y changed the tab to %v", m.tab)
	}
	if !strings.Contains(*got, "CREATE TABLE") {
		t.Fatalf("clipboard after a Data-tab copy = %q", *got)
	}
}

// With no clipboard in the environment a copy degrades to a temp file
// and the log names the path instead of just failing.
func TestCopyDDLFallsBackToFile(t *testing.T) {
	prev := clipboardWrite
	clipboardWrite = func(string) error { return context.Canceled }
	t.Cleanup(func() { clipboardWrite = prev })
	spilled := fakeSpill(t)

	m := send(t, metaBrowsing(t), press(']'), press(']'), press(']'), press('y'), press('d'))
	if !strings.Contains(*spilled, "CREATE TABLE") {
		t.Fatalf("spill file = %q", *spilled)
	}
	if !logContains(m, "no clipboard") || !logContains(m, "/tmp/fake-spill") {
		t.Fatalf("command log = %v", m.commandLog)
	}
}

// When even the temp file cannot be written the copy fails loudly in
// the log rather than silently doing nothing.
func TestCopyDDLFailureIsLogged(t *testing.T) {
	prev := clipboardWrite
	clipboardWrite = func(string) error { return context.Canceled }
	t.Cleanup(func() { clipboardWrite = prev })
	prevSpill := spillFile
	spillFile = func(string, string) (string, error) { return "", context.Canceled }
	t.Cleanup(func() { spillFile = prevSpill })

	m := send(t, metaBrowsing(t), press(']'), press(']'), press(']'), press('y'), press('d'))
	if !logContains(m, "-- copy DDL of orders FAILED") {
		t.Fatalf("command log = %v", m.commandLog)
	}
}

// Opening another relation keeps the selected tab but drops the
// metadata and the scroll positions of the one that was open.
func TestSelectingAnotherTableResetsTabState(t *testing.T) {
	m := send(t, metaBrowsing(t), press(']'))
	m = send(t, m, press('j'), press('j'))
	if m.meta.row[mainTabStructure] == 0 {
		t.Fatal("cursor did not move in the Structure tab")
	}

	m = send(t, m, press('3'))
	if !m.panels[panelTables].selectByName("people") {
		t.Fatalf("people not listed: %v", m.panels[panelTables].items)
	}
	m = send(t, m, special(tea.KeyEnter, 0))

	if m.tab != mainTabStructure {
		t.Errorf("tab = %v, want the Structure tab to survive", m.tab)
	}
	if m.meta.row[mainTabStructure] != 0 {
		t.Errorf("cursor = %d, want it reset for the new relation", m.meta.row[mainTabStructure])
	}
	if m.meta.table != "people" {
		t.Errorf("metadata table = %q, want people", m.meta.table)
	}
	names := make([]string, 0, len(m.meta.cols))
	for _, c := range m.meta.cols {
		names = append(names, c.Name)
	}
	if strings.Join(names, ",") != "id,email" {
		t.Errorf("columns = %v, want [id email]", names)
	}
}

// A stale reply — one for a relation the user has already left — is
// dropped instead of overwriting the metadata on screen.
func TestStaleMetadataReplyIsDropped(t *testing.T) {
	m := send(t, metaBrowsing(t), press(']'))
	stale := metaLoadedMsg{
		req:   m.meta.req - 1,
		conn:  m.active,
		table: m.data.table,
		cols:  []db.Column{{Name: "ghost"}},
	}
	next, _ := m.Update(stale)
	m = next.(Model)
	for _, c := range m.meta.cols {
		if c.Name == "ghost" {
			t.Fatal("a stale reply overwrote the metadata on screen")
		}
	}
}

// Introspection failures land in the log and in the tab, and never
// clear the relation.
func TestMetadataErrorIsShown(t *testing.T) {
	m := send(t, metaBrowsing(t), press(']'))
	next, _ := m.Update(metaLoadedMsg{
		req:   m.meta.req,
		conn:  m.active,
		table: m.data.table,
		err:   context.DeadlineExceeded,
	})
	m = next.(Model)
	if !strings.Contains(m.View().Content, context.DeadlineExceeded.Error()) {
		t.Errorf("the Structure tab does not show the failure")
	}
}

// R on an introspection tab re-reads the metadata rather than the page.
func TestRefreshReloadsMetadata(t *testing.T) {
	m := send(t, metaBrowsing(t), press(']'))
	before := m.meta.req
	m = send(t, m, press('R'))
	if m.meta.req == before {
		t.Fatalf("R did not re-read the metadata (req still %d)", before)
	}
	if !m.meta.loaded {
		t.Fatal("metadata not reloaded")
	}
}

func TestColumnKeyInfo(t *testing.T) {
	idx := []db.Index{
		{Name: "pk", Columns: []string{"id"}, Unique: true, Primary: true},
		{Name: "uq", Columns: []string{"email"}, Unique: true},
		{Name: "ix", Columns: []string{"org_id"}},
	}
	fks := []db.ForeignKey{{Columns: []string{"org_id"}, RefTable: "orgs"}}
	cases := []struct {
		col  db.Column
		want string
	}{
		{db.Column{Name: "id", PrimaryKey: true}, "PK"},
		{db.Column{Name: "email"}, "UNI"},
		{db.Column{Name: "org_id"}, "IDX,FK"},
		{db.Column{Name: "note"}, ""},
	}
	for _, c := range cases {
		if got := columnKeyInfo(c.col, idx, fks); got != c.want {
			t.Errorf("columnKeyInfo(%s) = %q, want %q", c.col.Name, got, c.want)
		}
	}
}
