package ui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"lazysql/internal/db"
	"lazysql/internal/version"
)

const appName = "lazysql"

// View composes side column + main view + command log + options bar, then
// composites the open modal centered on top.
func (m Model) View() tea.View {
	v := tea.NewView("")
	v.AltScreen = true
	v.WindowTitle = appName
	// Cell-motion tracking: clicks, releases and the wheel, but no motion
	// reports — nothing in the UI follows a drag, and all-motion would put
	// a message on the queue for every cell the pointer crosses. See
	// wiki/design/mouse-support.md.
	v.MouseMode = tea.MouseModeCellMotion

	if m.width <= 0 || m.height <= 0 {
		v.SetContent("")
		return v
	}
	if m.width < minWidth || m.height < minHeight {
		v.SetContent(m.tooSmallView())
		return v
	}

	bodyH := m.height - 1 // options bar
	var body string
	if m.screen == screenFull {
		// Panel [3] edits in the main view, so full-screening it means
		// the editor, not the side column's preview of it.
		if m.focus == panelMain || m.focus == panelQuery {
			body = m.renderMainColumn(m.width, bodyH)
		} else {
			body = m.renderPanel(m.focus, m.width, bodyH)
		}
	} else {
		sideW := m.sideWidth()
		mainW := m.width - sideW

		heights := m.panelHeights(bodyH)
		cols := make([]string, 0, panelCount)
		for id := panelID(0); id < panelCount; id++ {
			if heights[id] <= 0 {
				continue
			}
			cols = append(cols, m.renderPanel(id, sideW, heights[id]))
		}
		side := lipgloss.JoinVertical(lipgloss.Left, cols...)
		body = lipgloss.JoinHorizontal(lipgloss.Top, side, m.renderMainColumn(mainW, bodyH))
	}

	full := lipgloss.JoinVertical(lipgloss.Left, body, m.renderOptionsBar())

	// The completion popup and a modal never overlap: the popup lives in
	// insert mode and a modal takes every key before the editor sees one,
	// so whichever is up is the only floating layer on screen.
	if box, px, py, ok := m.completionLayer(); ok {
		full = lipgloss.NewCompositor(
			lipgloss.NewLayer(full),
			lipgloss.NewLayer(box).X(px).Y(py).Z(1),
		).Render()
	}

	if m.modal != nil {
		box := m.modal.view(m.style, m.width, m.height)
		px := maxInt((m.width-lipgloss.Width(box))/2, 0)
		py := maxInt((m.height-lipgloss.Height(box))/2, 0)
		layers := []*lipgloss.Layer{
			lipgloss.NewLayer(full),
			lipgloss.NewLayer(box).X(px).Y(py).Z(1),
		}
		// A form's path candidates float over the modal rather than inside
		// it, so they are a second layer on top of the one just placed —
		// anchored from the box's origin, which is only known here.
		if pop, x, y, ok := m.pathSuggestLayer(px, py); ok {
			layers = append(layers, lipgloss.NewLayer(pop).X(x).Y(y).Z(2))
		}
		full = lipgloss.NewCompositor(layers...).Render()
	}

	v.SetContent(full)
	return v
}

// sideWidth is the side column's total width, borders included. The main
// column gets the rest.
func (m Model) sideWidth() int {
	w := m.width / 3
	if w < 24 {
		w = 24
	}
	if maxSide := m.width - 30; w > maxSide {
		w = maxSide
	}
	return w
}

// mainColumnRect is where the main column's box sits on screen, in
// absolute cells. The completion popup is composited in screen
// coordinates, so it needs the same numbers View lays the body out with —
// which is why they live here rather than inline in View. ok is false in
// full-screen mode with a side panel focused, where the main column is
// not on screen at all.
func (m Model) mainColumnRect() (x, y, w, h int, ok bool) {
	bodyH := m.height - 1
	if m.screen == screenFull {
		if m.focus != panelMain && m.focus != panelQuery {
			return 0, 0, 0, 0, false
		}
		return 0, 0, m.width, bodyH, true
	}
	sideW := m.sideWidth()
	return sideW, 0, m.width - sideW, bodyH, true
}

func (m Model) tooSmallView() string {
	msg := fmt.Sprintf("terminal too small\n\n%dx%d — need at least %dx%d",
		m.width, m.height, minWidth, minHeight)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
		m.style.pending.Render(msg))
}

