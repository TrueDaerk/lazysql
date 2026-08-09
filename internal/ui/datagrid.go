package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"lazysql/internal/db"
)

// Grid geometry. Columns are sized to their content within these
// bounds; anything wider is truncated with an ellipsis and can still be
// read in full with `v`.
const (
	minColWidth = 4
	maxColWidth = 32
	colGap      = 1
)

// nullText is how SQL NULL reads in the grid. It is styled dim so it
// cannot be confused with the string "NULL".
const nullText = "NULL"

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
// and every row agree on the same strings.
func (m Model) buildGrid() []gridColumn {
	d := m.data
	rowKeys := m.stagedRowKeys()
	cols := make([]gridColumn, len(d.cols))
	for i, c := range d.cols {
		header := c.Name
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
			cells:  make([]string, len(d.rows)),
			nulls:  make([]bool, len(d.rows)),
			staged: make([]bool, len(d.rows)),
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
			g.cells[r] = flatten(db.FormatValue(v, nullText))
			if w := lipgloss.Width(g.cells[r]); w > g.width {
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
	return cols
}

// stagedRowKeys returns each page row's primary key values, or nil when
// nothing is staged for the open table. The key columns come from the
// changeset itself, so highlighting works without a metadata fetch.
func (m Model) stagedRowKeys() [][]any {
	pkCols := m.changes.PKColsFor(m.data.database, m.data.table)
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

// flatten collapses whitespace that would otherwise break the row into
// several lines.
func flatten(s string) string {
	if !strings.ContainsAny(s, "\n\r\t") {
		return s
	}
	return strings.Join(strings.Fields(strings.NewReplacer("\n", " ", "\r", " ", "\t", " ").Replace(s)), " ")
}

// columnWindow picks the slice of columns that fits in w cells while
// keeping the cursor column visible. The window is derived from the
// cursor rather than remembered, so it survives a resize: the cursor
// stays put and the window re-packs around it.
func columnWindow(cols []gridColumn, cursor, w int) (start, end int) {
	if len(cols) == 0 {
		return 0, 0
	}
	if cursor < 0 {
		cursor = 0
	}
	for start = 0; start <= cursor; start++ {
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

// rowWindow keeps the cursor row on screen: the page is anchored at the
// top until the cursor passes the last visible row, then it follows.
func rowWindow(n, cursor, rows int) (start, end int) {
	if rows <= 0 || n == 0 {
		return 0, 0
	}
	if cursor < rows {
		start = 0
	} else {
		start = cursor - rows + 1
	}
	end = start + rows
	if end > n {
		end = n
	}
	return start, end
}

// dataContent renders the main view's Data tab into a w x h content box.
func (m Model) dataContent(w, h int) string {
	d := m.data
	lines := []string{m.mainTabBar(w)}

	// header (2 lines) + status (1 line) + the title already written
	bodyRows := h - 4
	if bodyRows < 0 {
		bodyRows = 0
	}

	switch {
	case len(d.cols) == 0 && d.loading:
		lines = append(lines, "", m.style.pending.Render("running query…"))
	case d.err != "":
		lines = append(lines, "", m.style.danger.Render(truncate(d.err, w)))
	case len(d.cols) == 0:
		lines = append(lines, "", m.style.muted.Render("no columns"))
	default:
		cols := m.buildGrid()
		cs, ce := columnWindow(cols, d.col, w)
		rs, re := rowWindow(len(d.rows), d.row, bodyRows)

		lines = append(lines, m.gridHeader(cols[cs:ce], cs, w))
		for r := rs; r < re; r++ {
			lines = append(lines, m.gridRow(cols[cs:ce], cs, r, w))
		}
		if len(d.rows) == 0 {
			lines = append(lines, m.style.muted.Render("no rows match"))
		}
		if cs > 0 || ce < len(cols) {
			lines = append(lines, m.style.muted.Render(fmt.Sprintf(
				"columns %d–%d of %d — h/l scrolls", cs+1, ce, len(cols))))
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

// gridHeader renders the column names and, under them, their types.
func (m Model) gridHeader(cols []gridColumn, first, w int) string {
	var names, types strings.Builder
	for i, c := range cols {
		if i > 0 {
			names.WriteString(strings.Repeat(" ", colGap))
			types.WriteString(strings.Repeat(" ", colGap))
		}
		style := m.style.gridHeader
		if first+i == m.data.col && m.focus == panelMain {
			style = m.style.gridHeaderCursor
		}
		names.WriteString(style.Render(pad(truncate(c.header, c.width), c.width)))
		types.WriteString(m.style.muted.Render(pad(truncate(c.typ, c.width), c.width)))
	}
	return truncate(names.String(), w) + "\n" + truncate(types.String(), w)
}

// gridRow renders one row of the page, tinting the cursor row and, more
// strongly, the cursor cell.
func (m Model) gridRow(cols []gridColumn, first, r, w int) string {
	var b strings.Builder
	onRow := r == m.data.row && m.focus == panelMain
	for i, c := range cols {
		if i > 0 {
			gap := strings.Repeat(" ", colGap)
			if onRow {
				gap = m.style.rowCursor.Render(gap)
			}
			b.WriteString(gap)
		}
		text := ""
		isNull, isStaged := false, false
		if r < len(c.cells) {
			text, isNull, isStaged = c.cells[r], c.nulls[r], c.staged[r]
		}
		b.WriteString(m.cellStyle(onRow, first+i == m.data.col, isNull, isStaged).
			Render(pad(truncate(text, c.width), c.width)))
	}
	return truncate(b.String(), w)
}

// cellStyle picks the tint of one cell from the cursor position, whether
// the value is NULL and whether an edit of it is staged. Staged wins
// over NULL: yellow is the "pending" color throughout the app.
func (m Model) cellStyle(onRow, onCol, isNull, isStaged bool) lipgloss.Style {
	style := lipgloss.NewStyle()
	switch {
	case onRow && onCol && m.focus == panelMain:
		style = m.style.cellCursor
	case onRow:
		style = m.style.rowCursor
	}
	switch {
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

	switch {
	case len(d.rows) == 0 && d.hasTotal && d.total == 0:
		parts = append(parts, "rows 0 of 0")
	case len(d.rows) == 0:
		parts = append(parts, "no rows")
	default:
		span := fmt.Sprintf("rows %d–%d", d.offset()+1, d.offset()+len(d.rows))
		if d.hasTotal {
			span += fmt.Sprintf(" of ~%d", d.total)
		}
		parts = append(parts, span)
	}
	page := fmt.Sprintf("(page %d", d.page+1)
	if n := d.pageCount(); n > 0 {
		page += fmt.Sprintf("/%d", n)
	}
	parts = append(parts, page+")")

	line := m.style.muted.Render(strings.Join(parts, " "))
	if n := m.changes.Len(); n > 0 {
		line += m.style.pending.Render("  " + countChanges(n))
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
