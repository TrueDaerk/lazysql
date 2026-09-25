package ui

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// narrowGrid is the `grid` fixture (id, name, note, payload) in a
// terminal narrow enough that its columns cannot all share the main view.
func narrowGrid(t *testing.T) Model {
	t.Helper()
	next, _ := dataBrowsing(t).Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	return next.(Model)
}

// layoutOf is the layout the next frame draws.
func layoutOf(t *testing.T, m Model) gridLayout {
	t.Helper()
	w, h, ok := m.gridViewport()
	if !ok {
		t.Fatal("the grid is not on screen")
	}
	return m.gridLayout(w, h)
}

// gridHeaderLine is the first line of the grid inside the main view box:
// the column names.
func gridHeaderLine(m Model) string {
	for _, line := range strings.Split(gridBox(m), "\n") {
		if strings.Contains(line, "│") || strings.Contains(line, pinSepChar) {
			return line
		}
	}
	return ""
}

// A pinned column stays on screen, at the left edge, after `l` has
// scrolled the rest of the grid past it.
func TestPinnedColumnStaysVisibleAfterScrollingRight(t *testing.T) {
	m := narrowGrid(t)

	// Without a pin, reaching the last column scrolls id off the left.
	far := send(t, m, press('l'), press('l'), press('l'))
	if g := layoutOf(t, far); slices.Contains(g.shown(), 0) {
		t.Fatalf("fixture too wide to scroll: shown = %v", g.shown())
	}

	m = send(t, m, press('p'), press('l'), press('l'), press('l'))
	if m.data.col != 3 {
		t.Fatalf("cursor column = %d, want payload (3)", m.data.col)
	}
	g := layoutOf(t, m)
	shown := g.shown()
	if len(shown) == 0 || shown[0] != 0 || !slices.Contains(shown, 3) {
		t.Fatalf("shown = %v, want id pinned first and payload in view", shown)
	}
	if g.cs <= g.pinned {
		t.Fatalf("scrolling part did not scroll: cs = %d, pinned = %d", g.cs, g.pinned)
	}
	header := gridHeaderLine(m)
	if !strings.Contains(header, "id") || !strings.Contains(header, pinSepChar) {
		t.Fatalf("header %q does not lead with the pinned id column", header)
	}
	if strings.Index(header, "id") > strings.Index(header, pinSepChar) {
		t.Fatalf("pinned id is not left of the pin edge: %q", header)
	}
	assertCursorRendered(t, m, "cursor on a scrolled column next to a pinned one")

	// `p` again unpins, and id is back in its table position.
	m = send(t, m, press('h'), press('h'), press('h'), press('p'))
	if len(m.data.pinned) != 0 {
		t.Fatalf("pinned = %v after unpinning", m.data.pinned)
	}
}

// Several columns can be pinned; they lead in the order they were pinned
// and `h`/`l` walk the display order.
func TestSeveralPinnedColumnsLeadInPinOrder(t *testing.T) {
	m := narrowGrid(t)
	// Pin note (2): it moves to the front with the cursor on it, and `l`
	// then reaches id, the first scrolling column. Pin that too.
	m = send(t, m, press('l'), press('l'), press('p'), press('l'), press('p'))
	if got := m.data.visibleOrder(); !slices.Equal(got, []int{2, 0, 1, 3}) {
		t.Fatalf("display order = %v, want [2 0 1 3]", got)
	}
	// The cursor is on id, second in the display order: `h` reaches note.
	m = send(t, m, press('h'))
	if m.data.col != 2 {
		t.Fatalf("h from id went to %d, want note (2)", m.data.col)
	}
}