// panelHeights distributes the side column's rows across panels according to
// the current screen mode. Every returned height includes the border rows.
func (m Model) panelHeights(bodyH int) [panelCount]int {
	var out [panelCount]int
	// The title rides in the top border, so a collapsed panel still shows
	// its name plus one row of content.
	const collapsed = 3 // top border + one row + bottom border

	// Half mode expands the focused *side* panel. With the main view
	// focused there is nothing in the column to expand, so it keeps the
	// even split.
	if m.screen == screenHalf && m.focus < panelCount && bodyH >= collapsed*int(panelCount)+2 {
		for id := panelID(0); id < panelCount; id++ {
			out[id] = collapsed
		}
		out[m.focus] = bodyH - collapsed*(int(panelCount)-1)
		return out
	}

	base := bodyH / int(panelCount)
	for id := panelID(0); id < panelCount; id++ {
		out[id] = base
	}
	out[panelCount-1] = bodyH - base*(int(panelCount)-1)
	return out
}

func (m Model) renderPanel(id panelID, w, h int) string {
	border := m.style.blurredBorder
	if id == m.focus {
		border = m.style.focusedBorder
	}
	// In lipgloss v2 Style.Width/Height are the *total* block size, borders
	// included; the content area is 2 cells smaller in each direction.
	cw, ch := maxInt(w-2, 1), maxInt(h-2, 1)
	// Panel [3] is not a list, so it renders itself; every other side
	// panel is a cursor over a slice of strings.
	title := m.queryPanelTitle()
	body := clipHeight(m.queryPanelBody(cw, ch), ch)
	if id != panelQuery {
		title = m.panels[id].titleLine(m.style, id == m.focus)
		body = m.panels[id].render(m.style, id == m.focus, cw, ch)
	}
	return renderTitledBox(border, title, body, w, h)
}

// renderMainColumn stacks the main view and the command log beneath it.
func (m Model) renderMainColumn(w, h int) string {
	logH := commandLogHeight(h)
	mainH := h - logH

	border := m.style.blurredBorder
	if m.mainFocused() {
		border = m.style.focusedBorder
	}
	// The active connection's color tag tints only the top border segment,
	// never the sides/bottom that carry focus/blur — see
	// wiki/design/connection-color-tags.md for why the tag rides the top
	// border instead of replacing the focus color.
	if tc, ok := m.activeTagColor(); ok {
		border = border.BorderTopForeground(tc)
	}
	cw, ch := maxInt(w-2, 1), maxInt(mainH-2, 1)
	main := renderTitledBox(border, m.mainTitle(cw), m.mainContent(cw, ch), w, mainH)
	if logH <= 0 {
		return main
	}
	return lipgloss.JoinVertical(lipgloss.Left, main, m.renderCommandLog(w, logH))
}

// commandLogHeight is how many of the main column's rows the log strip
// under the main view takes, borders included.
func commandLogHeight(h int) int {
	logH := h / 4
	if logH < 5 {
		logH = 5
	}
	if logH > 10 {
		logH = 10
	}
	if logH > h-5 {
		logH = maxInt(h-5, 0)
	}
	return logH
}

// completionLayer is the autocomplete popup and the absolute cell it is
// drawn at. It walks the same nesting View renders — main column box,
// its border, the query view's header line, the editor block, or the
// grid's bottom-pinned filter line — because the layer compositor places
// by screen coordinate and none of those frames knows where it ended up.
//
// The popup is kept inside the terminal: it flips above the caret when
// there is no room below, and slides left at the right edge. The filter
// line takes the flip on every terminal tall enough to have a command
// log under it, which is what makes a popup anchored on the last row of
// the grid readable at all.
func (m Model) completionLayer() (box string, x, y int, ok bool) {
	if !m.completion.open || m.modal != nil {
		return "", 0, 0, false
	}
	mx, my, mw, mh, ok := m.mainColumnRect()
	if !ok {
		return "", 0, 0, false
	}
	var ax, ay int
	switch m.completion.site {
	case siteEditor:
		ax, ay, ok = m.editorAnchor(mx, my, mw, mh)
	case siteFilter:
		ax, ay, ok = m.filterAnchor(mx, my, mw, mh)
	default:
		ok = false
	}
	if !ok {
		return "", 0, 0, false
	}

	// The box is measured and placed against the main column alone, not
	// the full terminal: in a split layout the side column sits to its
	// left, and a popup wide enough to slide past mx would draw over the
	// side panels' borders instead of floating over the editor. Placing
	// in coordinates relative to the column and translating back keeps
	// placePopup's own logic untouched.
	//
	// The filter line's popup gets a shorter column still: the line is
	// the last row of the box, and everything under it — the box's own
	// bottom border, the command log — is not somewhere a list anchored
	// on the grid may be drawn. Ending the budget at the line is what
	// both sizes the box to the room above it and makes placePopup flip
	// it up there.
	limit := mh
	if m.completion.site == siteFilter {
		limit = ay - my + 1
	}
	box = m.completionPopup(mw, limit)
	if box == "" {
		return "", 0, 0, false
	}
	relX, relY := placePopup(ax-mx, ay-my, lipgloss.Width(box), lipgloss.Height(box), mw, limit)
	return box, mx + relX, my + relY, true
}

