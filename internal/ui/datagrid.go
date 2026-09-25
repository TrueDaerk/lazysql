package ui

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

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

// The separator after the last pinned column is heavier, so the edge the
// scrolling columns slide under reads as one. It is the same one cell
// wide as colSepChar: the hit test and the width math do not care which
// glyph a gap is drawn with.
const (
	pinSepChar      = "┃"
	pinRuleJunction = "╂"
)

// nullText is how SQL NULL reads in the grid. It is styled dim so it
// cannot be confused with the string "NULL".
const nullText = "NULL"

// rowKind is the whole-row tint a rendered row carries. The first three
// say how the row relates to the changeset: an untouched page row, a row
// staged for deletion, or a phantom row standing for a staged insert
// that does not exist in the database yet. The last two belong to the
// read-only grids built on roGrid, which have no changeset at all — a
// row the view wants to stand out (a session waiting on a lock) and one
// of lesser interest (lazysql's own connection).
type rowKind int

const (
	rowPlain rowKind = iota
	rowDeleted
	rowInserted
	rowAlert
	rowFaded
)

// defaultText marks a phantom row's cell that the INSERT leaves out.
const defaultText = "DEFAULT"

// gridColumn is one rendered column: its header, its type and the
// already-formatted cells of the page under it. typ is empty for a grid
// whose columns have no declared type — a read-only report over values
// the server hands out as text — and gridHeader then leaves the type
// line out rather than drawing a blank one.
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
	visible := make([]bool, len(d.cols))
	for _, i := range d.visibleOrder() {
		visible[i] = true
	}
	for i, c := range d.cols {
		// A hidden column is never drawn, so it is never formatted: the
		// 64 KB TEXT column a user hides to get it out of the way stops
		// costing the frame anything at all.
		if !visible[i] {
			continue
		}
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
		kind := db.ClassifyType(c.DataType)
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
			g.cells[r] = gridCellText(v, kind, nullText)
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
				g.cells[r] = gridCellText(v, kind, nullText)
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

// cellScanBytes bounds how much of a value the grid ever walks. A column
// is at most maxColWidth cells wide, so nothing past a prefix that
// already overflows the widest possible column can change what a frame
// shows — but buildGrid runs on every frame, and without a bound a page
// of JSON documents or long notes is flattened, UTF-8 checked, measured
// and truncated in full on every keystroke. Four bytes per cell is the
// widest a rune gets; a prefix whose display width still falls short of
// the column (a long run of whitespace that flatten collapses, or
// combining marks) simply renders as the short value it appears to be.
// The whole value is never lost: `v` opens it in the cell-detail popup,
// and the copy scopes read m.data.rows, not the rendered grid. See
// wiki/design/grid-cell-scan-bound.md.
const cellScanBytes = 4 * (maxColWidth + 1)

// gridCellText formats a cell's value for the grid, standing a placeholder
// in for BLOBs: raw bytes carry control characters that break the row and
// misalign the right border, and the cell-detail popup (`v`) already gives
// binary values a proper hex dump, so the grid does not need to show them.
//
// Only the first cellScanBytes of the value are looked at. The binary
// check is the same one classifyCell makes — the JSON arm it has is for
// the popup, which pretty-prints; the grid flattens either way.
//
// kind is the column's declared temporal kind, so a DATE or TIME column
// renders only the half of the value it actually carries (see
// db.FormatTemporalValue) instead of the RFC3339 timestamp FormatValue
// would otherwise invent a date or time-of-day for.
func gridCellText(v any, kind db.TypeKind, null string) string {
	raw := db.FormatTemporalValue(v, kind, null)
	head := cellHead(raw)
	if !utf8.ValidString(head) {
		// The placeholder reports the size of the whole value, not of
		// the prefix that gave it away.
		return fmt.Sprintf("<blob %d B>", len(raw))
	}
	return flatten(head)
}

// cellHead cuts s to the prefix the grid renders from, ending it on a
// rune boundary: a multi-byte rune the cut split would read as binary
// and turn a perfectly good text cell into a <blob> placeholder.
func cellHead(s string) string {
	if len(s) <= cellScanBytes {
		return s
	}
	head := s[:cellScanBytes]
	// At most one rune's worth of bytes goes. On binary data every one
	// of them decodes as RuneError and the loop stops on its own count;
	// the bytes before them have already settled the question.
	for i := 0; i < utf8.UTFMax-1 && head != ""; i++ {
		if r, size := utf8.DecodeLastRuneInString(head); r != utf8.RuneError || size > 1 {
			break
		}
		head = head[:len(head)-1]
	}
	return head
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
	cols  []gridColumn // every column of the page, in data order
	kinds []rowKind
	// order is the display order (dataView.visibleOrder) and pinned how
	// many of its leading entries are pinned; cs:ce is the window of
	// order the scrolling part shows, always at or right of pinned.
	order  []int
	pinned int
	cs, ce int  // visible column window, as positions in order
	rs, re int  // visible row window
	hint   bool // the columns do not all fit (or some are hidden), so the hint takes a row
	hidden int  // how many columns are hidden
}

// shown is the data indices of the columns the frame draws, left to
// right: the pinned ones, then the scrolled window.
func (g gridLayout) shown() []int {
	out := make([]int, 0, g.pinned+g.ce-g.cs)
	out = append(out, g.order[:g.pinned]...)
	return append(out, g.order[g.cs:g.ce]...)
}

// shownCols is shown as the formatted columns themselves.
func (g gridLayout) shownCols() []gridColumn {
	idx := g.shown()
	out := make([]gridColumn, len(idx))
	for i, c := range idx {
		out[i] = g.cols[c]
	}
	return out
}

// columnsHint is the line under the grid that says which columns are on
// screen. Positions count the display order, pinned columns included, so
// "columns 5–8 of 20 · 2 pinned" means positions 1–2 and 5–8 of the 20
// visible columns; hidden columns are not in the 20 and are named apart.
func (g gridLayout) columnsHint() string {
	first := g.cs + 1
	if g.cs == g.pinned {
		first = 1 // the pinned columns and the window are one run
	}
	s := fmt.Sprintf("columns %d–%d of %d", first, g.ce, len(g.order))
	if g.pinned > 0 {
		s += fmt.Sprintf(" · %d pinned", g.pinned)
	}
	if g.hidden > 0 {
		s += fmt.Sprintf(" · %d hidden (Z shows)", g.hidden)
	}
	if g.cs > g.pinned || g.ce < len(g.order) {
		s += " — h/l scrolls"
	}
	return s
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
	h = mh - m.commandLogHeight(mh) - 2
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
	g.order = m.data.visibleOrder()
	g.pinned = m.data.pinnedCount()
	g.hidden = len(g.cols) - len(g.order)

	// The pinned columns take their width off the top; the rest of the
	// box is what the scrolling columns are windowed into. colOff counts
	// the scrolling part only, so pinning a column does not shift it.
	pos := slices.Index(g.order, m.data.col)
	pinW := 0
	for _, c := range g.order[:g.pinned] {
		pinW += g.cols[c].width + colGap
	}
	// Pinned columns that leave no room for the cursor column — a narrow
	// terminal, or a lot pinned — stop being pinned for this frame and
	// scroll with the rest: the cursor cell must always be drawn, and a
	// pinned edge that hides it would break that.
	need := 0
	if pos >= g.pinned {
		need = g.cols[g.order[pos]].width
	}
	if g.pinned > 0 && pinW+need > w {
		g.pinned, pinW = 0, 0
	}
	scroll := make([]gridColumn, 0, len(g.order)-g.pinned)
	for _, c := range g.order[g.pinned:] {
		scroll = append(scroll, g.cols[c])
	}
	sw := maxInt(w-pinW, 1)
	var cs, ce int
	switch {
	case len(scroll) == 0:
	case pos >= g.pinned:
		cs, ce = columnWindow(scroll, pos-g.pinned, sw, m.data.colOff)
	default:
		// The cursor is on a pinned column, which is always on screen;
		// the scrolling part just stays where it was.
		off := clampInt(m.data.colOff, 0, len(scroll)-1)
		cs, ce = columnWindow(scroll, off, sw, off)
	}
	g.cs, g.ce = g.pinned+cs, g.pinned+ce
	g.hint = g.cs > g.pinned || g.ce < len(g.order) || g.hidden > 0
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

		cur := m.dataCursor()
		span := gridSpan{cols: g.shownCols(), idx: g.shown(), pinned: g.pinned}
		lines = append(lines, m.gridHeader(span, cur, w))
		for r := g.rs; r < g.re; r++ {
			lines = append(lines, m.gridRow(span, r, cur, g.kinds[r], w))
		}
		if len(g.kinds) == 0 {
			msg := "table is empty"
			if d.filter != nil {
				msg = "no rows match"
			}
			lines = append(lines, m.style.muted.Render(msg))
		}
		if g.hint {
			lines = append(lines, m.style.muted.Render(g.columnsHint()))
		}
	}

	// The status line is pinned to the bottom of the box — and the inline
	// WHERE line takes its place while it is open. The two never need to
	// be read at once: the status describes the page underneath, which is
	// exactly the page the clause being typed is about to replace.
	last := truncate(m.dataStatus(), w)
	if m.filterInputOpen() {
		last = m.filterInput.view(w)
	}
	body := joinTruncated(lines, w, maxInt(h-1, 1))
	pad := h - 1 - lipgloss.Height(body)
	if pad > 0 {
		body += strings.Repeat("\n", pad)
	}
	return body + "\n" + last
}

