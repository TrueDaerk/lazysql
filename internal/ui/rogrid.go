package ui

import (
	"fmt"

	"charm.land/lipgloss/v2"
)

// roGrid is the data grid without the database behind it: formatted
// columns, a cell cursor with its own scroll windows, and a selection —
// and nothing that belongs to an editable page (staged changes, phantom
// rows, paging, server round trips). A view that only ever shows rows
// gets the grid's navigation, its selection and its copy scopes by
// holding one of these instead of re-implementing them.
//
// The server activity report is the first user; see
// wiki/design/read-only-grid.md for why the split runs here.
//
// Cells are strings, because a read-only grid renders what it was handed
// rather than formatting values it owns. The values a copy carries are
// the owner's business — activityValues is where the report keeps its
// own — so a rendering placeholder like "—" never reaches the clipboard.
type roGrid struct {
	cols []gridColumn
	// kinds is the per-row tint, one entry per row. An owner that has no
	// use for one leaves it nil and every row renders plain.
	kinds []rowKind
	n     int

	// Cell cursor and the top-left cell of the scroll windows. The
	// offsets are hints clamped on every render, exactly like the data
	// grid's — see wiki/design/grid-cursor-window.md.
	row, col       int
	rowOff, colOff int

	// sel is the selection, shared with the data grid down to the type:
	// an anchor plus the cell cursor.
	sel gridSelection

	// cs/ce and rs/re are the windows the last render settled on. A
	// click is mapped back through them rather than through a layout
	// recomputed from a box the frame may no longer have.
	cs, ce int
	rs, re int
}

// gridSelection is a grid's selection: an anchor row and the cursor, with
// everything between them selected. Only the anchor is stored — the other
// end is the cell cursor itself, which is what makes `j`/`k` extend the
// selection without a second movement path.
//
// A selection starts out full-width — every column of the marked rows —
// and narrows to a block only once `shift+←`/`shift+→` anchors a column
// too (cols). The same anchor-plus-cursor trick applies sideways:
// colAnchor is one edge and the cell cursor's column is the other.
//
// Both anchors are indices into the rows that were on screen when the
// selection started. In the data grid every reload, sort, filter and page
// turn therefore drops it (see dataView.clearSelection) rather than risk
// a stale index pointing at a different row; the activity report, whose
// rows carry a session ID, re-anchors instead — see activityView.setRows.
type gridSelection struct {
	active bool
	anchor int

	// cols reports whether the column span is part of the selection. A
	// selection without one covers every column, which is what a
	// row-wise selection has always meant.
	cols      bool
	colAnchor int
}

// rowRange is the half-open range of rows the selection covers, given
// where the cursor sits and how many rows can take part. Both grids read
// it, which is why it takes the cursor rather than reading one.
func (s gridSelection) rowRange(cursor, n int) (start, end int) {
	if !s.active || n <= 0 {
		return 0, 0
	}
	start, end = s.anchor, cursor
	if start > end {
		start, end = end, start
	}
	if start < 0 {
		start = 0
	}
	if end >= n {
		end = n - 1
	}
	if start > end {
		return 0, 0
	}
	return start, end + 1
}

// colRange is the half-open range of columns the selection covers. A
// selection that never anchored a column covers all of them, so the
// row-wise scopes keep reading the full width without asking whether a
// block is up.
func (s gridSelection) colRange(cursor, n int) (start, end int) {
	if !s.active || n <= 0 {
		return 0, 0
	}
	if !s.cols {
		return 0, n
	}
	start, end = s.colAnchor, cursor
	if start > end {
		start, end = end, start
	}
	if start < 0 {
		start = 0
	}
	if end >= n {
		end = n - 1
	}
	if start > end {
		return 0, 0
	}
	return start, end + 1
}

// ---------- content ----------

// setCells replaces the grid's content. Widths are settled here, from the
// headers and every cell, so the header, the rows and a click all agree
// on one geometry until the next set of rows lands.
func (g *roGrid) setCells(headers []string, rows [][]string, kinds []rowKind) {
	g.cols = make([]gridColumn, len(headers))
	for i, h := range headers {
		// nulls and staged stay all-false: a read-only grid renders text it
		// was handed, so it has neither a SQL NULL nor a staged edit to
		// mark. They are allocated all the same, because gridRow reads
		// them per cell alongside the text.
		c := gridColumn{
			header: h,
			cells:  make([]string, len(rows)),
			nulls:  make([]bool, len(rows)),
			staged: make([]bool, len(rows)),
			width:  lipgloss.Width(h),
		}
		for r, row := range rows {
			if i < len(row) {
				c.cells[r] = row[i]
			}
			if w := lipgloss.Width(c.cells[r]); w > c.width {
				c.width = w
			}
		}
		if c.width < minColWidth {
			c.width = minColWidth
		}
		if c.width > maxColWidth {
			c.width = maxColWidth
		}
		g.cols[i] = c
	}
	g.n = len(rows)
	g.kinds = kinds
	g.clampCursor()
}

