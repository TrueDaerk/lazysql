package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/db"
)

// filterModal is the data grid's `/` popup: pick a column, an operator
// and a value, and the grid reloads behind a parameterized WHERE.
//
// It is a thin layer over formModal rather than a modal of its own —
// the field cursor, the select cycling and the inline error line are
// exactly what the connection form already does. What it adds is the
// advanced mode: `ctrl+t` swaps the three structured fields for one
// free-text WHERE fragment, which goes through db.ParseFilter and may
// end up verbatim. The two modes are one modal because they are one
// decision: "how do I narrow these rows".
type filterModal struct {
	form     *formModal
	advanced bool
}

const (
	filterFieldColumn   = "column"
	filterFieldOperator = "operator"
	filterFieldValue    = "value"
	filterFieldAnd      = "and"
	filterFieldWhere    = "where"
)

// newFilterModal builds the popup for one relation. cols is the column
// list of the open table, startCol the column the cell cursor sits on
// (the one the user most likely means), and hasConds reports whether a
// structured filter is already active — only then is the AND toggle
// offered. raw is the current filter text, so advanced mode opens on
// what is running rather than on an empty line.
func newFilterModal(cols []db.Column, startCol string, hasConds bool, raw string) *filterModal {
	fm := &filterModal{}

	names := make([]string, 0, len(cols))
	labels := make([]string, 0, len(cols))
	for _, c := range cols {
		names = append(names, c.Name)
		label := c.Name
		if c.DataType != "" {
			label += "  " + c.DataType
		}
		labels = append(labels, label)
	}

	ops := db.FilterOps()
	opValues := make([]string, 0, len(ops))
	for _, op := range ops {
		opValues = append(opValues, string(op))
	}

	fields := []*formField{
		newSelectField(filterFieldColumn, "Column", labels, names, startCol).
			withVisible(func(*formModal) bool { return !fm.advanced }),
		newSelectField(filterFieldOperator, "Operator", opValues, opValues, string(db.OpEq)).
			withVisible(func(*formModal) bool { return !fm.advanced }),
		newTextField(filterFieldValue, "Value", "", "bound as a parameter").
			withVisible(func(f *formModal) bool {
				return !fm.advanced && db.FilterOp(f.rawValue(filterFieldOperator)).NeedsValue()
			}),
		newBoolField(filterFieldAnd, "AND with current", false).
			withHelp("keep the active filter and add this condition").
			withVisible(func(*formModal) bool { return !fm.advanced && hasConds }),
		newTextField(filterFieldWhere, "WHERE", raw, "id > 100 AND name LIKE 'a%'").
			withHelp("free-form SQL — parameterized when it can be").
			withVisible(func(*formModal) bool { return fm.advanced }),
	}

	fm.form = newFormModal("Filter rows", fields, fm.submit)
	// A relation whose columns are not known yet has nothing to pick
	// from, so the popup opens on the mode that still works.
	if len(cols) == 0 {
		fm.advanced = true
	}
	fm.syncFooter()
	return fm
}

// submit applies the popup. A build error keeps the modal open with the
// reason on its error line, because a rejected value is a typo, not a
// reason to lose the rest of the form.
func (fm *filterModal) submit(m *Model, f *formModal) (bool, tea.Cmd) {
	if fm.advanced {
		return true, m.setDataFilter(f.value(filterFieldWhere))
	}
	column := f.rawValue(filterFieldColumn)
	if column == "" {
		f.err = "no column to filter on"
		return false, nil
	}
	cond := db.FilterCond{
		Column: column,
		Op:     db.FilterOp(f.rawValue(filterFieldOperator)),
		Value:  f.value(filterFieldValue),
		Type:   m.columnType(column),
	}
	conds := []db.FilterCond{cond}
	if f.value(filterFieldAnd) == "true" {
		conds = append(append([]db.FilterCond{}, m.data.conds...), cond)
	}
	cmd, err := m.applyFilterConds(conds)
	if err != nil {
		f.err = err.Error()
		return false, nil
	}
	return true, cmd
}

// toggleAdvanced switches between the structured fields and the
// free-text fragment. The cursor goes back to the first field because
// the two modes share no field.
func (fm *filterModal) toggleAdvanced() {
	fm.advanced = !fm.advanced
	fm.form.cursor = 0
	fm.form.err = ""
	fm.form.syncFocus()
	fm.syncFooter()
}

func (fm *filterModal) syncFooter() {
	if fm.advanced {
		fm.form.footer = "ctrl+t simple · enter apply · esc cancel"
		return
	}
	fm.form.footer = "tab/↑↓ field · ←→ change · ctrl+t advanced · enter apply · esc cancel"
}

func (fm *filterModal) update(msg tea.KeyPressMsg, m *Model) (bool, tea.Cmd) {
	if msg.String() == "ctrl+t" {
		fm.toggleAdvanced()
		return false, nil
	}
	return fm.form.update(msg, m)
}

func (fm *filterModal) view(s styles, maxW, maxH int) string {
	return fm.form.view(s, maxW, maxH)
}

// columnType is the declared type of one column of the open relation.
// It comes from the introspected metadata when that is loaded and from
// the page's own result columns otherwise; an unknown column yields "",
// which makes db.BuildFilter fall back to sniffing the value.
func (m Model) columnType(name string) string {
	for _, c := range m.filterColumns() {
		if strings.EqualFold(c.Name, name) {
			return c.DataType
		}
	}
	return ""
}

// filterColumns is the column list the filter modal offers. The
// introspected metadata (Driver.TableColumns) is preferred because it
// carries the declared types; the columns of the page on screen are the
// fallback for a relation whose Structure tab has never been opened.
func (m Model) filterColumns() []db.Column {
	if m.meta.loaded && m.meta.table == m.data.table && len(m.meta.cols) > 0 {
		return m.meta.cols
	}
	return m.data.cols
}
