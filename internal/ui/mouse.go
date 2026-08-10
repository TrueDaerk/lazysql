package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Mouse support. lazysql stays a keyboard app — every mouse gesture here
// is a shortcut for a key that already exists — so the mouse layer only
// has to answer three questions: what was clicked, what is under the
// pointer, and how to keep a wheel burst from queueing up behind the
// renderer. See wiki/design/mouse-support.md.
//
// Three rules shape it:
//
//   - Tracking is cell-motion level (tea.MouseModeCellMotion): clicks,
//     releases and the wheel, no motion reports. Nothing here follows a
//     drag, and all-motion would put a message on the queue for every
//     cell the pointer crosses.
//   - Nothing has its own screen rect. The panels are laid out by
//     View from m.width/m.height, m.screen and m.focus alone, so
//     hitTest recomputes the same geometry from the same numbers
//     instead of a rect cache that could go stale between a render and
//     the click that follows it.
//   - A wheel event is coalesced, never applied one-for-one. See
//     wheelState below.

// wheelStep is how many rows one wheel notch moves. Three is the
// terminal convention and what the alternate-scroll-mode arrow key
// emulation this feature replaces used to send.
const wheelStep = 3

// wheelFlushInterval is how long a wheel burst is allowed to accumulate
// before it is applied. It is one 60fps frame: short enough that the
// scroll tracks the hand, long enough that a fast wheel costs one state
// update per frame instead of one per event.
const wheelFlushInterval = 16 * time.Millisecond

// ---------- geometry ----------

// rect is a screen region in absolute cells.
type rect struct{ x, y, w, h int }

func (r rect) contains(x, y int) bool {
	return r.w > 0 && r.h > 0 && x >= r.x && x < r.x+r.w && y >= r.y && y < r.y+r.h
}

// inner is the content area of a bordered box: one cell in on every side.
func (r rect) inner() rect {
	return rect{x: r.x + 1, y: r.y + 1, w: maxInt(r.w-2, 0), h: maxInt(r.h-2, 0)}
}

// hitZone names the region of the layout a mouse event landed in.
type hitZone int

const (
	zoneNone  hitZone = iota
	zoneSide          // one of the numbered side panel boxes
	zoneMain          // the main view box
	zoneLog           // the command log strip under the main view
	zoneBar           // the options bar on the bottom row
	zoneModal         // the open modal, which swallows every mouse event
)

// hit is where a mouse event landed: the box, and the cell inside it.
// row/col are content coordinates — 0,0 is the first cell inside the
// border — and are -1 when the cell is not a content cell. title marks
// the box's top border line, where renderTitledBox splices the title.
type hit struct {
	zone  hitZone
	panel panelID
	title bool
	row   int
	col   int
}

// hitTest maps an absolute cell onto the layout. It walks exactly the
// numbers View lays the body out with — sideWidth, panelHeights,
// commandLogHeight — so the two cannot disagree.
func (m Model) hitTest(x, y int) hit {
	if m.width <= 0 || m.height <= 0 || m.width < minWidth || m.height < minHeight {
		return hit{}
	}
	if x < 0 || y < 0 || x >= m.width || y >= m.height {
		return hit{}
	}
	bodyH := m.height - 1 // options bar
	if y >= bodyH {
		return hit{zone: zoneBar, row: -1, col: -1}
	}

	if m.screen == screenFull {
		// Panel [3] edits in the main view, so full-screening it shows
		// the editor — the same branch View takes.
		if m.focus == panelMain || m.focus == panelQuery {
			return m.hitMainColumn(rect{0, 0, m.width, bodyH}, x, y)
		}
		return boxHit(hit{zone: zoneSide, panel: m.focus}, rect{0, 0, m.width, bodyH}, x, y)
	}

	sideW := m.sideWidth()
	if x >= sideW {
		return m.hitMainColumn(rect{sideW, 0, m.width - sideW, bodyH}, x, y)
	}
	heights := m.panelHeights(bodyH)
	top := 0
	for id := panelID(0); id < panelCount; id++ {
		h := heights[id]
		if h <= 0 {
			continue
		}
		if y < top+h {
			return boxHit(hit{zone: zoneSide, panel: id}, rect{0, top, sideW, h}, x, y)
		}
		top += h
	}
	return hit{}
}

