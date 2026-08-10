package ui

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"lazysql/internal/db"
)

// Whole-row operations, staged through the same changeset as cell edits
// and committed in the same transaction. `d` stages a DELETE of the row
// under the cursor, `n` opens an insert form, `D` opens the same form
// prefilled from the current row. Nothing here executes SQL.

// ---------- staged inserts as phantom rows ----------

// stagedInserts are the pending INSERTs of the open table, in staging
// order. The grid renders them after the last real row of the page.
func (m Model) stagedInserts() []db.RowInsert {
	if !m.data.browsing() {
		return nil
	}
	return m.changes.InsertsFor(m.data.database, m.data.table)
}

// phantomAtCursor returns the staged insert the cursor sits on, if the
// cursor left the real rows of the page.
func (m Model) phantomAtCursor() (db.RowInsert, bool) {
	ins := m.stagedInserts()
	i := m.data.row - len(m.data.rows)
	if i < 0 || i >= len(ins) {
		return db.RowInsert{}, false
	}
	return ins[i], true
}

func (m Model) onPhantomRow() bool {
	_, ok := m.phantomAtCursor()
	return ok
}

// clampCursor re-derives how many phantom rows the grid has and then
// clamps the cell cursor. Every place that moves the cursor or changes
// the page goes through here, so staging or unstaging an insert can
// never leave the cursor pointing past the last rendered row.
func (m *Model) clampCursor() {
	m.data.extraRows = len(m.stagedInserts())
	m.data.clampCursor()
}

// ---------- delete ----------

// startDelete is `d` on the Data tab. Like `e` it needs the primary key
// from the metadata, and waits for the fetch when nothing cached it yet.
func (m *Model) startDelete() tea.Cmd {
	if !m.data.browsing() || m.tab.metadata() || m.driver == nil {
		return nil
	}
	if m.readOnly() {
		return readOnlyBlocked("delete")
	}
	if m.onPhantomRow() {
		return logCmd("-- delete skipped: the row is a staged insert (u unstages it)")
	}
	if len(m.data.rows) == 0 {
		return logCmd("-- delete skipped: no rows on this page")
	}
	if m.meta.loaded {
		return m.stageDeleteAtCursor()
	}
	return m.deferUntilMeta(actDeleteRow)
}

// stageDeleteAtCursor records the DELETE of the row under the cursor.
// Staging it also drops that row's staged cell edits — see
// Changeset.StageDelete.
func (m *Model) stageDeleteAtCursor() tea.Cmd {
	pkCols, pkVals, problem := m.editIdentity(m.data.row)
	if problem != "" {
		m.modal = &confirmModal{title: "Deleting disabled", body: problem, danger: true}
		return nil
	}
	r := db.RowDelete{
		Database: m.data.database,
		Table:    m.data.table,
		PKCols:   pkCols,
		PKVals:   pkVals,
	}
	if !m.changes.StageDelete(r) {
		return logCmd("-- already staged: delete of %s (%s)", r.Table, rowLabel(pkCols, pkVals))
	}
	st := db.DeleteSQL(m.driver.Dialect(), r)
	return logCmd("-- stage: %s;  -- args %v", st.SQL, st.Args)
}

// ---------- insert and duplicate ----------

// startInsert is `n` (fresh row) and `D` (duplicate of the row under the
// cursor). Both need the column list, so both wait for the metadata.
// Unlike delete, neither needs a primary key: a PK-less table can still
// be inserted into.
func (m *Model) startInsert(duplicate bool) tea.Cmd {
	if !m.data.browsing() || m.tab.metadata() || m.driver == nil {
		return nil
	}
	if m.readOnly() {
		if duplicate {
			return readOnlyBlocked("duplicate")
		}
		return readOnlyBlocked("insert")
	}
	if duplicate {
		if m.onPhantomRow() {
			return logCmd("-- duplicate skipped: the row is itself a staged insert")
		}
		if len(m.data.rows) == 0 {
			return logCmd("-- duplicate skipped: no rows on this page")
		}
	}
	if m.meta.loaded {
		return m.openInsertModal(duplicate)
	}
	if duplicate {
		return m.deferUntilMeta(actDuplicateRow)
	}
	return m.deferUntilMeta(actInsertRow)
}

