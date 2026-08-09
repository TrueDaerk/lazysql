package ui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/db"
)

// insertForm drives a key that should open the insert form and returns
// it, so a test can fill fields in before submitting.
func insertForm(t *testing.T, m Model, key rune) (Model, *insertRowModal) {
	t.Helper()
	m = send(t, m, press(key))
	f, ok := m.modal.(*insertRowModal)
	if !ok {
		t.Fatalf("%c opened %T, want the insert form", key, m.modal)
	}
	return m, f
}

// setField puts a typed value into one field of the insert form.
func setField(f *insertRowModal, name, value string) {
	for _, fl := range f.fields {
		if fl.col.Name == name {
			fl.mode = insertValue
			fl.input.SetValue(value)
			return
		}
	}
}

// strictTable opens a fixture whose second column is NOT NULL without a
// default — the case the insert form has to reject before commit.
func strictTable(t *testing.T, m Model) Model {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS strictrows`,
		`CREATE TABLE strictrows (id INTEGER PRIMARY KEY, name TEXT NOT NULL, note TEXT)`,
		`INSERT INTO strictrows (id, name, note) VALUES (1, 'a', NULL)`,
	} {
		if _, err := m.driver.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	m = send(t, m, press('3'), press('R'))
	if !m.panels[panelTables].selectByName("strictrows") {
		t.Fatalf("fixture not listed: %v", m.panels[panelTables].items)
	}
	return send(t, m, special(tea.KeyEnter, 0))
}

// `d` stages a DELETE without executing it, the row is rendered
// struck through, and `u` takes it back.
func TestStageRowDeleteAndUnstage(t *testing.T) {
	m := dataBrowsing(t)
	m = send(t, m, press('d'))

	if m.changes.Len() != 1 {
		t.Fatalf("changeset = %d, want the delete staged", m.changes.Len())
	}
	if !m.changes.DeleteStaged("", "grid", []any{int64(1)}) {
		t.Fatal("the delete was not staged for the row under the cursor")
	}
	if !logContains(m, `-- stage: DELETE FROM "grid" WHERE "id" = ?;  -- args [1]`) {
		t.Fatalf("command log = %v", m.commandLog)
	}
	// Nothing ran: the row is still in the database.
	rs, err := m.driver.Query(context.Background(), "SELECT count(*) FROM grid WHERE id = 1")
	if err != nil {
		t.Fatal(err)
	}
	if rs.Rows[0][0] != int64(1) {
		t.Fatal("the DELETE executed before the commit")
	}
	// The grid marks it, and the badge counts it.
	_, kinds := m.buildGrid()
	if kinds[0] != rowDeleted {
		t.Fatalf("row kind = %v, want it rendered as deleted", kinds[0])
	}
	if !strings.Contains(m.View().Content, "1 staged change") {
		t.Error("status badge missing")
	}

	// Pressing d again says so rather than stacking a second DELETE.
	m = send(t, m, press('d'))
	if m.changes.Len() != 1 {
		t.Fatalf("changeset = %d, want the second d to be a no-op", m.changes.Len())
	}
	if !logContains(m, "-- already staged: delete of grid") {
		t.Fatalf("command log = %v", m.commandLog)
	}

	m = send(t, m, press('u'))
	if m.changes.Len() != 0 {
		t.Fatalf("changeset = %d after u, want the delete unstaged", m.changes.Len())
	}
}

// Staging a delete drops the pending edits of the row it removes, and
// editing a row already staged for deletion is refused.
func TestStagedDeleteSupersedesCellEdits(t *testing.T) {
	m := dataBrowsing(t)
	m = send(t, m, press('l'))
	m = stageEdit(t, m, "doomed")
	if m.changes.Len() != 1 {
		t.Fatalf("changeset = %d, want the edit staged", m.changes.Len())
	}

	m = send(t, m, press('d'))
	if m.changes.Len() != 1 {
		t.Fatalf("changeset = %d, want the edit replaced by the delete", m.changes.Len())
	}
	if _, ok := m.changes.Lookup("", "grid", []any{int64(1)}, "name"); ok {
		t.Fatal("the edit of the deleted row survived")
	}

	m = send(t, m, press('e'))
	if _, ok := m.modal.(*editCellModal); ok {
		t.Fatal("e opened the editor on a row staged for deletion")
	}
	if !logContains(m, "the row is staged for deletion") {
		t.Fatalf("command log = %v", m.commandLog)
	}
}

// A table without a primary key cannot be deleted from — the same rule
// as editing — but it can still be inserted into.
func TestDeleteDisabledWithoutPrimaryKeyInsertAllowed(t *testing.T) {
	m := dataBrowsing(t)
	ctx := context.Background()
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS nopkrows`,
		`CREATE TABLE nopkrows (x INTEGER, y TEXT)`,
		`INSERT INTO nopkrows (x, y) VALUES (1, 'a')`,
	} {
		if _, err := m.driver.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	m = send(t, m, press('3'), press('R'))
	if !m.panels[panelTables].selectByName("nopkrows") {
		t.Fatalf("fixture not listed: %v", m.panels[panelTables].items)
	}
	m = send(t, m, special(tea.KeyEnter, 0), press('d'))

	cm, ok := m.modal.(*confirmModal)
	if !ok {
		t.Fatalf("d opened %T, want the explanation", m.modal)
	}
	if !strings.Contains(cm.body, "no primary key") {
		t.Fatalf("body = %q, want it to name the missing key", cm.body)
	}
	if m.changes.Len() != 0 {
		t.Fatal("a delete got staged on a PK-less table")
	}

	m = send(t, m, special(tea.KeyEscape, 0))
	m, f := insertForm(t, m, 'n')
	setField(f, "x", "7")
	setField(f, "y", "inserted")
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.changes.Len() != 1 {
		t.Fatalf("changeset = %d, want the insert staged on the PK-less table", m.changes.Len())
	}
}

