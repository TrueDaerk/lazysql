package ui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/db"
)

// browsing returns a model whose [2] Objects tree holds fixture content
// and a live SQLite connection, so reloads have a driver to talk to.
func browsing(t *testing.T) Model {
	t.Helper()
	m := sized(120, 40)
	m = send(t, m, special(tea.KeyEnter, 0)) // connect the SQLite fixture
	if m.driver == nil {
		t.Fatal("fixture connection did not open")
	}
	return m
}

func TestFuzzyMatch(t *testing.T) {
	cases := []struct {
		pattern, s string
		want       bool
	}{
		{"", "users", true},
		{"usr", "users", true},
		{"usr", "user_roles", true},
		{"USR", "users", true},
		{"srz", "users", false},
		{"userss", "users", false},
	}
	for _, c := range cases {
		if got := fuzzyMatch(c.pattern, c.s); got != c.want {
			t.Errorf("fuzzyMatch(%q, %q) = %v, want %v", c.pattern, c.s, got, c.want)
		}
	}
}

// `/` narrows as you type and esc puts every row back. On the tree the
// filter keeps a branch whose subtree matches, so a hit never loses the
// category it hangs under.
func TestFilterNarrowsAndEscRestores(t *testing.T) {
	m := sized(120, 40)
	m = send(t, m, press('2'), press('/'), press('u'), press('s'))
	p := m.panels[panelObjects]
	if !p.filtering {
		t.Fatal("/ did not open the inline filter")
	}
	if p.filter != "us" {
		t.Fatalf("filter = %q, want %q", p.filter, "us")
	}
	// "accounts" matches too: fuzzy means subsequence, not prefix. The
	// Tables row survives because two of its children matched.
	if got := p.items; len(got) != 3 ||
		got[0] != "Tables" || got[1] != "users" || got[2] != "accounts" {
		t.Fatalf("filtered items = %v, want [Tables users accounts]", got)
	}
	m = send(t, m, special(tea.KeyEscape, 0))
	if p.filter != "" || p.filtering {
		t.Fatalf("esc left filter %q (filtering=%v)", p.filter, p.filtering)
	}
	// Tables + its four rows + the two collapsed categories.
	if len(p.items) != 7 {
		t.Fatalf("items after esc = %v, want the whole tree", p.items)
	}
}

// While the filter captures keys, digits and `q` are text, not commands.
func TestFilterCapturesGlobalKeys(t *testing.T) {
	m := sized(120, 40)
	seedMultiTree(&m, []string{"app_dev", "app_test", "q2_reports"})
	m = send(t, m, press('2'), press('/'), press('q'), press('2'))
	if m.focus != panelObjects {
		t.Fatalf("focus = %v, want the filter to swallow the digit", m.focus)
	}
	p := m.panels[panelObjects]
	if p.filter != "q2" {
		t.Fatalf("filter = %q, want %q", p.filter, "q2")
	}
	if got := p.items; len(got) != 1 || got[0] != "q2_reports" {
		t.Fatalf("items = %v, want [q2_reports]", got)
	}
	// enter keeps the filter but hands the panel's keys back.
	m = send(t, m, special(tea.KeyEnter, 0))
	if p.filtering {
		t.Fatal("enter did not leave filter input mode")
	}
	if p.filter != "q2" {
		t.Fatalf("enter dropped the filter: %q", p.filter)
	}
}

// A filter that matches nothing leaves an empty panel, and enter on it is a
// no-op rather than a crash.
func TestFilterWithNoMatchIsSafe(t *testing.T) {
	m := sized(120, 40)
	m = send(t, m, press('2'), press('/'), press('z'), press('z'), press('z'))
	if got := m.panels[panelObjects].items; len(got) != 0 {
		t.Fatalf("items = %v, want none", got)
	}
	m = send(t, m, special(tea.KeyEnter, 0), special(tea.KeyEscape, 0))
	if len(m.panels[panelObjects].items) != 7 {
		t.Fatalf("esc did not restore the tree: %v", m.panels[panelObjects].items)
	}
}