// hitMainColumn splits the main column the way renderMainColumn does:
// the main view box, and the command log strip under it.
func (m Model) hitMainColumn(r rect, x, y int) hit {
	logH := commandLogHeight(r.h)
	mainH := r.h - logH
	if y < r.y+mainH {
		return boxHit(hit{zone: zoneMain}, rect{r.x, r.y, r.w, mainH}, x, y)
	}
	return boxHit(hit{zone: zoneLog}, rect{r.x, r.y + mainH, r.w, logH}, x, y)
}

// boxHit resolves a cell inside a bordered box: the top border carries
// the title, everything one cell in is content, and the rest is border.
func boxHit(h hit, box rect, x, y int) hit {
	h.row, h.col = -1, -1
	if y == box.y {
		// The title starts after the corner rune and titleBorderPad
		// fill runes — see renderTitledBox.
		h.title = true
		h.col = x - box.x - 1 - titleBorderPad
		return h
	}
	if in := box.inner(); in.contains(x, y) {
		h.row, h.col = y-in.y, x-in.x
	}
	return h
}

// ---------- wheel coalescing ----------

// wheelState collapses a wheel burst into one state update per frame.
//
// A wheel that turns fast delivers events faster than the app can render
// them. Applying each one as it arrives makes every event cost a scroll
// plus a re-render, and the queue that builds up keeps scrolling after
// the hand has stopped — the backlog issue #78 describes. Instead the
// first event of a burst is applied immediately (so the scroll starts
// without a frame of delay) and arms a flush tick; every event that
// arrives before the tick only adds to pending, which is a single
// integer add. The tick applies the sum and re-arms while more is
// queued. Per-event work is therefore O(1) no matter how fast the wheel
// turns, and the scroll stops within one frame of the hand stopping.
//
// gen invalidates a tick that outlived its burst, the same way
// queryRun.id drops a cancelled run's late reply.
type wheelState struct {
	pending int
	target  scrollTarget
	armed   bool
	gen     int
}

// scrollTarget is what a wheel burst is aimed at. row is carried because
// the main view stacks several scrollable blocks (the editor over its
// result), so where in the box the pointer sits decides which one moves.
type scrollTarget struct {
	zone  hitZone
	panel panelID
	row   int
}

// wheelFlushMsg applies whatever a burst accumulated since the last one.
type wheelFlushMsg struct{ gen int }

func wheelFlushCmd(gen int) tea.Cmd {
	return tea.Tick(wheelFlushInterval, func(time.Time) tea.Msg {
		return wheelFlushMsg{gen: gen}
	})
}

// wheelDelta turns a wheel button into a signed row count, 0 for a
// button that is not a vertical wheel. Horizontal wheel events are left
// alone: the grid scrolls columns with h/l, which is a cursor move, not
// a scroll offset.
func wheelDelta(b tea.MouseButton) int {
	switch b {
	case tea.MouseWheelDown:
		return wheelStep
	case tea.MouseWheelUp:
		return -wheelStep
	}
	return 0
}

// scrollable reports whether a target has anything the wheel can move.
// An inert one is dropped before the coalescer sees it, so a wheel over
// the query panel's preview or an unscrollable popup costs nothing —
// not even a flush tick.
func (m Model) scrollable(t scrollTarget) bool {
	switch t.zone {
	case zoneModal:
		_, ok := m.modal.(wheelHandler)
		return ok
	case zoneSide:
		return t.panel < panelCount && t.panel != panelQuery
	case zoneMain:
		return true
	}
	return false
}