// editorAnchor is the caret cell of the query editor's buffer inside the
// main column at mx,my,mw,mh: the box's border — whose top line carries
// the title — then the editor block, which is the first content row.
func (m Model) editorAnchor(mx, my, mw, mh int) (x, y int, ok bool) {
	if m.focus != panelQuery || !m.editor.editing {
		return 0, 0, false
	}
	cw, rows := maxInt(mw-2, 1), maxInt(mh-commandLogHeight(mh)-2, 1)
	caretRow, caretCol, ok := m.editorCaret(cw, m.editorHeight(cw, rows))
	if !ok {
		return 0, 0, false
	}
	return mx + 1 + caretCol, my + 1 + caretRow, true
}

// filterAnchor is the caret cell of the grid's inline WHERE line. The
// line is pinned to the last content row of the main box — it stands in
// for the status line there — so its row is the box's height rather than
// anything the grid reports, and its column is what the line recorded
// while rendering itself.
func (m Model) filterAnchor(mx, my, mw, mh int) (x, y int, ok bool) {
	// The clause is only drawn on the Data tab: the metadata tabs render
	// their own content into the same box, with no line to anchor on.
	if !m.filterInputOpen() || !m.data.open() || m.tab.metadata() {
		return 0, 0, false
	}
	cw := maxInt(mw-2, 1)
	rows := maxInt(mh-commandLogHeight(mh)-2, 1)
	return mx + 1 + min(m.filterInput.caret, cw-1), my + rows, true
}

// pathSuggestLayer is an open form's path-completion box and the absolute
// cell it is drawn at. boxX/boxY are where the modal itself was placed:
// the form recorded its completing field's offset inside its own box while
// rendering it (see formModal.view), and this turns that offset into a
// screen coordinate the layer compositor can place.
//
// The box is kept inside the terminal the way the editor's popup is: it
// takes the rows below the field, flips above when there are more of them
// up there, slides left at the right edge, and is dropped entirely when
// neither side has room for a bordered row — a short terminal shows the
// field, not a box drawn over the form's footer.
func (m Model) pathSuggestLayer(boxX, boxY int) (box string, x, y int, ok bool) {
	f, isForm := m.modal.(*formModal)
	if !isForm || !f.anchorOK {
		return "", 0, 0, false
	}
	ax, ay := boxX+f.anchorX, boxY+f.anchorY
	// The border costs two of the rows on whichever side the box lands, and
	// placePopup prefers below, so the budget is the roomier side's.
	rows := min(maxSuggestLines, len(f.sugg.candidates))
	if room := maxInt(m.height-ay-3, ay-2); room < rows {
		rows = room
	}
	if rows < 1 {
		return "", 0, 0, false
	}
	box = f.sugg.popup(m.style, maxInt(min(f.anchorW, m.width-ax-2), minSuggestWidth), rows)
	if box == "" {
		return "", 0, 0, false
	}
	x, y = placePopup(ax, ay, lipgloss.Width(box), lipgloss.Height(box), m.width, m.height)
	return box, x, y, true
}

// placePopup puts a w x h box next to an anchor cell inside a screen of
// screenW x screenH: one row below the anchor, slid left at the right
// edge, and flipped above the anchor when the bottom would cut it off. A
// box that fits neither way stays below and is clipped there, which is
// the half the user is reading anyway.
func placePopup(anchorX, anchorY, w, h, screenW, screenH int) (x, y int) {
	x = anchorX
	if x+w > screenW {
		x = screenW - w
	}
	if x < 0 {
		x = 0
	}
	y = anchorY + 1
	if y+h > screenH && anchorY-h >= 0 {
		y = anchorY - h
	}
	if y < 0 {
		y = 0
	}
	return x, y
}

