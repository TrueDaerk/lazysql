package ui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// relBrowsing opens `orders` in the middle of a foreign-key chain:
// order_items → orders → customers, plus a second child table so the
// incoming half has more than one row to walk.
func relBrowsing(t *testing.T) Model {
	t.Helper()
	m := browsing(t)
	ctx := context.Background()
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS order_items`,
		`DROP TABLE IF EXISTS shipments`,
		`DROP TABLE IF EXISTS orders`,
		`DROP TABLE IF EXISTS customers`,
		`CREATE TABLE customers (id INTEGER PRIMARY KEY, name TEXT)`,
		`CREATE TABLE orders (
			id INTEGER PRIMARY KEY,
			customer_id INTEGER REFERENCES customers (id))`,
		`CREATE TABLE order_items (
			id INTEGER PRIMARY KEY,
			order_id INTEGER REFERENCES orders (id))`,
		`CREATE TABLE shipments (
			id INTEGER PRIMARY KEY,
			order_id INTEGER REFERENCES orders (id))`,
		`INSERT INTO customers (id, name) VALUES (1, 'alice')`,
		`INSERT INTO orders (id, customer_id) VALUES (10, 1)`,
		`INSERT INTO order_items (id, order_id) VALUES (100, 10)`,
		`INSERT INTO shipments (id, order_id) VALUES (200, 10)`,
	} {
		if _, err := m.driver.Exec(ctx, stmt); err != nil {
			t.Fatalf("fixture %q: %v", stmt, err)
		}
	}
	m = send(t, m, press('3'), press('R'))
	if !m.panels[panelTables].selectByName("orders") {
		t.Fatalf("fixture table not listed: %v", m.panels[panelTables].items)
	}
	return send(t, m, special(tea.KeyEnter, 0))
}

// openRelations puts the main view on the Relations tab of the open
// relation.
func openRelations(t *testing.T, m Model) Model {
	t.Helper()
	m = send(t, m, press(']'), press(']'), press(']'), press(']'))
	if m.tab != mainTabRelations {
		t.Fatalf("tab = %v, want Relations", m.tab)
	}
	return m
}

// The tab lists both directions with the columns each constraint binds.
func TestRelationsTabListsBothDirections(t *testing.T) {
	m := openRelations(t, relBrowsing(t))

	edges := m.relationEdges()
	if len(edges) != 3 {
		t.Fatalf("got %d edges, want 3: %+v", len(edges), edges)
	}
	if edges[0].incoming || edges[0].table != "customers" {
		t.Errorf("first edge = %+v, want the outgoing one to customers", edges[0])
	}
	if got := edges[0].label(); got != "customer_id → customers.id" {
		t.Errorf("outgoing label = %q", got)
	}
	incoming := map[string]string{}
	for _, e := range edges[1:] {
		if !e.incoming {
			t.Fatalf("edge %+v is not incoming", e)
		}
		incoming[e.table] = e.label()
	}
	if got := incoming["order_items"]; got != "order_items.order_id → id" {
		t.Errorf("order_items label = %q", got)
	}
	if _, ok := incoming["shipments"]; !ok {
		t.Errorf("shipments does not appear among the incoming edges: %v", incoming)
	}

	out := m.View().Content
	for _, want := range []string{"Relations", "Outgoing", "Incoming", "customers.id", "order_items"} {
		if !strings.Contains(out, want) {
			t.Errorf("view is missing %q", want)
		}
	}
}

// The incoming half is cached per namespace: the scan runs once and the
// second visit reads the cache.
func TestRelationsScanCachedPerDatabase(t *testing.T) {
	m := openRelations(t, relBrowsing(t))
	k := m.namespaceFKKey()
	if _, ok := m.refsCache[k]; !ok {
		t.Fatalf("the namespace scan did not land in the cache: %v", m.refsCache)
	}
	if m.relationsScanning() {
		t.Error("the tab still reports a scan in flight after the reply landed")
	}
	if m.fkLoading[k] {
		t.Error("the scan is still marked in flight")
	}

	// Walking to another table of the same namespace reuses it.
	before := len(m.refsCache)
	m = send(t, m, press('j'), special(tea.KeyEnter, 0))
	if len(m.refsCache) != before {
		t.Errorf("a walk within the namespace re-keyed the cache: %v", m.refsCache)
	}
}

// While the scan is in flight the tab says so instead of claiming the
// relation has no incoming references.
func TestRelationsShowsScanningState(t *testing.T) {
	m := openRelations(t, relBrowsing(t))
	k := m.namespaceFKKey()
	delete(m.refsCache, k)
	m.fkLoading[k] = true

	if !m.relationsScanning() {
		t.Fatal("relationsScanning() is false while the scan is in flight")
	}
	out := strings.Join(m.relationsLines(100, 24), "\n")
	if !strings.Contains(out, "scanning") {
		t.Errorf("no loading state while the scan runs:\n%s", out)
	}
	if strings.Contains(out, "no table in") {
		t.Errorf("an unfinished scan is rendered as an empty result:\n%s", out)
	}
}

// `enter` walks to the selected table with the Relations tab still open,
// and the walk can be repeated to follow a chain.
func TestRelationsWalkFollowsChain(t *testing.T) {
	m := openRelations(t, relBrowsing(t))

	// The cursor starts on the outgoing edge to customers.
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.data.table != "customers" {
		t.Fatalf("table = %q, want customers", m.data.table)
	}
	if m.tab != mainTabRelations {
		t.Fatalf("tab = %v, want the Relations tab to survive the walk", m.tab)
	}
	if m.data.filter != nil {
		t.Errorf("the walk applied a row filter: %+v", m.data.filter)
	}
	if m.panels[panelTables].selected() != "customers" {
		t.Errorf("panel [3] selection = %q, want customers",
			m.panels[panelTables].selected())
	}

	// customers has one incoming edge (orders) and no outgoing ones, so
	// the first entry walks straight back down the chain.
	edges := m.relationEdges()
	if len(edges) != 1 || edges[0].table != "orders" {
		t.Fatalf("customers edges = %+v, want the single incoming one from orders", edges)
	}
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.data.table != "orders" {
		t.Fatalf("table after the second walk = %q, want orders", m.data.table)
	}

	// esc unwinds the walk one table at a time.
	m = send(t, m, special(tea.KeyEscape, 0))
	if m.data.table != "customers" {
		t.Fatalf("table after esc = %q, want customers", m.data.table)
	}
	m = send(t, m, special(tea.KeyEscape, 0))
	if m.data.table != "orders" {
		t.Fatalf("table after the second esc = %q, want orders", m.data.table)
	}
}

// `j`/`k` move the selection over both halves and stop at the ends.
func TestRelationsCursorWalksBothHalves(t *testing.T) {
	m := openRelations(t, relBrowsing(t))
	if got := m.metaRowCount(); got != 3 {
		t.Fatalf("metaRowCount = %d, want 3", got)
	}
	m = send(t, m, press('j'), press('j'))
	if got := m.meta.row[mainTabRelations]; got != 2 {
		t.Fatalf("cursor = %d, want 2", got)
	}
	e, ok := m.selectedRelationEdge()
	if !ok || !e.incoming {
		t.Fatalf("selected edge = %+v (ok=%v), want an incoming one", e, ok)
	}
	m = send(t, m, press('j'))
	if got := m.meta.row[mainTabRelations]; got != 2 {
		t.Fatalf("cursor = %d, want it clamped at 2", got)
	}
	m = send(t, m, press('k'), press('k'), press('k'))
	if got := m.meta.row[mainTabRelations]; got != 0 {
		t.Fatalf("cursor = %d, want it clamped at 0", got)
	}
}

// A wide main view draws the hub box; a narrow one drops the box art and
// keeps the list readable.
func TestRelationsNarrowDegradesToList(t *testing.T) {
	m := openRelations(t, relBrowsing(t))

	wide := strings.Join(m.relationsLines(100, 24), "\n")
	if !strings.Contains(wide, "┌") || !strings.Contains(wide, "orders") {
		t.Errorf("wide render has no hub box:\n%s", wide)
	}

	narrow := m.relationsLines(30, 24)
	joined := strings.Join(narrow, "\n")
	if strings.ContainsAny(joined, "┌┐└┘│") {
		t.Errorf("narrow render still draws the box:\n%s", joined)
	}
	for _, l := range narrow {
		if got := lipgloss.Width(l); got > 30 {
			t.Errorf("line %q is %d wide, want at most 30", l, got)
		}
	}
	if !strings.Contains(joined, "customers") {
		t.Errorf("narrow render lost the outgoing edge:\n%s", joined)
	}
}

// A relation with no foreign keys at all says so in both halves.
func TestRelationsEmptyBothWays(t *testing.T) {
	m := relBrowsing(t)
	if !m.panels[panelTables].selectByName("customers") {
		t.Fatal("customers is not listed")
	}
	m = send(t, m, press('3'), special(tea.KeyEnter, 0))
	m = openRelations(t, m)

	// Force the empty case: customers has an incoming edge, so the test
	// checks the outgoing half only.
	out := strings.Join(m.relationsLines(100, 24), "\n")
	if !strings.Contains(out, "declares no foreign key") {
		t.Errorf("no empty state for the outgoing half:\n%s", out)
	}
}
