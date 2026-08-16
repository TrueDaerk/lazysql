package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

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

	// The candidates show up in the composited frame — as a floating box
	// over the modal, not as rows of the form — and the footer hint swaps.
	out := m.View().Content
	for _, want := range []string{"sales.duckdb", "sales.sqlite", "tab complete path"} {
		if !strings.Contains(out, want) {
			t.Errorf("frame missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "other.db") {
		t.Errorf("frame offers a non-matching entry:\n%s", out)
	}
	if view := form.view(m.style, m.width, m.height); strings.Contains(view, "sales.duckdb") {
		t.Errorf("candidates are still rendered as form rows:\n%s", view)
	}

	// The box is bordered and floats over the modal: its own layer, drawn
	// under the field it belongs to.
	box, _, y, ok := m.pathSuggestLayer(0, 0)
	if !ok {
		t.Fatal("no path suggestion layer while candidates are up")
	}
	if !strings.Contains(box, "╮") {
		t.Errorf("suggestion box is not bordered:\n%s", box)
	}
	if y <= form.anchorY {
		t.Errorf("box row %d is not below the field row %d", y, form.anchorY)
	}
}

// The form's own box keeps its size while the list appears and disappears:
// the candidates are a layer over it, so nothing in the layout reserves
// rows for them.
func TestFormHeightUnchangedByCandidates(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"sales.duckdb", "sales.sqlite", "sales.db"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	m, form := fileForm(t, sized(120, 40))
	base := form.field("file")
	base.input.SetValue(filepath.Join(dir, "s"))
	base.input.CursorEnd()
	form.sugg.clear()
	quiet := form.view(m.style, m.width, m.height)

	form.sugg.refresh(base.input.Value())
	if got := len(form.sugg.candidates); got != 3 {
		t.Fatalf("candidates = %d, want 3 (%v)", got, form.sugg.candidates)
	}
	open := form.view(m.style, m.width, m.height)

	if gotH, wantH := lipgloss.Height(open), lipgloss.Height(quiet); gotH != wantH {
		t.Errorf("form height with candidates = %d, want %d", gotH, wantH)
	}
	// The width is not asserted: the footer still swaps to the completion
	// hint, which is a longer line than the default one. That is the
	// remaining size wobble and belongs to the stable-popup-size work, not
	// here — the rows are what this change stopped moving.
}

// The overlay is clamped to the terminal: it flips above the field when the
// rows below it belong to the screen's bottom edge, and never draws past any
// edge — at the smallest terminal the app still renders at, either.
func TestSuggestOverlayStaysInBounds(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a1.db", "a2.db", "a3.db", "a4.db", "a5.db"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	for _, size := range [][2]int{{120, 40}, {minWidth, minHeight}, {80, 20}} {
		m, form := fileForm(t, sized(size[0], size[1]))
		form.field("file").input.SetValue(filepath.Join(dir, "a"))
		form.field("file").input.CursorEnd()
		form.sugg.refresh(form.field("file").input.Value())
		if !form.sugg.active() {
			t.Fatalf("%dx%d: no candidates", size[0], size[1])
		}

		// Render once so the form records its anchor, then place the layer
		// exactly where View would.
		box := form.view(m.style, m.width, m.height)
		px := maxInt((m.width-lipgloss.Width(box))/2, 0)
		py := maxInt((m.height-lipgloss.Height(box))/2, 0)
		pop, x, y, ok := m.pathSuggestLayer(px, py)
		if !ok {
			// Legitimate on a terminal with no room either side of the
			// field; the frame must then simply not mention a candidate.
			continue
		}
		if x < 0 || y < 0 {
			t.Errorf("%dx%d: overlay at (%d,%d) is off the top/left", size[0], size[1], x, y)
		}
		if r := x + lipgloss.Width(pop); r > m.width {
			t.Errorf("%dx%d: overlay right edge %d exceeds width %d", size[0], size[1], r, m.width)
		}
		if b := y + lipgloss.Height(pop); b > m.height {
			t.Errorf("%dx%d: overlay bottom edge %d exceeds height %d", size[0], size[1], b, m.height)
		}
		// The composited frame is still exactly the terminal's size — a box
		// that overflowed would grow it.
		out := m.View().Content
		if h := lipgloss.Height(out); h != m.height {
			t.Errorf("%dx%d: frame height = %d", size[0], size[1], h)
		}
		if w := lipgloss.Width(out); w != m.width {
			t.Errorf("%dx%d: frame width = %d", size[0], size[1], w)
		}
	}
}

