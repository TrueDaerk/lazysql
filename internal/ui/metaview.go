package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"lazysql/internal/db"
)

// metaColGap separates the columns of the Structure and Indexes tables.
const metaColGap = 2

// mainTabStripLevel is how much of the Data/Structure/Indexes/DDL/Relations
// strip mainTabBar draws before the relation name that follows it.
// Shortening starts at the strip — the focused tab is already highlighted,
// so the other labels are the only part safe to drop — and stops as soon as
// what remains plus the relation name fits the title (issue #217).
type mainTabStripLevel int

const (
	tabStripFull          mainTabStripLevel = iota // every tab name
	tabStripFocusedName                            // just the focused tab's name
	tabStripFocusedLetter                          // just the focused tab's first letter
)

// mainTabBar is the first line of the main view: the tab strip, shortened
// just enough to fit, then the relation the tabs describe.
func (m Model) mainTabBar(w int) string {
	suffix := m.mainTabSuffix(w)
	room := maxInt(w-2, 0)
	line := m.mainTabStrip(m.mainTabLevel(room, suffix)) + suffix
	return truncate(line, w)
}

// mainTabLevel picks the least-shortened strip that still leaves room for
// suffix — the relation name and status markers that must stay visible.
func (m Model) mainTabLevel(room int, suffix string) mainTabStripLevel {
	suffixW := lipgloss.Width(suffix)
	level := tabStripFull
	for level < tabStripFocusedLetter &&
		lipgloss.Width(m.mainTabStrip(level))+suffixW > room {
		level++
	}
	return level
}

// mainTabStrip renders the tab list at the given shortening level, over
// whichever tabs visibleMainTabs currently offers. The focused tab always
// keeps its emphasis style, so it stays identifiable even when it is the
// only label left.
func (m Model) mainTabStrip(level mainTabStripLevel) string {
	tabStyle := func(t mainTab) lipgloss.Style {
		if t != m.tab {
			return m.style.muted
		}
		if m.focus == panelMain {
			return m.style.titleFocused
		}
		return m.style.title
	}
	if level != tabStripFull {
		name := mainTabNames[m.tab]
		if level == tabStripFocusedLetter {
			name = name[:1]
		}
		return m.style.muted.Render("‹") + tabStyle(m.tab).Render(name) + m.style.muted.Render("›")
	}
	tabs := m.visibleMainTabs()
	parts := make([]string, 0, len(tabs))
	for _, t := range tabs {
		parts = append(parts, tabStyle(t).Render(mainTabNames[t]))
	}
	return m.style.muted.Render("‹") +
		strings.Join(parts, m.style.muted.Render("|")) +
		m.style.muted.Render("›")
}

// mainTabSuffix is the relation or query name and status markers that
// follow the tab strip — the part mainTabBar keeps visible.
func (m Model) mainTabSuffix(w int) string {
	var line string
	if m.grid.data.isQuery() {
		// A query result belongs to no relation, so the bar names the
		// statement instead of a table.
		line += m.style.muted.Render(" query ") + truncate(flatten(m.grid.data.query), maxInt(w/2, 20))
	} else {
		line += m.style.muted.Render(" "+displayDatabase(m.grid.data.database)+".") + m.grid.data.table
	}
	// The lock rides in the main view's title, not only in panel [1]: the
	// grid is where a write would be attempted, so that is where the mode
	// has to be visible.
	if m.readOnly() {
		line += " " + m.style.pending.Render(lockMark+" read-only")
	}
	if m.tab == mainTabData && m.grid.data.loading || m.tab.metadata() && m.meta.loading {
		line += " " + m.style.pending.Render("loading…")
	}
	return line
}

