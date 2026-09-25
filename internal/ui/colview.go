package ui

import (
	"fmt"
	"slices"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/db"
	"lazysql/internal/export"
)

// Pinned and hidden columns of the data grid (issue #222). Both are kept
// by column name on the dataView, so they survive everything that keeps
// the dataView — a page turn, a sort, a filter, a reload — and go with
// it when a different relation replaces it. The cell cursor stays a
// column index into d.cols: every action that reads it (edit, sort,
// view, follow a foreign key) keeps meaning the column it always meant.
// What pinning and hiding change is the *display order* — pinned columns
// first, in the order they were pinned, then the rest in table order,
// hidden ones left out — and every sideways gesture walks that order.
// See wiki/design/grid-pinned-hidden-columns.md.

// visibleOrder is the display order: the data-column indices the grid
// draws, left to right. It never comes back empty for a result that has
// columns — a hidden set that somehow covers them all (a relation whose
// columns were renamed under it cannot, but a guard is cheaper than a
// blank grid) is ignored rather than honoured.
func (d dataView) visibleOrder() []int {
	out := make([]int, 0, len(d.cols))
	for _, name := range d.pinned {
		if i := d.colIndex(name); i >= 0 && !slices.Contains(d.hidden, name) && !slices.Contains(out, i) {
			out = append(out, i)
		}
	}
	// Pinned columns are skipped by index, not by name: a query result
	// may carry two columns of one name, and pinning the first must not
	// drop the second.
	for i, c := range d.cols {
		if !slices.Contains(d.hidden, c.Name) && !slices.Contains(out, i) {
			out = append(out, i)
		}
	}
	if len(out) == 0 && len(d.cols) > 0 {
		for i := range d.cols {
			out = append(out, i)
		}
	}
	return out
}

// pinnedCount is how many columns lead the display order as pinned.
func (d dataView) pinnedCount() int {
	n := 0
	for _, name := range d.pinned {
		if d.colIndex(name) >= 0 && !slices.Contains(d.hidden, name) {
			n++
		}
	}
	return n
}

// colIndex is the index of the first column called name, or -1.
func (d dataView) colIndex(name string) int {
	for i, c := range d.cols {
		if c.Name == name {
			return i
		}
	}
	return -1
}

// colPos is where data column c sits in the display order, or -1 when
// it is hidden.
func (d dataView) colPos(c int) int {
	return slices.Index(d.visibleOrder(), c)
}

// colHidden reports whether data column c is hidden.
func (d dataView) colHidden(c int) bool {
	return c >= 0 && c < len(d.cols) && d.colPos(c) < 0
}

// stepCol moves the cell cursor delta columns through the display order,
// stopping at either edge.
func (d *dataView) stepCol(delta int) {
	order := d.visibleOrder()
	if len(order) == 0 {
		return
	}
	p := slices.Index(order, d.col)
	if p < 0 {
		p = 0
	}
	d.col = order[clampInt(p+delta, 0, len(order)-1)]
}

// settleCol moves a cursor that sits on a hidden column onto the nearest
// visible one — the next one in table order, or the previous one when
// it was the last — so a hide never leaves the cursor on something the
// grid does not draw.
func (d *dataView) settleCol() {
	if !d.colHidden(d.col) {
		return
	}
	for c := d.col + 1; c < len(d.cols); c++ {
		if !d.colHidden(c) {
			d.col = c
			return
		}
	}
	for c := d.col - 1; c >= 0; c-- {
		if !d.colHidden(c) {
			d.col = c
			return
		}
	}
}

// visibleColumns is the columns the copy and export scopes act on — the
// ones on screen, in the order they are drawn — with the data indices
// they sit at, so a row can be cut to the same shape. A hidden column is
// never silently carried into a copy or a file.
func (d dataView) visibleColumns() ([]db.Column, []int) {
	idx := d.visibleOrder()
	cols := make([]db.Column, len(idx))
	for i, c := range idx {
		cols[i] = d.cols[c]
	}
	return cols, idx
}