// A hidden column is absent from the grid and from a CSV export.
func TestHiddenColumnIsAbsentFromTheGridAndTheExport(t *testing.T) {
	m := send(t, copyBrowsing(t), press('l'), press('z'))
	if !slices.Equal(m.data.hidden, []string{"person_id"}) {
		t.Fatalf("hidden = %v", m.data.hidden)
	}
	if m.data.col != 2 {
		t.Fatalf("cursor stayed on the hidden column: %d", m.data.col)
	}
	header := gridHeaderLine(m)
	if strings.Contains(header, "person_id") {
		t.Fatalf("hidden column still drawn: %q", header)
	}
	if !strings.Contains(header, "status") {
		t.Fatalf("header %q lost a visible column", header)
	}

	path := filepath.Join(t.TempDir(), "orders.csv")
	m = typePath(t, send(t, m, press('E')), path)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if lines[0] != "id,status" {
		t.Fatalf("export header = %q, want the visible columns only", lines[0])
	}
	if len(lines) != 4 || lines[1] != "1,open" && !strings.HasPrefix(lines[1], "1,") {
		t.Fatalf("export rows:\n%s", data)
	}
}

// The row and table copy scopes act on the visible columns, in the
// order they are drawn.
func TestCopyScopesFollowTheVisibleColumns(t *testing.T) {
	got := fakeClipboard(t)
	// Hide person_id, pin status: the display order is status, id.
	m := send(t, copyBrowsing(t), press('l'), press('z'), press('p'), press('j'))

	send(t, m, press('y'), press('r'))
	if *got != ",2" {
		t.Errorf("row CSV = %q, want status then id", *got)
	}
	send(t, m, press('y'), press('i'))
	if want := `INSERT INTO "orders" ("status", "id") VALUES (NULL, 2);`; *got != want {
		t.Errorf("row INSERT =\n%s\nwant\n%s", *got, want)
	}
	send(t, m, press('y'), press('C'))
	if first := strings.SplitN(*got, "\n", 2)[0]; first != "status,id" {
		t.Errorf("table CSV header = %q, want status,id", first)
	}
}

// The column status line counts pinned and hidden columns truthfully.
func TestColumnsHintCountsPinnedAndHidden(t *testing.T) {
	for _, tc := range []struct {
		g    gridLayout
		want string
	}{
		{gridLayout{order: make([]int, 22), cs: 3, ce: 6},
			"columns 4–6 of 22 — h/l scrolls"},
		{gridLayout{order: make([]int, 20), pinned: 2, cs: 4, ce: 8, hidden: 3},
			"columns 5–8 of 20 · 2 pinned · 3 hidden (Z shows) — h/l scrolls"},
		{gridLayout{order: make([]int, 20), pinned: 2, cs: 2, ce: 6},
			"columns 1–6 of 20 · 2 pinned — h/l scrolls"},
		{gridLayout{order: make([]int, 3), cs: 0, ce: 3, hidden: 1},
			"columns 1–3 of 3 · 1 hidden (Z shows)"},
	} {
		if got := tc.g.columnsHint(); got != tc.want {
			t.Errorf("hint = %q, want %q", got, tc.want)
		}
	}

	// And the rendered grid agrees with its own layout.
	m := send(t, narrowGrid(t), press('p'), press('l'), press('l'), press('z'))
	g := layoutOf(t, m)
	if len(g.order) != 3 || g.pinned != 1 || g.hidden != 1 {
		t.Fatalf("layout order=%v pinned=%d hidden=%d", g.order, g.pinned, g.hidden)
	}
	if out := m.View().Content; !strings.Contains(out, g.columnsHint()) || !strings.Contains(out, "of 3 · 1 pinned · 1 hidden") {
		t.Fatalf("status line missing %q:\n%s", g.columnsHint(), out)
	}
}

// Every column but one can be hidden; the last one stays.
func TestTheLastVisibleColumnCannotBeHidden(t *testing.T) {
	m := send(t, copyBrowsing(t), press('z'), press('z'), press('z'), press('z'))
	if n := len(m.data.visibleOrder()); n != 1 {
		t.Fatalf("visible columns = %d, want 1", n)
	}
	if !logContains(m, "last visible column") {
		t.Fatalf("command log = %v", m.commandLog)
	}
}

// `Z` lists the hidden columns and shows them again.
func TestHiddenColumnsMenuRestores(t *testing.T) {
	m := send(t, copyBrowsing(t), press('z'), press('z'), press('Z'))
	labels := menuLabels(t, m)
	if !hasLabel(labels, "show id") || !hasLabel(labels, "show person_id") {
		t.Fatalf("menu = %v", labels)
	}
	m = send(t, m, press('1'))
	if slices.Contains(m.data.hidden, "id") || !slices.Contains(m.data.hidden, "person_id") {
		t.Fatalf("hidden = %v after showing id", m.data.hidden)
	}
	m = send(t, m, press('Z'), press('A'))
	if len(m.data.hidden) != 0 {
		t.Fatalf("hidden = %v after show all", m.data.hidden)
	}
}

