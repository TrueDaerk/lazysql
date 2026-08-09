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
	panelQuery
	panelCount
)

// panelMain is the focusable main view. It is not a side panel — it has
// no number, no entry in Model.panels and no slot in the side column —
// but it takes focus like one so the keybinding table, the options bar,
// the actions menu and `?` stay keyed by a single identifier.
const panelMain = panelCount

var panelTitles = [panelCount + 1]string{
	"Connections",
	"Databases",
	"Tables",
	"Query",
	"Data",
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
//
// all/allStatus hold everything the panel knows; items/status hold the rows
// the current filter lets through and are what the cursor indexes into.
type sidePanel struct {
	id        panelID
	all       []string
	allStatus []itemStatus // parallel to all; shorter means "idle" for the rest
	items     []string
	status    []itemStatus
	cursor    int
	offset    int // index of the first visible row, for scrolling

	// filter is the fuzzy pattern narrowing the list; filtering reports
	// whether `/` input mode is still capturing keys.
	filter    string
	filtering bool
	// idx maps a visible row back to its position in all. It is nil
	// while no filter narrows the list, where the mapping is the
	// identity.
	idx []int

	// tabs are the sub-tab labels drawn next to the title ([3] Tables).
	tabs []string
	tab  int

	// loading marks an in-flight reload: the previous content stays on
	// screen with a "loading…" marker in the title.
	loading bool
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
	p.setItemsWithStatus(items, nil)
}

// setItemsWithStatus replaces the rows together with their tint. The filter
// survives a reload, and so does the selection whenever the named row is
// still present.
func (p *sidePanel) setItemsWithStatus(items []string, status []itemStatus) {
	keep := p.selected()
	p.all = items
	p.allStatus = status
	p.applyFilter()
	if keep == "" || !p.selectByName(keep) {
		p.cursor, p.offset = 0, 0
	}
}

// setFilter narrows the visible rows to the fuzzy matches of pattern.
func (p *sidePanel) setFilter(pattern string) {
	keep := p.selected()
	p.filter = pattern
	p.applyFilter()
	if keep != "" {
		p.selectByName(keep)
	}
}

// clearFilter restores the full list and leaves `/` input mode.
func (p *sidePanel) clearFilter() {
	p.filtering = false
	if p.filter == "" {
		return
	}
	p.setFilter("")
}

// applyFilter recomputes items/status from all/allStatus.
func (p *sidePanel) applyFilter() {
	if p.filter == "" {
		p.items, p.status, p.idx = p.all, p.allStatus, nil
		p.move(0)
		return
	}
	items := make([]string, 0, len(p.all))
	idx := make([]int, 0, len(p.all))
	var status []itemStatus
	for i, it := range p.all {
		if !fuzzyMatch(p.filter, it) {
			continue
		}
		items = append(items, it)
		idx = append(idx, i)
		if len(p.allStatus) > 0 {
			st := statusIdle
			if i < len(p.allStatus) {
				st = p.allStatus[i]
			}
			status = append(status, st)
		}
	}
	p.items, p.status, p.idx = items, status, idx
	p.cursor, p.offset = 0, 0
	p.move(0)
}

// sourceIndex maps a visible row back onto the unfiltered list, so a
// caller holding the data behind the panel can find the record the
// cursor is on even when two records render identically. It returns -1
// when the row does not exist.
func (p *sidePanel) sourceIndex(i int) int {
	if i < 0 || i >= len(p.items) {
		return -1
	}
	if p.idx == nil {
		return i
	}
	return p.idx[i]
}

// selectByName moves the cursor onto the named row and reports whether the
// row was there at all.
func (p *sidePanel) selectByName(name string) bool {
	for i, it := range p.items {
		if it == name {
			p.cursor = i
			p.move(0)
			return true
		}
	}
	p.move(0)
	return false
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

// titleLine is the panel header: number, name, sub-tabs and load state.
func (p *sidePanel) titleLine(s styles, focused bool) string {
	titleStyle := s.title
	if focused {
		titleStyle = s.titleFocused
	}
	line := titleStyle.Render(fmt.Sprintf("[%d] %s", int(p.id)+1, panelTitles[p.id]))
	if len(p.tabs) > 0 {
		parts := make([]string, 0, len(p.tabs))
		for i, t := range p.tabs {
			if i == p.tab {
				parts = append(parts, s.keyHint.Render(t))
				continue
			}
			parts = append(parts, s.muted.Render(t))
		}
		line += " " + s.muted.Render("‹") + strings.Join(parts, s.muted.Render("|")) + s.muted.Render("›")
	}
	if p.loading {
		line += " " + s.pending.Render("loading…")
	}
	return line
}

// filterLine is the inline `/` prompt; it only takes a row while a filter is
// being typed or is still narrowing the list.
func (p *sidePanel) filterLine(s styles) (string, bool) {
	if !p.filtering && p.filter == "" {
		return "", false
	}
	cursor := ""
	if p.filtering {
		cursor = "▏"
	}
	line := s.keyHint.Render("/"+p.filter) + cursor
	if len(p.items) == 0 {
		line += " " + s.danger.Render("no match")
	}
	return line, true
}

// render draws the panel body (title + rows) for a content box of w x h cells.
func (p *sidePanel) render(s styles, focused bool, w, h int) string {
	var b strings.Builder
	b.WriteString(truncate(p.titleLine(s, focused), w))

	rows := h - 1 // title line
	if line, ok := p.filterLine(s); ok && rows > 0 {
		b.WriteString("\n" + truncate(line, w))
		rows--
	}
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

// fuzzyMatch reports whether pattern occurs in s as a case-insensitive
// subsequence — "usr" matches "users" and "user_roles". Cheap enough to run
// on every keystroke, and no dependency needed.
func fuzzyMatch(pattern, s string) bool {
	if pattern == "" {
		return true
	}
	pr := []rune(strings.ToLower(pattern))
	i := 0
	for _, c := range strings.ToLower(s) {
		if c == pr[i] {
			if i++; i == len(pr) {
				return true
			}
		}
	}
	return false
}