// `n` stages an INSERT that shows up as a phantom row at the end of the
// page, reachable by the cursor and removable with `u`.
func TestInsertStagesPhantomRow(t *testing.T) {
	m := dataBrowsing(t)
	m, f := insertForm(t, m, 'n')

	// The auto-assigned key defaults; the nullable columns start as NULL.
	if f.fields[0].col.Name != "id" || f.fields[0].mode != insertDefault {
		t.Fatalf("id field mode = %v, want DEFAULT", f.fields[0].mode)
	}
	if f.fields[2].mode != insertNull {
		t.Fatalf("nullable field mode = %v, want NULL", f.fields[2].mode)
	}
	setField(f, "name", "phantom")
	m = send(t, m, special(tea.KeyEnter, 0))

	ins := m.changes.InsertsFor("", "grid")
	if len(ins) != 1 {
		t.Fatalf("staged inserts = %d, want 1", len(ins))
	}
	if len(ins[0].Columns) != 3 || ins[0].Columns[0] != "name" {
		t.Fatalf("insert columns = %v, want id left to its default", ins[0].Columns)
	}
	if !logContains(m, `-- stage: INSERT INTO "grid" ("name", "note", "payload") VALUES (?, ?, ?)`) {
		t.Fatalf("command log = %v", m.commandLog)
	}

	// It renders as an extra green row after the page.
	cols, kinds := m.buildGrid()
	if len(kinds) != len(m.data.rows)+1 || kinds[len(kinds)-1] != rowInserted {
		t.Fatalf("row kinds = %d rows, last = %v", len(kinds), kinds[len(kinds)-1])
	}
	if got := cols[1].cells[len(kinds)-1]; got != "phantom" {
		t.Fatalf("phantom cell = %q, want the staged value", got)
	}
	if got := cols[0].cells[len(kinds)-1]; got != defaultText {
		t.Fatalf("phantom key cell = %q, want %q", got, defaultText)
	}

	// The cursor reaches it, and `u` there unstages the whole INSERT.
	m.data.row = len(m.data.rows)
	m.clampCursor()
	if m.data.row != len(m.data.rows) {
		t.Fatalf("cursor row = %d, want the phantom row reachable", m.data.row)
	}
	m = send(t, m, press('u'))
	if m.changes.Len() != 0 {
		t.Fatalf("changeset = %d after u, want the insert unstaged", m.changes.Len())
	}
	if m.data.row >= len(m.data.rows) {
		t.Fatal("the cursor stayed on a row that no longer exists")
	}
}

