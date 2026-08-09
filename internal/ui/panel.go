package ui

import (
	"fmt"
	"strings"
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

// sidePanel is a plain cursor-over-slice list. Bubbles' list component carries
// filtering and pagination chrome we don't want in a lazygit-style column.
type sidePanel struct {
	id     panelID
	items  []string
	cursor int
	offset int // index of the first visible row, for scrolling
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
		line := truncate(item, w)
		if focused && p.offset+i == p.cursor {
			line = s.selected.Width(w).Render(line)
		}
		b.WriteString("\n" + line)
	}
	return b.String()
}