// Once tab has extended the input as far as the shared prefix goes, the next
// tab press highlights the first candidate instead of moving to the next
// field — but stops there without accepting it, since a highlight has only
// just appeared. ↓ previews the other candidate and shift+tab reverses the
// cycle, neither accepting or moving the cursor; once a candidate is
// highlighted, a further tab accepts it and advances (see
// TestTabAcceptsHighlightedCandidateAndAdvances for the general case,
// TestDirectoryCandidateStaysAndDescends for the directory exception).
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

	// Nothing left to extend: the first tab starts cycling on candidate 0,
	// staying on the field.
	cursor := form.cursor
	m = send(t, m, special(tea.KeyTab, 0))
	form = m.modal.(*formModal)
	if form.cursor != cursor {
		t.Fatalf("the first tab on an ambiguous prefix moved the cursor to %d, want to stay at %d", form.cursor, cursor)
	}
	if !form.sugg.cycling {
		t.Fatal("tab on an ambiguous, already-extended prefix should start cycling")
	}
	first := form.field("file").input.Value()
	if first != filepath.Join(dir, "sales.duckdb") && first != filepath.Join(dir, "sales.sqlite") {
		t.Fatalf("cycled value = %q, want one of the two candidates", first)
	}
	// The highlighted candidate is marked in the overlay — a styled bar, so
	// the box differs from the one drawn with nothing selected.
	rows := form.sugg.rows(maxSuggestLines)
	selected := 0
	for _, r := range rows {
		if r.selected {
			selected++
		}
	}
	if selected != 1 {
		t.Errorf("rows mark %d candidates selected, want exactly 1: %+v", selected, rows)
	}
	highlighted := form.sugg.popup(m.style, 20, maxSuggestLines)
	plain := (&pathSuggest{candidates: form.sugg.candidates}).popup(m.style, 20, maxSuggestLines)
	if highlighted == plain {
		t.Errorf("overlay does not mark the selected candidate:\n%s", highlighted)
	}
	if view := form.view(m.style, m.width, m.height); !strings.Contains(view, "tab/enter accept & next") {
		t.Errorf("footer does not advertise accept & next while cycling:\n%s", view)
	}

	// ↓ previews the other candidate without accepting or moving the cursor.
	m = send(t, m, special(tea.KeyDown, 0))
	form = m.modal.(*formModal)
	second := form.field("file").input.Value()
	if second == first {
		t.Fatalf("down left the value at %q, want the other candidate", second)
	}
	if form.cursor != cursor {
		t.Errorf("down moved the cursor to field %d while previewing candidates", form.cursor)
	}

	// shift+tab reverses the cycle, back to the first candidate, still
	// without accepting.
	m = send(t, m, special(tea.KeyTab, tea.ModShift))
	form = m.modal.(*formModal)
	if got := form.field("file").input.Value(); got != first {
		t.Errorf("shift+tab = %q, want back to %q", got, first)
	}
	if form.cursor != cursor {
		t.Errorf("shift+tab moved the cursor to field %d while reversing the cycle", form.cursor)
	}

	// A further tab now accepts the highlighted (file) candidate and
	// advances to the next field.
	m = send(t, m, special(tea.KeyTab, 0))
	form = m.modal.(*formModal)
	if got := form.field("file").input.Value(); got != first {
		t.Errorf("accepting tab changed the value to %q, want %q", got, first)
	}
	if form.cursor == cursor {
		t.Error("tab on a highlighted file candidate should advance the cursor")
	}
	if form.sugg.active() {
		t.Error("accepting a file candidate should dismiss the candidate list")
	}
}