// The form refuses a NOT NULL column that nothing would fill in, both
// for an explicit NULL and for a DEFAULT the column does not have.
func TestInsertFormValidatesNotNull(t *testing.T) {
	m := strictTable(t, browsing(t))
	m, f := insertForm(t, m, 'n')

	// cursor onto `name`, then stage NULL into it.
	m = send(t, m, special(tea.KeyDown, 0), ctrl('n'))
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.modal != f {
		t.Fatal("the form closed on an invalid NULL")
	}
	if !strings.Contains(f.err, "name is NOT NULL") {
		t.Fatalf("error = %q, want it to name the column", f.err)
	}
	if m.changes.Len() != 0 {
		t.Fatal("an invalid row got staged")
	}

	m = send(t, m, ctrl('d'))
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.modal != f || !strings.Contains(f.err, "no default") {
		t.Fatalf("error = %q, want the missing default named", f.err)
	}

	setField(f, "name", "valid")
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.modal != nil || m.changes.Len() != 1 {
		t.Fatalf("modal = %T, changeset = %d, want the valid row staged", m.modal, m.changes.Len())
	}
}

// `D` prefills the form from the row under the cursor and clears the key
// so the duplicate cannot collide with its original.
func TestDuplicateRowPrefillsAndClearsKey(t *testing.T) {
	m := dataBrowsing(t)
	m, f := insertForm(t, m, 'D')

	if f.fields[0].col.Name != "id" || f.fields[0].mode != insertDefault {
		t.Fatalf("id field mode = %v, want the key cleared to DEFAULT", f.fields[0].mode)
	}
	if got := f.fields[1].input.Value(); got != "name-1" {
		t.Fatalf("name field = %q, want the row's value", got)
	}
	if f.fields[2].mode != insertNull {
		t.Fatalf("note field mode = %v, want NULL prefilled from the row", f.fields[2].mode)
	}

	m = send(t, m, special(tea.KeyEnter, 0))
	ins := m.changes.InsertsFor("", "grid")
	if len(ins) != 1 {
		t.Fatalf("staged inserts = %d, want 1", len(ins))
	}
	for _, c := range ins[0].Columns {
		if c == "id" {
			t.Fatal("the duplicate carried the original's primary key")
		}
	}
	v, bound := insertValueFor(ins[0], "name")
	if !bound || v != "name-1" {
		t.Fatalf("duplicated name = %v, want the original value", v)
	}
}

// A duplicate copies the value the grid shows, staged edits included.
func TestDuplicateUsesStagedValues(t *testing.T) {
	m := dataBrowsing(t)
	m = send(t, m, press('l'))
	m = stageEdit(t, m, "edited")
	m, f := insertForm(t, m, 'D')
	if got := f.fields[1].input.Value(); got != "edited" {
		t.Fatalf("name field = %q, want the staged value", got)
	}
}

