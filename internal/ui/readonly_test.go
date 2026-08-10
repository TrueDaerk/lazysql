package ui

import (
	"context"
	"strings"
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"lazysql/internal/db"
)

// readOnlyGrid opens the `grid` fixture over a read-only connection. The
// fixture is seeded through a writable connection first: SQLite's
// `mode=ro` cannot create the database file.
func readOnlyGrid(t *testing.T) Model {
	t.Helper()
	seed := dataBrowsing(t)
	seed.closeSession()

	m := sized(120, 40)
	m.cfg.Connections[0].ReadOnly = true
	m.refreshConnections(m.cfg.Connections[0].Name)
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.driver == nil {
		t.Fatal("read-only fixture connection did not open")
	}
	if !m.driver.ReadOnly() {
		t.Fatal("the session opened read-write for a read-only profile")
	}
	m = send(t, m, press('2'), press('R'))
	if !m.panels[panelObjects].selectByName("grid") {
		t.Fatalf("fixture table not listed: %v", m.panels[panelObjects].items)
	}
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.focus != panelMain {
		t.Fatalf("focus = %v, want the data grid", m.focus)
	}
	m.commandLog = nil
	return m
}

// rowsInGrid reads the fixture's row count through the live (read-only)
// connection, which is what proves a blocked write never happened.
func rowsInGrid(t *testing.T, m Model) int64 {
	t.Helper()
	rs, err := m.driver.Query(context.Background(), "SELECT COUNT(*) FROM grid")
	if err != nil {
		t.Fatalf("count rows: %v", err)
	}
	n, ok := rs.Rows[0][0].(int64)
	if !ok {
		t.Fatalf("count = %v (%T), want an integer", rs.Rows[0][0], rs.Rows[0][0])
	}
	return n
}

// Browsing itself is unaffected: the page loads and the grid renders.
func TestReadOnlyStillBrowses(t *testing.T) {
	m := readOnlyGrid(t)
	if len(m.data.rows) == 0 {
		t.Fatal("no rows loaded on a read-only connection")
	}
}

// Every staging key answers with the same status message and stages
// nothing.
func TestReadOnlyBlocksStagingKeys(t *testing.T) {
	cases := []struct {
		key  rune
		want string
	}{
		{'e', "-- edit blocked: connection is read-only"},
		{'d', "-- delete blocked: connection is read-only"},
		{'n', "-- insert blocked: connection is read-only"},
		{'D', "-- duplicate blocked: connection is read-only"},
	}
	for _, c := range cases {
		t.Run(string(c.key), func(t *testing.T) {
			m := readOnlyGrid(t)
			m = send(t, m, press(c.key))
			if m.modal != nil {
				t.Fatalf("%q opened %T, want no modal", c.key, m.modal)
			}
			if m.changes.Len() != 0 {
				t.Fatalf("%q staged %d changes", c.key, m.changes.Len())
			}
			if !logContains(m, c.want) {
				t.Fatalf("log = %v, want %q", m.commandLog, c.want)
			}
		})
	}
}

// A changeset staged before the connection became read-only cannot be
// committed either.
func TestReadOnlyBlocksCommit(t *testing.T) {
	m := readOnlyGrid(t)
	// Stage behind the UI's back: the keys that would normally do it are
	// already blocked, and the commit path is what is under test.
	m.changes.Stage(db.CellChange{
		Database: m.data.database, Table: "grid",
		PKCols: []string{"id"}, PKVals: []any{int64(1)},
		Column: "name", OldValue: "name-1", NewValue: "renamed",
	})
	before := rowsInGrid(t, m)

	m = send(t, m, press('c'))
	if m.modal != nil {
		t.Fatalf("c opened %T, want no commit modal", m.modal)
	}
	if !logContains(m, "-- commit blocked: connection is read-only") {
		t.Fatalf("log = %v, want the commit refusal", m.commandLog)
	}
	if m.changes.Len() != 1 {
		t.Fatal("the changeset was cleared by a blocked commit")
	}
	if got := rowsInGrid(t, m); got != before {
		t.Fatalf("rows = %d, want %d — the commit ran", got, before)
	}
}