// projection is visibleColumns for a result streamed back from the
// server; nil when every column is on screen in table order, so a plain
// grid streams exactly as it always did.
func (d dataView) projection() export.Projection {
	idx := d.visibleOrder()
	if len(idx) == len(d.cols) && slices.IsSorted(idx) {
		return nil
	}
	p := make(export.Projection, len(idx))
	for i, c := range idx {
		p[i] = export.ProjectedColumn{Name: d.cols[c].Name, Index: c}
	}
	return p
}

// ---------- actions ----------

// togglePin is `p`: pin the cursor column to the left edge of the grid,
// or unpin it again. Several columns can be pinned; they lead the grid
// in the order they were pinned.
func (m *Model) togglePin() tea.Cmd {
	d := &m.data
	if m.tab.metadata() || d.col < 0 || d.col >= len(d.cols) {
		return nil
	}
	name := d.cols[d.col].Name
	if i := slices.Index(d.pinned, name); i >= 0 {
		d.pinned = slices.Delete(slices.Clone(d.pinned), i, i+1)
		m.clampCursor()
		return logCmd("-- column %s unpinned", name)
	}
	d.pinned = append(slices.Clone(d.pinned), name)
	m.clampCursor()
	return logCmd("-- column %s pinned (p unpins it)", name)
}

// hideColumn is `z`: take the cursor column out of the grid — and out of
// every copy and export scope. The last visible column cannot go: a grid
// with nothing in it would have no cursor to bring the others back from.
func (m *Model) hideColumn() tea.Cmd {
	d := &m.data
	if m.tab.metadata() || d.col < 0 || d.col >= len(d.cols) {
		return nil
	}
	name := d.cols[d.col].Name
	if len(d.visibleOrder()) <= 1 {
		return logCmd("-- hide skipped: %s is the last visible column", name)
	}
	d.hidden = append(slices.Clone(d.hidden), name)
	// A pinned column that is hidden is no longer pinned: showing it again
	// puts it back in its table position, not at an edge it left.
	if i := slices.Index(d.pinned, name); i >= 0 {
		d.pinned = slices.Delete(slices.Clone(d.pinned), i, i+1)
	}
	anchorGone := d.sel.cols && d.sel.colAnchor == d.col
	d.settleCol()
	if anchorGone {
		d.sel.colAnchor = d.col
	}
	m.clampCursor()
	return logCmd("-- column %s hidden (Z shows hidden columns)", name)
}

// hiddenColumnsMenu is `Z`: every hidden column as a menu entry that
// shows it again, plus one that shows them all.
func (m *Model) hiddenColumnsMenu() tea.Cmd {
	d := m.data
	if m.tab.metadata() || !d.hasResult() {
		return nil
	}
	var names []string
	for _, c := range d.cols {
		if slices.Contains(d.hidden, c.Name) && !slices.Contains(names, c.Name) {
			names = append(names, c.Name)
		}
	}
	if len(names) == 0 {
		return logCmd("-- no hidden columns")
	}
	var entries []menuEntry
	for i, name := range names {
		k := ""
		if i < 9 {
			k = fmt.Sprint(i + 1)
		}
		entries = append(entries, menuEntry{key: k, label: "show " + name, action: func(mm *Model) tea.Cmd {
			return mm.showColumns(name)
		}})
	}
	entries = append(entries,
		menuEntry{key: "A", label: fmt.Sprintf("show all %d", len(names)), action: func(mm *Model) tea.Cmd {
			return mm.showColumns(names...)
		}},
		menuEntry{key: "esc", label: "cancel"})
	m.modal = &menuModal{title: fmt.Sprintf("Hidden columns — %s", m.dataSubject()), entries: entries}
	return nil
}

// showColumns brings hidden columns back into the grid.
func (m *Model) showColumns(names ...string) tea.Cmd {
	d := &m.data
	d.hidden = slices.DeleteFunc(slices.Clone(d.hidden), func(h string) bool {
		return slices.Contains(names, h)
	})
	m.clampCursor()
	if len(names) == 1 {
		return logCmd("-- column %s shown", names[0])
	}
	return logCmd("-- %d columns shown", len(names))
}
