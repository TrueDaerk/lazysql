package ui

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// click builds a left-button click at an absolute cell.
func click(x, y int) tea.MouseClickMsg {
	return tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}
}

// wheelUp/wheelDown build one wheel notch at an absolute cell.
func wheelDown(x, y int) tea.MouseWheelMsg {
	return tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelDown}
}

func wheelUp(x, y int) tea.MouseWheelMsg {
	return tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelUp}
}

// raw drives one message without running the commands it returns, so a
// test can look at the coalescer between a burst and its flush.
func raw(m Model, msg tea.Msg) (Model, tea.Cmd) {
	next, cmd := m.Update(msg)
	return next.(Model), cmd
}

// A 120x40 model lays out a 40-cell side column of three boxes — 13 rows
// each — and an 80-cell main column split into a 30-row main view and a
// 9-row command log. hitTest has to reproduce exactly that.
func TestHitTestMapsTheLayout(t *testing.T) {
	m := sized(120, 40)
	cases := []struct {
		name  string
		x, y  int
		zone  hitZone
		panel panelID
		title bool
		row   int
	}{
		{"connections title", 5, 0, zoneSide, panelConnections, true, -1},
		{"connections first row", 5, 1, zoneSide, panelConnections, false, 0},
		{"objects title", 5, 13, zoneSide, panelObjects, true, -1},
		{"objects first row", 5, 14, zoneSide, panelObjects, false, 0},
		{"objects third row", 5, 16, zoneSide, panelObjects, false, 2},
		{"query box", 5, 27, zoneSide, panelQuery, false, 0},
		{"main view", 60, 5, zoneMain, 0, false, 4},
		{"main view title", 60, 0, zoneMain, 0, true, -1},
		{"command log", 60, 32, zoneLog, 0, false, 1},
		{"options bar", 60, 39, zoneBar, 0, false, -1},
		{"left border of a panel", 0, 5, zoneSide, panelConnections, false, -1},
	}
	for _, c := range cases {
		got := m.hitTest(c.x, c.y)
		if got.zone != c.zone || got.title != c.title || got.row != c.row ||
			(c.zone == zoneSide && got.panel != c.panel) {
			t.Errorf("%s: hitTest(%d,%d) = %+v, want zone %v panel %v title %v row %d",
				c.name, c.x, c.y, got, c.zone, c.panel, c.title, c.row)
		}
	}
}

// A terminal below the guard renders the "too small" notice instead of
// the layout, so there is nothing to hit.
func TestHitTestIgnoresTinyTerminal(t *testing.T) {
	m := sized(40, 10)
	if got := m.hitTest(5, 5); got.zone != zoneNone {
		t.Fatalf("zone = %v, want none while the too-small notice is up", got.zone)
	}
}

// Clicking a panel focuses it, exactly like pressing its number.
func TestClickFocusesSidePanel(t *testing.T) {
	m := sized(120, 40)
	m = send(t, m, click(5, 14)) // [2] Objects, first row
	if m.focus != panelObjects {
		t.Fatalf("focus = %v, want the objects panel", m.focus)
	}
	m = send(t, m, click(5, 27)) // [3] Query
	if m.focus != panelQuery {
		t.Fatalf("focus = %v, want the query panel", m.focus)
	}
}

// The first click on a row moves the cursor onto it; a second click on
// the row the cursor is already on is `enter`.
func TestClickSelectsRowAndSecondClickDrillsIn(t *testing.T) {
	m := sized(120, 40)
	// Row 2 of [2] Objects: the box starts at y=13, so its content does
	// at y=14. Row 0 is the Tables category, so row 2 is a table.
	m = send(t, m, click(5, 16))
	if m.focus != panelObjects {
		t.Fatalf("focus = %v, want the objects panel", m.focus)
	}
	if got := m.panels[panelObjects].cursor; got != 2 {
		t.Fatalf("cursor = %d, want the clicked row", got)
	}
	if m.table != "" {
		t.Fatalf("table = %q, want the first click to select only", m.table)
	}
	m = send(t, m, click(5, 16))
	if got := m.panels[panelObjects].selected(); m.table != got {
		t.Fatalf("table = %q, want the second click to open %q", m.table, got)
	}
}

