package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// panelID enumerates the numbered side panels of the left column.
type panelID int

const (
	panelConnections panelID = iota
	panelDatabases
	panelTables
	panelHistory
	panelCount
)

var panelTitles = [panelCount]string{
	"Connections",
	"Databases",
	"Tables",
	"Query history",
}

// itemStatus tints a row. The [1] Connections panel uses it to show which
// profile is live (green), which one last failed (red) and which are idle.
type itemStatus int

const (
	statusIdle itemStatus = iota
	statusOK
	statusError
	statusPending
)

// sidePanel is a plain cursor-over-slice list. Bubbles' list component carries
// filtering and pagination chrome we don't want in a lazygit-style column.
type sidePanel struct {
	id     panelID
	items  []string
	status []itemStatus // parallel to items; shorter means "idle" for the rest
	cursor int
	offset int // index of the first visible row, for scrolling
}

// statusAt reports the status of row i, defaulting to idle.
func (p *sidePanel) statusAt(i int) itemStatus {
	if i >= 0 && i < len(p.status) {
		return p.status[i]
	}
	return statusIdle
}

func (p *sidePanel) move(delta int) {
	if len(p.items) == 0 {
		p.cursor, p.offset = 0, 0
		return
	}
	p.cursor += delta
	if p.cursor < 0 {
		p.cursor = 0
	}
	if p.cursor >= len(p.items) {
		p.cursor = len(p.items) - 1
	}
}

func (p *sidePanel) selected() string {
	if p.cursor >= 0 && p.cursor < len(p.items) {
		return p.items[p.cursor]
	}
	return ""
}

func (p *sidePanel) setItems(items []string) {
	p.items = items
	p.status = nil
	p.move(0)
}

// setItemsWithStatus replaces the rows together with their tint.
func (p *sidePanel) setItemsWithStatus(items []string, status []itemStatus) {
	p.items = items
	p.status = status
	p.move(0)
}

// selectByName moves the cursor onto the named row when it exists.
func (p *sidePanel) selectByName(name string) {
	for i, it := range p.items {
		if it == name {
			p.cursor = i
			p.move(0)
			return
		}
	}
	p.move(0)
}

// visible returns the rows that fit in rows lines, scrolling the window so the
// cursor stays inside it.
func (p *sidePanel) visible(rows int) []string {
	if rows <= 0 || len(p.items) == 0 {
		return nil
	}
	if p.cursor < p.offset {
		p.offset = p.cursor
	}
	if p.cursor >= p.offset+rows {
		p.offset = p.cursor - rows + 1
	}
	if maxOffset := len(p.items) - rows; p.offset > maxOffset {
		p.offset = maxOffset
	}
	if p.offset < 0 {
		p.offset = 0
	}
	end := p.offset + rows
	if end > len(p.items) {
		end = len(p.items)
	}
	return p.items[p.offset:end]
}

// render draws the panel body (title + rows) for a content box of w x h cells.
func (p *sidePanel) render(s styles, focused bool, w, h int) string {
	titleStyle := s.title
	if focused {
		titleStyle = s.titleFocused
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("[%d] %s", int(p.id)+1, panelTitles[p.id])))

	rows := h - 1 // title line
	if rows <= 0 {
		return b.String()
	}
	for i, item := range p.visible(rows) {
		idx := p.offset + i
		line := truncate(item, w)
		style := lipgloss.NewStyle()
		if focused && idx == p.cursor {
			style = s.selected.Width(w)
		}
		// Status color survives selection: the selected row keeps its
		// highlight background and only swaps foreground.
		if fg, ok := statusColor(p.statusAt(idx)); ok {
			style = style.Foreground(fg)
		}
		b.WriteString("\n" + style.Render(line))
	}
	return b.String()
}