// wheelAt is the coalescing entry point: it applies delta to t now if no
// burst is running, and otherwise only accumulates.
func (m *Model) wheelAt(t scrollTarget, delta int) tea.Cmd {
	if m.wheel.armed {
		if m.wheel.target == t {
			m.wheel.pending += delta
			return nil
		}
		// The pointer moved to another view mid-burst: what is queued
		// belongs to where it was aimed, not to where it is now.
		m.applyScroll(m.wheel.target, m.wheel.pending)
		m.wheel.pending = 0
	}
	m.applyScroll(t, delta)
	m.wheel.target = t
	m.wheel.armed = true
	m.wheel.gen++
	return wheelFlushCmd(m.wheel.gen)
}

// flushWheel applies one frame's worth of accumulated notches.
func (m *Model) flushWheel(msg wheelFlushMsg) tea.Cmd {
	if msg.gen != m.wheel.gen {
		return nil
	}
	if m.wheel.pending == 0 {
		m.wheel.armed = false
		return nil
	}
	delta := m.wheel.pending
	m.wheel.pending = 0
	m.applyScroll(m.wheel.target, delta)
	m.wheel.gen++
	return wheelFlushCmd(m.wheel.gen)
}

// applyScroll moves the target by delta rows. Every branch is a plain
// state change — no round trip is ever started by the wheel, which is
// what keeps a burst cheap enough to coalesce at all.
func (m *Model) applyScroll(t scrollTarget, delta int) {
	if delta == 0 {
		return
	}
	switch t.zone {
	case zoneModal:
		if h, ok := m.modal.(wheelHandler); ok {
			h.scroll(delta)
		}
	case zoneSide:
		// Panel [3] previews the buffer from its first line; there is no
		// window to move. Its editor scrolls in the main view instead.
		if t.panel == panelQuery || t.panel >= panelCount {
			return
		}
		m.panels[t.panel].move(delta)
	case zoneMain:
		m.scrollMain(t.row, delta)
	}
}

// scrollMain routes the wheel inside the main view to whichever block is
// under the pointer — the same stack mainContent and queryContent render.
func (m *Model) scrollMain(row, delta int) {
	switch {
	case m.focus == panelConnections:
		// An open schema diff is the only scrollable thing panel [1]
		// puts here; the profile detail always fits.
		if m.diff != nil {
			m.diff.offset += delta
		}
	case m.focus == panelQuery:
		if m.plan != nil {
			m.plan.offset += delta
			return
		}
		// The editor sits on top of its own result: below the buffer and
		// its hint line the wheel belongs to the grid.
		if m.data.open() && row >= m.editorBlockRows() {
			m.scrollGrid(delta)
			return
		}
		m.scrollEditor(delta)
	case m.trigger != nil:
		// The trigger definition owns the main view while it is up, the
		// same way the plan does for panel [3].
		m.scrollTrigger(delta)
	case m.data.open():
		if m.tab.metadata() {
			mm, _ := m.updateMetaKeys(delta)
			*m = mm
			return
		}
		m.scrollGrid(delta)
	}
}

// scrollGrid moves the cell cursor's row, which is what scrolls the grid:
// the row window is derived from the cursor, not stored. It stops at the
// page boundary rather than turning the page — a page turn is a server
// round trip, and a wheel must never issue one.
func (m *Model) scrollGrid(delta int) {
	m.data.row += delta
	m.clampCursor()
}

// scrollEditor moves the caret by delta lines. The editor has no scroll
// offset of its own either — renderEditor derives its window from the
// caret — so moving the caret is how the buffer scrolls. The buffer is
// read and written back once for the whole delta, not once per line:
// vimBuffer splits the text and applyVim walks the textarea's cursor
// from the top, both O(buffer), and a coalesced flush hands several
// lines at a time.
func (m *Model) scrollEditor(delta int) {
	if delta == 0 {
		return
	}
	move := (*vimBuffer).down
	if delta < 0 {
		move, delta = (*vimBuffer).up, -delta
	}
	b := m.vimBuffer()
	for i := 0; i < delta; i++ {
		move(&b)
	}
	m.applyVim(b, false)
}