// A click on a different row of an already focused panel selects, it
// does not drill in: only a click on the row under the cursor does.
func TestClickOnAnotherRowOnlySelects(t *testing.T) {
	m := sized(120, 40)
	m = send(t, m, press('2'), click(5, 17))
	if got := m.panels[panelObjects].cursor; got != 3 {
		t.Fatalf("cursor = %d, want the clicked row", got)
	}
	if m.table != "" {
		t.Fatalf("table = %q, want no drill-in from a first click", m.table)
	}
}

func TestMainTabHitOffsets(t *testing.T) {
	at := 1 // the `‹`
	for want := mainTab(0); want < mainTabCount; want++ {
		if want > 0 {
			at++ // the `|`
		}
		for i := 0; i < len(mainTabNames[want]); i++ {
			got, ok := mainTabHit(at + i)
			if !ok || got != want {
				t.Fatalf("mainTabHit(%d) = %v,%v, want %v", at+i, got, ok, want)
			}
		}
		at += len(mainTabNames[want])
	}
	if _, ok := mainTabHit(0); ok {
		t.Fatal("mainTabHit(0) hit a tab, want the `‹` to miss")
	}
}

// The wheel is aimed by the pointer, not by the focus: hovering an
// unfocused panel scrolls that one and leaves the focus alone.
func TestWheelScrollsHoveredPanelNotFocusedOne(t *testing.T) {
	m := sized(120, 40)
	m.panels[panelObjects].setItems(manyItems(20))
	if m.focus != panelConnections {
		t.Fatalf("focus = %v, want the connections panel to start", m.focus)
	}
	m, _ = raw(m, wheelDown(5, 20)) // over [2] Objects
	if m.focus != panelConnections {
		t.Fatalf("focus = %v, want the wheel to leave the focus alone", m.focus)
	}
	if got := m.panels[panelObjects].cursor; got != wheelStep {
		t.Fatalf("objects cursor = %d, want %d", got, wheelStep)
	}
	if got := m.panels[panelConnections].cursor; got != 0 {
		t.Fatalf("connections cursor = %d, want the focused panel untouched", got)
	}
	m, _ = raw(m, wheelUp(5, 20))
	m, _ = raw(m, wheelFlushMsg{gen: m.wheel.gen})
	if got := m.panels[panelObjects].cursor; got != 0 {
		t.Fatalf("objects cursor = %d, want the wheel back at the top", got)
	}
}

// A wheel burst costs one scroll for its first event and an integer add
// for every other one; the rest lands on the next flush. That is what
// keeps a fast wheel from queueing scrolls behind the renderer.
func TestWheelCoalescesABurst(t *testing.T) {
	m := sized(120, 40)
	m.panels[panelObjects].setItems(manyItems(60))

	m, cmd := raw(m, wheelDown(5, 20))
	if cmd == nil {
		t.Fatal("the first event of a burst must arm a flush")
	}
	if got := m.panels[panelObjects].cursor; got != wheelStep {
		t.Fatalf("cursor = %d, want the first notch applied at once", got)
	}
	for i := 0; i < 3; i++ {
		var c tea.Cmd
		m, c = raw(m, wheelDown(5, 20))
		if c != nil {
			t.Fatalf("event %d armed a second flush, want it coalesced", i)
		}
	}
	if got := m.panels[panelObjects].cursor; got != wheelStep {
		t.Fatalf("cursor = %d, want the burst still queued", got)
	}
	if got := m.wheel.pending; got != 3*wheelStep {
		t.Fatalf("pending = %d, want %d", got, 3*wheelStep)
	}

	gen := m.wheel.gen
	m, cmd = raw(m, wheelFlushMsg{gen: gen})
	if got := m.panels[panelObjects].cursor; got != 4*wheelStep {
		t.Fatalf("cursor = %d, want the whole burst applied", got)
	}
	if m.wheel.pending != 0 {
		t.Fatalf("pending = %d, want it drained", m.wheel.pending)
	}
	if cmd == nil {
		t.Fatal("a flush that applied notches must re-arm")
	}
	// A tick from the burst that just ended is stale and must not move
	// anything.
	m, _ = raw(m, wheelFlushMsg{gen: gen})
	if got := m.panels[panelObjects].cursor; got != 4*wheelStep {
		t.Fatalf("cursor = %d, want a stale flush ignored", got)
	}
	// The next real flush finds nothing pending and disarms.
	m, cmd = raw(m, wheelFlushMsg{gen: m.wheel.gen})
	if cmd != nil || m.wheel.armed {
		t.Fatal("an empty flush must end the burst")
	}
}