// gridCursor is where a rendered grid's cell cursor sits, whether the box
// it is drawn in has the focus, and which of its cells the selection
// covers. It is what the renderers below read instead of m.data, so the
// editable data grid and the read-only grids built on roGrid share one
// set of them — see wiki/design/read-only-grid.md.
type gridCursor struct {
	row, col int
	// focused is whether the box the grid is drawn in owns the keyboard.
	// A cursor that cannot be moved is not highlighted as one.
	focused bool
	// idle is whether the box has the focus but something inside it has
	// the keyboard — the inline WHERE line. The cursor stays drawn, so
	// the page keeps its place, but weakly: the loud highlight belongs
	// to whatever the keys are actually going to.
	idle bool
	// selected reports whether a cell takes part in the selection; nil
	// when no selection is up.
	selected func(r, c int) bool
}

func (c gridCursor) cellSelected(r, col int) bool {
	return c.selected != nil && c.selected(r, col)
}

// dataCursor is the Data tab's cursor as the shared renderers want it.
func (m Model) dataCursor() gridCursor {
	return gridCursor{
		row: m.data.row, col: m.data.col,
		focused:  m.focus == panelMain,
		idle:     m.filterInputOpen(),
		selected: m.data.cellSelector(),
	}
}

// gridSpan is the columns one frame draws, left to right, with the
// column index each of them stands for — which is what the cursor and the
// selection are compared against — and how many of them lead as pinned
// columns. The read-only grids have no pinning and pass a contiguous run.
type gridSpan struct {
	cols   []gridColumn
	idx    []int
	pinned int
}

