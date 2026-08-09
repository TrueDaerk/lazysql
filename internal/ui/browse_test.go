package ui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/db"
)

// browsing returns a model whose [2]/[3] panels hold fixture content and a
// live SQLite connection, so reloads have a driver to talk to.
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

// `/` narrows as you type and esc puts every row back.
func TestFilterNarrowsAndEscRestores(t *testing.T) {
	m := sized(120, 40)
	m = send(t, m, press('3'), press('/'), press('u'), press('s'))
	p := m.panels[panelTables]
	if !p.filtering {
		t.Fatal("/ did not open the inline filter")
	}
	if p.filter != "us" {
		t.Fatalf("filter = %q, want %q", p.filter, "us")
	}
	// "accounts" matches too: fuzzy means subsequence, not prefix.
	if got := p.items; len(got) != 2 || got[0] != "users" || got[1] != "accounts" {
		t.Fatalf("filtered items = %v, want [users accounts]", got)
	}
	m = send(t, m, special(tea.KeyEscape, 0))
	if p.filter != "" || p.filtering {
		t.Fatalf("esc left filter %q (filtering=%v)", p.filter, p.filtering)
	}
	if len(p.items) != 4 {
		t.Fatalf("items after esc = %v, want all four", p.items)
	}
}

// While the filter captures keys, digits and `q` are text, not commands.
func TestFilterCapturesGlobalKeys(t *testing.T) {
	m := sized(120, 40)
	m.panels[panelDatabases].setItems([]string{"app_dev", "app_test", "q2_reports"})
	m = send(t, m, press('2'), press('/'), press('q'), press('2'))
	if m.focus != panelDatabases {
		t.Fatalf("focus = %v, want the filter to swallow the digit", m.focus)
	}
	p := m.panels[panelDatabases]
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
	m = send(t, m, press('3'), press('/'), press('z'), press('z'), press('z'))
	if got := m.panels[panelTables].items; len(got) != 0 {
		t.Fatalf("items = %v, want none", got)
	}
	m = send(t, m, special(tea.KeyEnter, 0), special(tea.KeyEscape, 0))
	if len(m.panels[panelTables].items) != 4 {
		t.Fatalf("esc did not restore the list: %v", m.panels[panelTables].items)
	}
}

// `[`/`]` swap panel [3] between its two sub-tabs without touching the server.
func TestSubTabSwitchesTablesAndViews(t *testing.T) {
	m := sized(120, 40)
	m.relations = []db.Relation{
		{Name: "users", Kind: db.RelationTable},
		{Name: "accounts", Kind: db.RelationTable},
		{Name: "active_users", Kind: db.RelationView},
	}
	m.refreshRelations()
	m = send(t, m, press('3'))
	if got := m.panels[panelTables].items; len(got) != 2 {
		t.Fatalf("tables tab = %v, want the two tables", got)
	}
	m = send(t, m, press(']'))
	if m.tableTab != tabViews {
		t.Fatalf("tab = %v, want tabViews", m.tableTab)
	}
	if got := m.panels[panelTables].items; len(got) != 1 || got[0] != "active_users" {
		t.Fatalf("views tab = %v, want [active_users]", got)
	}
	m = send(t, m, press('['))
	if m.tableTab != tabTables {
		t.Fatalf("tab = %v, want tabTables", m.tableTab)
	}
}

// Connecting a single-namespace engine shows the pseudo entry and loads its
// tables without a database pick.
func TestSingleDatabaseEngineGoesStraightToTables(t *testing.T) {
	m := browsing(t)
	if got := m.panels[panelDatabases].items; len(got) != 1 || got[0] != pseudoDatabase {
		t.Fatalf("databases = %v, want [%s]", got, pseudoDatabase)
	}
	if m.database != "" {
		t.Fatalf("database = %q, want the driver default", m.database)
	}
	if m.focus != panelTables {
		t.Fatalf("focus = %v, want %v", m.focus, panelTables)
	}
	if m.panels[panelTables].loading {
		t.Fatal("tables panel stayed in its loading state")
	}
}

