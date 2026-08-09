package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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
		body = m.renderPanel(m.focus, m.width, bodyH)
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

	if m.screen == screenHalf && bodyH >= collapsed*int(panelCount)+2 {
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

	main := m.style.blurredBorder.
		Width(w).Height(mainH).
		Render(m.mainContent(maxInt(w-2, 1), maxInt(mainH-2, 1)))
	if logH <= 0 {
		return main
	}
	return lipgloss.JoinVertical(lipgloss.Left, main, m.renderCommandLog(w, logH))
}

// mainContent is the placeholder detail view for the focused selection. The
// result grid replaces it once the driver layer lands.
func (m Model) mainContent(w, h int) string {
	sel := m.panels[m.focus].selected()
	if sel == "" {
		sel = "(nothing selected)"
	}
	lines := []string{
		m.style.titleFocused.Render(panelTitles[m.focus]) + m.style.muted.Render(" — main view"),
		"",
		"selected: " + sel,
		"",
		m.style.muted.Render("no result set yet — connect and drill in with enter"),
	}
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
