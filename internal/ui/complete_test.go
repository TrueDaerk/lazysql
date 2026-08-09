package ui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"lazysql/internal/db"
	"lazysql/internal/sqlhl"
)

// ---------- helpers ----------

// completing is a connected model with two relations whose columns are
// worth completing, and an editor in insert mode.
func completing(t *testing.T) Model {
	t.Helper()
	m := browsing(t)
	ctx := context.Background()
	for _, stmt := range []string{
		`DROP VIEW IF EXISTS active_customers`,
		`DROP TABLE IF EXISTS customers`,
		`DROP TABLE IF EXISTS invoices`,
		`CREATE TABLE customers (customer_id INTEGER PRIMARY KEY, customer_name TEXT, email TEXT)`,
		`CREATE TABLE invoices (invoice_id INTEGER PRIMARY KEY, customer_id INTEGER, total REAL)`,
		`CREATE VIEW active_customers AS SELECT * FROM customers`,
	} {
		if _, err := m.driver.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	// The relation list is what completion reads; reload it now that the
	// fixtures exist.
	m = send(t, m, press('3'), press('R'))
	if _, ok := m.relationByName("customers"); !ok {
		t.Fatalf("the fixture relations did not load: %v", db.RelationNames(m.relations))
	}
	m = send(t, m, press(':'))
	m.commandLog = nil
	return m
}

// typeInto puts text in the buffer and leaves the caret at rune column
// col of its first line, the way a user who went back to fill in a
// select list would.
func typeInto(m *Model, text string, col int) {
	m.setScript(text)
	moveEditorCursor(&m.editor.area, 0, col)
}

func completionTexts(c completion) []string {
	out := make([]string, 0, len(c.items))
	for _, it := range c.items {
		out = append(out, it.text)
	}
	return out
}

func hasCompletion(c completion, text string) bool {
	for _, it := range c.items {
		if it.text == text {
			return true
		}
	}
	return false
}

// ---------- ranking and filtering ----------

func TestRankPutsPrefixMatchesBeforeSubstringMatches(t *testing.T) {
	items := []completionItem{
		{text: "campus_id", kind: completeColumn},
		{text: "users", kind: completeColumn},
	}
	got := completionTexts(completion{items: rankCompletions("us", items, 0)})
	want := []string{"users", "campus_id"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("rank = %v, want %v", got, want)
	}
}

func TestRankIsCaseInsensitive(t *testing.T) {
	items := []completionItem{{text: "CustomerName", kind: completeColumn}}
	if got := rankCompletions("cUsTo", items, 0); len(got) != 1 {
		t.Fatalf("rank(%q) = %v, want the item to match whatever the case", "cUsTo", got)
	}
	if got := rankCompletions("zzz", items, 0); len(got) != 0 {
		t.Fatalf("rank(%q) = %v, want no match", "zzz", got)
	}
}

// Schema names sort above keywords: the catalog is what a user cannot
// remember, and the keyword list is what they can.
func TestRankPutsSchemaBeforeKeywords(t *testing.T) {
	items := []completionItem{
		{text: "TABLE", kind: completeKeyword},
		{text: "tab_state", kind: completeView},
		{text: "tabs", kind: completeTable},
		{text: "tab_id", kind: completeColumn},
	}
	got := completionTexts(completion{items: rankCompletions("tab", items, 0)})
	want := []string{"tab_id", "tabs", "tab_state", "TABLE"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("rank = %v, want %v", got, want)
	}
}

func TestRankObeysTheLimit(t *testing.T) {
	var items []completionItem
	for _, n := range []string{"a1", "a2", "a3", "a4"} {
		items = append(items, completionItem{text: n, kind: completeColumn})
	}
	if got := rankCompletions("a", items, 2); len(got) != 2 {
		t.Fatalf("rank returned %d items, want the limit of 2", len(got))
	}
}

func TestRankDropsDuplicatesOfOneKind(t *testing.T) {
	items := []completionItem{
		{text: "id", kind: completeColumn, detail: "customers"},
		{text: "id", kind: completeColumn, detail: "invoices"},
	}
	got := rankCompletions("i", items, 0)
	if len(got) != 1 {
		t.Fatalf("rank = %v, want one row for a name two tables share", completionTexts(completion{items: got}))
	}
	if got[0].detail != "customers" {
		t.Errorf("kept %q, want the first (highest ranked) source", got[0].detail)
	}
}

// ---------- reading the buffer ----------

func TestCompletionContextAtReadsTheWordUnderTheCaret(t *testing.T) {
	cases := []struct {
		line          string
		col           int
		word, qualify string
	}{
		{"SELECT cus", 10, "cus", ""},
		{"SELECT cus", 8, "c", ""},
		{"SELECT * FROM ", 14, "", ""},
		{"SELECT customers.na", 19, "na", "customers"},
		{"SELECT customers.", 17, "", "customers"},
		{"SELECT \"my table\".co", 20, "co", "my table"},
		{"SELECT `t`.x", 12, "x", "t"},
		// A numeric literal is not an identifier, so neither half of
		// `1.5` is a word to complete or a relation to qualify with.
		{"SELECT 1.5", 10, "", ""},
		{"SELECT x.1", 10, "", ""},
	}
	for _, c := range cases {
		got := completionContextAt(c.line, c.col)
		if got.word != c.word || got.qualifier != c.qualify {
			t.Errorf("completionContextAt(%q, %d) = word %q qualifier %q, want %q / %q",
				c.line, c.col, got.word, got.qualifier, c.word, c.qualify)
		}
	}
}

func TestCompletionContextClampsAnOutOfRangeCaret(t *testing.T) {
	if got := completionContextAt("abc", 99); got.word != "abc" || got.col != 3 {
		t.Fatalf("out-of-range caret gave %#v", got)
	}
	if got := completionContextAt("abc", -1); got.word != "" || got.col != 0 {
		t.Fatalf("negative caret gave %#v", got)
	}
}

func TestReferencedRelationsScansTheBuffer(t *testing.T) {
	known := []db.Relation{
		{Name: "customers"}, {Name: "invoices"}, {Name: "unused", Kind: db.RelationView},
	}
	src := "SELECT i.total FROM Invoices i JOIN \"customers\" c ON c.id = i.customer_id -- unused"
	got := referencedRelations(sqlhl.SQLite, src, known)
	want := []string{"invoices", "customers"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("referencedRelations = %v, want %v (order of first appearance, comments ignored)", got, want)
	}
}

func TestReferencedRelationsIgnoresStringLiterals(t *testing.T) {
	known := []db.Relation{{Name: "customers"}}
	if got := referencedRelations(sqlhl.SQLite, "SELECT 'customers'", known); len(got) != 0 {
		t.Fatalf("referencedRelations = %v, want a string literal not to count as a reference", got)
	}
}

// ---------- quoting ----------

func TestQuotingOnlyWhereItIsRequired(t *testing.T) {
	cases := []struct {
		engine db.Engine
		name   string
		want   string
	}{
		{db.EngineSQLite, "customers", "customers"},
		{db.EngineSQLite, "customer_id2", "customer_id2"},
		// Spaces and other punctuation always force quoting.
		{db.EngineSQLite, "my table", `"my table"`},
		{db.EngineMySQL, "my table", "`my table`"},
		// So does a keyword, whatever the engine spells it with.
		{db.EngineSQLite, "order", `"order"`},
		{db.EnginePostgres, "select", `"select"`},
		// A dialect-only keyword is quoted only on that dialect.
		{db.EngineDuckDB, "qualify", `"qualify"`},
		{db.EngineMySQL, "qualify", "qualify"},
		// PostgreSQL folds an unquoted identifier, so mixed case has to
		// be quoted to resolve back to itself. Nothing else folds.
		{db.EnginePostgres, "Customers", `"Customers"`},
		{db.EngineMySQL, "Customers", "Customers"},
		{db.EngineSQLite, "Customers", "Customers"},
	}
	for _, c := range cases {
		dialect, err := db.DialectFor(c.engine)
		if err != nil {
			t.Fatal(err)
		}
		got := quoteCompletion(dialect, sqlhl.For(string(c.engine)), c.name)
		if got != c.want {
			t.Errorf("quoteCompletion(%s, %q) = %q, want %q", c.engine, c.name, got, c.want)
		}
	}
}

func TestQuotingIsSkippedWithoutADialect(t *testing.T) {
	if got := quoteCompletion(nil, sqlhl.Generic, "my table"); got != "my table" {
		t.Fatalf("quoteCompletion(nil, …) = %q, want the bare name when nothing is connected", got)
	}
}

// ---------- the cache ----------

func TestSchemaCacheInvalidatesWhenTheDatabaseChanges(t *testing.T) {
	m := sized(120, 40)
	m.active, m.database = "conn", "db1"
	req := m.syncSchema()
	m.schema.cols["customers"] = []string{"customer_id"}
	if got := m.schemaColumns("customers"); len(got) != 1 {
		t.Fatalf("cached columns = %v, want the entry just written", got)
	}

	m.database = "db2"
	if m.syncSchema() == req {
		t.Fatal("switching database did not bump the cache generation")
	}
	if got := m.schemaColumns("customers"); got != nil {
		t.Fatalf("cached columns = %v, want the other database's entry dropped", got)
	}

	// The same for a different connection with the same database name.
	m.schema.cols["customers"] = []string{"customer_id"}
	m.active = "other"
	m.syncSchema()
	if got := m.schemaColumns("customers"); got != nil {
		t.Fatalf("cached columns = %v, want the other connection's entry dropped", got)
	}
}

func TestSchemaReplyForASupersededNamespaceIsDropped(t *testing.T) {
	m := sized(120, 40)
	m.active, m.database = "conn", "db1"
	stale := m.syncSchema()
	m.database = "db2"
	m.syncSchema()

	m.applySchemaColumns(schemaColumnsMsg{
		req: stale, conn: "conn", database: "db1", table: "customers",
		cols: []db.Column{{Name: "customer_id"}},
	})
	if got := m.schemaColumns("customers"); got != nil {
		t.Fatalf("cached columns = %v, want a reply for the old namespace ignored", got)
	}

	// The current namespace's reply lands.
	m.applySchemaColumns(schemaColumnsMsg{
		req: m.schema.req, conn: "conn", database: "db2", table: "customers",
		cols: []db.Column{{Name: "customer_id"}},
	})
	if got := m.schemaColumns("customers"); len(got) != 1 {
		t.Fatalf("cached columns = %v, want the fresh reply stored", got)
	}
}

func TestFailedColumnFetchIsNotRetried(t *testing.T) {
	m := completing(t)
	req := m.syncSchema()
	m.applySchemaColumns(schemaColumnsMsg{
		req: req, conn: m.active, database: m.database, table: "customers",
		err: context.DeadlineExceeded,
	})
	cmd, _ := m.ensureSchemaColumns([]string{"customers"})
	if cmd != nil {
		t.Fatal("a relation whose introspection failed was queued again")
	}
}

// ---------- the popup ----------

// The issue's first acceptance criterion, on a model with no connection
// at all: keywords alone are enough to complete with.
func TestTypingSelOffersSelectAndEnterInsertsIt(t *testing.T) {
	m := sized(120, 40)
	m = send(t, m, press(':'), press('S'), press('E'), press('L'))
	if !m.completion.open {
		t.Fatal("typing three identifier characters did not open the popup")
	}
	if got := m.completion.items[0].text; got != "SELECT" {
		t.Fatalf("best match = %q, want SELECT; list is %v", got, completionTexts(m.completion))
	}
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.script() != "SELECT" {
		t.Fatalf("buffer = %q, want the accepted keyword", m.script())
	}
	if m.completion.open {
		t.Fatal("accepting left the popup open")
	}
	if !m.editor.editing {
		t.Fatal("accepting left insert mode")
	}
}

// One identifier character is not enough to open the popup by itself;
// ctrl+space opens it on nothing at all.
func TestPopupTriggers(t *testing.T) {
	m := sized(120, 40)
	m = send(t, m, press(':'), press('S'))
	if m.completion.open {
		t.Fatalf("the popup opened on one character: %v", completionTexts(m.completion))
	}
	m = send(t, m, tea.KeyPressMsg{Code: ' ', Mod: tea.ModCtrl})
	if !m.completion.open {
		t.Fatal("ctrl+space did not open the popup")
	}
}

// `tab` completes a word and types a tab where there is none, so
// indentation still works.
func TestTabCompletesAWordAndOtherwiseTypes(t *testing.T) {
	m := sized(120, 40)
	m = send(t, m, press(':'), press('S'), special(tea.KeyTab, 0))
	if !m.completion.open {
		t.Fatal("tab after an identifier character did not open the popup")
	}
	// A second tab accepts.
	m = send(t, m, special(tea.KeyTab, 0))
	if m.completion.open {
		t.Fatal("tab did not accept the selection")
	}
	if m.script() == "S" {
		t.Fatal("tab accepted nothing")
	}

	m2 := sized(120, 40)
	m2 = send(t, m2, press(':'), special(tea.KeyTab, 0))
	if m2.completion.open {
		t.Fatal("tab on an empty buffer opened the popup instead of typing")
	}
}

func TestPopupKeysSelectAndAccept(t *testing.T) {
	m := sized(120, 40)
	// `IN` matches a whole family of keywords, so there is a second row
	// to move onto.
	m = send(t, m, press(':'), press('I'), press('N'))
	if len(m.completion.items) < 2 {
		t.Fatalf("suggestions = %v, want at least two", completionTexts(m.completion))
	}
	m = send(t, m, special(tea.KeyDown, 0))
	if m.completion.cursor != 1 {
		t.Fatalf("cursor = %d after down, want 1", m.completion.cursor)
	}
	second := m.completion.items[1].text
	// ctrl+p is the other spelling of up.
	m = send(t, m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if m.completion.cursor != 0 {
		t.Fatalf("cursor = %d after ctrl+p, want 0", m.completion.cursor)
	}
	// The cursor clamps rather than wrapping.
	m = send(t, m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if m.completion.cursor != 0 {
		t.Fatalf("cursor = %d, want it to stop at the top", m.completion.cursor)
	}
	m = send(t, m, tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl}, special(tea.KeyEnter, 0))
	if m.script() != second {
		t.Fatalf("buffer = %q, want the second suggestion %q", m.script(), second)
	}
}

// esc closes the popup and nothing else: the buffer, the mode and the
// focus are all untouched.
func TestEscClosesOnlyThePopup(t *testing.T) {
	m := sized(120, 40)
	m = send(t, m, press(':'), press('S'), press('E'), press('L'))
	m = send(t, m, special(tea.KeyEscape, 0))
	if m.completion.open {
		t.Fatal("esc did not close the popup")
	}
	if !m.editor.editing {
		t.Fatal("esc left insert mode")
	}
	if m.focus != panelQuery {
		t.Fatalf("focus = %v, want the editor panel", m.focus)
	}
	if m.script() != "SEL" {
		t.Fatalf("buffer = %q, want it untouched", m.script())
	}
}

func TestAcceptedIdentifierIsInsertedAtTheCaret(t *testing.T) {
	m := completing(t)
	typeInto(&m, "SELECT  FROM customers", 7)
	m = send(t, m, press('c'), press('u'), press('s'))
	if !m.completion.open {
		t.Fatal("typing did not open the popup")
	}
	m = send(t, m, special(tea.KeyEnter, 0))
	if !strings.HasPrefix(m.script(), "SELECT customer") ||
		!strings.HasSuffix(m.script(), " FROM customers") {
		t.Fatalf("buffer = %q, want the completion spliced in at the caret", m.script())
	}
	// The caret follows the inserted text, so typing continues after it.
	m = send(t, m, press(','))
	if !strings.Contains(m.script(), ", FROM customers") {
		t.Fatalf("buffer = %q, want the caret left at the end of the insertion", m.script())
	}
}

// ---------- schema-aware suggestions ----------

func TestRelationsOfTheCurrentDatabaseAreSuggested(t *testing.T) {
	m := completing(t)
	typeInto(&m, "SELECT * FROM cus", 17)
	m.refreshCompletion(false)
	if !hasCompletion(m.completion, "customers") {
		t.Fatalf("suggestions = %v, want the table", completionTexts(m.completion))
	}
	// Views are offered too, and tagged apart from tables.
	typeInto(&m, "SELECT * FROM act", 17)
	m.refreshCompletion(false)
	found := false
	for _, it := range m.completion.items {
		if it.text == "active_customers" {
			found = it.kind == completeView
		}
	}
	if !found {
		t.Fatalf("suggestions = %v, want the view tagged as one", completionTexts(m.completion))
	}
}

func TestColumnsOfReferencedTablesAreSuggested(t *testing.T) {
	m := completing(t)
	typeInto(&m, "SELECT ema FROM customers", 10)
	m = send(t, m, tea.KeyPressMsg{Code: ' ', Mod: tea.ModCtrl})
	if !hasCompletion(m.completion, "email") {
		t.Fatalf("suggestions = %v, want a column of the referenced table", completionTexts(m.completion))
	}
	// A table the buffer does not mention contributes no columns.
	if hasCompletion(m.completion, "total") {
		t.Fatalf("suggestions = %v, want no columns of an unreferenced table", completionTexts(m.completion))
	}
}

func TestQualifiedWordCompletesOnlyThatRelationsColumns(t *testing.T) {
	m := completing(t)
	typeInto(&m, "SELECT invoices.", 16)
	m = send(t, m, tea.KeyPressMsg{Code: ' ', Mod: tea.ModCtrl})
	if !hasCompletion(m.completion, "total") {
		t.Fatalf("suggestions = %v, want the qualified table's columns", completionTexts(m.completion))
	}
	for _, it := range m.completion.items {
		if it.kind != completeColumn {
			t.Fatalf("suggestions = %v, want columns only after a dot", completionTexts(m.completion))
		}
		if it.detail != "invoices" {
			t.Fatalf("suggestion %q came from %q, want invoices only", it.text, it.detail)
		}
	}
}

// The popup opens on what is cached and grows when the fetch lands; the
// fetch is a command, so Update never waits for it.
func TestPopupOpensBeforeTheColumnsArrive(t *testing.T) {
	m := completing(t)
	// A cold cache: nothing has asked for this relation's columns yet.
	m.schema = schemaCache{}
	typeInto(&m, "SELECT ema FROM customers", 10)

	cmd := m.refreshCompletion(true)
	if !m.completion.open {
		t.Fatal("the popup waited for the metadata instead of showing what it had")
	}
	if !m.completion.loading {
		t.Fatal("the popup does not report the fetch in flight")
	}
	if cmd == nil {
		t.Fatal("no fetch was started for the referenced relation")
	}
	if hasCompletion(m.completion, "email") {
		t.Fatal("a column appeared before its fetch returned")
	}

	for _, msg := range drain(cmd) {
		m = send(t, m, msg)
	}
	if !hasCompletion(m.completion, "email") {
		t.Fatalf("suggestions = %v, want the popup to update when the fetch landed",
			completionTexts(m.completion))
	}
	if m.completion.loading {
		t.Fatal("the popup still reports a fetch in flight")
	}
}

// A refresh triggered by a landed fetch keeps the row the user selected.
func TestLateColumnsKeepTheSelection(t *testing.T) {
	m := completing(t)
	m.schema = schemaCache{}
	typeInto(&m, "SELECT c FROM customers", 8)
	cmd := m.refreshCompletion(true)
	m.moveCompletion(1)
	want := m.completion.items[1].text

	for _, msg := range drain(cmd) {
		m = send(t, m, msg)
	}
	got, ok := m.completion.selected()
	if !ok || got.text != want {
		t.Fatalf("selection = %q, want %q kept across the metadata reply", got.text, want)
	}
}

// The keyword set follows the connected engine.
func TestKeywordSuggestionsDifferPerDriver(t *testing.T) {
	cases := []struct {
		engine     db.Engine
		want, gone string
	}{
		{db.EngineDuckDB, "QUALIFY", "AUTO_INCREMENT"},
		{db.EngineMySQL, "AUTO_INCREMENT", "QUALIFY"},
		{db.EnginePostgres, "ILIKE", "AUTOINCREMENT"},
		{db.EngineSQLite, "AUTOINCREMENT", "ILIKE"},
	}
	for _, c := range cases {
		drv, err := db.Open(c.engine)
		if err != nil {
			t.Fatal(err)
		}
		m := sized(120, 40)
		m.driver = drv
		m = send(t, m, press(':'))
		m.setScript("")
		if cmd := m.refreshCompletion(true); cmd != nil {
			drain(cmd)
		}
		if !hasCompletion(m.completion, c.want) {
			t.Errorf("%s: %q is missing from the suggestions", c.engine, c.want)
		}
		if hasCompletion(m.completion, c.gone) {
			t.Errorf("%s: %q is another dialect's keyword and should not be offered", c.engine, c.gone)
		}
	}
}

// ---------- placement and rendering ----------

// placePopup is where the popup ends up on screen. It is unit-tested
// apart from the layout because the editor lives in the top half of the
// main view, so the flip-above case is not reachable by resizing.
func TestPlacePopup(t *testing.T) {
	cases := []struct {
		name                 string
		ax, ay, w, h, sw, sh int
		wantX, wantY         int
	}{
		{"below the caret", 10, 4, 20, 6, 80, 24, 10, 5},
		{"slid left at the right edge", 70, 4, 20, 6, 80, 24, 60, 5},
		{"clamped left when wider than the screen", 5, 4, 30, 6, 20, 24, 0, 5},
		{"flipped above with no room below", 10, 18, 20, 6, 80, 24, 10, 12},
		{"kept below when neither side fits", 10, 4, 20, 22, 80, 24, 10, 5},
	}
	for _, c := range cases {
		x, y := placePopup(c.ax, c.ay, c.w, c.h, c.sw, c.sh)
		if x != c.wantX || y != c.wantY {
			t.Errorf("%s: placePopup = (%d,%d), want (%d,%d)", c.name, x, y, c.wantX, c.wantY)
		}
	}
}

func TestPopupStaysInsideTheTerminal(t *testing.T) {
	for _, size := range [][2]int{{60, 18}, {80, 24}, {120, 40}, {200, 60}} {
		m := completing(t)
		m = send(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		mm := m
		// A caret at the far right of a long line is the case that would
		// push the box off screen.
		typeInto(&mm, strings.Repeat("x", size[0])+" cus", size[0]+4)
		mm.refreshCompletion(true)
		box, x, y, ok := mm.completionLayer()
		if !ok {
			t.Fatalf("%dx%d: the popup is not on screen", size[0], size[1])
		}
		w, h := lipgloss.Width(box), lipgloss.Height(box)
		if x < 0 || y < 0 {
			t.Errorf("%dx%d: popup at (%d,%d)", size[0], size[1], x, y)
		}
		if x+w > size[0] {
			t.Errorf("%dx%d: popup right edge at %d overflows the terminal", size[0], size[1], x+w)
		}
		if y+h > size[1] {
			t.Errorf("%dx%d: popup bottom edge at %d overflows the terminal", size[0], size[1], y+h)
		}
		if !strings.Contains(mm.View().Content, "customers") {
			t.Errorf("%dx%d: the popup is not in the rendered view", size[0], size[1])
		}
	}
}

func TestPopupIsNotDrawnBehindAModal(t *testing.T) {
	m := sized(120, 40)
	m = send(t, m, press(':'), press('S'), press('E'), press('L'))
	if _, _, _, ok := m.completionLayer(); !ok {
		t.Fatal("the popup is not on screen to begin with")
	}
	m.modal = &confirmModal{title: "anything"}
	if _, _, _, ok := m.completionLayer(); ok {
		t.Fatal("the popup is drawn under an open modal")
	}
}

func TestRendersWithAnOpenPopupAtManySizes(t *testing.T) {
	for _, size := range [][2]int{{60, 18}, {61, 19}, {80, 24}, {100, 30}, {200, 60}} {
		m := sized(size[0], size[1])
		m = send(t, m, press(':'), press('S'), press('E'), press('L'))
		out := m.View().Content
		if lipgloss.Height(out) > size[1] {
			t.Errorf("%dx%d: view is %d rows tall", size[0], size[1], lipgloss.Height(out))
		}
	}
}

// ---------- documentation ----------

func TestCompletionKeysAreDocumented(t *testing.T) {
	k := newKeyMap()
	documented := map[string]bool{}
	for _, group := range k.helpGroups(panelQuery) {
		for _, b := range group {
			documented[b.Help().Key] = true
		}
	}
	for _, b := range k.editorCompletion() {
		if !documented[b.Help().Key] {
			t.Errorf("`?` omits the popup key %q", b.Help().Key)
		}
	}
	if !documented[k.Complete.Help().Key] {
		t.Errorf("`?` omits %q", k.Complete.Help().Key)
	}
}

func TestCompletionKeysAreRebindable(t *testing.T) {
	km := newKeyMap()
	if err := applyKeyOverrides(&km, map[string]string{"complete": "ctrl+j"}); err != nil {
		t.Fatalf("applyKeyOverrides: %v", err)
	}
	if got := km.Complete.Keys(); len(got) != 1 || got[0] != "ctrl+j" {
		t.Fatalf("Complete.Keys() = %v, want [ctrl+j]", got)
	}
}