// editorBlockRows is how many of the main view's content rows the editor
// and its hint line take, so a wheel below them can be aimed at the
// result instead. It walks the nesting queryContent renders, the same
// way completionLayer does.
func (m Model) editorBlockRows() int {
	_, _, mw, mh, ok := m.mainColumnRect()
	if !ok {
		return 0
	}
	cw := maxInt(mw-2, 1)
	rows := maxInt(mh-commandLogHeight(mh)-2, 1)
	return m.editorHeight(cw, rows) + 1 // + the hint line under the buffer
}

// wheelHandler is the optional half of the modal contract, mirroring
// pasteHandler: a popup with a scroll offset of its own implements it and
// the wheel moves that offset. A modal that does not still swallows the
// event, so the wheel never reaches the view behind an open popup.
type wheelHandler interface {
	scroll(delta int)
}

// ---------- routing ----------

// updateMouse takes the same slot in the routing order a key does: a
// modal swallows it, then a click that moves the focus is global, then
// the view under the pointer acts on it. Unlike a key, the wheel goes to
// the panel it is *over* rather than the focused one — hovering is the
// only aiming a wheel has.
func (m Model) updateMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	// 1. A modal swallows every mouse event, the way it swallows every
	// key. Only the scrollable ones do anything with the wheel.
	if m.modal != nil {
		e, ok := msg.(tea.MouseWheelMsg)
		if !ok {
			return m, nil
		}
		t := scrollTarget{zone: zoneModal}
		delta := wheelDelta(e.Button)
		if delta == 0 || !m.scrollable(t) {
			return m, nil
		}
		cmd := m.wheelAt(t, delta)
		return m, cmd
	}

	switch e := msg.(type) {
	case tea.MouseWheelMsg:
		delta := wheelDelta(e.Button)
		if delta == 0 {
			return m, nil
		}
		h := m.hitTest(e.X, e.Y)
		t := scrollTarget{zone: h.zone, panel: h.panel, row: h.row}
		if !m.scrollable(t) {
			return m, nil
		}
		cmd := m.wheelAt(t, delta)
		return m, cmd

	case tea.MouseClickMsg:
		if e.Button != tea.MouseLeft {
			return m, nil
		}
		// A click while `/` is capturing keys is not a key: it moves the
		// focus like any other click and leaves the pattern alone.
		return m.mouseClick(m.hitTest(e.X, e.Y))
	}
	return m, nil
}

func (m Model) mouseClick(h hit) (tea.Model, tea.Cmd) {
	switch h.zone {
	case zoneSide:
		return m.clickSide(h)
	case zoneMain:
		return m.clickMain(h)
	}
	// The command log strip and the options bar are not focusable.
	return m, nil
}

// clickSide focuses a side panel and, on a content row, moves its cursor
// there. Clicking the row the cursor already sits on in the already
// focused panel is `enter` — the second click drills in, which is why
// this is not a double-click: a slow, deliberate second click means the
// same thing as a fast one.
func (m Model) clickSide(h hit) (tea.Model, tea.Cmd) {
	p := m.panels[h.panel]

	if h.title {
		m.setFocus(h.panel)
		return m, nil
	}
	if h.row < 0 {
		return m, nil // the border
	}

	focused := m.focus == h.panel
	m.setFocus(h.panel)
	// Panel [3] is not a list: its box previews the buffer, and the
	// click has done its job by handing it the focus.
	if h.panel == panelQuery {
		return m, nil
	}

	row := h.row
	if _, ok := p.filterLine(m.style); ok {
		row-- // the inline `/` prompt takes the first content row
	}
	if row < 0 {
		return m, nil
	}
	idx := p.offset + row
	if idx < 0 || idx >= len(p.items) {
		return m, nil
	}
	if focused && idx == p.cursor {
		// Connecting can open a password prompt, which drillIn (a plain
		// tea.Cmd) cannot do — the same split updateFocused's enter makes.
		if h.panel == panelConnections {
			return m.runAction(actConnect)
		}
		cmd := m.drillIn()
		return m, cmd
	}
	p.cursor = idx
	p.move(0)
	return m, nil
}