// contiguousSpan is the span of cols[start:end] drawn in order.
func contiguousSpan(cols []gridColumn, start, end int) gridSpan {
	idx := make([]int, 0, end-start)
	for i := start; i < end; i++ {
		idx = append(idx, i)
	}
	return gridSpan{cols: cols[start:end], idx: idx}
}

// pinEdge reports whether the separator before drawn column i is the one
// between the pinned columns and the scrolling ones.
func (s gridSpan) pinEdge(i int) bool { return s.pinned > 0 && i == s.pinned }

// gridHeaderRows is how many lines gridHeader draws for a set of columns:
// the names, the types when the columns declare any, and the rule under
// them. The content box is budgeted with it and a click is mapped back
// through it, so the number lives in one place.
func gridHeaderRows(cols []gridColumn) int {
	if gridHasTypes(cols) {
		return 3
	}
	return 2
}

func gridHasTypes(cols []gridColumn) bool {
	for _, c := range cols {
		if c.typ != "" {
			return true
		}
	}
	return false
}

// gridHeader renders the column names and, under them, their types, then a
// rule that sets the header off from the data. The rule's `┼` junctions
// line up with the `│` separators above and below it. A grid whose
// columns declare no types gets no type line — there would be nothing in
// it, and a read-only report should not spend a row on a blank.
func (m Model) gridHeader(span gridSpan, cur gridCursor, w int) string {
	cols := span.cols
	var names, types, rule strings.Builder
	for i, c := range cols {
		if i > 0 {
			sepChar, junction := colSepChar, ruleJunction
			if span.pinEdge(i) {
				sepChar, junction = pinSepChar, pinRuleJunction
			}
			sep := m.style.gridSeparator.Render(sepChar)
			names.WriteString(sep)
			types.WriteString(sep)
			rule.WriteString(junction)
		}
		style := m.style.gridHeader
		if span.idx[i] == cur.col && cur.focused && !cur.idle {
			style = m.style.gridHeaderCursor
		}
		names.WriteString(style.Render(pad(truncate(c.header, c.width), c.width)))
		types.WriteString(m.style.muted.Render(pad(truncate(c.typ, c.width), c.width)))
		rule.WriteString(strings.Repeat(ruleChar, c.width))
	}
	out := truncate(names.String(), w)
	if gridHasTypes(cols) {
		out += "\n" + truncate(types.String(), w)
	}
	return out + "\n" + m.style.gridSeparator.Render(truncate(rule.String(), w))
}

