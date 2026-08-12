package ui

import (
	"context"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// stageEdit drives `e` on the cell under the cursor and submits value
// through the edit modal.
func stageEdit(t *testing.T, m Model, value string) Model {
	t.Helper()
	m = send(t, m, press('e'))
	e, ok := m.modal.(*editCellModal)
	if !ok {
		t.Fatalf("e opened %T, want the edit modal", m.modal)
	}
	e.input.SetValue(value)
	e.null = false
	return send(t, m, special(tea.KeyEnter, 0))
}

// The full round trip: edit stages, nothing executes until the commit
// modal is confirmed, and the commit runs, logs, clears and reloads.
func TestEditStageCommitRoundTrip(t *testing.T) {
	m := dataBrowsing(t)
	m = send(t, m, press('l')) // cursor onto `name`
	m = stageEdit(t, m, "renamed")

	if m.changes.Len() != 1 {
		t.Fatalf("changeset = %d, want the edit staged", m.changes.Len())
	}
	if !logContains(m, `-- stage: UPDATE "grid" SET "name" = ? WHERE "id" = ?`) {
		t.Fatalf("command log = %v", m.commandLog)
	}
	// Nothing executed yet: the database still holds the old value.
	rs, err := m.driver.Query(context.Background(), "SELECT name FROM grid WHERE id = 1")
	if err != nil {
		t.Fatal(err)
	}
	if rs.Rows[0][0] != "name-1" {
		t.Fatalf("value = %v, want the edit NOT executed before commit", rs.Rows[0][0])
	}
	// The staged value shows in the grid, and the badge counts it.
	out := m.View().Content
	if !strings.Contains(out, "renamed") {
		t.Error("staged value not rendered in the grid")
	}
	if !strings.Contains(out, "1 staged change") {
		t.Error("status badge missing")
	}

	// `c` lists the parameterized SQL, enter commits in one transaction.
	m = send(t, m, press('c'))
	cm, ok := m.modal.(*confirmModal)
	if !ok {
		t.Fatalf("c opened %T, want the commit confirmation", m.modal)
	}
	if !strings.Contains(cm.body, `UPDATE "grid" SET "name" = ?`) {
		t.Fatalf("commit modal body = %q, want the generated SQL", cm.body)
	}
	m = send(t, m, special(tea.KeyEnter, 0))

	if m.changes.Len() != 0 {
		t.Fatalf("changeset = %d after commit, want it cleared", m.changes.Len())
	}
	if !logContains(m, `UPDATE "grid" SET "name" = ? WHERE "id" = ?;  -- args [renamed 1]`) ||
		!logContains(m, "COMMIT;") {
		t.Fatalf("command log = %v", m.commandLog)
	}
	rs, err = m.driver.Query(context.Background(), "SELECT name FROM grid WHERE id = 1")
	if err != nil {
		t.Fatal(err)
	}
	if rs.Rows[0][0] != "renamed" {
		t.Fatalf("value = %v, want the committed value", rs.Rows[0][0])
	}
	// The page was refreshed, so the grid shows the committed value too.
	if got := m.data.rows[0][1]; got != "renamed" {
		t.Fatalf("grid value = %v, want the refreshed page", got)
	}
}

// ctrl+n stages NULL, and the second edit of the same cell replaces the
// first instead of stacking.
func TestEditNullToggleAndReplace(t *testing.T) {
	m := dataBrowsing(t)
	m = send(t, m, press('l'), press('e'))
	e := m.modal.(*editCellModal)
	if e.null {
		t.Fatal("null pre-toggled on a non-NULL cell")
	}
	m = send(t, m, ctrl('n'))
	if !e.null {
		t.Fatal("ctrl+n did not toggle NULL")
	}
	m = send(t, m, special(tea.KeyEnter, 0))
	if got, _ := m.changes.Lookup("", "grid", []any{int64(1)}, "name"); got.NewValue != nil {
		t.Fatalf("staged value = %v, want NULL", got.NewValue)
	}

	m = stageEdit(t, m, "again")
	if m.changes.Len() != 1 {
		t.Fatalf("changeset = %d, want the re-edit to replace", m.changes.Len())
	}
	got, _ := m.changes.Lookup("", "grid", []any{int64(1)}, "name")
	if got.NewValue != "again" || got.OldValue != "name-1" {
		t.Fatalf("change = %+v, want the new value with the original OldValue", got)
	}
}

// Restoring the original value in the modal unstages instead of staging
// a no-op, `u` unstages the cursor cell, and `U` discards everything
// after a confirmation.
func TestUnstageAndDiscard(t *testing.T) {
	m := dataBrowsing(t)
	m = send(t, m, press('l'))
	m = stageEdit(t, m, "tmp")
	m = stageEdit(t, m, "name-1") // original value back
	if m.changes.Len() != 0 {
		t.Fatalf("changeset = %d, want restoring the original to unstage", m.changes.Len())
	}

	m = stageEdit(t, m, "one")
	m = send(t, m, press('j'))
	m = stageEdit(t, m, "two")
	if m.changes.Len() != 2 {
		t.Fatalf("changeset = %d, want 2", m.changes.Len())
	}
	m = send(t, m, press('u'))
	if m.changes.Len() != 1 {
		t.Fatalf("changeset = %d after u, want 1", m.changes.Len())
	}
	if _, ok := m.changes.Lookup("", "grid", []any{int64(1)}, "name"); !ok {
		t.Fatal("u removed the wrong cell")
	}

	m = send(t, m, press('U'))
	cm, ok := m.modal.(*confirmModal)
	if !ok {
		t.Fatalf("U opened %T, want a confirmation", m.modal)
	}
	if !strings.Contains(cm.body, "1 staged change") {
		t.Fatalf("body = %q", cm.body)
	}
	// esc keeps the changeset, enter discards it.
	m = send(t, m, special(tea.KeyEscape, 0))
	if m.changes.Len() != 1 {
		t.Fatal("esc on the discard confirmation dropped the changeset")
	}
	m = send(t, m, press('U'), special(tea.KeyEnter, 0))
	if m.changes.Len() != 0 {
		t.Fatal("U did not discard the changeset")
	}
}

// A table without a primary key cannot be edited: `e` explains instead
// of opening the editor.
func TestEditDisabledWithoutPrimaryKey(t *testing.T) {
	m := dataBrowsing(t)
	if _, err := m.driver.Exec(context.Background(),
		`CREATE TABLE IF NOT EXISTS nopk (x INTEGER, y TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := m.driver.Exec(context.Background(),
		`INSERT INTO nopk (x, y) VALUES (1, 'a')`); err != nil {
		t.Fatal(err)
	}
	m = send(t, m, press('2'), press('R'))
	if !m.panels[panelObjects].selectByName("nopk") {
		t.Fatalf("nopk not listed: %v", m.panels[panelObjects].items)
	}
	m = send(t, m, special(tea.KeyEnter, 0), press('e'))
	cm, ok := m.modal.(*confirmModal)
	if !ok {
		t.Fatalf("e opened %T, want the explanation", m.modal)
	}
	if !strings.Contains(cm.body, "no primary key") {
		t.Fatalf("body = %q, want it to name the missing key", cm.body)
	}
	if m.changes.Len() != 0 {
		t.Fatal("something got staged on a PK-less table")
	}
}

// A failing commit applies nothing and keeps the changeset.
func TestCommitFailureKeepsChangeset(t *testing.T) {
	m := dataBrowsing(t)
	ctx := context.Background()
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS strict (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`,
		`DELETE FROM strict`,
		`INSERT INTO strict (id, name) VALUES (1, 'a'), (2, 'b')`,
	} {
		if _, err := m.driver.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	m = send(t, m, press('2'), press('R'))
	m.panels[panelObjects].selectByName("strict")
	m = send(t, m, special(tea.KeyEnter, 0), press('l'))

	m = stageEdit(t, m, "ok-value")
	// Second change violates NOT NULL.
	m = send(t, m, press('j'), press('e'))
	m = send(t, m, ctrl('n'), special(tea.KeyEnter, 0))
	if m.changes.Len() != 2 {
		t.Fatalf("changeset = %d, want 2", m.changes.Len())
	}

	m = send(t, m, press('c'), special(tea.KeyEnter, 0))
	if !logContains(m, "COMMIT FAILED") {
		t.Fatalf("command log = %v", m.commandLog)
	}
	if m.changes.Len() != 2 {
		t.Fatalf("changeset = %d after failed commit, want it kept", m.changes.Len())
	}
	rs, err := m.driver.Query(ctx, "SELECT name FROM strict WHERE id = 1")
	if err != nil {
		t.Fatal(err)
	}
	if rs.Rows[0][0] != "a" {
		t.Fatalf("value = %v, want the transaction rolled back entirely", rs.Rows[0][0])
	}
}

// ---------- bulk edit ----------

// The whole bulk round trip: one edit with N rows selected stages N
// pending changes of the same column, nothing runs before the commit,
// and the commit executes and logs one parameterized statement per row.
func TestBulkEditStagesEverySelectedRow(t *testing.T) {
	m := dataBrowsing(t)
	m = send(t, m, press('l'))                        // cursor onto `name`
	m = send(t, m, ctrl('v'), press('j'), press('j')) // rows 0-2
	m = stageEdit(t, m, "bulk")

	if m.changes.Len() != 3 {
		t.Fatalf("changeset = %d, want one staged change per selected row", m.changes.Len())
	}
	// One log line carries the parameterized SQL and says how many more
	// rows ride along with it.
	if !logContains(m, `-- stage: UPDATE "grid" SET "name" = ? WHERE "id" = ?`) ||
		!logContains(m, "(and 2 more rows, same column and value)") {
		t.Fatalf("command log = %v", m.commandLog)
	}
	// Staging is not executing: all three rows still hold their old value.
	for id := 1; id <= 3; id++ {
		if got := gridName(t, m, id); got != "name-"+strconv.Itoa(id) {
			t.Fatalf("row %d = %v, want nothing executed before the commit", id, got)
		}
	}
	// The staged value is what the grid shows, on every selected row.
	if got := strings.Count(m.View().Content, "bulk"); got < 3 {
		t.Errorf("the grid shows the staged value %d times, want it on all 3 rows", got)
	}
	if !strings.Contains(m.View().Content, "3 staged changes") {
		t.Error("the status badge does not count the bulk edit")
	}
	// The operator consumed the selection, the way vim leaves visual mode.
	if m.data.selecting() {
		t.Error("the selection is still up after the bulk edit staged")
	}

	m = send(t, m, press('c'))
	cm, ok := m.modal.(*confirmModal)
	if !ok {
		t.Fatalf("c opened %T, want the commit confirmation", m.modal)
	}
	if got := strings.Count(cm.body, `UPDATE "grid" SET "name" = ?`); got != 3 {
		t.Fatalf("commit modal lists %d statements, want one per row:\n%s", got, cm.body)
	}
	m = send(t, m, special(tea.KeyEnter, 0))

	for id := 1; id <= 3; id++ {
		if got := gridName(t, m, id); got != "bulk" {
			t.Fatalf("row %d = %v after the commit, want the bulk value", id, got)
		}
	}
	if got := gridName(t, m, 4); got != "name-4" {
		t.Fatalf("row 4 = %v, want rows outside the selection untouched", got)
	}
	// Every executed statement is in the command log, inside one
	// transaction.
	for _, want := range []string{
		"BEGIN;",
		`UPDATE "grid" SET "name" = ? WHERE "id" = ?;  -- args [bulk 1]`,
		`UPDATE "grid" SET "name" = ? WHERE "id" = ?;  -- args [bulk 2]`,
		`UPDATE "grid" SET "name" = ? WHERE "id" = ?;  -- args [bulk 3]`,
		"COMMIT;",
	} {
		if !logContains(m, want) {
			t.Fatalf("command log is missing %q: %v", want, m.commandLog)
		}
	}
	if m.changes.Len() != 0 {
		t.Fatalf("changeset = %d after the commit, want it cleared", m.changes.Len())
	}
}

// The modal names what it is about to do to how many rows, so a bulk
// edit is never confirmed by accident.
func TestBulkEditModalNamesTheRowCount(t *testing.T) {
	m := dataBrowsing(t)
	m = send(t, m, press('l'), ctrl('v'), press('j'), press('e'))
	if _, ok := m.modal.(*editCellModal); !ok {
		t.Fatalf("e opened %T, want the edit modal", m.modal)
	}
	out := m.View().Content
	for _, want := range []string{"2 rows selected", "bulk edit — stages name in all 2 rows"} {
		if !strings.Contains(out, want) {
			t.Errorf("the edit modal is missing %q", want)
		}
	}
}

// A row the selection covers but that cannot be updated safely — one
// already staged for deletion — drops out of the bulk edit and is
// reported instead of being staged anyway.
func TestBulkEditSkipsRowsStagedForDeletion(t *testing.T) {
	m := dataBrowsing(t)
	m = send(t, m, press('j'), press('d')) // stage the delete of row 1 (id=2)
	if m.changes.Len() != 1 {
		t.Fatalf("changeset = %d, want the staged delete", m.changes.Len())
	}
	m = send(t, m, press('k'), press('l'))            // back to row 0, column `name`
	m = send(t, m, ctrl('v'), press('j'), press('j')) // rows 0-2, the deleted one among them
	m = stageEdit(t, m, "bulk")

	// The delete plus the two edits that were allowed.
	if m.changes.Len() != 3 {
		t.Fatalf("changeset = %d, want the delete and 2 cell edits", m.changes.Len())
	}
	if !logContains(m, "-- bulk edit: 1 row left out") {
		t.Fatalf("command log = %v", m.commandLog)
	}
	m = send(t, m, press('c'), special(tea.KeyEnter, 0))
	if got := gridName(t, m, 2); got != nil {
		t.Fatalf("row 2 = %v, want it deleted rather than updated", got)
	}
	for _, id := range []int{1, 3} {
		if got := gridName(t, m, id); got != "bulk" {
			t.Fatalf("row %d = %v, want the bulk value", id, got)
		}
	}
}

// Bulk-editing back to a row's original value unstages that row instead
// of staging a no-op UPDATE, the same rule a single cell edit follows.
func TestBulkEditRestoringOriginalValueUnstages(t *testing.T) {
	m := dataBrowsing(t)
	m = send(t, m, press('l'))
	m = send(t, m, ctrl('v'), press('j'))
	m = stageEdit(t, m, "name-1") // row 0's own value, row 1's is name-2

	if m.changes.Len() != 1 {
		t.Fatalf("changeset = %d, want only the row that actually changes", m.changes.Len())
	}
	// And back again: both rows are now at their original value.
	m = send(t, m, press('k'), ctrl('v'), press('j'))
	m = stageEdit(t, m, "name-2")
	if m.changes.Len() != 1 {
		t.Fatalf("changeset = %d, want row 1 unstaged and row 0 staged", m.changes.Len())
	}
	if !logContains(m, "-- unstage 1 row (original value restored)") {
		t.Fatalf("command log = %v", m.commandLog)
	}
}

// gridName reads one row's name straight from the database, which is
// how the tests tell staging from executing.
func gridName(t *testing.T, m Model, id int) any {
	t.Helper()
	rs, err := m.driver.Query(context.Background(), "SELECT name FROM grid WHERE id = ?", id)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs.Rows) == 0 {
		return nil
	}
	return rs.Rows[0][0]
}

// The staged-editing keys appear in the help modal like every other
// binding.
func TestEditKeysAreDocumented(t *testing.T) {
	m := dataBrowsing(t)
	m = send(t, m, press('?'))
	out := m.View().Content
	for _, want := range []string{
		"edit cell", "stage row delete", "insert row", "duplicate row",
		"commit staged changes", "unstage", "discard staged changes",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("help is missing %q", want)
		}
	}
}