// The editor refuses DML, DDL and a data-modifying CTE before the run
// starts, and says why.
func TestReadOnlyEditorRejectsWrites(t *testing.T) {
	for _, script := range []string{
		"DELETE FROM grid",
		"UPDATE grid SET name = 'x' WHERE id = 1",
		"INSERT INTO grid (id, name) VALUES (9999, 'new')",
		"DROP TABLE grid",
		"WITH doomed AS (DELETE FROM grid WHERE id = 1 RETURNING *) SELECT * FROM doomed",
	} {
		t.Run(strings.Fields(script)[0], func(t *testing.T) {
			m := readOnlyGrid(t)
			before := rowsInGrid(t, m)
			m = runQuery(t, m, script)

			if m.run.running {
				t.Fatal("a rejected script started a run")
			}
			if !logContains(m, "REJECTED") || !logContains(m, "connection is read-only") {
				t.Fatalf("log = %v, want a read-only rejection", m.commandLogEntries())
			}
			if !strings.Contains(m.data.err, "connection is read-only") {
				t.Fatalf("Data tab error = %q, want the rejection", m.data.err)
			}
			if got := rowsInGrid(t, m); got != before {
				t.Fatalf("rows = %d, want %d — the statement ran", got, before)
			}
		})
	}
}

// Reads go through untouched: the same editor runs a SELECT and shows it.
func TestReadOnlyEditorRunsSelect(t *testing.T) {
	m := readOnlyGrid(t)
	m = runQuery(t, m, "SELECT id, name FROM grid ORDER BY id LIMIT 3")
	if m.data.err != "" {
		t.Fatalf("SELECT failed on a read-only connection: %s", m.data.err)
	}
	if len(m.data.rows) != 3 {
		t.Fatalf("rows = %d, want the 3 the SELECT asked for", len(m.data.rows))
	}
}

// A read-write connection is unchanged by any of this.
func TestReadWriteConnectionStillStages(t *testing.T) {
	m := dataBrowsing(t)
	m = send(t, m, press('l'), press('e'))
	if _, ok := m.modal.(*editCellModal); !ok {
		t.Fatalf("e opened %T on a read-write connection, want the edit modal", m.modal)
	}
}

// The lock is visible where a write would be attempted: on the profile in
// panel [1] and in the main view's title.
func TestReadOnlyLockIndicator(t *testing.T) {
	m := readOnlyGrid(t)
	out := m.View().Content
	if !strings.Contains(out, lockMark) {
		t.Fatalf("no lock indicator on screen:\n%s", out)
	}
	if !strings.Contains(out, "read-only") {
		t.Fatalf("main view does not name the mode:\n%s", out)
	}
	// The panel keeps the undecorated name, so selection and lookup still
	// work on it.
	if got := m.panels[panelConnections].selected(); got != "local-sqlite" {
		t.Fatalf("selected profile = %q, want the undecorated name", got)
	}
}

// The options bar drops the keys that could only answer "read-only",
// while `?` keeps documenting every binding.
func TestReadOnlyHidesWriteBindingsFromOptionsBar(t *testing.T) {
	m := readOnlyGrid(t)
	for _, desc := range []string{"edit cell", "stage row delete", "insert row",
		"duplicate row", "commit staged changes"} {
		if offersBinding(m.optionsBarBindings(), desc) {
			t.Errorf("options bar still offers %q", desc)
		}
		if !offersBinding(flattenGroups(m.keys.helpGroups(panelMain)), desc) {
			t.Errorf("`?` no longer documents %q", desc)
		}
	}
	rw := dataBrowsing(t)
	if !offersBinding(rw.optionsBarBindings(), "edit cell") {
		t.Error("read-write options bar lost its write keys")
	}
}

func offersBinding(bindings []key.Binding, desc string) bool {
	for _, b := range bindings {
		if b.Help().Desc == desc {
			return true
		}
	}
	return false
}

func flattenGroups(groups [][]key.Binding) []key.Binding {
	var out []key.Binding
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

// The form writes the flag onto the profile, and an untouched form leaves
// a read-write profile read-write.
func TestConnectionFormPersistsReadOnly(t *testing.T) {
	f := newConnectionForm("New connection", testConnections()[0], "")
	fl := f.field("read_only")
	if fl == nil {
		t.Fatal("the form has no read-only field")
	}
	if fl.on {
		t.Fatal("a read-write profile opened the form with read-only on")
	}
	c, _, err := f.toConnection()
	if err != nil {
		t.Fatal(err)
	}
	if c.ReadOnly {
		t.Fatal("an untouched form turned the profile read-only")
	}

	fl.on = true
	c, _, err = f.toConnection()
	if err != nil {
		t.Fatal(err)
	}
	if !c.ReadOnly {
		t.Fatal("the toggle did not reach the profile")
	}

	// Editing a read-only profile opens with the toggle already on.
	back := newConnectionForm("Edit", c, c.Name)
	if !back.field("read_only").on {
		t.Fatal("the form did not load the profile's read-only flag")
	}
}
