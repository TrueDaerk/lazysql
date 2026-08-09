package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"lazysql/internal/db"
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
		if m.focus == panelMain {
			body = m.renderMainColumn(m.width, bodyH)
		} else {
			body = m.renderPanel(m.focus, m.width, bodyH)
		}
	} else {
		sideW := m.width / 3
		if sideW < 24 {
			sideW = 24
		}
		if maxSide := m.width - 30; sideW > maxSide {
			sideW = maxSide
		}
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
	body := m.panels[id].render(m.style, id == m.focus, cw, ch)
	return border.Width(w).Height(h).Render(body)
}

// renderMainColumn stacks the main view and the command log beneath it.
func (m Model) renderMainColumn(w, h int) string {
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
	mainH := h - logH

	border := m.style.blurredBorder
	if m.focus == panelMain {
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

// renderCommandLog shows every statement the app executed, newest last.
func (m Model) renderCommandLog(w, h int) string {
	cw, ch := maxInt(w-2, 1), maxInt(h-2, 1)
	rows := ch - 1 // title line

	var b strings.Builder
	b.WriteString(m.style.title.Render("Command log"))
	if rows > 0 {
		start := len(m.commandLog) - rows
		if start < 0 {
			start = 0
		}
		for _, line := range m.commandLog[start:] {
			b.WriteString("\n" + m.style.muted.Render(truncate(line, cw)))
		}
	}
	return m.style.blurredBorder.Width(w).Height(h).Render(b.String())
}

// renderOptionsBar shows the focused panel's bindings — same slices as `?`.
func (m Model) renderOptionsBar() string {
	h := m.help
	h.SetWidth(maxInt(m.width-len(appName)-len(screenModeNames[m.screen])-6, 10))
	left := h.ShortHelpView(m.keys.optionsBarBindings(m.focus))
	right := fmt.Sprintf("%s · %s", screenModeNames[m.screen], appName)

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