// mainFocused reports whether the main view owns the keyboard. Exactly one
// green border: the side column keeps it while panel [3] is focused,
// except in full-screen mode where the column is gone.
func (m Model) mainFocused() bool {
	return m.focus == panelMain || (m.focus == panelQuery && m.screen == screenFull)
}

// mainTitle is the main view's border title. It shadows the switch in
// mainContent: whatever owns the box names itself in the top border, and
// its renderer keeps every content row for content.
func (m Model) mainTitle(w int) string {
	// The generic titles follow the box: green and bold while the main
	// view has focus, muted while a side panel does. The views that own
	// the box outright — the diff, the plan, the editor, the tab bar —
	// carry their own emphasis.
	titleStyle := m.style.title
	if m.mainFocused() {
		titleStyle = m.style.titleFocused
	}
	// The activity report is focused in this box rather than overlaid on
	// panel [1], so it names the box wherever the focus currently is.
	if m.activityOwnsMain() {
		return m.activityTitle()
	}
	if m.focus == panelConnections {
		if m.diff != nil {
			return m.diffTitle()
		}
		return titleStyle.Render(panelTitles[panelConnections]) +
			m.style.muted.Render(" — main view")
	}
	if m.focus == panelQuery {
		if m.plan != nil {
			return m.planTitle()
		}
		return m.queryTitle()
	}
	// An open trigger definition owns the main view until esc closes it,
	// the way the plan owns it for panel [3].
	if m.trigger != nil {
		return m.triggerTitle()
	}
	if m.data.open() {
		// The tab bar is the Data/Structure/… title: sub-tabs, the
		// relation, the read-only lock and the loading marker.
		return m.mainTabBar(w)
	}
	if m.focus >= panelCount {
		return titleStyle.Render(panelTitles[panelMain])
	}
	return titleStyle.Render(panelTitles[m.focus]) +
		m.style.muted.Render(" — main view")
}

// mainContent is the main view: the Data tab of the open relation, the
// selected connection's settings, or — with nothing opened yet — a
// summary of what the focused panel points at.
func (m Model) mainContent(w, h int) string {
	// The activity report is the main view's own content: it stays on
	// screen — without claiming any keys — while a side panel is focused,
	// and only esc (or a relation opening here) takes the box back.
	if m.activityOwnsMain() {
		return m.activityContent(w, h)
	}
	if m.focus == panelConnections {
		// An open schema diff replaces the profile detail until esc
		// dismisses it.
		if m.diff != nil {
			return m.diffContent(w, h)
		}
		return m.connectionDetail(w, h)
	}
	// Panel [3] edits here: the side column has room for a preview of the
	// buffer, not for typing in it.
	if m.focus == panelQuery {
		// An open EXPLAIN result replaces the editor until esc dismisses
		// it; the buffer is untouched underneath.
		if m.plan != nil {
			return m.planContent(w, h)
		}
		return m.queryContent(w, h)
	}
	if m.trigger != nil {
		return m.triggerContent(w, h)
	}
	if m.data.open() {
		if m.tab.metadata() {
			return m.metaContent(w, h)
		}
		return m.dataBody(w, h)
	}
	if m.focus >= panelCount {
		return m.style.muted.Render("no relation open — pick one in [2] Objects")
	}
	sel := m.panels[m.focus].selected()
	if sel == "" {
		sel = "(nothing selected)"
	}
	lines := []string{
		"selected: " + sel,
	}
	if m.active != "" {
		lines = append(lines,
			"connection: "+m.tagMarkerFor(m.active)+m.active,
			"database: "+displayDatabase(m.database),
			fmt.Sprintf("relations: %d tables · %d views",
				len(db.FilterRelations(m.relations, db.RelationTable)),
				len(db.FilterRelations(m.relations, db.RelationView))),
		)
	}
	if m.table != "" {
		lines = append(lines, "opened: "+m.table)
	}
	lines = append(lines, "",
		m.style.muted.Render("nothing open — pick a relation in [2] Objects, or press : to run a query"))
	return joinTruncated(lines, w, h)
}

