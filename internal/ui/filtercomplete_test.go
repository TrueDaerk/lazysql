package ui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// filterCompleting is a grid open on a relation worth completing against
// — one of its columns spelled so it cannot be written bare — with the
// inline WHERE line open on it and its own state directory, so nothing
// another test recorded is in the recall list.
func filterCompleting(t *testing.T) Model {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := browsing(t)
	ctx := context.Background()
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS shipments`,
		`CREATE TABLE shipments (id INTEGER PRIMARY KEY, carrier TEXT, "order date" TEXT)`,
		`INSERT INTO shipments VALUES (1, 'dhl', '2024-01-01')`,
	} {
		if _, err := m.driver.Exec(ctx, stmt); err != nil {
			t.Fatalf("fixture %q: %v", stmt, err)
		}
	}
	m = send(t, m, press('2'), press('R'))
	if !m.panels[panelObjects].selectByName("shipments") {
		t.Fatalf("fixture table not listed: %v", m.panels[panelObjects].items)
	}
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.focus != panelMain {
		t.Fatalf("focus = %v, want the data grid", m.focus)
	}
	m = send(t, m, press('/'))
	if !m.filterInputOpen() {
		t.Fatal("/ opened no filter line")
	}
	return m
}

// ---------- what the line offers ----------

// The clause names no relation — the prefix in front of it does — so the
// popup only knows which columns to offer if it reads the statement and
// not just what was typed.
func TestFilterCompletionOffersTheOpenRelationsColumns(t *testing.T) {
	m := filterCompleting(t)
	m = typeKeys(t, m, "ca")

	if !m.completion.open {
		t.Fatal("typing two characters in the clause opened no popup")
	}
	if !hasCompletion(m.completion, "carrier") {
		t.Fatalf("suggestions = %v, want the open relation's column", completionTexts(m.completion))
	}
	// Schema before keywords: `CASE` and `CAST` match `ca` too, and the
	// column is the one the user cannot look up in their head.
	if got := m.completion.items[0]; got.text != "carrier" || got.kind != completeColumn {
		t.Fatalf("first suggestion = %+v, want the column", got)
	}
	if got := m.completion.items[0].detail; got != "shipments" {
		t.Fatalf("column detail = %q, want the relation it belongs to", got)
	}
}

// The context is read against the whole statement: the clause under the
// prefix the line cannot edit, which is what puts the word after a WHERE
// rather than at the start of a statement.
func TestFilterCompletionContextSpansThePrefix(t *testing.T) {
	m := filterCompleting(t)
	m = typeKeys(t, m, "ca")

	scope, ok := m.completionScopeAt(siteFilter)
	if !ok {
		t.Fatal("the open filter line has no completion scope")
	}
	if scope.ctx.word != "ca" {
		t.Fatalf("word = %q, want what was typed", scope.ctx.word)
	}
	if !strings.HasSuffix(scope.stmt, "WHERE ca") {
		t.Fatalf("statement = %q, want the clause under the line's prefix", scope.stmt)
	}
	if got := referencedRelations(m.sqlDialect(), scope.stmt, m.relations); len(got) != 1 ||
		got[0] != "shipments" {
		t.Fatalf("referenced relations = %v, want the one the prefix names", got)
	}
}

// Keywords are completed too — a WHERE clause is half operators — and
// ctrl+space opens the popup on a clause with nothing in it at all.
func TestFilterCompletionOffersKeywordsAndOpensOnNothing(t *testing.T) {
	m := filterCompleting(t)
	m = send(t, m, tea.KeyPressMsg{Code: ' ', Mod: tea.ModCtrl})
	if !m.completion.open {
		t.Fatal("ctrl+space opened no popup on an empty clause")
	}
	if !hasCompletion(m.completion, "carrier") {
		t.Fatalf("suggestions = %v, want the relation's columns", completionTexts(m.completion))
	}

	m = typeKeys(t, m, "carrier li")
	if !hasCompletion(m.completion, "LIKE") {
		t.Fatalf("suggestions = %v, want the operator keyword", completionTexts(m.completion))
	}
	m = send(t, m, special(tea.KeyTab, 0))
	if got := m.filterInput.value(); got != "carrier LIKE" {
		t.Fatalf("clause = %q, want the keyword accepted as it is spelled", got)
	}
}

// ---------- accepting ----------

// An accepted identifier is written the way it has to be written to
// resolve back to itself — the same dialect quoting the editor's popup
// applies — and the caret lands after it, ready for the operator.
func TestFilterCompletionQuotesAnAcceptedIdentifier(t *testing.T) {
	m := filterCompleting(t)
	m = typeKeys(t, m, "or")
	if !hasCompletion(m.completion, "order date") {
		t.Fatalf("suggestions = %v, want the column that needs quoting",
			completionTexts(m.completion))
	}
	m = send(t, m, special(tea.KeyTab, 0))

	if got := m.filterInput.input.Value(); got != `"order date"` {
		t.Fatalf("clause = %q, want the identifier quoted for the dialect", got)
	}
	if m.completion.open {
		t.Fatal("accepting left the popup open")
	}
	if !m.filterInputOpen() {
		t.Fatal("accepting closed the filter line")
	}
	// Typing continues where the insertion ended.
	m = typeKeys(t, m, " IS")
	if got := m.filterInput.value(); got != `"order date" IS` {
		t.Fatalf("clause = %q, want the caret left at the end of the insertion", got)
	}
}

// The word under the caret is replaced, not appended to — including when
// the caret sits in the middle of the clause.
func TestFilterCompletionReplacesTheWordUnderTheCaret(t *testing.T) {
	m := filterCompleting(t)
	m = typeKeys(t, m, "ca = 1")
	m.filterInput.input.SetCursor(2) // just after `ca`
	m = send(t, m, tea.KeyPressMsg{Code: ' ', Mod: tea.ModCtrl})
	m = send(t, m, special(tea.KeyTab, 0))

	if got := m.filterInput.value(); got != "carrier = 1" {
		t.Fatalf("clause = %q, want the word under the caret replaced", got)
	}
	if got := m.filterInput.input.Position(); got != len("carrier") {
		t.Fatalf("caret = %d, want it after the insertion", got)
	}
}

// ---------- who owns which key ----------

// esc is the popup's first and the line's second: dismissing a
// suggestion list must not cost the clause it was floating over.
func TestFilterEscClosesThePopupThenTheLine(t *testing.T) {
	m := filterCompleting(t)
	m = typeKeys(t, m, "ca")
	if !m.completion.open {
		t.Fatal("typing opened no popup")
	}

	m = send(t, m, special(tea.KeyEscape, 0))
	if m.completion.open {
		t.Fatal("the first esc did not close the popup")
	}
	if !m.filterInputOpen() {
		t.Fatal("the first esc closed the filter line as well")
	}
	if got := m.filterInput.value(); got != "ca" {
		t.Fatalf("clause = %q, want it untouched by the popup's esc", got)
	}

	m = send(t, m, special(tea.KeyEscape, 0))
	if m.filterInputOpen() {
		t.Fatal("the second esc did not close the filter line")
	}
}

// ↑/↓ are the popup's while one is open and the relation's filter
// history's the rest of the time. The precedence is the popup, and it
// hands the keys straight back when it closes.
func TestFilterPopupTakesTheArrowsFromTheHistory(t *testing.T) {
	m := filterCompleting(t)
	m = applyWhereFilter(t, m, "id > 0")
	m = send(t, m, press('/'))
	m = send(t, m, ctrl('u'))
	m = typeKeys(t, m, "ca")

	m = send(t, m, special(tea.KeyDown, 0))
	if got := m.filterInput.value(); got != "ca" {
		t.Fatalf("clause = %q, want ↓ to have moved the selection, not recalled", got)
	}
	if m.completion.cursor != 1 {
		t.Fatalf("popup cursor = %d, want ↓ to have moved it", m.completion.cursor)
	}

	m = send(t, m, special(tea.KeyEscape, 0))
	m = send(t, m, special(tea.KeyUp, 0))
	if got := m.filterInput.value(); got != "id > 0" {
		t.Fatalf("clause = %q, want ↑ to recall once the popup is gone", got)
	}
}

// enter is the line's verb: with a popup open over the last word but
// untouched it still runs the clause, and only a row the user picked
// with ↑/↓ turns it into an accept.
func TestFilterEnterAppliesUnlessARowWasPicked(t *testing.T) {
	m := filterCompleting(t)
	m = typeKeys(t, m, "carrier = 'dhl' AND id")
	if !m.completion.open {
		t.Fatal("the last word opened no popup — the test asserts nothing")
	}
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.filterInputOpen() {
		t.Fatal("enter accepted a suggestion instead of applying the clause")
	}
	if got := m.data.filter; got == nil || got.Raw != "carrier = 'dhl' AND id" {
		t.Fatalf("filter = %+v, want the clause as typed", got)
	}

	// The same keystroke after picking a row accepts it, and the line
	// stays open on the completed clause.
	m = send(t, m, press('/'))
	m = send(t, m, ctrl('u'))
	m = typeKeys(t, m, "ca")
	m = send(t, m, special(tea.KeyUp, 0)) // picks the row it is already on
	m = send(t, m, special(tea.KeyEnter, 0))
	if !m.filterInputOpen() {
		t.Fatal("enter on a picked row applied the clause instead of accepting")
	}
	if got := m.filterInput.value(); got != "carrier" {
		t.Fatalf("clause = %q, want the picked suggestion", got)
	}
}

// ---------- rendering ----------

// The line is pinned to the bottom of the grid, so its popup takes the
// rows above it rather than the ones the command log is drawn in — and
// stays inside the main column either way.
func TestFilterCompletionPopupSitsAboveTheLine(t *testing.T) {
	m := filterCompleting(t)
	m = typeKeys(t, m, "ca")
	// The line records the cell its caret landed on while rendering, so
	// the popup is anchored against a frame that has actually been drawn.
	_ = m.View()

	box, x, y, ok := m.completionLayer()
	if !ok {
		t.Fatal("the open popup produced no layer")
	}
	mx, my, mw, mh, ok := m.mainColumnRect()
	if !ok {
		t.Fatal("no main column to place it against")
	}
	lineY := my + maxInt(mh-commandLogHeight(mh)-2, 1)
	if got := y + lipgloss.Height(box); got > lineY {
		t.Fatalf("the popup ends at row %d, want it above the filter line at %d", got, lineY)
	}
	if x < mx || x+lipgloss.Width(box) > mx+mw {
		t.Fatalf("the popup spans %d–%d, outside the main column %d–%d",
			x, x+lipgloss.Width(box), mx, mx+mw)
	}
	// The box floats over the grid, not over the clause being typed.
	if lipgloss.Height(box) < 3 {
		t.Fatalf("the popup is %d rows, want a bordered list", lipgloss.Height(box))
	}
}

// The smallest terminal the app runs in has room for a page of grid and
// little else. The popup shrinks into it instead of being drawn off
// screen or over the options bar.
func TestFilterCompletionPopupFitsASmallTerminal(t *testing.T) {
	m := filterCompleting(t)
	for _, size := range []tea.WindowSizeMsg{
		{Width: minWidth, Height: minHeight},
		{Width: minWidth + 5, Height: minHeight + 3},
	} {
		m = send(t, m, size)
		m = send(t, m, tea.KeyPressMsg{Code: ' ', Mod: tea.ModCtrl})
		if !m.completion.open {
			t.Fatalf("%dx%d: ctrl+space opened no popup", size.Width, size.Height)
		}
		_ = m.View()

		box, x, y, ok := m.completionLayer()
		if !ok {
			continue // no room for a bordered box is a legitimate answer
		}
		if x < 0 || y < 0 ||
			x+lipgloss.Width(box) > m.width || y+lipgloss.Height(box) > m.height {
			t.Errorf("%dx%d: popup at %d,%d sized %dx%d falls outside the screen",
				size.Width, size.Height, x, y, lipgloss.Width(box), lipgloss.Height(box))
		}
	}
}

// ---------- the editor's popup is not this one ----------

// The two lines share the popup's state, so closing one must not take
// the other's list with it — and the site is what tells them apart.
func TestFilterAndEditorPopupsDoNotShareASite(t *testing.T) {
	m := filterCompleting(t)
	m = typeKeys(t, m, "ca")
	if got := m.completion.site; got != siteFilter {
		t.Fatalf("site = %v, want the filter line's", got)
	}

	// Leaving the grid closes the line; the popup goes with it. (The key
	// that does it cannot be pressed here — while the line is open a `3`
	// types into the clause — so this is the focus change itself.)
	m.setFocus(panelQuery)
	if m.completion.open || m.filterInput != nil {
		t.Fatal("the popup outlived the line it was floating over")
	}

	m = send(t, m, press(':'))
	m = typeKeys(t, m, "SEL")
	if !m.completion.open || m.completion.site != siteEditor {
		t.Fatalf("the editor's popup did not open (site = %v)", m.completion.site)
	}
	// closeFilterInput runs on paths the editor takes too — it must not
	// clear a popup that is not the filter line's.
	m.closeFilterInput()
	if !m.completion.open {
		t.Fatal("closing the (absent) filter line cleared the editor's popup")
	}
}
