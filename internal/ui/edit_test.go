package ui

import (
	"context"
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
	m = send(t, m, press('3'), press('R'))
	if !m.panels[panelTables].selectByName("nopk") {
		t.Fatalf("nopk not listed: %v", m.panels[panelTables].items)
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
	m = send(t, m, press('3'), press('R'))
	m.panels[panelTables].selectByName("strict")
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
