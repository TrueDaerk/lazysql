package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

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
		// Panel [5] edits in the main view, so full-screening it means
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
		px := (m.width - lipgloss.Width(box)) / 2
		py := (m.height - lipgloss.Height(box)) / 2
		full = lipgloss.NewCompositor(
			lipgloss.NewLayer(full),
			lipgloss.NewLayer(box).X(maxInt(px, 0)).Y(maxInt(py, 0)).Z(1),
		).Render()
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
	const collapsed = 3 // top border + title + bottom border

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
	// Panel [5] is not a list, so it renders itself; every other side
	// panel is a cursor over a slice of strings.
	body := clipHeight(m.queryPanelBody(cw, ch), ch)
	if id != panelQuery {
		body = m.panels[id].render(m.style, id == m.focus, cw, ch)
	}
	return border.Width(w).Height(h).Render(body)
}

// renderMainColumn stacks the main view and the command log beneath it.
func (m Model) renderMainColumn(w, h int) string {
	logH := commandLogHeight(h)
	mainH := h - logH

	border := m.style.blurredBorder
	// Exactly one green border: the side column keeps it while panel [5]
	// is focused, except in full-screen mode where the column is gone.
	if m.focus == panelMain || (m.focus == panelQuery && m.screen == screenFull) {
		border = m.style.focusedBorder
	}
	main := border.
		Width(w).Height(mainH).
		Render(m.mainContent(maxInt(w-2, 1), maxInt(mainH-2, 1)))
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
// its border, the query view's header line, the editor block — because
// the layer compositor places by screen coordinate and none of those
// frames knows where it ended up.
//
// The popup is kept inside the terminal: it flips above the caret when
// there is no room below, and slides left at the right edge.
func (m Model) completionLayer() (box string, x, y int, ok bool) {
	if !m.completion.open || m.modal != nil || m.focus != panelQuery || !m.editor.editing {
		return "", 0, 0, false
	}
	mx, my, mw, mh, ok := m.mainColumnRect()
	if !ok {
		return "", 0, 0, false
	}
	// Inside the main column: the box's border, then the query view's
	// header line, then the editor block.
	cw, ch := maxInt(mw-2, 1), maxInt(mh-commandLogHeight(mh)-2, 1)
	rows := ch - 1 // the header
	if rows < 1 {
		return "", 0, 0, false
	}
	editorH := m.editorHeight(cw, rows)
	caretRow, caretCol, ok := m.editorCaret(cw, editorH)
	if !ok {
		return "", 0, 0, false
	}
	ax, ay := mx+1+caretCol, my+1+1+caretRow

	box = m.completionPopup(m.width, m.height-1)
	if box == "" {
		return "", 0, 0, false
	}
	x, y = placePopup(ax, ay, lipgloss.Width(box), lipgloss.Height(box), m.width, m.height-1)
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

// mainContent is the main view: the Data tab of the open relation, the
// selected connection's settings, or — with nothing opened yet — a
// summary of what the focused panel points at.
func (m Model) mainContent(w, h int) string {
	if m.focus == panelConnections {
		return m.connectionDetail(w, h)
	}
	// Panel [4] shows the selected statement in full: the side column
	// has room for one line of it and the engine and timestamp have to
	// live somewhere.
	if m.focus == panelHistory {
		return m.historyDetail(w, h)
	}
	// Panel [5] edits here: the side column has room for a preview of the
	// buffer, not for typing in it.
	if m.focus == panelQuery {
		return m.queryContent(w, h)
	}
	if m.data.open() {
		if m.tab.metadata() {
			return m.metaContent(w, h)
		}
		return m.dataContent(w, h)
	}
	if m.focus >= panelCount {
		return m.style.muted.Render("no relation open — pick one in [3] Tables")
	}
	sel := m.panels[m.focus].selected()
	if sel == "" {
		sel = "(nothing selected)"
	}
	lines := []string{
		m.style.titleFocused.Render(panelTitles[m.focus]) + m.style.muted.Render(" — main view"),
		"",
		"selected: " + sel,
	}
	if m.active != "" {
		lines = append(lines,
			"connection: "+m.active,
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
		m.style.muted.Render("nothing open — pick a relation in [3] Tables, or press : to run a query"))
	return joinTruncated(lines, w, h)
}

// connectionDetail is the main view for panel [1]: the selected profile's
// settings, its live status, and the last error it produced. It never shows a
// password — passwords live only in the keyring.
func (m Model) connectionDetail(w, h int) string {
	lines := []string{
		m.style.titleFocused.Render(panelTitles[panelConnections]) + m.style.muted.Render(" — main view"),
		"",
	}
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
	lines = append(lines,
		"name    "+c.Name,
		"engine  "+engine,
		"status  "+statusStyle.Render(status),
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
	cw, ch := maxInt(w-2, 1), maxInt(h-2, 1)
	rows := ch - 1 // title line

	var b strings.Builder
	b.WriteString(m.style.title.Render("Command log"))
	if rows > 0 {
		entries := m.commandLogEntries()
		start := len(entries) - rows
		if start < 0 {
			start = 0
		}
		for _, e := range entries[start:] {
			style := m.style.muted
			if e.err {
				style = m.style.danger
			}
			b.WriteString("\n" + style.Render(truncate(e.render(), cw)))
		}
	}
	return m.style.blurredBorder.Width(w).Height(h).Render(b.String())
}

// renderOptionsBar shows the focused panel's bindings — same slices as `?`.
func (m Model) renderOptionsBar() string {
	h := m.help
	h.SetWidth(maxInt(m.width-len(appName)-len(screenModeNames[m.screen])-len(version.Version)-9, 10))
	bindings := m.keys.optionsBarBindings(m.focus)
	// Insert mode leaves almost nothing bound, so the bar shows the keys
	// that still act instead of a list the buffer would swallow — and an
	// open popup narrows that further to the four keys it claims.
	if m.focus == panelQuery && m.editor.editing {
		bindings = m.keys.editorInsert()
		if m.completion.open {
			bindings = m.keys.editorCompletion()
		}
	}
	left := h.ShortHelpView(bindings)
	right := fmt.Sprintf("%s · %s · %s", screenModeNames[m.screen], appName, version.Version)

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
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	runes := []rune(s)
	for len(runes) > 0 && lipgloss.Width(string(runes))+1 > w {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…"
}
