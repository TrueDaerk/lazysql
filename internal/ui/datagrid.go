package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"lazysql/internal/db"
)

// Grid geometry. Columns are sized to their content within these
// bounds; anything wider is truncated with an ellipsis and can still be
// read in full with `v`. colGap is the width taken by the column
// separator; it stays a plain int so the width math in columnWindow
// does not care what glyph fills it.
const (
	minColWidth = 4
	maxColWidth = 32
	colGap      = 1
)

// Border glyphs for the grid: a vertical rule between columns and, under
// the header, a horizontal rule with junctions lined up on the same column
// boundaries.
const (
	colSepChar   = "│"
	ruleChar     = "─"
	ruleJunction = "┼"
)

// nullText is how SQL NULL reads in the grid. It is styled dim so it
// cannot be confused with the string "NULL".
const nullText = "NULL"

// rowKind is how a rendered row relates to the changeset: an untouched
// page row, a row staged for deletion, or a phantom row standing for a
// staged insert that does not exist in the database yet.
type rowKind int

const (
	rowPlain rowKind = iota
	rowDeleted
	rowInserted
)

// defaultText marks a phantom row's cell that the INSERT leaves out.
const defaultText = "DEFAULT"

// gridColumn is one rendered column: its header, its type and the
// already-formatted cells of the page under it.
type gridColumn struct {
	header string // name plus the sort marker, if any
	typ    string
	width  int
	cells  []string
	nulls  []bool
	staged []bool
}

// buildGrid formats the whole page once so column widths, the header
// and every row agree on the same strings. The staged inserts of the
// open table are appended as phantom rows after the page, which is why
// it also returns what each rendered row is.
func (m Model) buildGrid() ([]gridColumn, []rowKind) {
	d := m.data
	rowKeys := m.stagedRowKeys()
	inserts := m.stagedInserts()
	n := len(d.rows) + len(inserts)

	kinds := make([]rowKind, n)
	for r := range d.rows {
		if rowKeys != nil && rowKeys[r] != nil &&
			m.changes.DeleteStaged(d.database, d.table, rowKeys[r]) {
			kinds[r] = rowDeleted
		}
	}
	for i := range inserts {
		kinds[len(d.rows)+i] = rowInserted
	}

	fkCols := m.fkColumnSet()
	cols := make([]gridColumn, len(d.cols))
	for i, c := range d.cols {
		header := c.Name
		// A column that takes part in a foreign key is marked, so `g`
		// is discoverable without opening the Indexes tab first.
		if fkCols[strings.ToLower(c.Name)] {
			header += fkMark
		}
		if desc, ok := d.sortOn(c.Name); ok {
			if desc {
				header += " ▼"
			} else {
				header += " ▲"
			}
		}
		g := gridColumn{
			header: header,
			typ:    strings.ToLower(c.DataType),
			cells:  make([]string, n),
			nulls:  make([]bool, n),
			staged: make([]bool, n),
			width:  maxInt(lipgloss.Width(header), lipgloss.Width(c.DataType)),
		}
		for r, row := range d.rows {
			var v any
			if i < len(row) {
				v = row[i]
			}
			// A staged cell shows its staged value — seeing the pending
			// edit in place is the point of staging.
			if rowKeys != nil && rowKeys[r] != nil {
				if ch, ok := m.changes.Lookup(d.database, d.table, rowKeys[r], c.Name); ok {
					v = ch.NewValue
					g.staged[r] = true
				}
			}
			g.nulls[r] = v == nil
			g.cells[r] = gridCellText(v, nullText)
		}
		for j, ins := range inserts {
			r := len(d.rows) + j
			v, bound := insertValueFor(ins, c.Name)
			switch {
			case !bound:
				g.cells[r] = defaultText
			case v == nil:
				g.nulls[r] = true
				g.cells[r] = nullText
			default:
				g.cells[r] = gridCellText(v, nullText)
			}
		}
		for _, cell := range g.cells {
			if w := lipgloss.Width(cell); w > g.width {
				g.width = w
			}
		}
		if g.width < minColWidth {
			g.width = minColWidth
		}
		if g.width > maxColWidth {
			g.width = maxColWidth
		}
		cols[i] = g
	}
	return cols, kinds
}

// stagedRowKeys returns each page row's primary key values, or nil when
// the primary key of the open table is not known. The key columns come
// from the metadata when it is loaded and from the changeset otherwise,
// so highlighting survives a dropped metadata cache.
func (m Model) stagedRowKeys() [][]any {
	pkCols := m.pkColumns()
	if pkCols == nil {
		return nil
	}
	keys := make([][]any, len(m.data.rows))
	for r := range m.data.rows {
		if vals, ok := m.rowKeyVals(pkCols, r); ok {
			keys[r] = vals
		}
	}
	return keys
}