// connectionDetail is the main view for panel [1]: the selected profile's
// settings, its live status, and the last error it produced. It never shows a
// password — passwords live only in the keyring.
func (m Model) connectionDetail(w, h int) string {
	var lines []string
	c, ok := m.selectedConnection()
	if !ok {
		lines = append(lines, m.style.muted.Render("no connections yet — press n to add one"))
		return joinTruncated(lines, w, h)
	}

	state := m.connState[c.Name]
	status := "idle"
	statusStyle := m.style.muted
	switch state.status {
	case statusOK:
		status, statusStyle = "connected", m.style.titleFocused
	case statusError:
		status, statusStyle = "error", m.style.danger
	case statusPending:
		status, statusStyle = "connecting…", m.style.pending
	}

	engine := string(c.Engine)
	if d, err := db.DialectFor(c.Engine); err == nil {
		engine = d.DisplayName()
	}
	access := "read-write"
	accessStyle := m.style.muted
	if c.ReadOnly {
		access, accessStyle = lockMark+" read-only", m.style.pending
	}
	lines = append(lines,
		"name    "+m.tagMarkerFor(c.Name)+c.Name,
		"engine  "+engine,
		"status  "+statusStyle.Render(status),
		"access  "+accessStyle.Render(access),
	)
	if db.FileBased(c.Engine) {
		file := c.File
		if file == "" {
			file = "(in-memory)"
		}
		lines = append(lines, "file    "+file)
	} else {
		lines = append(lines,
			fmt.Sprintf("address %s:%d", c.Host, c.Port),
			"user    "+c.User,
			"db      "+c.Database,
		)
		secret := "OS keyring"
		if c.AskPassword {
			secret = "prompt on connect"
		}
		lines = append(lines, "secret  "+m.style.muted.Render(secret))
	}
	if len(c.Options) > 0 {
		lines = append(lines, "options "+formatOptions(c.Options))
	}
	if state.lastErr != "" {
		lines = append(lines, "", m.style.danger.Render("last error: "+state.lastErr))
	}
	return joinTruncated(lines, w, h)
}

// joinTruncated clips a block of lines to a w x h content box.
func joinTruncated(lines []string, w, h int) string {
	out := make([]string, 0, h)
	for i, l := range lines {
		if i >= h {
			break
		}
		out = append(out, truncate(l, w))
	}
	return strings.Join(out, "\n")
}

// renderCommandLog shows the tail of the command log — every statement
// the app executed plus its own notes, newest last. `@` expands it into
// a full scrollable view; this slim strip truncates long SQL to fit.
func (m Model) renderCommandLog(w, h int) string {
	cw, rows := maxInt(w-2, 1), maxInt(h-2, 1)

	lines := make([]string, 0, rows)
	entries := m.commandLogEntries()
	if start := len(entries) - rows; start > 0 {
		entries = entries[start:]
	}
	for _, e := range entries {
		style := m.style.muted
		if e.err {
			style = m.style.danger
		}
		for _, l := range strings.Split(e.render(), "\n") {
			lines = append(lines, style.Render(truncate(l, cw)))
		}
	}
	// An entry can itself span multiple display lines (wrapped SQL), so
	// the split above may still exceed rows — clip to the newest lines
	// that fit or the box overflows its border and hides the options bar.
	if start := len(lines) - rows; start > 0 {
		lines = lines[start:]
	}
	return renderTitledBox(m.style.blurredBorder,
		m.style.title.Render("Command log"), strings.Join(lines, "\n"), w, h)
}

// optionsBarBindings is what the bottom bar offers for the focused panel.
// A read-only connection drops the keys that could only ever answer
// "connection is read-only"; `?` still lists them, so no binding goes
// undocumented.
func (m Model) optionsBarBindings() []key.Binding {
	out := m.keys.optionsBarBindings(m.focus)
	if m.readOnly() {
		out = withoutBindings(out, m.keys.writeBindings())
	}
	return out
}

