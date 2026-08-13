package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/config"
	"lazysql/internal/db"
)

// fileForm opens the new-connection form on a file engine. The create flow
// itself parks the cursor on the File field, which is where path
// completion lives.
func fileForm(t *testing.T, m Model) (Model, *formModal) {
	t.Helper()
	m = send(t, m, press('n'))
	m = send(t, m, pickEngineKey(t, m, db.EngineSQLite))
	form, ok := m.modal.(*formModal)
	if !ok {
		t.Fatalf("picking SQLite opened %T, want *formModal", m.modal)
	}
	if cur := form.current(); cur == nil || cur.name != "file" {
		t.Fatalf("a fresh SQLite form opens on %v, want the file field", cur)
	}
	return m, form
}

// Typing into the File field must produce live candidates, and tab must
// extend the input to the longest unambiguous prefix rather than move the
// cursor to the next field.
func TestFileFieldCompletes(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"sales.duckdb", "sales.sqlite", "other.db"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	m, form := fileForm(t, sized(120, 40))
	base := form.field("file")
	base.input.SetValue(filepath.Join(dir, "s"))
	base.input.CursorEnd()
	form.sugg.refresh(base.input.Value())

	if got := len(form.sugg.candidates); got != 2 {
		t.Fatalf("candidates for %q = %d, want 2 (%v)", base.input.Value(), got, form.sugg.candidates)
	}
	cursor := form.cursor
	m = send(t, m, special(tea.KeyTab, 0))
	form = m.modal.(*formModal)
	if form.cursor != cursor {
		t.Errorf("tab moved the cursor to field %d while candidates were up", form.cursor)
	}
	if got, want := form.field("file").input.Value(), filepath.Join(dir, "sales."); got != want {
		t.Errorf("tab completed to %q, want %q", got, want)
	}

	// The rendered modal shows the candidates and swaps the footer hint.
	view := form.view(m.style, m.width, m.height)
	for _, want := range []string{"sales.duckdb", "sales.sqlite", "tab complete path"} {
		if !strings.Contains(view, want) {
			t.Errorf("form view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "other.db") {
		t.Errorf("form view offers a non-matching entry:\n%s", view)
	}
}

// Once tab has extended the input as far as the shared prefix goes, further
// tab presses cycle through the remaining candidates one at a time, and
// shift+tab reverses that cycle instead of moving to the previous field.
func TestFileFieldCyclesOnAmbiguousPrefix(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"sales.duckdb", "sales.sqlite"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	m, form := fileForm(t, sized(120, 40))
	base := form.field("file")
	base.input.SetValue(filepath.Join(dir, "sales."))
	base.input.CursorEnd()
	form.sugg.refresh(base.input.Value())
	if got := len(form.sugg.candidates); got != 2 {
		t.Fatalf("candidates = %d, want 2 (%v)", got, form.sugg.candidates)
	}

	// Nothing left to extend: the first tab starts cycling on candidate 0.
	m = send(t, m, special(tea.KeyTab, 0))
	form = m.modal.(*formModal)
	if !form.sugg.cycling {
		t.Fatal("tab on an ambiguous, already-extended prefix should start cycling")
	}
	first := form.field("file").input.Value()
	if first != filepath.Join(dir, "sales.duckdb") && first != filepath.Join(dir, "sales.sqlite") {
		t.Fatalf("cycled value = %q, want one of the two candidates", first)
	}
	view := form.view(m.style, m.width, m.height)
	if !strings.Contains(view, "▸ ") {
		t.Errorf("view does not mark the selected candidate:\n%s", view)
	}
	if !strings.Contains(view, "tab/shift+tab cycle path") {
		t.Errorf("footer does not advertise cycling:\n%s", view)
	}

	// A second tab moves to the other candidate.
	m = send(t, m, special(tea.KeyTab, 0))
	form = m.modal.(*formModal)
	second := form.field("file").input.Value()
	if second == first {
		t.Fatalf("second tab left the value at %q, want the other candidate", second)
	}

	// shift+tab reverses the cycle, back to the first candidate.
	m = send(t, m, special(tea.KeyTab, tea.ModShift))
	form = m.modal.(*formModal)
	if got := form.field("file").input.Value(); got != first {
		t.Errorf("shift+tab = %q, want back to %q", got, first)
	}
}

// With nothing to complete, tab keeps its usual meaning: move to the next
// field. Same for a field that never opted into completion.
func TestTabMovesWithoutCandidates(t *testing.T) {
	m, form := fileForm(t, sized(120, 40))
	form.field("file").input.SetValue(filepath.Join(t.TempDir(), "nope-no-such-prefix"))
	form.sugg.refresh(form.field("file").input.Value())
	if form.sugg.active() {
		t.Fatalf("unexpected candidates: %v", form.sugg.candidates)
	}
	cursor := form.cursor
	m = send(t, m, special(tea.KeyTab, 0))
	if got := m.modal.(*formModal).cursor; got != cursor+1 {
		t.Errorf("tab with no candidates left the cursor at %d, want %d", got, cursor+1)
	}
}

// Suggestions belong to the file field alone: leaving it clears them, and a
// server engine has no file field to complete at all.
func TestSuggestionsClearOnBlur(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.db"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	m, form := fileForm(t, sized(120, 40))
	form.field("file").input.SetValue(filepath.Join(dir, "a"))
	form.sugg.refresh(form.field("file").input.Value())
	if !form.sugg.active() {
		t.Fatal("no candidates to clear")
	}

	m = send(t, m, special(tea.KeyDown, 0))
	form = m.modal.(*formModal)
	if form.sugg.active() {
		t.Errorf("candidates survived leaving the field: %v", form.sugg.candidates)
	}
	if got := form.view(m.style, m.width, m.height); strings.Contains(got, "tab complete path") {
		t.Errorf("footer still advertises completion off the file field:\n%s", got)
	}

	// A server engine has no file field at all, so no field on its form
	// offers path completion.
	pg := newConnectionForm("New", config.Connection{Engine: db.EnginePostgres}, "")
	for range pg.visibleFields() {
		if pg.suggestField() != nil {
			t.Error("a server engine offers path completion")
		}
		pg.move(1)
	}
}

// A short terminal must not grow the modal past the screen: the rows shrink
// and collapse into the "+N more" tail.
func TestSuggestionRowsCapped(t *testing.T) {
	s := &pathSuggest{candidates: []string{"a", "b", "c", "d"}}
	if got := len(s.lines(0)); got != 0 {
		t.Errorf("lines(0) = %d rows, want 0", got)
	}
	got := s.lines(2)
	if len(got) != 2 || got[0] != "a" || !strings.Contains(got[1], "+3 more") {
		t.Errorf("lines(2) = %v, want [a, … +3 more]", got)
	}
	if got := s.lines(99); len(got) != 4 {
		t.Errorf("lines(99) = %v, want all four candidates", got)
	}
}