// Pins and hides are per relation: a sort, a page turn, a filter and a
// reload keep them, another relation drops them.
func TestPinAndHideSurviveTheQueryShapeButNotAnotherRelation(t *testing.T) {
	m := send(t, dataBrowsing(t), press('p'), press('l'), press('z'))
	check := func(what string) {
		t.Helper()
		if !slices.Equal(m.data.pinned, []string{"id"}) || !slices.Equal(m.data.hidden, []string{"name"}) {
			t.Fatalf("after %s: pinned = %v, hidden = %v", what, m.data.pinned, m.data.hidden)
		}
	}
	m = send(t, m, press('s'))
	check("a sort")
	m = send(t, m, ctrl('f'))
	check("a page turn")
	m = applyWhereFilter(t, m, "id > 10")
	check("a filter")
	m = send(t, m, press('R'))
	check("a reload")

	if _, err := m.driver.Exec(context.Background(), `CREATE TABLE IF NOT EXISTS other (id INTEGER, name TEXT)`); err != nil {
		t.Fatal(err)
	}
	m = send(t, m, press('2'), press('R'))
	m.panels[panelObjects].selectByName("other")
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.data.table != "other" || len(m.data.pinned) != 0 || len(m.data.hidden) != 0 {
		t.Fatalf("table %q carried pinned = %v, hidden = %v", m.data.table, m.data.pinned, m.data.hidden)
	}
}

// The column block spans what is on screen between its edges: pinned
// columns where they are drawn, hidden ones left out.
func TestColumnSelectionSpansTheDisplayOrder(t *testing.T) {
	// Pin status, hide nothing: display order status, id, person_id.
	m := send(t, copyBrowsing(t), press('l'), press('l'), press('p'))
	m = send(t, m, press('C'), press('l'))
	if got := m.data.selectedCols(); !slices.Equal(got, []int{2, 0}) {
		t.Fatalf("selected columns = %v, want status and id", got)
	}
	if !m.data.narrowedToCols() {
		t.Fatal("a two-of-three span is not reported as a block")
	}

	// Hide person_id: a whole-row selection no longer carries it.
	m = send(t, copyBrowsing(t), press('l'), press('z'), special(tea.KeyDown, tea.ModShift))
	if got := m.data.selectedCols(); !slices.Equal(got, []int{0, 2}) {
		t.Fatalf("whole-row selection columns = %v, want the visible ones", got)
	}
	if m.data.narrowedToCols() {
		t.Fatal("a whole-row selection over the visible columns reads as a block")
	}
}

// A click on a pinned column lands on it, after the rest has scrolled.
func TestClickOnAPinnedColumnSelectsIt(t *testing.T) {
	m := send(t, narrowGrid(t), press('p'), press('l'), press('l'), press('l'))
	m.clickGrid(3, 0)
	if m.data.col != 0 {
		t.Fatalf("click on the pinned column selected %d, want 0", m.data.col)
	}
}

// Pinned columns wider than the box give way rather than push the
// cursor column off the screen.
func TestOverwidePinsNeverHideTheCursor(t *testing.T) {
	m := narrowGrid(t)
	// Pin id, name and note, then walk onto the 32-cell payload column in
	// a terminal too narrow to draw the pins and it side by side.
	m = send(t, m, press('p'), press('l'), press('p'), press('l'), press('p'), press('l'))
	next, _ := m.Update(tea.WindowSizeMsg{Width: 70, Height: 30})
	m = next.(Model)
	if m.data.col != 3 {
		t.Fatalf("cursor column = %d, want payload (3)", m.data.col)
	}
	if !slices.Contains(layoutOf(t, m).shown(), 3) {
		t.Fatal("the cursor column is not drawn")
	}
	assertCursorRendered(t, m, "cursor right of over-wide pins")
}