// Plain longest-common-prefix extension keeps its stay-in-field behavior
// even once it silently starts cycling: TestFileFieldCyclesOnAmbiguousPrefix
// covers the transition into cycling explicitly. This test is the
// complementary case for tab and enter both accepting a highlighted
// candidate once ↑↓ made the selection explicit.
func TestTabAcceptsHighlightedCandidateAndAdvances(t *testing.T) {
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

	cursor := form.cursor
	m = send(t, m, special(tea.KeyDown, 0))
	form = m.modal.(*formModal)
	selected := form.field("file").input.Value()

	m = send(t, m, special(tea.KeyTab, 0))
	form, ok := m.modal.(*formModal)
	if !ok {
		t.Fatalf("tab on a highlighted candidate closed the form: modal is now %T", m.modal)
	}
	if got := form.field("file").input.Value(); got != selected {
		t.Errorf("tab changed the value to %q, want the highlighted candidate %q", got, selected)
	}
	if form.cursor == cursor {
		t.Error("tab on a highlighted file candidate should advance the cursor")
	}
	if form.sugg.active() {
		t.Error("accepting a file candidate should dismiss the candidate list")
	}
}

// A directory candidate is not a complete value for a general (non
// directories-only) path field: accepting one re-runs completion inside the
// directory and keeps the cursor on the field, rather than advancing to a
// value that could never resolve to a file.
func TestDirectoryCandidateStaysAndDescends(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "proj"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "proj", "data.db"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "other.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	m, form := fileForm(t, sized(120, 40))
	base := form.field("file")
	base.input.SetValue(filepath.Join(dir, "p"))
	base.input.CursorEnd()
	form.sugg.refresh(base.input.Value())
	if got := len(form.sugg.candidates); got != 1 {
		t.Fatalf("candidates = %d, want 1 (%v)", got, form.sugg.candidates)
	}

	cursor := form.cursor
	m = send(t, m, special(tea.KeyDown, 0))
	m = send(t, m, special(tea.KeyEnter, 0))
	form, ok := m.modal.(*formModal)
	if !ok {
		t.Fatalf("accepting a directory closed the form: modal is now %T", m.modal)
	}

	if form.cursor != cursor {
		t.Errorf("accepting a directory moved the cursor to %d, want to stay at %d", form.cursor, cursor)
	}
	want := filepath.Join(dir, "proj") + string(filepath.Separator)
	if got := form.field("file").input.Value(); got != want {
		t.Errorf("field = %q, want %q", got, want)
	}
	if !form.sugg.active() {
		t.Fatal("accepting a directory should re-run completion inside it")
	}
	wantInner := filepath.Join(dir, "proj", "data.db")
	if got := form.sugg.candidates; len(got) != 1 || got[0] != wantInner {
		t.Errorf("candidates after descending = %v, want [%s]", got, wantInner)
	}
}