// gridCellText formats a cell's value for the grid, standing a placeholder
// in for BLOBs: raw bytes carry control characters that break the row and
// misalign the right border, and the cell-detail popup (`v`) already gives
// binary values a proper hex dump, so the grid does not need to show them.
func gridCellText(v any, null string) string {
	raw := db.FormatValue(v, null)
	if classifyCell(raw) == cellBinary {
		return fmt.Sprintf("<blob %d B>", len(raw))
	}
	return flatten(raw)
}

// flatten collapses whitespace that would otherwise break the row into
// several lines.
func flatten(s string) string {
	if !strings.ContainsAny(s, "\n\r\t") {
		return s
	}
	return strings.Join(strings.Fields(strings.NewReplacer("\n", " ", "\r", " ", "\t", " ").Replace(s)), " ")
}

// columnWindow picks the slice of columns that fits in w cells, starting
// at off and scrolled just far enough to keep the cursor column visible.
//
// The offset is a hint, not the truth: it is clamped here on every use,
// so a window left over from a wider terminal — or from a result with
// more columns — can never hide the cursor. Scrolling minimally from it
// rather than re-deriving the window from the cursor alone is what keeps
// the cursor cell rendered where it was: a window derived from the cursor
// only would snap back to column 0 the moment the cursor fits in the
// leftmost window, moving the highlight out from under the user.
func columnWindow(cols []gridColumn, cursor, w, off int) (start, end int) {
	if len(cols) == 0 {
		return 0, 0
	}
	cursor = clampInt(cursor, 0, len(cols)-1)
	off = clampInt(off, 0, cursor)
	for start = off; start <= cursor; start++ {
		total := 0
		for end = start; end < len(cols); end++ {
			need := cols[end].width
			if end > start {
				need += colGap
			}
			if total+need > w {
				break
			}
			total += need
		}
		if end == start {
			end = start + 1 // never render zero columns
		}
		if cursor < end {
			return start, end
		}
	}
	return cursor, cursor + 1
}

// rowWindow picks the rows visible in a window of `rows` lines that
// starts at off, scrolled just far enough to keep the cursor row on
// screen. Like columnWindow the offset is clamped rather than trusted,
// which is what makes a resize — or a page that came back shorter —
// safe without a separate invalidation step.
func rowWindow(n, cursor, rows, off int) (start, end int) {
	if rows <= 0 || n <= 0 {
		return 0, 0
	}
	cursor = clampInt(cursor, 0, n-1)
	// Scroll to the cursor when it sits outside the window, and never
	// leave a gap under the last row.
	off = clampInt(off, cursor-rows+1, cursor)
	off = clampInt(off, 0, maxInt(n-rows, 0))
	end = off + rows
	if end > n {
		end = n
	}
	return off, end
}

// clampInt confines v to [lo, hi]. lo wins when the range is empty, which
// is what the window clamps above rely on: the cursor is always inside.
func clampInt(v, lo, hi int) int {
	if v > hi {
		v = hi
	}
	if v < lo {
		v = lo
	}
	return v
}

// gridLayout is the geometry of one rendered grid: the formatted page and
// the two windows of it that fit in the content box. dataBody renders it,
// clickGrid maps a click back through it and Model.clampCursor stores the
// offsets it settled on — one function, so what is highlighted, what a
// click selects and what the cursor points at cannot drift apart.
type gridLayout struct {
	cols   []gridColumn
	kinds  []rowKind
	cs, ce int  // visible column window
	rs, re int  // visible row window
	hint   bool // the columns do not all fit, so the h/l hint takes a row
}

// gridViewport is the content box the grid is rendered into by the
// current layout, in cells. It walks the same numbers View does —
// mainColumnRect, commandLogHeight and, while panel [3] is focused, the
// editor block the result sits under — so a cursor move can settle the
// scroll window on exactly the box the next frame will draw. ok is false
// when the grid is not on screen at all.
func (m Model) gridViewport() (w, h int, ok bool) {
	_, _, mw, mh, ok := m.mainColumnRect()
	if !ok {
		return 0, 0, false
	}
	w = maxInt(mw-2, 1)
	h = mh - commandLogHeight(mh) - 2
	if m.focus == panelQuery {
		// queryContent stacks the editor, its status line and the Data
		// tab's own tab bar above the grid.
		h -= m.editorBlockRows() + 1
	}
	if w <= 0 || h <= 0 {
		return 0, 0, false
	}
	return w, h, true
}