// gridRow renders one row of the page, tinting the cursor row and, more
// strongly, the cursor cell.
func (m Model) gridRow(span gridSpan, r int, cur gridCursor, kind rowKind, w int) string {
	var b strings.Builder
	onRow := r == cur.row && cur.focused
	for i, c := range span.cols {
		if i > 0 {
			sepChar := colSepChar
			if span.pinEdge(i) {
				sepChar = pinSepChar
			}
			sep := m.style.gridSeparator.Render(sepChar)
			if onRow && !cur.idle {
				sep = m.style.rowCursor.Render(sepChar)
			}
			b.WriteString(sep)
		}
		text := ""
		isNull, isStaged := false, false
		if r < len(c.cells) {
			text, isNull, isStaged = c.cells[r], c.nulls[r], c.staged[r]
		}
		// The tint is per cell, not per row: a selection narrowed to a
		// block of columns has to show which columns it kept.
		col := span.idx[i]
		b.WriteString(m.cellStyle(cur.idle, onRow, cur.cellSelected(r, col), col == cur.col && cur.focused,
			isNull, isStaged, kind).
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
// idle weakens the cursor tints without moving them: the grid keeps the
// focus, but the keys are going to the inline WHERE line.
func (m Model) cellStyle(idle, onRow, selected, onCol, isNull, isStaged bool, kind rowKind) lipgloss.Style {
	style := lipgloss.NewStyle()
	switch {
	case onRow && onCol && idle:
		style = m.style.cellCursorIdle
	case onRow && idle:
		// The row tint drops entirely: two weakened tints on one row
		// would blur into the cursor cell they are meant to set off.
	case onRow && onCol:
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
	case kind == rowAlert:
		return style.Foreground(colorError)
	case kind == rowFaded:
		return style.Foreground(colorMuted)
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
	if d.loading {
		// The marker sits next to the page counter rather than only in
		// the border title: the status line is where the sort and the
		// filter already say what shaped the page, so it is where a
		// reload in flight for a *newer* sort has to say so too.
		// Without it, `s` on a slow table looks like it did nothing and
		// the user presses it again. See
		// wiki/design/page-query-cancellation.md.
		line += m.style.pending.Render("  loading…")
	}
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
		sel := fmt.Sprintf("  %d rows selected", n)
		// A block says both of its dimensions: "3 rows selected" would
		// otherwise claim columns the copy scopes have left out.
		if d.narrowedToCols() {
			sel = fmt.Sprintf("  %d rows × %d columns selected", n, len(d.selectedCols()))
		}
		line += m.style.keyHint.Render(sel)
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