// metaContent renders the Structure, Indexes or DDL tab into a w x h
// content box. Their shared loading and error states live here so the
// three renderers only ever see data. The tab bar is not part of it — the
// main view splices it into its top border.
func (m Model) metaContent(w, h int) string {
	var lines []string
	body := maxInt(h, 0)

	switch {
	case m.meta.err != "":
		lines = append(lines, "", m.style.danger.Render(truncate(m.meta.err, w)))
	case !m.meta.loaded && m.meta.loading:
		lines = append(lines, "", m.style.pending.Render("reading table metadata…"))
	case !m.meta.loaded:
		lines = append(lines, "", m.style.muted.Render("no metadata yet"))
	default:
		// What the changeset will do to the table closes the Structure and
		// Indexes tabs, and the table above it gives up the rows it needs.
		var staged []string
		if m.tab == mainTabStructure || m.tab == mainTabIndexes {
			staged = m.stagedSchemaLines(w)
			body = maxInt(body-len(staged), 1)
		}
		switch m.tab {
		case mainTabStructure:
			lines = append(lines, m.structureLines(w, body)...)
			lines = append(lines, staged...)
		case mainTabIndexes:
			lines = append(lines, m.indexLines(w, body)...)
			lines = append(lines, staged...)
		case mainTabDDL:
			lines = append(lines, m.ddlLines(w, body)...)
		case mainTabRelations:
			lines = append(lines, m.relationsLines(w, body)...)
		}
	}
	return joinTruncated(lines, w, h)
}

// structureLines is the Structure tab: one row per column, with the key
// and extra notes the engine reports.
func (m Model) structureLines(w, h int) []string {
	if len(m.meta.cols) == 0 {
		return []string{"", m.style.muted.Render("no columns")}
	}
	rows := make([][]string, 0, len(m.meta.cols))
	for i, c := range m.meta.cols {
		null := "no"
		if c.Nullable {
			null = "yes"
		}
		def := ""
		if c.Default != nil {
			def = flatten(*c.Default)
		}
		rows = append(rows, []string{
			fmt.Sprintf("%d", i+1),
			c.Name,
			c.DataType,
			null,
			def,
			columnKeyInfo(c, m.meta.indexes, m.meta.fks),
			c.Extra,
		})
	}
	return m.metaTable([]string{"#", "name", "type", "null", "default", "key", "extra"},
		rows, m.meta.row[mainTabStructure], true, w, h)
}

// columnKeyInfo summarizes how a column participates in the table's
// keys: PK for the primary key, UNI/IDX for the indexes covering it and
// FK when a foreign key constrains it.
func columnKeyInfo(c db.Column, idx []db.Index, fks []db.ForeignKey) string {
	var marks []string
	add := func(mark string) {
		for _, m := range marks {
			if m == mark {
				return
			}
		}
		marks = append(marks, mark)
	}
	if c.PrimaryKey {
		add("PK")
	}
	for _, ix := range idx {
		if !containsName(ix.Columns, c.Name) {
			continue
		}
		switch {
		case ix.Primary:
			add("PK")
		case ix.Unique:
			add("UNI")
		default:
			add("IDX")
		}
	}
	for _, fk := range fks {
		if containsName(fk.Columns, c.Name) {
			add("FK")
		}
	}
	return strings.Join(marks, ",")
}