// gridLayout lays the grid out for a w x h content box.
func (m Model) gridLayout(w, h int) gridLayout {
	g := gridLayout{}
	g.cols, g.kinds = m.buildGrid()
	g.cs, g.ce = columnWindow(g.cols, m.data.col, w, m.data.colOff)
	g.hint = g.cs > 0 || g.ce < len(g.cols)
	g.rs, g.re = rowWindow(len(g.kinds), m.data.row, gridBodyRows(h, g.hint), m.data.rowOff)
	return g
}

// gridBodyRows is how many rows of the page fit in an h-row content box.
// The three header lines and the status line each take one, and the
// horizontal-scroll hint takes one more when it is shown — budgeting it
// is what keeps the grid from rendering one line more than the box holds
// and pushing the command log and the options bar off the screen.
func gridBodyRows(h int, hint bool) int {
	rows := h - 4
	if hint {
		rows--
	}
	return maxInt(rows, 0)
}

// dataContent renders the Data tab with its tab bar on top. The main view
// draws the bar in its border instead and calls dataBody directly; this is
// for the nested case — the result under the editor in the query view,
// which has a border title of its own.
func (m Model) dataContent(w, h int) string {
	if h <= 0 {
		return ""
	}
	if h == 1 {
		return m.mainTabBar(w)
	}
	return m.mainTabBar(w) + "\n" + m.dataBody(w, h-1)
}

// dataBody renders the grid and its status line into a w x h content box.
func (m Model) dataBody(w, h int) string {
	d := m.data
	var lines []string

	switch {
	case len(d.cols) == 0 && d.loading:
		lines = append(lines, "", m.style.pending.Render("running query…"))
	case d.err != "":
		lines = append(lines, "", m.style.danger.Render(truncate(d.err, w)))
	case len(d.cols) == 0 && d.notice != "":
		// A statement that returns no result set: the affected-row
		// count is the whole outcome.
		lines = append(lines, "", m.style.pending.Render(truncate(d.notice, w)))
	case len(d.cols) == 0:
		lines = append(lines, "", m.style.muted.Render("no columns"))
	default:
		g := m.gridLayout(w, h)

		lines = append(lines, m.gridHeader(g.cols[g.cs:g.ce], g.cs, w))
		for r := g.rs; r < g.re; r++ {
			lines = append(lines, m.gridRow(g.cols[g.cs:g.ce], g.cs, r, g.kinds[r], w))
		}
		if len(g.kinds) == 0 {
			msg := "table is empty"
			if d.filter != nil {
				msg = "no rows match"
			}
			lines = append(lines, m.style.muted.Render(msg))
		}
		if g.hint {
			lines = append(lines, m.style.muted.Render(fmt.Sprintf(
				"columns %d–%d of %d — h/l scrolls", g.cs+1, g.ce, len(g.cols))))
		}
	}

	// The status line is pinned to the bottom of the box.
	body := joinTruncated(lines, w, maxInt(h-1, 1))
	pad := h - 1 - lipgloss.Height(body)
	if pad > 0 {
		body += strings.Repeat("\n", pad)
	}
	return body + "\n" + truncate(m.dataStatus(), w)
}

// gridHeader renders the column names and, under them, their types, then a
// rule that sets the header off from the data. The rule's `┼` junctions
// line up with the `│` separators above and below it.
func (m Model) gridHeader(cols []gridColumn, first, w int) string {
	var names, types, rule strings.Builder
	for i, c := range cols {
		if i > 0 {
			sep := m.style.gridSeparator.Render(colSepChar)
			names.WriteString(sep)
			types.WriteString(sep)
			rule.WriteString(ruleJunction)
		}
		style := m.style.gridHeader
		if first+i == m.data.col && m.focus == panelMain {
			style = m.style.gridHeaderCursor
		}
		names.WriteString(style.Render(pad(truncate(c.header, c.width), c.width)))
		types.WriteString(m.style.muted.Render(pad(truncate(c.typ, c.width), c.width)))
		rule.WriteString(strings.Repeat(ruleChar, c.width))
	}
	return truncate(names.String(), w) + "\n" +
		truncate(types.String(), w) + "\n" +
		m.style.gridSeparator.Render(truncate(rule.String(), w))
}

