package ui

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

// panelID enumerates the numbered side panels of the left column.
type panelID int

const (
	panelConnections panelID = iota
	// panelObjects is the object tree: databases, their object categories
	// (Tables, Views, Triggers) and the objects themselves, in one panel.
	// It replaced the separate Databases and Tables panels — see
	// objtree.go and wiki/design/object-tree-panel.md.
	panelObjects
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
	"Objects",
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

	// decor prefixes a row with a marker, keyed by the row's own text —
	// panel [1] marks its read-only profiles that way. It decorates the
	// rendering only: the filter, the cursor and selectByName all keep
	// working on the undecorated names.
	decor map[string]string

	// tagColor renders a colored bullet ahead of decor, keyed the same
	// way — panel [1] marks its color-tagged connections that way. It is
	// styled on its own rather than folded into decor because its color
	// must survive the row's status/selection styling, not be overridden
	// by it.
	tagColor map[string]color.Color

	// rows is the flattened object tree behind panel [2], parallel to all.
	// It is nil for a plain list panel, and its presence is what switches
	// the filter and the renderer into their tree behaviour — see
	// objtree.go.
	rows []treeRow

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
	// A plain list replaces whatever tree the panel held: setTreeRows is
	// the only way back into the tree behaviour.
	p.rows = nil
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
		if !p.keepRow(i, it) {
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

// keepRow reports whether row i of all survives the current filter. A
// plain list tests the row's own name; the object tree tests its whole
// subtree, so a match keeps the branches it hangs under — see treeKeep.
func (p *sidePanel) keepRow(i int, name string) bool {
	if p.rows != nil && i < len(p.rows) {
		return p.treeKeep(i)
	}
	return fuzzyMatch(p.filter, name)
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

// titleLine is the panel header: number, name and load state. It is
// spliced into the panel's top border by renderTitledBox, not written as
// a content row.
func (p *sidePanel) titleLine(s styles, focused bool) string {
	titleStyle := s.title
	if focused {
		titleStyle = s.titleFocused
	}
	line := titleStyle.Render(fmt.Sprintf("[%d] %s", int(p.id)+1, panelTitles[p.id]))
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

// render draws the panel body for a content box of w x h cells. The title
// rides in the top border, so every row here is content.
func (p *sidePanel) render(s styles, focused bool, w, h int) string {
	lines := make([]string, 0, h)

	rows := h
	if line, ok := p.filterLine(s); ok && rows > 0 {
		lines = append(lines, truncate(line, w))
		rows--
	}
	if rows <= 0 {
		return strings.Join(lines, "\n")
	}
	for i, item := range p.visible(rows) {
		idx := p.offset + i
		selected := focused && idx == p.cursor

		marker := ""
		if tc, ok := p.tagColor[item]; ok {
			ms := lipgloss.NewStyle().Foreground(tc)
			if selected {
				ms = ms.Background(colorSelectionBg)
			}
			marker = ms.Render(tagMarker + " ")
		}
		markerW := lipgloss.Width(marker)

		// A tree row's indent and chevron ride in front of the name and a
		// load/detail note behind it. Like the color tag the note is
		// rendered on its own rather than folded into the row's text: its
		// color has to survive the selection highlight, not be swallowed
		// by it.
		prefix, note, noteSt := "", "", noteMuted
		if r, ok := p.rowAt(idx); ok {
			prefix = r.prefix()
			if text, st := r.note(); text != "" {
				note, noteSt = " "+text, st
			}
		}
		noteW := lipgloss.Width(note)
		avail := maxInt(w-markerW-noteW, 0)

		style := lipgloss.NewStyle()
		if selected {
			style = s.selected.Width(avail)
		}
		// Status color survives selection: the selected row keeps its
		// highlight background and only swaps foreground.
		if fg, ok := statusColor(p.statusAt(idx)); ok {
			style = style.Foreground(fg)
		}
		line := truncate(prefix+p.decor[item]+item, avail)
		lines = append(lines, marker+style.Render(line)+noteStyleOf(s, noteSt).Render(note))
	}
	return strings.Join(lines, "\n")
}

// noteStyleOf maps a tree row's note kind onto the panel's styles.
func noteStyleOf(s styles, n noteStyle) lipgloss.Style {
	switch n {
	case notePending:
		return s.pending
	case noteDanger:
		return s.danger
	}
	return s.muted
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