// openInsertModal builds the insert form from the table's columns,
// optionally prefilled with the row under the cursor.
func (m *Model) openInsertModal(duplicate bool) tea.Cmd {
	if len(m.meta.cols) == 0 {
		m.modal = &confirmModal{
			title:  "Insert disabled",
			body:   "the engine reported no columns for this relation.",
			danger: true,
		}
		return nil
	}
	title := "Insert row into " + m.data.table
	var prefill map[string]any
	if duplicate {
		prefill = m.effectiveRow(m.data.row)
		if prefill == nil {
			return logCmd("-- duplicate skipped: no row under the cursor")
		}
		title = "Duplicate row of " + m.data.table
	}
	m.modal = newInsertRowModal(title, m.data.database, m.data.table, m.meta.cols, prefill)
	return nil
}

// effectiveRow is one page row as the grid shows it: the fetched values
// with the row's staged cell edits applied, keyed by column name. That
// is what `D` duplicates — copying values the user can see beats copying
// values only the server still holds.
func (m Model) effectiveRow(row int) map[string]any {
	if row < 0 || row >= len(m.data.rows) {
		return nil
	}
	var pkVals []any
	if pkCols := m.pkColumns(); pkCols != nil {
		pkVals, _ = m.rowKeyVals(pkCols, row)
	}
	out := make(map[string]any, len(m.data.cols))
	for i, c := range m.data.cols {
		var v any
		if i < len(m.data.rows[row]) {
			v = m.data.rows[row][i]
		}
		if pkVals != nil {
			if ch, ok := m.changes.Lookup(m.data.database, m.data.table, pkVals, c.Name); ok {
				v = ch.NewValue
			}
		}
		out[c.Name] = v
	}
	return out
}

// stageInsert records a confirmed insert form.
func (m *Model) stageInsert(r db.RowInsert) tea.Cmd {
	r = m.changes.StageInsert(r)
	m.clampCursor()
	st := db.InsertSQL(m.driver.Dialect(), r)
	return logCmd("-- stage: %s;  -- args %v", st.SQL, st.Args)
}

// insertValueFor returns the value a staged insert binds for a column.
// bound is false when the insert leaves the column out, i.e. the engine
// will apply its default.
func insertValueFor(r db.RowInsert, column string) (value any, bound bool) {
	for i, c := range r.Columns {
		if c != column {
			continue
		}
		if i < len(r.Values) {
			return r.Values[i], true
		}
		return nil, true
	}
	return nil, false
}

// ---------- column value inference ----------

// autoAssigned reports whether the engine fills a column in by itself
// when the INSERT leaves it out — a DEFAULT clause, an auto-increment /
// identity / serial column, or SQLite's `INTEGER PRIMARY KEY` rowid
// alias, which auto-assigns without declaring anything.
func autoAssigned(c db.Column) bool {
	if c.Default != nil {
		return true
	}
	extra := strings.ToLower(c.Extra)
	for _, marker := range []string{"auto_increment", "autoincrement", "identity", "generated", "serial"} {
		if strings.Contains(extra, marker) {
			return true
		}
	}
	return c.PrimaryKey && columnKind(c.DataType) == kindInt
}

// columnKind classifies a declared type well enough to bind a typed
// parameter. Anything unrecognized — and deliberately NUMERIC/DECIMAL,
// where a float64 would lose digits — is bound as a string and the
// engine converts it.
type valueKind int

const (
	kindText valueKind = iota
	kindInt
	kindFloat
	kindBool
)

func columnKind(dataType string) valueKind {
	base := strings.ToLower(strings.TrimSpace(dataType))
	if i := strings.IndexAny(base, " ("); i >= 0 {
		base = base[:i]
	}
	switch base {
	case "int", "integer", "bigint", "smallint", "tinyint", "mediumint",
		"int2", "int4", "int8", "serial", "bigserial", "smallserial",
		"hugeint", "ubigint", "uinteger", "usmallint", "utinyint":
		return kindInt
	case "float", "double", "real", "float4", "float8":
		return kindFloat
	case "bool", "boolean":
		return kindBool
	}
	return kindText
}