// gridRow renders one row of the page, tinting the cursor row and, more
// strongly, the cursor cell.
func (m Model) gridRow(cols []gridColumn, first, r int, kind rowKind, w int) string {
	var b strings.Builder
	onRow := r == m.data.row && m.focus == panelMain
	selected := m.data.inSelection(r)
	for i, c := range cols {
		if i > 0 {
			sep := m.style.gridSeparator.Render(colSepChar)
			if onRow {
				sep = m.style.rowCursor.Render(colSepChar)
			}
			b.WriteString(sep)
		}
		text := ""
		isNull, isStaged := false, false
		if r < len(c.cells) {
			text, isNull, isStaged = c.cells[r], c.nulls[r], c.staged[r]
		}
		b.WriteString(m.cellStyle(onRow, selected, first+i == m.data.col, isNull, isStaged, kind).
			Render(pad(truncate(text, c.width), c.width)))
	}
	return truncate(b.String(), w)
}

// cellStyle picks the tint of one cell from the cursor position, whether
// the row is part of the multi-row selection, what
// the row is staged as, whether the value is NULL and whether an edit of
// it is staged. A staged row op wins over everything below it: the whole
// row is going away or arriving, so a per-cell tint would only muddle
// it. Otherwise staged wins over NULL — yellow is the "pending" color
// throughout the app.
func (m Model) cellStyle(onRow, selected, onCol, isNull, isStaged bool, kind rowKind) lipgloss.Style {
	style := lipgloss.NewStyle()
	switch {
	case onRow && onCol && m.focus == panelMain:
		style = m.style.cellCursor
	case onRow:
		style = m.style.rowCursor
	case selected:
		// A selected row that is not the cursor row: tinted, not
		// highlighted, so the cursor stays findable inside the block.
		style = style.Background(colorSelectionBg)
	}
	switch {
	case kind == rowDeleted:
		return style.Foreground(colorDeleted).Strikethrough(true)
	case kind == rowInserted:
		return style.Foreground(colorGreen).Bold(true)
	case isStaged:
		style = style.Foreground(colorYellow).Bold(true)
	case isNull:
		style = style.Foreground(colorMuted)
	}
	return style
}

// dataStatus is the bottom line: which rows of how many are on screen,
// which page they are, and the filter and sort that produced them.
func (m Model) dataStatus() string {
	d := m.data
	var parts []string

	if d.notice != "" && len(d.cols) == 0 {
		return m.style.pending.Render(d.notice)
	}

	switch {
	case len(d.rows) == 0 && d.hasTotal && d.total == 0:
		parts = append(parts, "rows 0 of 0")
	case len(d.rows) == 0:
		parts = append(parts, "no rows")
	default:
		span := fmt.Sprintf("rows %d–%d", d.offset()+1, d.offset()+len(d.rows))
		if d.hasTotal {
			// A browsed table's total is a separate COUNT(*) round trip
			// that may already be stale; a query result is fully in
			// memory, so its total is exact.
			if d.isQuery() {
				span += fmt.Sprintf(" of %d", d.total)
			} else {
				span += fmt.Sprintf(" of ~%d", d.total)
			}
		}
		parts = append(parts, span)
	}
	page := fmt.Sprintf("(page %d", d.page+1)
	if n := d.pageCount(); n > 0 {
		page += fmt.Sprintf("/%d", n)
	}
	parts = append(parts, page+")")

	line := m.style.muted.Render(strings.Join(parts, " "))
	if d.truncated {
		// A capped result looks exactly like a complete one, so the
		// status line has to say it is not.
		line += m.style.danger.Render(fmt.Sprintf("  capped at %d rows", maxQueryRows))
	}
	if n := m.changes.Len(); n > 0 {
		line += m.style.pending.Render("  " + countChanges(n))
	}
	// Selection mode is a mode: the status line says so, the way the
	// filter and the sort do, so it is never on without being visible.
	if n := len(d.selectedRows()); n > 0 {
		line += m.style.keyHint.Render(fmt.Sprintf("  %d rows selected", n))
	}
	if d.sort != nil {
		dir := "asc"
		if d.sort.Desc {
			dir = "desc"
		}
		line += m.style.keyHint.Render(fmt.Sprintf("  sort %s %s", d.sort.Column, dir))
	}
	if d.filter != nil {
		style := m.style.keyHint
		mark := "where "
		if d.filter.Verbatim {
			// Verbatim means the fragment was not parameterized; the
			// grid says so as loudly as the command log does.
			style = m.style.danger
			mark = "where (verbatim) "
		}
		line += style.Render("  " + mark + d.filter.Raw)
	}
	return line
}

// pad right-pads s to w display cells.
func pad(s string, w int) string {
	if n := w - lipgloss.Width(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}
