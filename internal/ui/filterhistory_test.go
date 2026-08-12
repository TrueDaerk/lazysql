package ui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/history"
)

// filterBrowsing is dataBrowsing with the filter history pointed at a
// temp state dir, so what one test records cannot be recalled by
// another.
func filterBrowsing(t *testing.T) Model {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	return dataBrowsing(t)
}

// scopeOf is the key an applied filter is recorded under.
func scopeOf(m Model, table string) string {
	return history.Scope(m.active, m.data.database, table)
}

// An applied clause is recorded under the relation it ran on, persisted,
// and offered back by the recall keys in the order it was typed.
func TestFilterHistoryRecallsWhatWasApplied(t *testing.T) {
	m := filterBrowsing(t)
	m = applyWhereFilter(t, m, "id > 100")
	m = applyWhereFilter(t, m, "id > 200")

	if got := m.filterHistory(); len(got) != 2 || got[0] != "id > 200" || got[1] != "id > 100" {
		t.Fatalf("recall list = %v, want both clauses newest first", got)
	}

	// ctrl+p walks back through them, ctrl+n forward, and stepping past
	// the newest restores the half-typed clause.
	m = send(t, m, press('/'))
	m = send(t, m, ctrl('u'))
	m = typeKeys(t, m, "id > 3")
	m = send(t, m, ctrl('p'))
	if got := m.filterInput.value(); got != "id > 200" {
		t.Fatalf("first recall = %q, want the newest filter", got)
	}
	m = send(t, m, ctrl('p'))
	if got := m.filterInput.value(); got != "id > 100" {
		t.Fatalf("second recall = %q, want the older filter", got)
	}
	// Nothing older to walk to.
	m = send(t, m, ctrl('p'))
	if got := m.filterInput.value(); got != "id > 100" {
		t.Fatalf("recall walked past the end: %q", got)
	}
	m = send(t, m, ctrl('n'), ctrl('n'))
	if got := m.filterInput.value(); got != "id > 3" {
		t.Fatalf("walking forward = %q, want the draft back", got)
	}

	// The arrows are the same two keys.
	m = send(t, m, special(tea.KeyUp, 0))
	if got := m.filterInput.value(); got != "id > 200" {
		t.Fatalf("↑ recall = %q, want the newest filter", got)
	}
	m = send(t, m, special(tea.KeyDown, 0))
	if got := m.filterInput.value(); got != "id > 3" {
		t.Fatalf("↓ recall = %q, want the draft back", got)
	}

	// And it is on disk, keyed to the relation, for the next session.
	stored, err := history.LoadFilters()
	if err != nil {
		t.Fatal(err)
	}
	mine := history.InScope(stored, scopeOf(m, "grid"))
	if len(mine) != 2 || mine[0].SQL != "id > 200" {
		t.Fatalf("stored filters = %#v, want both under the relation's scope", mine)
	}
	if mine[0].Engine != "sqlite" {
		t.Fatalf("stored entry lost its engine: %#v", mine[0])
	}
}

// The history is per relation: `/` on one table never offers what was
// typed on another.
func TestFilterHistoryIsPerTable(t *testing.T) {
	m := filterBrowsing(t)
	m = applyWhereFilter(t, m, "id > 100")

	if _, err := m.driver.Exec(context.Background(),
		`CREATE TABLE IF NOT EXISTS other (id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	m = send(t, m, press('2'), press('R'))
	m = treeSelect(t, m, "other")
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.data.table != "other" {
		t.Fatalf("table = %q, want other", m.data.table)
	}

	if got := m.filterHistory(); len(got) != 0 {
		t.Fatalf("the other relation offers %v, want its own empty history", got)
	}
	m = send(t, m, press('/'))
	m = send(t, m, ctrl('p'))
	if got := m.filterInput.value(); got != "" {
		t.Fatalf("recall on an unfiltered relation = %q, want nothing to recall", got)
	}
	m = send(t, m, special(tea.KeyEscape, 0))

	m = applyWhereFilter(t, m, "id = 1")
	if got := m.filterHistory(); len(got) != 1 || got[0] != "id = 1" {
		t.Fatalf("recall list = %v, want only this relation's own filter", got)
	}

	// Back on the first relation, its own history is still there.
	m = send(t, m, press('2'), press('R'))
	m = treeSelect(t, m, "grid")
	m = send(t, m, special(tea.KeyEnter, 0))
	if got := m.filterHistory(); len(got) != 1 || got[0] != "id > 100" {
		t.Fatalf("recall list = %v, want the first relation's filter back", got)
	}
}

// Re-applying a filter moves it to the front instead of storing it
// twice: the list is what to try next, not a tally of what ran.
func TestFilterHistoryDeduplicatesWithinAScope(t *testing.T) {
	m := filterBrowsing(t)
	m = applyWhereFilter(t, m, "id > 100")
	m = applyWhereFilter(t, m, "id > 200")
	m = applyWhereFilter(t, m, "id > 100")

	if got := m.filterHistory(); len(got) != 2 || got[0] != "id > 100" || got[1] != "id > 200" {
		t.Fatalf("recall list = %v, want the re-applied filter moved to the front", got)
	}
	stored, err := history.LoadFilters()
	if err != nil {
		t.Fatal(err)
	}
	if mine := history.InScope(stored, scopeOf(m, "grid")); len(mine) != 2 {
		t.Fatalf("stored filters = %#v, want the duplicate rewritten away", mine)
	}
}

// A clause is recorded even when the engine rejects it — a broken filter
// is exactly the one worth recalling and fixing — and clearing the
// filter records nothing.
func TestFilterHistoryRecordsFailuresButNotClears(t *testing.T) {
	m := filterBrowsing(t)
	// A bare identifier in a fragment SQLite cannot parameterize reaches
	// it verbatim, so this one really does fail — see
	// wiki/reference/sqlite-double-quoted-strings for why the quoted,
	// bound spelling would not.
	m = applyWhereFilter(t, m, "no_such_column IN (1, 2)")
	if m.data.err == "" {
		t.Fatal("the fixture clause did not fail")
	}
	if got := m.filterHistory(); len(got) != 1 || got[0] != "no_such_column IN (1, 2)" {
		t.Fatalf("recall list = %v, want the rejected clause", got)
	}

	m = applyWhereFilter(t, m, "")
	if got := m.filterHistory(); len(got) != 1 {
		t.Fatalf("recall list = %v, want clearing to record nothing", got)
	}
}

// A history file left by an earlier session is loaded at startup, so the
// first `/` of a session already recalls.
func TestFilterHistoryLoadsAtStartup(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	if err := history.SaveFilters([]history.Entry{
		{SQL: "id > 100", Engine: "sqlite", Key: history.Scope("local-sqlite", "", "grid")},
	}); err != nil {
		t.Fatal(err)
	}

	m := sized(120, 40)
	for _, msg := range drain(m.Init()) {
		next, _ := m.Update(msg)
		m = next.(Model)
	}
	if len(m.filters) != 1 {
		t.Fatalf("filters = %#v, want the file loaded at startup", m.filters)
	}
}

// A write that fails is reported in the command log rather than
// swallowed — and rather than costing the session its recall list.
func TestFilterHistoryWriteFailureIsLogged(t *testing.T) {
	m := sized(120, 40)
	m = send(t, m, filtersWrittenMsg{err: context.DeadlineExceeded})
	if !logContains(m, "write filter history FAILED") {
		t.Fatalf("command log = %v", m.commandLog)
	}
	if !strings.Contains(m.View().Content, "write filter history FAILED") {
		t.Error("the failure is not on screen")
	}
}