func containsName(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

// indexLines is the Indexes tab: the indexes first, then the foreign
// keys with the table and columns they point at. Both are scrolled by
// the same offset, so a long index list can be walked past to reach the
// constraints under it.
func (m Model) indexLines(w, h int) []string {
	var lines []string

	if len(m.meta.indexes) == 0 {
		lines = append(lines, m.style.muted.Render("no indexes"))
	} else {
		rows := make([][]string, 0, len(m.meta.indexes))
		for _, ix := range m.meta.indexes {
			kind := "index"
			switch {
			case ix.Primary:
				kind = "primary"
			case ix.Unique:
				kind = "unique"
			}
			rows = append(rows, []string{
				ix.Name,
				kind,
				yesNo(ix.Unique),
				strings.Join(ix.Columns, ", "),
			})
		}
		lines = append(lines, m.metaTable(
			[]string{"index", "type", "unique", "columns"}, rows, -1, false, w, 0)...)
	}

	lines = append(lines, "", m.style.title.Render("Foreign keys"))
	if len(m.meta.fks) == 0 {
		lines = append(lines, m.style.muted.Render("none"))
	} else {
		rows := make([][]string, 0, len(m.meta.fks))
		for _, fk := range m.meta.fks {
			ref := fk.RefTable
			if len(fk.RefColumns) > 0 {
				ref += " (" + strings.Join(fk.RefColumns, ", ") + ")"
			}
			rows = append(rows, []string{
				fk.Name,
				strings.Join(fk.Columns, ", "),
				ref,
				orDash(fk.OnUpdate),
				orDash(fk.OnDelete),
			})
		}
		lines = append(lines, m.metaTable(
			[]string{"constraint", "columns", "references", "on update", "on delete"},
			rows, -1, false, w, 0)...)
	}
	return scrollLines(lines, m.meta.row[mainTabIndexes], w, h)
}

// ddlLines is the DDL tab: the statement as the engine reports it (or,
// where the engine keeps none, as lazysql synthesizes it), scrolled a
// line at a time.
func (m Model) ddlLines(w, h int) []string {
	if m.meta.ddl == "" {
		return []string{"", m.style.danger.Render(truncate("no DDL: "+m.ddlProblem(), w))}
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimRight(m.meta.ddl, "\n"), "\n") {
		lines = append(lines, strings.ReplaceAll(l, "\t", "    "))
	}
	out := scrollLines(lines, m.meta.row[mainTabDDL], w, maxInt(h-1, 0))
	hint := fmt.Sprintf("%d lines — y opens the copy menu", len(lines))
	return append(out, m.style.keyHint.Render(truncate(hint, w)))
}

// scrollLines takes the window of a rendered block that starts at
// offset, padding the offset back into range when the block shrank.
func scrollLines(lines []string, offset, w, h int) []string {
	if h <= 0 || len(lines) == 0 {
		return nil
	}
	if max := len(lines) - h; offset > max {
		offset = max
	}
	if offset < 0 {
		offset = 0
	}
	end := offset + h
	if end > len(lines) {
		end = len(lines)
	}
	out := make([]string, 0, end-offset)
	for _, l := range lines[offset:end] {
		out = append(out, truncate(l, w))
	}
	return out
}

// metaTable renders a header row plus rows, every column padded to its
// widest cell. With cursor >= 0 the matching row is highlighted and the
// window follows it; with h == 0 the whole table is returned and the
// caller scrolls it.
func (m Model) metaTable(headers []string, rows [][]string, cursor int, follow bool, w, h int) []string {
	widths := make([]int, len(headers))
	for i, hd := range headers {
		widths[i] = lipgloss.Width(hd)
	}
	for _, r := range rows {
		for i, cell := range r {
			if i < len(widths) {
				if n := lipgloss.Width(cell); n > widths[i] {
					widths[i] = n
				}
			}
		}
	}
	for i := range widths {
		if widths[i] > maxColWidth {
			widths[i] = maxColWidth
		}
	}

	render := func(cells []string, style lipgloss.Style) string {
		var b strings.Builder
		for i, cell := range cells {
			if i > 0 {
				b.WriteString(strings.Repeat(" ", metaColGap))
			}
			width := widths[i]
			// The last column takes the space it needs instead of
			// padding out to the right edge.
			text := truncate(flatten(cell), width)
			if i < len(cells)-1 {
				text = pad(text, width)
			}
			b.WriteString(text)
		}
		return style.Render(truncate(b.String(), w))
	}

	out := []string{render(headers, m.style.gridHeader)}
	body := rows
	first := 0
	if follow && h > 1 {
		// One line went to the header.
		// The Structure tab has no scroll offset of its own: passing 0
		// asks for the top-anchored window, which is what it has always
		// rendered.
		start, end := rowWindow(len(rows), cursor, h-1, 0)
		body, first = rows[start:end], start
	}
	for i, r := range body {
		style := lipgloss.NewStyle()
		if first+i == cursor && m.focus == panelMain {
			style = m.style.rowCursor
		}
		out = append(out, render(r, style))
	}
	return out
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// orDash renders a referential action, or a dash when the engine leaves
// it at its default.
func orDash(s string) string {
	if s = strings.TrimSpace(s); s == "" || strings.EqualFold(s, "NO ACTION") {
		return "—"
	}
	return s
}