// The directories-only flavor has nothing further to descend into that the
// field cares about: a directory is its final value there, so accepting one
// advances like a file would in the general flavor.
func TestDirsOnlyFlavorAdvancesOnDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "proj"), 0o700); err != nil {
		t.Fatal(err)
	}

	m, form := fileForm(t, sized(120, 40))
	form.sugg.dirs = true
	base := form.field("file")
	base.input.SetValue(filepath.Join(dir, "p"))
	base.input.CursorEnd()
	form.sugg.refresh(base.input.Value())
	if got := len(form.sugg.candidates); got != 1 {
		t.Fatalf("candidates = %d, want 1 (%v)", got, form.sugg.candidates)
	}

	cursor := form.cursor
	m = send(t, m, special(tea.KeyDown, 0))
	m = send(t, m, special(tea.KeyEnter, 0))
	form, ok := m.modal.(*formModal)
	if !ok {
		t.Fatalf("accepting a directory closed the form: modal is now %T", m.modal)
	}

	want := filepath.Join(dir, "proj") + string(filepath.Separator)
	if got := form.field("file").input.Value(); got != want {
		t.Errorf("field = %q, want %q", got, want)
	}
	if form.cursor == cursor {
		t.Error("accepting a directory in the dirs-only flavor should advance the cursor")
	}
	if form.sugg.active() {
		t.Error("advancing should dismiss the candidate list")
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
// server engine has no file field to complete at all. ↓/↑ are captured by
// the open list (see TestArrowKeysNavigateCandidates), so the field is left
// with shift+tab here instead — the one field-navigation key the list does
// not take over.
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

	m = send(t, m, special(tea.KeyTab, tea.ModShift))
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

// While the candidate list is open, ↓/↑ walk the list instead of moving to
// another field, and wrap.
func TestArrowKeysNavigateCandidates(t *testing.T) {
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

	cursor := form.cursor
	m = send(t, m, special(tea.KeyDown, 0))
	form = m.modal.(*formModal)
	if form.cursor != cursor {
		t.Errorf("down moved the cursor to field %d while candidates were up", form.cursor)
	}
	if !form.sugg.cycling {
		t.Fatal("down on an open list should highlight a candidate")
	}
	first := form.field("file").input.Value()
	if first != filepath.Join(dir, "sales.duckdb") && first != filepath.Join(dir, "sales.sqlite") {
		t.Fatalf("down selected %q, want one of the two candidates", first)
	}

	m = send(t, m, special(tea.KeyDown, 0))
	form = m.modal.(*formModal)
	second := form.field("file").input.Value()
	if second == first {
		t.Fatalf("second down left the value at %q, want the other candidate", second)
	}

	// Up wraps back to the first candidate.
	m = send(t, m, special(tea.KeyUp, 0))
	form = m.modal.(*formModal)
	if got := form.field("file").input.Value(); got != first {
		t.Errorf("up = %q, want back to %q", got, first)
	}
}

// Enter with the list open accepts the highlighted candidate into the field
// and advances to the next field, instead of submitting the form.
func TestEnterAcceptsCandidateWithoutSubmitting(t *testing.T) {
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

	cursor := form.cursor
	m = send(t, m, special(tea.KeyDown, 0))
	form = m.modal.(*formModal)
	selected := form.field("file").input.Value()

	m = send(t, m, special(tea.KeyEnter, 0))
	form, ok := m.modal.(*formModal)
	if !ok {
		t.Fatalf("enter on an open list submitted the form: modal is now %T", m.modal)
	}
	if got := form.field("file").input.Value(); got != selected {
		t.Errorf("enter changed the value to %q, want the accepted candidate %q", got, selected)
	}
	if form.cursor == cursor {
		t.Error("enter on a highlighted file candidate should advance the cursor")
	}
	if form.sugg.active() {
		t.Error("accepting a file candidate should dismiss the candidate list")
	}
}

// Esc dismisses the open list on the first press and only cancels the form
// on the next one.
func TestEscDismissesListBeforeCancelingForm(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.db"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	m, form := fileForm(t, sized(120, 40))
	form.field("file").input.SetValue(filepath.Join(dir, "a"))
	form.sugg.refresh(form.field("file").input.Value())
	if !form.sugg.active() {
		t.Fatal("no candidates to dismiss")
	}

	m = send(t, m, special(tea.KeyEscape, 0))
	form, ok := m.modal.(*formModal)
	if !ok {
		t.Fatalf("esc on an open list cancelled the form: modal is now %T", m.modal)
	}
	if form.sugg.active() {
		t.Fatal("first esc should have dismissed the list")
	}

	m = send(t, m, special(tea.KeyEscape, 0))
	if _, ok := m.modal.(*enginePickerModal); !ok {
		t.Fatalf("second esc did not cancel the form: modal is %T", m.modal)
	}
}

// A short terminal must not grow the overlay past the screen: the rows shrink
// and collapse into the "+N more" tail.
func TestSuggestionRowsCapped(t *testing.T) {
	s := &pathSuggest{candidates: []string{"a", "b", "c", "d"}}
	if got := len(s.rows(0)); got != 0 {
		t.Errorf("rows(0) = %d rows, want 0", got)
	}
	got := s.rows(2)
	if len(got) != 2 || got[0].text != "a" || !strings.Contains(got[1].text, "+3 more") {
		t.Errorf("rows(2) = %+v, want [a, … +3 more]", got)
	}
	if !got[1].tail {
		t.Errorf("the +N more row is not marked as the tail: %+v", got[1])
	}
	if got := s.rows(99); len(got) != 4 {
		t.Errorf("rows(99) = %+v, want all four candidates", got)
	}
	// A row budget of one is still a drawable box — one candidate, no tail
	// row to spare, so the box says what it can rather than nothing.
	if got := s.rows(1); len(got) != 1 || got[0].text != "a" {
		t.Errorf("rows(1) = %+v, want [a]", got)
	}
}