// Aiming somewhere else mid-burst applies what is queued to the panel it
// was aimed at, never to the new one.
func TestWheelRetargetFlushesTheOldPanel(t *testing.T) {
	m := sized(120, 40)
	m.panels[panelObjects].setItems(manyItems(60))
	m.panels[panelConnections].setItems(manyItems(60))

	m, _ = raw(m, wheelDown(5, 20)) // [2] Objects
	m, _ = raw(m, wheelDown(5, 20))
	m, _ = raw(m, wheelDown(5, 5)) // [1] Connections
	if got := m.panels[panelObjects].cursor; got != 2*wheelStep {
		t.Fatalf("objects cursor = %d, want the queued notches flushed to it", got)
	}
	if got := m.panels[panelConnections].cursor; got != wheelStep {
		t.Fatalf("connections cursor = %d, want only the new notch", got)
	}
}

// Panel [3] previews the buffer from its first line: there is no window
// to move, so the wheel over it does nothing.
func TestWheelOverQueryPanelIsInert(t *testing.T) {
	m := sized(120, 40)
	before := m.script()
	m, cmd := raw(m, wheelDown(5, 30))
	if cmd != nil {
		t.Fatal("the query panel has nothing to scroll, want no flush armed")
	}
	if m.script() != before {
		t.Fatal("the wheel must not touch the buffer")
	}
}

// An open modal swallows the wheel: the popup scrolls, the view behind
// it does not.
func TestWheelInModalScrollsTheModalOnly(t *testing.T) {
	m := sized(120, 40)
	m.panels[panelObjects].setItems(manyItems(60))
	for i := 0; i < 40; i++ {
		m.commandLog = append(m.commandLog, logLine{text: fmt.Sprintf("-- line %d", i)})
	}
	m = send(t, m, press('@'))
	lm, ok := m.modal.(*commandLogModal)
	if !ok {
		t.Fatalf("modal = %T, want the command log", m.modal)
	}
	before := lm.offset
	m, _ = raw(m, wheelUp(5, 20)) // over [2] Objects, but a modal is open
	if got := lm.offset; got != before-wheelStep {
		t.Fatalf("modal offset = %d, want %d", got, before-wheelStep)
	}
	if got := m.panels[panelObjects].cursor; got != 0 {
		t.Fatalf("objects cursor = %d, want the view behind the modal untouched", got)
	}
}

// A click while a modal is open is swallowed too — it must not move the
// focus out from under the popup.
func TestClickIsSwallowedByAModal(t *testing.T) {
	m := sized(120, 40)
	m = send(t, m, press('?'))
	focus := m.focus
	m = send(t, m, click(5, 20))
	if m.modal == nil {
		t.Fatal("the modal closed on a click, want it swallowed")
	}
	if m.focus != focus {
		t.Fatalf("focus = %v, want %v", m.focus, focus)
	}
}

// Clicking the options bar or the command log strip is a no-op: neither
// is focusable.
func TestClickOnUnfocusableChromeDoesNothing(t *testing.T) {
	m := sized(120, 40)
	focus, cursor := m.focus, m.panels[panelConnections].cursor
	m = send(t, m, click(60, 39), click(60, 33))
	if m.focus != focus || m.panels[panelConnections].cursor != cursor {
		t.Fatalf("focus = %v cursor = %d, want both unchanged",
			m.focus, m.panels[panelConnections].cursor)
	}
}

// The main view takes the focus on a click, and once it has it a click
// picks the cell under the pointer.
func TestClickGridFocusesThenSelectsACell(t *testing.T) {
	m := dataBrowsing(t)
	m = send(t, m, press('2')) // hand the focus to a side panel first
	if m.focus != panelObjects {
		t.Fatalf("focus = %v, want the objects panel", m.focus)
	}
	// The main view box starts at x=40,y=0; its content at 41,1. The
	// grid's first data row is the fourth content row (names, types,
	// rule, then rows).
	m = send(t, m, click(45, 1+3))
	if m.focus != panelMain {
		t.Fatalf("focus = %v, want the click to focus the main view", m.focus)
	}
	if got := m.data.row; got != 0 {
		t.Fatalf("row = %d, want the focusing click not to move the cursor", got)
	}
	m = send(t, m, click(45, 1+3+5))
	if got := m.data.row; got != 5 {
		t.Fatalf("row = %d, want the clicked row", got)
	}

	// The second column starts one separator past the first one's width.
	cols, _ := m.buildGrid()
	x := 41 + cols[0].width + colGap
	m = send(t, m, click(x, 1+3+5))
	if got := m.data.col; got != 1 {
		t.Fatalf("col = %d, want the clicked column", got)
	}
}