// kindAt is the tint of one row; rowPlain when the owner set none.
func (g *roGrid) kindAt(r int) rowKind {
	if r < 0 || r >= len(g.kinds) {
		return rowPlain
	}
	return g.kinds[r]
}

// cellAt is the rendered text of one cell.
func (g *roGrid) cellAt(r, c int) (string, bool) {
	if c < 0 || c >= len(g.cols) || r < 0 || r >= len(g.cols[c].cells) {
		return "", false
	}
	return g.cols[c].cells[r], true
}

// header names one column, for a menu label or a copy's file name.
func (g *roGrid) header(c int) string {
	if c < 0 || c >= len(g.cols) {
		return ""
	}
	return g.cols[c].header
}

func (g *roGrid) clampCursor() {
	g.row = clampInt(g.row, 0, maxInt(g.n-1, 0))
	g.col = clampInt(g.col, 0, maxInt(len(g.cols)-1, 0))
}

// ---------- selection ----------

func (g *roGrid) selecting() bool { return g.sel.active }

func (g *roGrid) selectedRows() []int  { return rangeSlice(g.sel.rowRange(g.row, g.n)) }
func (g *roGrid) selectedCols() []int  { return rangeSlice(g.sel.colRange(g.col, len(g.cols))) }
func (g *roGrid) clearSelection()      { g.sel = gridSelection{} }
func (g *roGrid) inSelection(r int) bool {
	start, end := g.sel.rowRange(g.row, g.n)
	return r >= start && r < end
}

// narrowedToCols reports whether the selection is a block rather than
// whole rows — i.e. whether a column span was anchored and it leaves at
// least one column out.
func (g *roGrid) narrowedToCols() bool {
	if !g.sel.cols {
		return false
	}
	start, end := g.sel.colRange(g.col, len(g.cols))
	return end-start < len(g.cols)
}

// cellSelected reports whether a rendered cell is part of the selection:
// its row takes part and its column is inside the block.
func (g *roGrid) cellSelected(r, c int) bool {
	if !g.inSelection(r) {
		return false
	}
	start, end := g.sel.colRange(g.col, len(g.cols))
	return c >= start && c < end
}

// startSelection anchors a selection at the cursor row if none is up, and
// reports whether the grid can hold one at all.
func (g *roGrid) startSelection() bool {
	if g.sel.active {
		return true
	}
	if g.n == 0 || g.row < 0 || g.row >= g.n {
		return false
	}
	g.sel = gridSelection{active: true, anchor: g.row}
	return true
}

// rangeSlice expands a half-open range into the indices it holds.
func rangeSlice(start, end int) []int {
	if end <= start {
		return nil
	}
	out := make([]int, 0, end-start)
	for i := start; i < end; i++ {
		out = append(out, i)
	}
	return out
}

// ---------- rendering ----------

// roGridLines renders the grid into a w x h content box: the header, the
// visible window of rows and, when the columns do not all fit, the same
// horizontal-scroll hint the data grid shows. It settles the scroll
// windows as a side effect, which is what a click is then mapped back
// through — one function behind what is drawn and what a click selects,
// exactly like gridLayout is for the data grid.
func (m Model) roGridLines(g *roGrid, cur gridCursor, w, h int) []string {
	if h <= 0 || len(g.cols) == 0 {
		return nil
	}
	g.clampCursor()
	g.cs, g.ce = columnWindow(g.cols, g.col, w, g.colOff)
	g.colOff = g.cs
	hint := g.cs > 0 || g.ce < len(g.cols)

	head := gridHeaderRows(g.cols)
	body := h - head
	if hint {
		body--
	}
	g.rs, g.re = rowWindow(g.n, g.row, maxInt(body, 0), g.rowOff)
	g.rowOff = g.rs

	out := []string{m.gridHeader(g.cols[g.cs:g.ce], g.cs, cur, w)}
	for r := g.rs; r < g.re; r++ {
		out = append(out, m.gridRow(g.cols[g.cs:g.ce], g.cs, r, cur, g.kindAt(r), w))
	}
	if hint {
		out = append(out, m.style.muted.Render(fmt.Sprintf(
			"columns %d–%d of %d — h/l scrolls", g.cs+1, g.ce, len(g.cols))))
	}
	return out
}

// clickRow maps a content row of the box the grid was last rendered into
// onto one of its rows, and a cell offset onto one of its columns. It
// walks the windows the render settled, so what the frame shows and what
// the click lands on cannot drift apart.
func (g *roGrid) clickRow(row, col int) bool {
	head := gridHeaderRows(g.cols)
	r := g.rs + row - head
	if row < head || r < g.rs || r >= g.re || r >= g.n {
		return false
	}
	g.row = r
	if g.ce <= g.cs {
		return true
	}
	if c, ok := gridColumnAt(g.cols[g.cs:g.ce], col); ok {
		g.col = g.cs + c
	}
	g.clampCursor()
	return true
}