// convertForColumn turns form text into a typed parameter guided by the
// column's declared type. Text that does not parse stays a string, the
// same rule the cell editor uses.
func convertForColumn(text string, c db.Column) any {
	switch columnKind(c.DataType) {
	case kindInt:
		if v, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64); err == nil {
			return v
		}
	case kindFloat:
		if v, err := strconv.ParseFloat(strings.TrimSpace(text), 64); err == nil {
			return v
		}
	case kindBool:
		if v, err := strconv.ParseBool(strings.TrimSpace(text)); err == nil {
			return v
		}
	}
	return text
}

// ---------- insert modal ----------

// insertMode is what one field contributes to the staged INSERT.
type insertMode int

const (
	insertValue   insertMode = iota // bind the typed value
	insertNull                      // bind an explicit SQL NULL
	insertDefault                   // leave the column out of the statement
)

// insertField is one column's row in the form.
type insertField struct {
	col   db.Column
	input textinput.Model
	mode  insertMode
}

// insertRowModal is the `n` / `D` popup: one field per column, each
// cycling between a typed value, NULL and the column's default.
type insertRowModal struct {
	title           string
	database, table string
	fields          []*insertField
	cursor          int
	offset          int
	err             string
}

func newInsertRowModal(title, database, table string, cols []db.Column, prefill map[string]any) *insertRowModal {
	f := &insertRowModal{title: title, database: database, table: table}
	for _, c := range cols {
		ti := textinput.New()
		ti.Placeholder = strings.ToLower(c.DataType)
		ti.SetWidth(34)
		fl := &insertField{col: c, input: ti}

		v, prefilled := prefill[c.Name]
		switch {
		// A duplicate clears the key: reusing it would collide, and an
		// auto-assigned column gets a fresh value from the engine.
		case prefilled && (c.PrimaryKey || autoAssigned(c)):
			fl.mode = insertDefault
			if !autoAssigned(c) {
				// Nothing will fill it in, so the user has to.
				fl.mode = insertValue
			}
		case prefilled && v == nil:
			fl.mode = insertNull
		case prefilled:
			fl.mode = insertValue
			fl.input.SetValue(db.FormatValue(v, ""))
		case autoAssigned(c):
			fl.mode = insertDefault
		case c.Nullable:
			fl.mode = insertNull
		}
		fl.input.CursorEnd()
		f.fields = append(f.fields, fl)
	}
	f.syncFocus()
	return f
}

// syncFocus keeps exactly the field under the cursor focused, so only
// one input blinks and receives runes.
func (f *insertRowModal) syncFocus() {
	for i, fl := range f.fields {
		if i == f.cursor {
			fl.input.Focus()
		} else {
			fl.input.Blur()
		}
	}
}

func (f *insertRowModal) move(delta int) {
	if len(f.fields) == 0 {
		return
	}
	f.cursor = (f.cursor + delta + len(f.fields)) % len(f.fields)
	f.syncFocus()
}

func (f *insertRowModal) current() *insertField {
	if f.cursor < 0 || f.cursor >= len(f.fields) {
		return nil
	}
	return f.fields[f.cursor]
}

func (f *insertRowModal) update(msg tea.KeyPressMsg, m *Model) (bool, tea.Cmd) {
	cur := f.current()
	switch msg.String() {
	case "esc":
		return true, nil
	case "tab", "down":
		f.move(1)
		return false, nil
	case "shift+tab", "up":
		f.move(-1)
		return false, nil
	case "ctrl+n":
		if cur != nil {
			cur.mode = toggleMode(cur.mode, insertNull)
		}
		return false, nil
	case "ctrl+d":
		if cur != nil {
			cur.mode = toggleMode(cur.mode, insertDefault)
		}
		return false, nil
	case "enter":
		r, problem := f.build()
		if problem != "" {
			f.err = problem
			return false, nil
		}
		return true, m.stageInsert(r)
	}
	if cur == nil {
		return false, nil
	}
	// Typing means the user wants a value again, whatever the mode was.
	if msg.Text != "" {
		cur.mode = insertValue
	}
	var cmd tea.Cmd
	cur.input, cmd = cur.input.Update(msg)
	return false, cmd
}

// paste fills the field under the cursor from the system clipboard, and
// switches it to the typed-value mode for the same reason typing does.
func (f *insertRowModal) paste(msg tea.PasteMsg, _ *Model) tea.Cmd {
	cur := f.current()
	if cur == nil {
		return nil
	}
	cur.mode = insertValue
	var cmd tea.Cmd
	cur.input, cmd = cur.input.Update(msg)
	return cmd
}

