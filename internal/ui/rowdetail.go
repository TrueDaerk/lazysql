package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"lazysql/internal/db"
)

// rowDetailField is one column of the row `x` opens: its name, declared
// type and how its value renders — NULL, a staged edit, a staged insert
// left to the column's DEFAULT, or a plain fetched value.
type rowDetailField struct {
	name string
	typ  string // declared SQL type, as in the grid header

	text  string // flattened display text, already NULL/DEFAULT-formatted
	value any    // the raw value `v` hands to the cell modal; unset when isDefault

	isNull    bool
	isStaged  bool
	isDefault bool
}

// rowDetailModal is `x`: the cursor row as a scrollable name/type/value
// list instead of the grid's horizontal slice — psql's `\x` for a table
// too wide to read a row of at a glance.
type rowDetailModal struct {
	subject string
	status  rowKind // rowDeleted/rowInserted mirror the grid's whole-row tint
	fields  []rowDetailField
	cursor  int
	offset  int
}

// newRowDetailModal builds the popup for the row under the cursor. It
// refuses on the metadata tabs and on an empty result, where there is no
// data row for the cursor to mean.
func newRowDetailModal(m Model) (*rowDetailModal, bool) {
	if m.tab.metadata() || len(m.data.cols) == 0 {
		return nil, false
	}
	d := m.data
	rd := &rowDetailModal{subject: m.dataSubject()}

	if ins, ok := m.phantomAtCursor(); ok {
		rd.status = rowInserted
		rd.fields = make([]rowDetailField, len(d.cols))
		for i, c := range d.cols {
			f := rowDetailField{name: c.Name, typ: c.DataType}
			v, bound := insertValueFor(ins, c.Name)
			switch {
			case !bound:
				f.isDefault = true
				f.text = defaultText
			case v == nil:
				f.isNull = true
				f.text = nullText
			default:
				f.value = v
				f.text = flatten(db.FormatValue(v, nullText))
			}
			rd.fields[i] = f
		}
		return rd, true
	}

	if d.row < 0 || d.row >= len(d.rows) {
		return nil, false
	}
	var pkVals []any
	if pkCols := m.pkColumns(); pkCols != nil {
		pkVals, _ = m.rowKeyVals(pkCols, d.row)
		if pkVals != nil && m.changes.DeleteStaged(d.database, d.table, pkVals) {
			rd.status = rowDeleted
		}
	}
	rd.fields = make([]rowDetailField, len(d.cols))
	for i, c := range d.cols {
		var v any
		if i < len(d.rows[d.row]) {
			v = d.rows[d.row][i]
		}
		f := rowDetailField{name: c.Name, typ: c.DataType}
		if pkVals != nil {
			if ch, ok := m.changes.Lookup(d.database, d.table, pkVals, c.Name); ok {
				v = ch.NewValue
				f.isStaged = true
			}
		}
		if v == nil {
			f.isNull = true
			f.text = nullText
		} else {
			f.value = v
			f.text = flatten(db.FormatValue(v, nullText))
		}
		rd.fields[i] = f
	}
	return rd, true
}

// scroll is the wheel: the pane has a field cursor rather than a scroll
// offset — view derives the offset from it — so the wheel walks fields.
func (rd *rowDetailModal) scroll(delta int) {
	rd.cursor += delta
	if rd.cursor >= len(rd.fields) {
		rd.cursor = len(rd.fields) - 1
	}
	if rd.cursor < 0 {
		rd.cursor = 0
	}
}

func (rd *rowDetailModal) update(msg tea.KeyPressMsg, m *Model) (bool, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", "x":
		return true, nil
	case "down", "j":
		if rd.cursor < len(rd.fields)-1 {
			rd.cursor++
		}
	case "up", "k":
		if rd.cursor > 0 {
			rd.cursor--
		}
	case "g", "home":
		rd.cursor = 0
	case "G", "end":
		rd.cursor = len(rd.fields) - 1
	case "v":
		if rd.cursor < 0 || rd.cursor >= len(rd.fields) {
			return false, nil
		}
		f := rd.fields[rd.cursor]
		if f.isDefault {
			m.modal = &confirmModal{title: "Field — " + f.name, body: "left to the column's DEFAULT."}
			return false, nil
		}
		m.modal = newCellModal(rd.subject, f.name, f.typ, f.value)
	}
	return false, nil
}

// valueStyle picks the tint of one field's value the way cellStyle does
// for the grid: a staged row op wins over everything (the whole row is
// going away or arriving), then a staged cell edit, then NULL.
func (rd *rowDetailModal) valueStyle(f rowDetailField) lipgloss.Style {
	style := lipgloss.NewStyle()
	switch {
	case rd.status == rowDeleted:
		return style.Foreground(colorDeleted).Strikethrough(true)
	case rd.status == rowInserted && f.isDefault:
		return style.Foreground(colorMuted)
	case rd.status == rowInserted:
		return style.Foreground(colorGreen).Bold(true)
	case f.isStaged:
		return style.Foreground(colorYellow).Bold(true)
	case f.isNull:
		return style.Foreground(colorMuted)
	}
	return style
}

func (rd *rowDetailModal) view(s styles, maxW, maxH int) string {
	width := min(maxW-8, 100)
	if width < 20 {
		width = 20
	}
	rows := maxH - 6
	if rows < 1 {
		rows = 1
	}
	if rows > len(rd.fields) {
		rows = len(rd.fields)
	}
	if rd.cursor < rd.offset {
		rd.offset = rd.cursor
	}
	if rd.cursor >= rd.offset+rows {
		rd.offset = rd.cursor - rows + 1
	}
	if maxOff := len(rd.fields) - rows; rd.offset > maxOff {
		rd.offset = maxOff
	}
	if rd.offset < 0 {
		rd.offset = 0
	}

	nameWidth, typeWidth := 4, 4
	for _, f := range rd.fields {
		if w := lipgloss.Width(f.name); w > nameWidth {
			nameWidth = w
		}
		if w := lipgloss.Width(f.typ); w > typeWidth {
			typeWidth = w
		}
	}
	nameWidth = min(nameWidth, 24)
	typeWidth = min(typeWidth, 16)
	valueWidth := width - nameWidth - typeWidth - 3
	if valueWidth < 8 {
		valueWidth = 8
	}

	title := "Row — " + rd.subject
	switch rd.status {
	case rowDeleted:
		title += "  (staged for deletion)"
	case rowInserted:
		title += "  (staged insert)"
	}

	var b strings.Builder
	b.WriteString(s.modalTitle.Render(truncate(title, width)) + "\n\n")
	for i := rd.offset; i < rd.offset+rows && i < len(rd.fields); i++ {
		f := rd.fields[i]
		name := pad(truncate(f.name, nameWidth), nameWidth)
		typ := pad(truncate(strings.ToLower(f.typ), typeWidth), typeWidth)
		val := pad(truncate(f.text, valueWidth), valueWidth)

		line := name + " " + s.muted.Render(typ) + " " + rd.valueStyle(f).Render(val)
		if i == rd.cursor {
			plain := name + " " + typ + " " + val
			line = s.selected.Render(truncate(plain, width))
		}
		b.WriteString(truncate(line, width) + "\n")
	}
	footer := "j/k move · v view field · esc close"
	if len(rd.fields) > rows {
		footer = fmt.Sprintf("fields %d–%d of %d · j/k move · v view field · esc close",
			rd.offset+1, rd.offset+rows, len(rd.fields))
	}
	b.WriteString("\n" + s.muted.Render(footer))
	return s.modal.Render(b.String())
}
