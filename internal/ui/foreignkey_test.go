package ui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/db"
)

// fkBrowsing opens the `orders` fixture in the grid: a child table with
// a single-column key to `customers`, a composite key to `tenants` and a
// NULL key value on the last row.
func fkBrowsing(t *testing.T) Model {
	t.Helper()
	m := browsing(t)
	ctx := context.Background()
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS orders`,
		`DROP TABLE IF EXISTS customers`,
		`DROP TABLE IF EXISTS tenants`,
		`CREATE TABLE customers (id INTEGER PRIMARY KEY, name TEXT)`,
		`CREATE TABLE tenants (tenant INTEGER, code TEXT, label TEXT,
			PRIMARY KEY (tenant, code))`,
		`CREATE TABLE orders (
			id INTEGER PRIMARY KEY,
			customer_id INTEGER REFERENCES customers (id),
			tenant INTEGER,
			code TEXT,
			FOREIGN KEY (tenant, code) REFERENCES tenants (tenant, code))`,
		`INSERT INTO customers (id, name) VALUES (1, 'alice'), (2, 'bob')`,
		`INSERT INTO tenants (tenant, code, label) VALUES (1, 'a', 'one-a'), (2, 'b', 'two-b')`,
		`INSERT INTO orders (id, customer_id, tenant, code) VALUES
			(10, 2, 2, 'b'),
			(11, NULL, NULL, NULL)`,
	} {
		if _, err := m.driver.Exec(ctx, stmt); err != nil {
			t.Fatalf("fixture %q: %v", stmt, err)
		}
	}
	m = send(t, m, press('2'), press('R'))
	if !m.panels[panelObjects].selectByName("orders") {
		t.Fatalf("fixture table not listed: %v", m.panels[panelObjects].items)
	}
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.focus != panelMain {
		t.Fatalf("focus = %v, want the data grid", m.focus)
	}
	return m
}

// colCursor moves the cell cursor onto a named column.
func colCursor(t *testing.T, m Model, name string) Model {
	t.Helper()
	for i, c := range m.data.cols {
		if c.Name == name {
			m.data.col = i
			return m
		}
	}
	t.Fatalf("column %q not in %v", name, m.data.cols)
	return m
}

// Opening a relation fetches its foreign keys without being asked, so
// the header can mark the columns that have one.
func TestOpenTableCachesForeignKeys(t *testing.T) {
	m := fkBrowsing(t)
	fks, ok := m.tableFKs()
	if !ok {
		t.Fatal("foreign keys of the open relation were not cached")
	}
	if len(fks) != 2 {
		t.Fatalf("got %d foreign keys, want 2: %+v", len(fks), fks)
	}
	set := m.fkColumnSet()
	for _, c := range []string{"customer_id", "tenant", "code"} {
		if !set[c] {
			t.Errorf("%s is not marked as a foreign key column: %v", c, set)
		}
	}
	if set["id"] {
		t.Error("the primary key is marked as a foreign key column")
	}
}

// The grid header marks foreign key columns, and only those.
func TestGridHeaderMarksForeignKeys(t *testing.T) {
	m := fkBrowsing(t)
	cols, _ := m.buildGrid()
	byName := map[string]string{}
	for i, c := range cols {
		byName[m.data.cols[i].Name] = c.header
	}
	if got := byName["customer_id"]; !strings.HasSuffix(got, fkMark) {
		t.Errorf("customer_id header = %q, want the %q mark", got, fkMark)
	}
	if got := byName["id"]; strings.Contains(got, fkMark) {
		t.Errorf("id header = %q, want no foreign key mark", got)
	}
}

// `g` on a single-column key opens the referenced table showing exactly
// the referenced row.
func TestFollowFKSingleColumn(t *testing.T) {
	m := fkBrowsing(t)
	m = colCursor(t, m, "customer_id")
	m = send(t, m, press('g'))

	if m.data.table != "customers" {
		t.Fatalf("table = %q, want customers", m.data.table)
	}
	if len(m.data.rows) != 1 {
		t.Fatalf("got %d rows, want exactly the referenced one: %+v", len(m.data.rows), m.data.rows)
	}
	if name := db.FormatValue(m.data.rows[0][1], "NULL"); name != "bob" {
		t.Errorf("followed to %q, want bob", name)
	}
	if m.data.filter == nil || m.data.filter.Verbatim {
		t.Errorf("filter = %+v, want a parameterized one", m.data.filter)
	}
	if got := len(db.FilterArgs(m.data.filter)); got != 1 {
		t.Errorf("filter args = %d, want 1", got)
	}
	// The [3] panel follows along so the shell does not claim another
	// relation is open.
	if got := m.panels[panelObjects].selected(); got != "customers" {
		t.Errorf("panel [3] selection = %q, want customers", got)
	}
}

// A composite key builds one condition per column pair.
func TestFollowFKComposite(t *testing.T) {
	m := fkBrowsing(t)
	m = colCursor(t, m, "code")
	m = send(t, m, press('g'))

	if m.data.table != "tenants" {
		t.Fatalf("table = %q, want tenants", m.data.table)
	}
	if got := len(db.FilterArgs(m.data.filter)); got != 2 {
		t.Fatalf("filter args = %d, want 2: %+v", got, m.data.filter)
	}
	if len(m.data.rows) != 1 {
		t.Fatalf("got %d rows, want exactly the referenced one: %+v", len(m.data.rows), m.data.rows)
	}
	if label := db.FormatValue(m.data.rows[0][2], "NULL"); label != "two-b" {
		t.Errorf("followed to %q, want two-b", label)
	}
}

// A NULL key value goes nowhere and says why.
func TestFollowFKOnNULLExplains(t *testing.T) {
	m := fkBrowsing(t)
	m = colCursor(t, m, "customer_id")
	m.data.row = 1 // the row whose customer_id is NULL
	m = send(t, m, press('g'))

	if m.data.table != "orders" {
		t.Fatalf("table = %q, want to stay on orders", m.data.table)
	}
	if len(m.browseStack) != 0 {
		t.Errorf("a refused jump pushed %d history entries", len(m.browseStack))
	}
	if !logContains(m, "customer_id is NULL") {
		t.Errorf("the log does not explain the NULL: %v", logText(m))
	}
}

// A column with no constraint on it is a no-op with a note.
func TestFollowFKOnPlainColumnExplains(t *testing.T) {
	m := fkBrowsing(t)
	m = colCursor(t, m, "id")
	m = send(t, m, press('g'))

	if m.data.table != "orders" {
		t.Fatalf("table = %q, want to stay on orders", m.data.table)
	}
	if !logContains(m, "not part of a foreign key") {
		t.Errorf("the log does not explain the no-op: %v", logText(m))
	}
}

// The back binding restores the previous table, its filter and the cell
// cursor. `esc` does the same before it hands the key to the focus stack.
func TestBrowseBackRestoresPreviousState(t *testing.T) {
	for _, back := range []struct {
		name string
		key  tea.KeyPressMsg
	}{
		{"ctrl+o", ctrl('o')},
		{"esc", special(tea.KeyEscape, 0)},
	} {
		t.Run(back.name, func(t *testing.T) {
			m := fkBrowsing(t)
			m = send(t, m, press('s')) // sort on id, so the state is not the default
			m = colCursor(t, m, "customer_id")
			m.data.row = 0
			wantCol, wantSort := m.data.col, m.data.sort

			m = send(t, m, press('g'))
			if m.data.table != "customers" {
				t.Fatalf("table = %q, want customers", m.data.table)
			}

			m = send(t, m, back.key)
			if m.data.table != "orders" {
				t.Fatalf("table = %q, want orders back", m.data.table)
			}
			if m.focus != panelMain {
				t.Errorf("focus = %v, want the grid to keep it", m.focus)
			}
			if m.data.col != wantCol {
				t.Errorf("column cursor = %d, want %d", m.data.col, wantCol)
			}
			if m.data.sort == nil || wantSort == nil || *m.data.sort != *wantSort {
				t.Errorf("sort = %+v, want %+v", m.data.sort, wantSort)
			}
			if m.data.filter != nil {
				t.Errorf("filter = %+v, want the unfiltered page back", m.data.filter)
			}
			if len(m.data.rows) != 2 {
				t.Errorf("got %d rows, want the whole table back", len(m.data.rows))
			}
			if len(m.browseStack) != 0 {
				t.Errorf("history left %d entries", len(m.browseStack))
			}
			// With the history empty, the key means what it always meant.
			m = send(t, m, back.key)
			if back.name == "esc" && m.focus == panelMain {
				t.Error("esc on an empty history did not leave the grid")
			}
		})
	}
}

// The reverse direction lists the tables that reference the row under
// the cursor and jumps to the matching rows.
func TestIncomingRefsJumpsToReferencingRows(t *testing.T) {
	m := fkBrowsing(t)
	if !m.panels[panelObjects].selectByName("customers") {
		t.Fatalf("customers not listed: %v", m.panels[panelObjects].items)
	}
	m = send(t, m, press('2'), special(tea.KeyEnter, 0))
	if m.data.table != "customers" {
		t.Fatalf("table = %q, want customers", m.data.table)
	}
	m.data.row = 1 // customer 2, the one order 10 points at

	m = send(t, m, press('G'))
	menu, ok := m.modal.(*menuModal)
	if !ok {
		t.Fatalf("modal = %T, want the referencing-rows menu; log: %v", m.modal, logText(m))
	}
	if !strings.Contains(menu.title, "customers") {
		t.Errorf("menu title = %q", menu.title)
	}
	if len(menu.entries) != 2 { // one reference plus cancel
		t.Fatalf("menu entries = %d: %+v", len(menu.entries), menu.entries)
	}
	if !strings.Contains(menu.entries[0].label, "orders") {
		t.Errorf("entry = %q, want the orders reference", menu.entries[0].label)
	}

	m = send(t, m, special(tea.KeyEnter, 0))
	if m.data.table != "orders" {
		t.Fatalf("table = %q, want orders", m.data.table)
	}
	if len(m.data.rows) != 1 {
		t.Fatalf("got %d rows, want only the referencing one: %+v", len(m.data.rows), m.data.rows)
	}
	if id := db.FormatValue(m.data.rows[0][0], "NULL"); id != "10" {
		t.Errorf("jumped to order %q, want 10", id)
	}
	// The jump is undoable like any other.
	m = send(t, m, ctrl('o'))
	if m.data.table != "customers" {
		t.Errorf("table = %q, want customers back", m.data.table)
	}
}

// Every foreign-key binding is dispatchable, listed for the main view
// and documented in `?`.
func TestForeignKeyBindingsAreDocumented(t *testing.T) {
	k := newKeyMap()
	want := map[actionID]string{
		actFollowFK:     "g",
		actIncomingRefs: "G",
		actBrowseBack:   "ctrl+o",
	}
	found := map[actionID]bool{}
	for _, a := range k.panelActions(panelMain) {
		if key, ok := want[a.id]; ok {
			found[a.id] = true
			if got := a.binding.Keys(); len(got) == 0 || got[0] != key {
				t.Errorf("action %d bound to %v, want %s", a.id, got, key)
			}
		}
	}
	for id := range want {
		if !found[id] {
			t.Errorf("action %d is missing from the main view's actions", id)
		}
	}
	var listed int
	for _, group := range k.helpGroups(panelMain) {
		for _, b := range group.bindings {
			for _, keyName := range want {
				if len(b.Keys()) > 0 && b.Keys()[0] == keyName {
					listed++
				}
			}
		}
	}
	if listed != len(want) {
		t.Errorf("? lists %d of the %d foreign-key bindings", listed, len(want))
	}
}

// logText is the command log as plain lines, for assertions about why
// an action refused to run.
func logText(m Model) []string {
	out := make([]string, 0, len(m.commandLog))
	for _, l := range m.commandLog {
		out = append(out, l.text)
	}
	return out
}