// A click lands the highlight on the line that was clicked and leaves it
// there. The row window used to be derived from the cursor alone, so
// clicking near the top of a scrolled page re-anchored the window and the
// highlight appeared several lines below the pointer — on the right row,
// but not where the user had aimed.
func TestClickKeepsTheHighlightOnTheClickedLine(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {100, 30}, {80, 24}, {60, 18}} {
		for _, scrolled := range []int{0, 30, 99} {
			m := dataBrowsing(t)
			m = send(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			m = send(t, m, repeatKey('j', scrolled)...)

			x, y, _, _, ok := m.mainColumnRect()
			if !ok {
				t.Fatalf("%dx%d: the main column is not on screen", size[0], size[1])
			}
			// The first three content rows are the header; the data rows
			// start under them.
			for row := 3; row < 6; row++ {
				clicked := send(t, m, click(x+2, y+1+row))
				_, line, n := highlightedCell(gridBox(clicked))
				if n != 1 || line != row {
					t.Fatalf("%dx%d scrolled %d: a click on content row %d highlighted line %d (%d tinted cells, cursor row %d)",
						size[0], size[1], scrolled, row, line, n, clicked.data.row)
				}
				assertCursorRendered(t, clicked, fmt.Sprintf("%dx%d click on row %d", size[0], size[1], row))
			}
		}
	}
}

// The wheel moves the grid's row cursor and stops at the page boundary:
// a page turn is a round trip, and the wheel must never issue one.
func TestWheelScrollsGridWithoutTurningThePage(t *testing.T) {
	m := dataBrowsing(t)
	m, _ = raw(m, wheelDown(60, 10))
	if got := m.data.row; got != wheelStep {
		t.Fatalf("row = %d, want %d", got, wheelStep)
	}
	page := m.data.page
	for i := 0; i < 2*dataPageSize/wheelStep; i++ {
		m, _ = raw(m, wheelDown(60, 10))
		m, _ = raw(m, wheelFlushMsg{gen: m.wheel.gen})
	}
	if m.data.page != page {
		t.Fatalf("page = %d, want the wheel to stop at %d", m.data.page, page)
	}
	if got := m.data.row; got != m.data.rowCount()-1 {
		t.Fatalf("row = %d, want it clamped to the last row of the page", got)
	}
}

// Clicking a tab header of the main view switches tabs, the way `[`/`]`
// does.
func TestClickMainTabHeaderSwitchesTab(t *testing.T) {
	m := dataBrowsing(t)
	// The main view's title starts at x=42: the box's left edge (40),
	// its corner rune and one fill rune. mainTabBar opens with `‹`.
	x := 42 + 1 + len("Data") + 1
	m = send(t, m, click(x, 0))
	if m.tab != mainTabStructure {
		t.Fatalf("tab = %v, want Structure", m.tab)
	}
}

// The wheel scrolls the query editor by moving its caret — the editor
// derives its window from the caret rather than storing an offset.
func TestWheelScrollsQueryEditor(t *testing.T) {
	m := sized(120, 40)
	m = send(t, m, press('3'))
	var script string
	for i := 0; i < 40; i++ {
		script += fmt.Sprintf("SELECT %d;\n", i)
	}
	m.setScript(script)
	m.editor.area.MoveToBegin()
	m, _ = raw(m, wheelDown(60, 5)) // over the editor in the main view
	if got := m.editor.area.Line(); got != wheelStep {
		t.Fatalf("caret line = %d, want %d", got, wheelStep)
	}
	m, _ = raw(m, wheelUp(60, 5))
	m, _ = raw(m, wheelFlushMsg{gen: m.wheel.gen})
	if got := m.editor.area.Line(); got != 0 {
		t.Fatalf("caret line = %d, want the top back", got)
	}
}

// manyItems is a filler list long enough that a wheel burst has
// somewhere to go.
func manyItems(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("row-%02d", i)
	}
	return out
}