// The whole changeset — an edit, a delete and an insert — commits in one
// transaction, and the commit confirmation shows all three statements.
func TestRowOpsCommitInOneTransaction(t *testing.T) {
	m := strictTable(t, browsing(t))
	ctx := context.Background()
	if _, err := m.driver.Exec(ctx,
		`INSERT INTO strictrows (id, name, note) VALUES (2, 'b', NULL), (3, 'c', NULL)`); err != nil {
		t.Fatal(err)
	}
	m = send(t, m, press('R'))

	// edit row 1, delete row 2, insert a fresh row.
	m = send(t, m, press('l'))
	m = stageEdit(t, m, "edited")
	m = send(t, m, press('j'), press('d'))
	m, f := insertForm(t, m, 'n')
	setField(f, "name", "fresh")
	m = send(t, m, special(tea.KeyEnter, 0))

	if m.changes.Len() != 3 {
		t.Fatalf("changeset = %d, want the edit, the delete and the insert", m.changes.Len())
	}

	m = send(t, m, press('c'))
	cm, ok := m.modal.(*confirmModal)
	if !ok {
		t.Fatalf("c opened %T, want the commit confirmation", m.modal)
	}
	for _, want := range []string{"UPDATE", "DELETE FROM", "INSERT INTO"} {
		if !strings.Contains(cm.body, want) {
			t.Errorf("commit modal is missing the %s statement:\n%s", want, cm.body)
		}
	}
	if !strings.Contains(cm.body, "one transaction") {
		t.Error("commit modal does not say the statements share a transaction")
	}

	m = send(t, m, special(tea.KeyEnter, 0))
	if m.changes.Len() != 0 {
		t.Fatalf("changeset = %d after commit, want it cleared", m.changes.Len())
	}
	if !logContains(m, "BEGIN;") || !logContains(m, "COMMIT;") {
		t.Fatalf("command log = %v", m.commandLog)
	}

	rs, err := m.driver.Query(ctx, "SELECT id, name FROM strictrows ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	if len(rs.Rows) != 3 {
		t.Fatalf("rows = %v, want 1 edited, 2 deleted and one inserted", rs.Rows)
	}
	if rs.Rows[0][1] != "edited" {
		t.Errorf("row 1 name = %v, want the edit applied", rs.Rows[0][1])
	}
	if rs.Rows[1][0] != int64(3) {
		t.Errorf("second row id = %v, want row 2 deleted", rs.Rows[1][0])
	}
	if rs.Rows[2][1] != "fresh" {
		t.Errorf("last row = %v, want the inserted one", rs.Rows[2])
	}
}

// A failing commit applies none of the row operations and keeps them.
func TestRowOpsCommitFailureRollsBack(t *testing.T) {
	m := strictTable(t, browsing(t))
	m = send(t, m, press('d')) // delete the only row
	m, f := insertForm(t, m, 'n')
	// Reuse the key of the row this same commit deletes — but the INSERT
	// runs after the DELETE, so make it collide with itself instead.
	setField(f, "id", "1")
	setField(f, "name", "collides")
	m = send(t, m, special(tea.KeyEnter, 0))
	m, f2 := insertForm(t, m, 'n')
	setField(f2, "id", "1")
	setField(f2, "name", "collides again")
	m = send(t, m, special(tea.KeyEnter, 0))

	m = send(t, m, press('c'), special(tea.KeyEnter, 0))
	if !logContains(m, "COMMIT FAILED") {
		t.Fatalf("command log = %v", m.commandLog)
	}
	if m.changes.Len() != 3 {
		t.Fatalf("changeset = %d after a failed commit, want it kept", m.changes.Len())
	}
	rs, err := m.driver.Query(context.Background(), "SELECT count(*) FROM strictrows")
	if err != nil {
		t.Fatal(err)
	}
	if rs.Rows[0][0] != int64(1) {
		t.Fatalf("rows = %v, want the transaction rolled back entirely", rs.Rows[0][0])
	}
}

// The type of a form value follows the column's declared type, so an
// integer column gets an int64 parameter rather than its text.
func TestConvertForColumnFollowsDeclaredType(t *testing.T) {
	cases := []struct {
		typ  string
		text string
		want any
	}{
		{"INTEGER", "42", int64(42)},
		{"bigint", "42", int64(42)},
		{"double precision", "1.5", 1.5},
		{"boolean", "true", true},
		{"text", "42", "42"},
		{"numeric(10,2)", "1.50", "1.50"}, // exact digits, not a float64
		{"INTEGER", "not a number", "not a number"},
	}
	for _, tc := range cases {
		got := convertForColumn(tc.text, db.Column{DataType: tc.typ})
		if got != tc.want {
			t.Errorf("%s %q = %#v, want %#v", tc.typ, tc.text, got, tc.want)
		}
	}
}