// The tree's three levels: a collapsed category shows nothing, `enter`
// expands it and `enter` again folds it back.
func TestTreeExpandsAndCollapsesCategories(t *testing.T) {
	m := sized(120, 40)
	seedTree(&m, []string{"users", "accounts"}, []string{"active_users"})
	m = send(t, m, press('2'))
	if got := m.panels[panelObjects].items; len(got) != 5 {
		t.Fatalf("items = %v, want Tables + 2 tables + Views + Triggers", got)
	}
	m = treeSelect(t, m, "Views")
	m = send(t, m, special(tea.KeyEnter, 0))
	if got := m.panels[panelObjects].items; len(got) != 6 || got[4] != "active_users" {
		t.Fatalf("items after expanding Views = %v", got)
	}
	m = treeSelect(t, m, "Views")
	m = send(t, m, special(tea.KeyEnter, 0))
	if got := m.panels[panelObjects].items; len(got) != 5 {
		t.Fatalf("items after collapsing Views = %v", got)
	}
}

// `l` opens a branch and steps into it; `h` closes it and steps back out.
func TestTreeExpandCollapseKeys(t *testing.T) {
	m := sized(120, 40)
	seedTree(&m, []string{"users"}, []string{"active_users"})
	m = send(t, m, press('2'))
	m = treeSelect(t, m, "Views")
	m = send(t, m, press('l'))
	if !m.tree.category("", catViews).expanded {
		t.Fatal("l did not expand the Views category")
	}
	// Already open: l steps onto the first child.
	m = send(t, m, press('l'))
	if got := m.selectedNode().name; got != "active_users" {
		t.Fatalf("l on an open branch selected %q", got)
	}
	// h on a leaf steps out to the parent, h again folds it.
	m = send(t, m, press('h'))
	if got := m.selectedNode().name; got != "Views" {
		t.Fatalf("h on a leaf selected %q, want Views", got)
	}
	m = send(t, m, press('h'))
	if m.tree.category("", catViews).expanded {
		t.Fatal("h did not collapse the Views category")
	}
}

// Connecting a single-namespace engine skips the database level entirely
// and loads its tables without a pick.
func TestSingleDatabaseEngineGoesStraightToTables(t *testing.T) {
	m := browsing(t)
	if !m.tree.single {
		t.Fatalf("tree kept a database level for a single namespace: %v", m.tree.databaseNames())
	}
	if m.database != "" {
		t.Fatalf("database = %q, want the driver default", m.database)
	}
	if m.focus != panelObjects {
		t.Fatalf("focus = %v, want %v", m.focus, panelObjects)
	}
	tables := m.tree.category("", catTables)
	if !tables.expanded || !tables.loaded || tables.loading {
		t.Fatalf("Tables category = %+v, want expanded and loaded", tables)
	}
	if !contains(m.panels[panelObjects].items, "Triggers") {
		t.Fatalf("tree = %v, want a Triggers category", m.panels[panelObjects].items)
	}
}

// Expanding a category runs its introspection query and fills the node;
// a second expand is served from the cache.
func TestCategoryLoadsLazilyAndCaches(t *testing.T) {
	m := browsing(t)
	trig := m.tree.category("", catTriggers)
	if trig.loaded {
		t.Fatal("the Triggers category was read before it was expanded")
	}
	m = treeSelect(t, m, "Triggers")
	m = send(t, m, special(tea.KeyEnter, 0))
	if !m.tree.category("", catTriggers).loaded {
		t.Fatal("expanding Triggers did not load it")
	}

	before := len(m.driver.Logger().Entries())
	m = treeSelect(t, m, "Triggers")
	m = send(t, m, special(tea.KeyEnter, 0)) // collapse
	m = send(t, m, special(tea.KeyEnter, 0)) // expand again
	if got := len(m.driver.Logger().Entries()); got != before {
		t.Fatalf("re-expanding queried the server again: %d entries, want %d", got, before)
	}
}