// toggleMode switches a field into a mode, or back to a typed value when
// it is already in it, so one key both sets and clears NULL / DEFAULT.
func toggleMode(cur, want insertMode) insertMode {
	if cur == want {
		return insertValue
	}
	return want
}

// build validates the form and renders it as a staged insert. A NOT NULL
// column that nothing will fill in is the one thing the form refuses:
// the engine would reject it on commit and roll the whole changeset
// back, which is a slow way to learn about a typo.
func (f *insertRowModal) build() (db.RowInsert, string) {
	r := db.RowInsert{Database: f.database, Table: f.table}
	for _, fl := range f.fields {
		switch fl.mode {
		case insertNull:
			if !fl.col.Nullable {
				return r, fmt.Sprintf("%s is NOT NULL — give it a value", fl.col.Name)
			}
			r.Columns = append(r.Columns, fl.col.Name)
			r.Values = append(r.Values, nil)
		case insertDefault:
			if !fl.col.Nullable && !autoAssigned(fl.col) {
				return r, fmt.Sprintf("%s is NOT NULL and has no default — give it a value", fl.col.Name)
			}
		default:
			r.Columns = append(r.Columns, fl.col.Name)
			r.Values = append(r.Values, convertForColumn(fl.input.Value(), fl.col))
		}
	}
	return r, ""
}

func (f *insertRowModal) view(s styles, maxW, maxH int) string {
	width := min(maxW-8, 72)
	if width < 20 {
		width = 20
	}
	labelW := 0
	for _, fl := range f.fields {
		if w := lipgloss.Width(fl.col.Name); w > labelW {
			labelW = w
		}
	}
	inputW := min(38, maxInt(width-labelW-6, 12))

	// title, blank, error, blank, footer, borders
	rows := maxH - 8
	if rows < 1 {
		rows = 1
	}
	if rows > len(f.fields) {
		rows = len(f.fields)
	}
	if f.cursor < f.offset {
		f.offset = f.cursor
	}
	if f.cursor >= f.offset+rows {
		f.offset = f.cursor - rows + 1
	}
	if maxOff := len(f.fields) - rows; f.offset > maxOff {
		f.offset = maxOff
	}
	if f.offset < 0 {
		f.offset = 0
	}

	var b strings.Builder
	b.WriteString(s.modalTitle.Render(truncate(f.title, width)) + "\n\n")
	for i := f.offset; i < f.offset+rows && i < len(f.fields); i++ {
		fl := f.fields[i]
		fl.input.SetWidth(inputW)
		label := fl.col.Name + strings.Repeat(" ", labelW-lipgloss.Width(fl.col.Name))
		marker := "  "
		if i == f.cursor {
			marker = s.keyHint.Render("▸ ")
			label = s.titleFocused.Render(label)
		} else {
			label = s.muted.Render(label)
		}
		b.WriteString(marker + label + "  " + truncate(f.fieldValue(s, fl), inputW+22) + "\n")
	}
	if len(f.fields) > rows {
		b.WriteString(s.muted.Render(fmt.Sprintf("… %d of %d columns\n",
			f.offset+rows, len(f.fields))))
	}
	if f.err != "" {
		b.WriteString("\n" + s.danger.Render("✗ "+f.err) + "\n")
	}
	b.WriteString("\n" + s.muted.Render(
		"tab/↑↓ field · ctrl+n NULL · ctrl+d default · enter stage · esc cancel"))
	return s.modal.Render(b.String())
}

// fieldValue renders one field's contribution: the input, a dim NULL or
// the default the column falls back to.
func (f *insertRowModal) fieldValue(s styles, fl *insertField) string {
	switch fl.mode {
	case insertNull:
		return s.muted.Render(nullText)
	case insertDefault:
		if fl.col.Default != nil {
			return s.pending.Render("DEFAULT ") + s.muted.Render(flatten(*fl.col.Default))
		}
		if fl.col.Extra != "" {
			return s.pending.Render("DEFAULT ") + s.muted.Render("("+fl.col.Extra+")")
		}
		return s.pending.Render("DEFAULT")
	default:
		return fl.input.View()
	}
}