// renderOptionsBar shows the focused panel's bindings — same slices as `?`.
func (m Model) renderOptionsBar() string {
	h := m.help
	h.SetWidth(maxInt(m.width-len(appName)-len(screenModeNames[m.screen])-len(version.Version)-9, 10))
	bindings := m.optionsBarBindings()
	// Insert mode leaves almost nothing bound, so the bar shows the keys
	// that still act instead of a list the buffer would swallow — and an
	// open popup narrows that further to the four keys it claims.
	if m.focus == panelQuery && m.editor.editing {
		bindings = m.keys.editorInsert()
		if m.completion.open {
			bindings = m.keys.completionKeys()
		}
	}
	// The inline WHERE line claims every key it does not bind, exactly
	// like insert mode, so the bar shows the ones it does — and narrows
	// to the popup's four while one is open, since ↑/↓ and enter mean
	// something else there.
	if m.filterInputOpen() {
		bindings = m.keys.filterInput()
		if m.completion.open {
			bindings = m.keys.completionKeys()
		}
	}
	// The date picker claims every key while it is open, so the bar shows
	// its own set instead of the grid's — its keys are motions rather
	// than a footer's worth of verbs.
	if _, ok := m.modal.(*datePickerModal); ok {
		bindings = m.keys.datePicker()
	}
	// A form popup swallows every key too, so while one is open the bar
	// offers the form contract — the same slices `?` documents — instead
	// of panel verbs that could not act. A form with its own bar (the
	// connection form adds ctrl+t and ctrl+e) supplies it here.
	if fm, ok := m.modal.(*formModal); ok {
		bindings = m.keys.formKeys()
		if fm.bar != nil {
			bindings = fm.bar(m.keys)
		}
	}
	// A JSON cell popup is a tree, navigated rather than scrolled, so the
	// bar offers its fold keys instead of grid verbs that could not act.
	if cm, ok := m.modal.(*cellModal); ok && cm.tree != nil {
		bindings = m.keys.jsonCellKeys()
	}
	// The engine picker (step one of the connection flow) has its own
	// five-key contract, documented in `?` under the Connections panel.
	if _, ok := m.modal.(*enginePickerModal); ok {
		bindings = m.keys.enginePickerKeys()
	}
	// The activity report claims the main view's keys while it is focused
	// there — `K` kills a session rather than moving a connection — so the
	// bar offers its set instead of the grid's, all of it documented under
	// `?` in the same group. With a side panel focused the list is only on
	// display and the bar stays that panel's.
	if m.activityFocused() && m.modal == nil {
		own := m.keys.serverActivity()
		// A read-only connection cannot kill anything — the driver
		// refuses it — so the key comes off the bar the way the grid's
		// write keys do. `?` keeps listing it.
		if m.readOnly() {
			own = withoutBindings(own, []key.Binding{m.keys.KillProcess})
		}
		bindings = append(own,
			m.keys.Jump, m.keys.NextPanel, m.keys.OpenEditor, m.keys.Help, m.keys.Quit)
	}
	// A trigger definition is a read-only text block, so the grid's own
	// actions would only ever be no-ops there: the bar offers the keys
	// that still act, all of them already documented under `?`.
	if m.focus == panelMain && m.trigger != nil {
		bindings = append([]key.Binding{m.keys.Up, m.keys.Down, m.keys.Back},
			m.keys.Jump, m.keys.NextPanel, m.keys.OpenEditor, m.keys.Help, m.keys.Quit)
	}
	left := h.ShortHelpView(bindings)
	right := fmt.Sprintf("%s · %s · %s", screenModeNames[m.screen], appName, version.Version)
	if m.run.running {
		right = m.runningIndicator() + " · " + right
	}

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return m.style.optionsBar.Width(m.width).Render(truncate(left, m.width))
	}
	return m.style.optionsBar.Width(m.width).Render(left + strings.Repeat(" ", gap) + right)
}

// ---------- helpers ----------

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// truncate shortens s to w display cells, appending an ellipsis when cut.
//
// It measures with ansi.Truncate rather than walking runes itself. Most
// callers hand it text that is already styled — a grid row, the header,
// the options bar — and an escape sequence is bytes, not cells: counting
// its `[`, its digits and its `m` as visible width cut a tinted row cells
// short of its box *and* threw away the closing reset, so the cursor
// cell's tint was both too narrow and bled into the rest of the frame
// (issue #132). ansi.Truncate skips the sequences, counts grapheme
// clusters rather than runes, and re-closes the style it cut through.
// A multi-line string is cut line by line: w is a box width, and
// ansi.Truncate would otherwise measure the rows end to end and swallow
// every one after the first.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if !strings.Contains(s, "\n") {
		return ansi.Truncate(s, w, "…")
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, w, "…")
	}
	return strings.Join(lines, "\n")
}