// clickMain focuses the main view and, when it was already focused and is
// showing the grid, moves the cell cursor onto the clicked cell.
func (m Model) clickMain(h hit) (tea.Model, tea.Cmd) {
	// While panel [3] has the focus the main view *is* its editor, so a
	// click there must not hand the focus to the grid behind it.
	if m.focus == panelQuery {
		return m, nil
	}
	if h.title {
		// The Data/Structure/Indexes/DDL/Relations bar rides the title
		// whenever a relation or a result is open — see mainTitle.
		if m.focus != panelConnections && m.data.open() {
			if t, ok := mainTabHit(h.col); ok {
				m.setFocus(panelMain)
				cmd := m.setMainTab(t)
				return m, cmd
			}
		}
		return m, nil
	}
	if !m.data.open() && m.trigger == nil {
		// With nothing open the main view has no cursor to give; taking
		// the focus would only replace the focused panel's summary with
		// "no relation open".
		return m, nil
	}
	wasMain := m.focus == panelMain
	m.setFocus(panelMain)
	if !wasMain || h.row < 0 || m.tab != mainTabData || m.trigger != nil {
		// The first click focuses; the second one, now that the grid is
		// what the box shows, picks a cell in it.
		return m, nil
	}
	m.clickGrid(h.row, h.col)
	return m, nil
}

// clickGrid moves the cell cursor onto the clicked cell. It walks the
// same geometry dataBody renders with: three header rows (names, types,
// rule) and then the row window.
func (m *Model) clickGrid(row, col int) {
	if len(m.data.cols) == 0 || m.data.err != "" || row < 3 {
		return
	}
	_, _, mw, mh, ok := m.mainColumnRect()
	if !ok {
		return
	}
	cw := maxInt(mw-2, 1)
	ch := maxInt(mh-commandLogHeight(mh)-2, 1)

	cols, kinds := m.buildGrid()
	cs, ce := columnWindow(cols, m.data.col, cw)
	rs, re := rowWindow(len(kinds), m.data.row, maxInt(ch-4, 0))
	r := rs + row - 3
	if r < rs || r >= re {
		return
	}
	m.data.row = r
	if c, ok := gridColumnAt(cols[cs:ce], col); ok {
		m.data.col = cs + c
	}
	m.clampCursor()
}

// gridColumnAt maps a cell offset in a rendered grid row onto a column of
// the visible window, accounting for the `│` separator between them. A
// click on a separator belongs to no column and leaves it unchanged.
func gridColumnAt(cols []gridColumn, x int) (int, bool) {
	if x < 0 {
		return 0, false
	}
	at := 0
	for i, c := range cols {
		if i > 0 {
			at += colGap
		}
		if x >= at && x < at+c.width {
			return i, true
		}
		at += c.width
	}
	return 0, false
}

// ---------- tab hit-testing ----------

// mainTabHit maps a cell offset inside the main view's title onto a tab.
// mainTabBar opens with `‹` and separates the labels with `|`.
func mainTabHit(col int) (mainTab, bool) {
	if col < 0 {
		return 0, false
	}
	at := lipgloss.Width("‹")
	for t := mainTab(0); t < mainTabCount; t++ {
		if t > 0 {
			at += lipgloss.Width("|")
		}
		w := lipgloss.Width(mainTabNames[t])
		if col >= at && col < at+w {
			return t, true
		}
		at += w
	}
	return 0, false
}