// A trigger is listed under its category, and `enter` shows its
// definition in the main view.
func TestTriggerDefinitionOpensInMainView(t *testing.T) {
	m := browsing(t)
	ctx := context.Background()
	if _, err := m.driver.Exec(ctx,
		`CREATE TABLE IF NOT EXISTS trig_src (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := m.driver.Exec(ctx, `CREATE TRIGGER IF NOT EXISTS trig_src_ai
		AFTER INSERT ON trig_src BEGIN SELECT 1; END`); err != nil {
		t.Fatal(err)
	}

	m = treeSelect(t, m, "Triggers")
	m = send(t, m, special(tea.KeyEnter, 0))
	if !contains(m.panels[panelObjects].items, "trig_src_ai") {
		t.Fatalf("tree = %v, want the trigger listed", m.panels[panelObjects].items)
	}

	m = treeSelect(t, m, "trig_src_ai")
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.trigger == nil {
		t.Fatal("enter on a trigger opened nothing")
	}
	if m.trigger.loading || m.trigger.err != "" {
		t.Fatalf("trigger view = %+v", m.trigger)
	}
	if !strings.Contains(m.trigger.ddl, "CREATE TRIGGER") {
		t.Fatalf("definition = %q", m.trigger.ddl)
	}
	if m.focus != panelMain {
		t.Fatalf("focus = %v, want the main view", m.focus)
	}
	if out := m.View().Content; !strings.Contains(out, "trig_src_ai") {
		t.Error("the main view does not name the trigger")
	}
	// esc closes it and hands the focus back to the tree.
	m = send(t, m, special(tea.KeyEscape, 0))
	if m.trigger != nil {
		t.Fatal("esc did not close the trigger view")
	}
	if m.focus != panelObjects {
		t.Fatalf("focus after esc = %v, want %v", m.focus, panelObjects)
	}
}

// Reloading re-runs the category's query, and the result lands as a message.
func TestReloadPopulatesTreeFromDriver(t *testing.T) {
	m := browsing(t)
	ctx := context.Background()
	if _, err := m.driver.Exec(ctx, `CREATE TABLE IF NOT EXISTS widgets (id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := m.driver.Exec(ctx, `CREATE VIEW IF NOT EXISTS widget_ids AS SELECT id FROM widgets`); err != nil {
		t.Fatal(err)
	}

	m = treeSelect(t, m, "Tables")
	m = send(t, m, press('R'))
	if !contains(m.panels[panelObjects].items, "widgets") {
		t.Fatalf("tables = %v, want to contain widgets", m.panels[panelObjects].items)
	}
	if !logContains(m, "-- reload tables of") {
		t.Fatalf("command log = %v", m.commandLog)
	}
	// Views come from the same round trip, so they are already there.
	m = treeSelect(t, m, "Views")
	m = send(t, m, special(tea.KeyEnter, 0))
	if !contains(m.panels[panelObjects].items, "widget_ids") {
		t.Fatalf("views = %v, want to contain widget_ids", m.panels[panelObjects].items)
	}
}

// A failed load keeps the tree's previous content and reports in the log.
func TestFailedLoadKeepsPreviousContent(t *testing.T) {
	m := browsing(t)
	before := append([]string(nil), m.panels[panelObjects].items...)
	if len(before) == 0 {
		t.Fatal("fixture connection listed no tables")
	}
	m = send(t, m, relationsLoadedMsg{
		conn:     m.active,
		database: m.database,
		err:      context.DeadlineExceeded,
	})
	if got := m.panels[panelObjects].items; !equal(got, before) {
		t.Fatalf("items = %v, want the previous %v", got, before)
	}
	if m.tree.category("", catTables).loading {
		t.Fatal("loading flag survived the error")
	}
	if !logContains(m, "FAILED") {
		t.Fatalf("command log = %v", m.commandLog)
	}
}

// Replies for a connection that is no longer live are dropped.
func TestStaleLoadIsIgnored(t *testing.T) {
	m := browsing(t)
	before := append([]string(nil), m.panels[panelObjects].items...)
	m = send(t, m, relationsLoadedMsg{
		conn:      "some-other-connection",
		database:  m.database,
		relations: []db.Relation{{Name: "ghost", Kind: db.RelationTable}},
	})
	if got := m.panels[panelObjects].items; !equal(got, before) {
		t.Fatalf("stale reply landed: %v", got)
	}
}

// Reloading without a connection must not dial anything.
func TestReloadWithoutConnectionIsANoop(t *testing.T) {
	m := sized(120, 40)
	m = send(t, m, press('2'), press('R'))
	if !logContains(m, "not connected") {
		t.Fatalf("command log = %v", m.commandLog)
	}
	if m.panels[panelObjects].loading {
		t.Fatal("panel entered a loading state with no driver")
	}
}

// The filter must not leak into the next connection's listing.
func TestConnectResetsBrowseState(t *testing.T) {
	m := browsing(t)
	m = send(t, m, press('/'), press('u'))
	if m.panels[panelObjects].filter == "" {
		t.Fatal("filter was not set")
	}
	// enter leaves input mode with the filter still applied, so the digit
	// jumps panels instead of typing.
	m = send(t, m, special(tea.KeyEnter, 0), press('1'), special(tea.KeyEnter, 0))
	p := m.panels[panelObjects]
	if p.filter != "" || p.filtering {
		t.Fatalf("filter survived the reconnect: %q", p.filter)
	}
}

// The rendered panel shows the tree's categories, the chevrons, the
// filter prompt and the loading marker.
func TestPanelRendersBrowseChrome(t *testing.T) {
	m := sized(120, 40)
	m = send(t, m, press('2'))
	out := m.View().Content
	for _, want := range []string{"Objects", "▾ Tables", "▸ Views", "▸ Triggers"} {
		if !strings.Contains(out, want) {
			t.Errorf("view is missing %q", want)
		}
	}

	m = send(t, m, press('/'), press('u'))
	m.panels[panelObjects].loading = true
	out = m.View().Content
	for _, want := range []string{"/u", "loading…"} {
		if !strings.Contains(out, want) {
			t.Errorf("view is missing %q", want)
		}
	}
}

// A category still being read says so on its own row rather than looking
// empty.
func TestLoadingCategoryShowsOnTheNode(t *testing.T) {
	m := sized(120, 40)
	m = send(t, m, press('2'))
	m.tree.category("", catViews).loading = true
	m.refreshTree()
	if out := m.View().Content; !strings.Contains(out, "loading…") {
		t.Error("the loading category is not marked in the tree")
	}
}

// A connection with several namespaces keeps the database level. It
// starts collapsed, and opening a database node costs no query: the
// category set is fixed.
func TestMultiNamespaceTreeKeepsDatabaseLevel(t *testing.T) {
	m := browsing(t)
	if _, err := m.driver.Exec(context.Background(),
		`CREATE TABLE IF NOT EXISTS nsprobe (id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	m = send(t, m, databasesLoadedMsg{conn: m.active, databases: []string{"main", "extra"}})
	if m.tree.single {
		t.Fatal("two namespaces collapsed into the single-namespace shape")
	}
	if got := m.panels[panelObjects].items; len(got) != 2 ||
		got[0] != "main" || got[1] != "extra" {
		t.Fatalf("tree roots = %v, want the two namespaces", got)
	}

	before := len(m.driver.Logger().Entries())
	m = treeSelect(t, m, "main")
	m = send(t, m, special(tea.KeyEnter, 0))
	if got := len(m.driver.Logger().Entries()); got != before {
		t.Fatalf("expanding a database queried the server: %d entries, want %d", got, before)
	}
	if got := m.panels[panelObjects].items; len(got) != 5 || got[1] != "Tables" {
		t.Fatalf("items after expanding main = %v, want its three categories", got)
	}

	// Opening a relation there makes it the browsed namespace.
	m = treeSelect(t, m, "Tables")
	m = send(t, m, special(tea.KeyEnter, 0))
	if !contains(m.panels[panelObjects].items, "nsprobe") {
		t.Fatalf("tables of main = %v", m.panels[panelObjects].items)
	}
	m = treeSelect(t, m, "nsprobe")
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.database != "main" {
		t.Fatalf("database = %q, want the opened object's namespace", m.database)
	}
	if m.data.table != "nsprobe" {
		t.Fatalf("table = %q, want nsprobe", m.data.table)
	}
	if !logContains(m, "USE main;") {
		t.Fatalf("command log = %v", m.commandLog)
	}
}

// An engine without the concept says so on the node instead of looking
// like an empty category.
func TestUnsupportedCategoryIsMarkedNotSupported(t *testing.T) {
	m := sized(120, 40)
	m = send(t, m, press('2'))
	m.tree.category("", catTriggers).expanded = true
	m = send(t, m, triggersLoadedMsg{conn: m.active, database: "", err: db.ErrUnsupported})
	c := m.tree.category("", catTriggers)
	if !c.loaded || c.err != "" || c.hint != "not supported" {
		t.Fatalf("Triggers node = %+v, want a not-supported note", c)
	}
	if out := m.View().Content; !strings.Contains(out, "not supported") {
		t.Error("the tree does not say the engine has no triggers")
	}
}

// The tree's own keys are documented where every other key is.
func TestObjectsPanelDocumentsItsTreeKeys(t *testing.T) {
	k := newKeyMap()
	want := map[string]bool{"expand": false, "collapse": false}
	for _, group := range k.helpGroups(panelObjects) {
		for _, b := range group.bindings {
			if _, ok := want[b.Help().Desc]; ok {
				want[b.Help().Desc] = true
			}
		}
	}
	for desc, found := range want {
		if !found {
			t.Errorf("`?` on [2] Objects does not document %q", desc)
		}
	}
}

func contains(items []string, want string) bool {
	for _, it := range items {
		if it == want {
			return true
		}
	}
	return false
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