// Both panels reload through commands, and the results land as messages.
func TestReloadPopulatesPanelsFromDriver(t *testing.T) {
	m := browsing(t)
	ctx := context.Background()
	if _, err := m.driver.Exec(ctx, `CREATE TABLE IF NOT EXISTS widgets (id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := m.driver.Exec(ctx, `CREATE VIEW IF NOT EXISTS widget_ids AS SELECT id FROM widgets`); err != nil {
		t.Fatal(err)
	}

	m = send(t, m, press('R'))
	if !contains(m.panels[panelTables].items, "widgets") {
		t.Fatalf("tables = %v, want to contain widgets", m.panels[panelTables].items)
	}
	m = send(t, m, press(']'))
	if !contains(m.panels[panelTables].items, "widget_ids") {
		t.Fatalf("views = %v, want to contain widget_ids", m.panels[panelTables].items)
	}
	if !logContains(m, "-- reload tables of") {
		t.Fatalf("command log = %v", m.commandLog)
	}

	m = send(t, m, press('2'), press('R'))
	if got := m.panels[panelDatabases].items; len(got) != 1 || got[0] != pseudoDatabase {
		t.Fatalf("databases after reload = %v", got)
	}
	if m.panels[panelDatabases].loading {
		t.Fatal("databases panel stayed in its loading state")
	}
}

// A failed load keeps the panel's previous content and reports in the log.
func TestFailedLoadKeepsPreviousContent(t *testing.T) {
	m := browsing(t)
	before := append([]string(nil), m.panels[panelTables].items...)
	if len(before) == 0 {
		t.Fatal("fixture connection listed no tables")
	}
	m.panels[panelTables].loading = true
	m = send(t, m, relationsLoadedMsg{
		conn:     m.active,
		database: m.database,
		err:      context.DeadlineExceeded,
	})
	if got := m.panels[panelTables].items; !equal(got, before) {
		t.Fatalf("items = %v, want the previous %v", got, before)
	}
	if m.panels[panelTables].loading {
		t.Fatal("loading flag survived the error")
	}
	if !logContains(m, "FAILED") {
		t.Fatalf("command log = %v", m.commandLog)
	}
}

// Replies for a connection that is no longer live are dropped.
func TestStaleLoadIsIgnored(t *testing.T) {
	m := browsing(t)
	before := append([]string(nil), m.panels[panelTables].items...)
	m = send(t, m, relationsLoadedMsg{
		conn:      "some-other-connection",
		database:  m.database,
		relations: []db.Relation{{Name: "ghost", Kind: db.RelationTable}},
	})
	if got := m.panels[panelTables].items; !equal(got, before) {
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
	if m.panels[panelDatabases].loading {
		t.Fatal("panel entered a loading state with no driver")
	}
}

// The filter must not leak into the next connection's listing.
func TestConnectResetsBrowseState(t *testing.T) {
	m := browsing(t)
	m = send(t, m, press('/'), press('u'))
	if m.panels[panelTables].filter == "" {
		t.Fatal("filter was not set")
	}
	// enter leaves input mode with the filter still applied, so the digit
	// jumps panels instead of typing.
	m = send(t, m, special(tea.KeyEnter, 0), press('1'), special(tea.KeyEnter, 0))
	p := m.panels[panelTables]
	if p.filter != "" || p.filtering {
		t.Fatalf("filter survived the reconnect: %q", p.filter)
	}
	if m.tableTab != tabTables {
		t.Fatalf("tab = %v, want tabTables", m.tableTab)
	}
}

// The rendered panel shows the sub-tabs, the filter and the loading marker.
func TestPanelRendersBrowseChrome(t *testing.T) {
	m := sized(120, 40)
	m = send(t, m, press('3'), press('/'), press('u'))
	m.panels[panelTables].loading = true
	out := m.View().Content
	for _, want := range []string{"Tables", "Views", "/u", "loading…"} {
		if !strings.Contains(out, want) {
			t.Errorf("view is missing %q", want)
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
