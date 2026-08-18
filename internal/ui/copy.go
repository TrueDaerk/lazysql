package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/db"
	"lazysql/internal/export"
)

// The `y` copy menu. Every entry ends up as text on the clipboard (or,
// with no clipboard around, in a temp file — see copyOut). The three
// scopes are the cell under the cursor, the row under the cursor and
// the whole table with the grid's filter and sort applied.
//
// Cell and row copies are served from the page already in memory. Table
// copies stream pages exactly like the file export does, capped at
// copyRowLimit because a clipboard has to hold the whole thing at once.

// copyRowLimit bounds a whole-table clipboard copy. Beyond it the copy
// is truncated and the command log says so — `E` writes the rest to a
// file without ever materializing it.
const copyRowLimit = 5000

// copyTimeout bounds a whole-table clipboard copy. It is generous
// because the copy walks every page, and the export flow (`E`), which
// is the right tool for a table that takes longer, is cancellable.
const copyTimeout = 2 * time.Minute

// ---------- messages ----------

// copiedMsg is the outcome of a copy, already rendered for the log.
// osc52, when set, is text the update loop still has to push out as an
// OSC 52 escape sequence: copyOut chose the terminal's own clipboard
// write, and only the program can write to the terminal.
type copiedMsg struct {
	line  string
	osc52 string
}

// ---------- the menu ----------

// copyMenu is `y`: the context-aware copy menu. Which entries it offers
// depends on where the cursor is — the cell and row scopes need a real
// row under it, and the metadata tabs have no row at all.
func (m *Model) copyMenu() tea.Cmd {
	// The activity report owns the main view where the grid would be, so
	// `y` there copies its sessions. It has no relation behind it, so it
	// offers the scopes that need none and leaves the rest out.
	if m.activityFocused() {
		return m.activityCopyMenu()
	}
	if !m.data.open() {
		return logCmd("-- copy skipped: nothing open")
	}
	var entries []menuEntry
	add := func(k, label string, id actionID) {
		entries = append(entries, menuEntry{key: k, label: label, action: func(mm *Model) tea.Cmd {
			next, cmd := mm.runAction(id)
			*mm = next
			return cmd
		}})
	}

	// A selection outranks the cursor row: with N rows marked, "row" is
	// no longer what the user means by a copy, so the selection scopes
	// come first and take the row scope's own keys.
	sel := m.data.selectedRows()
	// A block selection names its columns too, so the entries say what
	// they will leave out.
	scope := fmt.Sprintf("%d selected rows", len(sel))
	if m.data.narrowedToCols() {
		scope = fmt.Sprintf("%d selected rows × %d columns", len(sel), len(m.data.selectedCols()))
	}
	if len(sel) > 0 {
		add("r", scope+" — CSV", actCopySelectionCSV)
		add("o", scope+" — JSON array", actCopySelectionJSON)
		// An INSERT needs a table to insert into, which a free-form result
		// set does not have.
		if m.data.browsing() {
			add("i", scope+" — INSERT statements", actCopySelectionInsert)
		}
		// The one scope only a selection has: the cursor column's value in
		// every selected row, one per line — a list of ids to paste into
		// the next query.
		add("c", "column values of selection — "+m.cursorColumnLabel(), actCopySelectionColumn)
	}
	// Lowercase is the row scope, uppercase the table scope. `j` and `k`
	// are not available here: the menu itself moves with them.
	if len(sel) == 0 && m.copyableRow() {
		add("c", "cell — raw value", actCopyCell)
		add("r", "row — CSV line", actCopyRowCSV)
		add("o", "row — JSON object", actCopyRowJSON)
		// An INSERT needs a table to insert into, which a free-form
		// result set does not have.
		if m.data.browsing() {
			add("i", "row — INSERT statement", actCopyRowInsert)
		}
	}
	// The table scopes re-read the relation through the server; a query
	// result has no relation behind it, only the rows already on screen,
	// so its scope is the loaded page rather than the whole result — `E`
	// is the way to get the rest.
	switch {
	case m.data.browsing():
		add("C", "table — CSV", actCopyTableCSV)
		add("O", "table — JSON array", actCopyTableJSON)
		add("I", "table — INSERT statements", actCopyTableInsert)
		add("A", "table — CREATE TABLE + INSERTs", actCopyTableSchema)
		add("d", "DDL statement", actCopyDDL)
	case m.data.isQuery():
		add("C", "page — CSV", actCopyPageCSV)
		add("O", "page — JSON array", actCopyPageJSON)
	}
	entries = append(entries, menuEntry{key: "esc", label: "cancel"})

	title := "Copy — " + m.dataSubject()
	if len(sel) > 0 {
		title = fmt.Sprintf("Copy — %s (%s)", m.dataSubject(), scope)
	}
	m.modal = &menuModal{title: title, entries: entries}
	return nil
}

// copyableRow reports whether the cursor sits on a row a copy can be
// built from: a fetched row of the Data tab. A staged insert has no
// value for the columns it leaves to the engine, so copying it would
// have to invent NULLs — the copy menu leaves it out instead.
func (m Model) copyableRow() bool {
	if m.tab.metadata() || m.onPhantomRow() {
		return false
	}
	return m.data.row >= 0 && m.data.row < len(m.data.rows)
}

// ---------- cell and row ----------

// rowValues returns one page row as the grid shows it: the fetched
// values with that row's staged cell edits applied, in column order.
// Copying what is on screen beats copying what only the server still
// holds — the same rule `D` (duplicate row) follows.
func (m Model) rowValues(row int) ([]any, bool) {
	if row < 0 || row >= len(m.data.rows) {
		return nil, false
	}
	var pkVals []any
	if pkCols := m.pkColumns(); pkCols != nil {
		pkVals, _ = m.rowKeyVals(pkCols, row)
	}
	out := make([]any, len(m.data.cols))
	for i, c := range m.data.cols {
		if i < len(m.data.rows[row]) {
			out[i] = m.data.rows[row][i]
		}
		if pkVals != nil {
			if ch, ok := m.changes.Lookup(m.data.database, m.data.table, pkVals, c.Name); ok {
				out[i] = ch.NewValue
			}
		}
	}
	return out, true
}

// ---------- the scopes, as plain values ----------
//
// The four functions below are the cell, row, selection and column
// scopes with no grid behind them: a set of columns, the values under
// them, and a subject to name the copy by. The Data tab feeds them the
// page plus its staged edits; the server activity report feeds them its
// own list. Written once, so a scope cannot mean two things — see
// wiki/design/read-only-grid.md.

// copyCellValue copies one raw value. A NULL copies as an empty string:
// the cell holds no text, and pasting the four letters "NULL" into a
// form would be a silent lie. The log says which it was.
func copyCellValue(subject, name string, v any) tea.Cmd {
	label := fmt.Sprintf("cell %s.%s", subject, name)
	if v == nil {
		label += " (NULL → empty)"
	}
	return copyTextCmd(label, subject+"-"+name+".txt", db.FormatValue(v, ""))
}

// copyRowValues copies one row in one of the row formats.
func copyRowValues(f export.Format, opts export.Options, subject string, cols []db.Column, values []any) tea.Cmd {
	text, err := export.Row(f, opts, cols, values)
	if err != nil {
		return logCmd("-- copy row FAILED: %v", err)
	}
	return copyTextCmd(
		fmt.Sprintf("row of %s as %s", subject, strings.ToUpper(string(f))),
		fmt.Sprintf("%s-row.%s", subject, f),
		text,
	)
}

// copyRowBlock copies a block of rows through export.Rows, so a CSV copy
// carries its header and a JSON copy is one array — a multi-row copy is
// a small table, not a stack of single-row copies glued together. scope
// names it in the log line, file in the clipboard-less fallback.
func copyRowBlock(f export.Format, opts export.Options, scope, file string, cols []db.Column, rows [][]any) tea.Cmd {
	text, err := export.Rows(f, opts, cols, rows)
	if err != nil {
		return logCmd("-- copy selection FAILED: %v", err)
	}
	return copyTextCmd(
		fmt.Sprintf("%s as %s", scope, strings.ToUpper(string(f))),
		fmt.Sprintf("%s.%s", file, f),
		text,
	)
}

// copyColumnValues copies one column's value in every given row, one per
// line. NULL copies as an empty line for the same reason copyCellValue
// copies it as an empty string. The log says how many of them there were.
func copyColumnValues(subject, name string, values []any) tea.Cmd {
	var b strings.Builder
	nulls := 0
	for _, v := range values {
		if v == nil {
			nulls++
		}
		b.WriteString(db.FormatValue(v, ""))
		b.WriteString("\n")
	}
	label := fmt.Sprintf("%s.%s of %d selected rows", subject, name, len(values))
	if nulls > 0 {
		label += fmt.Sprintf(" (%d NULL → empty)", nulls)
	}
	return copyTextCmd(label, fmt.Sprintf("%s-%s-selection.txt", subject, name), b.String())
}

// cutColumns narrows every row to the given column indices, which is what
// turns a full-width selection into the block `shift+←`/`shift+→` marked.
func cutColumns(rows [][]any, idx []int) [][]any {
	out := make([][]any, 0, len(rows))
	for _, values := range rows {
		row := make([]any, 0, len(idx))
		for _, c := range idx {
			var v any
			if c >= 0 && c < len(values) {
				v = values[c]
			}
			row = append(row, v)
		}
		out = append(out, row)
	}
	return out
}

// ---------- the Data tab's scopes ----------

// copyCell copies the raw value under the cursor.
func (m Model) copyCell() tea.Cmd {
	values, ok := m.rowValues(m.data.row)
	if !ok || m.data.col < 0 || m.data.col >= len(values) {
		return logCmd("-- copy cell skipped: no cell under the cursor")
	}
	return copyCellValue(m.dataSubject(), m.data.cols[m.data.col].Name, values[m.data.col])
}

// copyRow copies the row under the cursor in one of the three row
// formats.
func (m Model) copyRow(f export.Format) tea.Cmd {
	values, ok := m.rowValues(m.data.row)
	if !ok {
		return logCmd("-- copy row skipped: no row under the cursor")
	}
	return copyRowValues(f, m.exportOptions(""), m.dataSubject(), m.data.cols, values)
}

// ---------- the multi-row selection ----------

// copySelectionMenu is `ctrl+c` with a selection up. It is the `y` menu:
// copyMenu already puts the selection scopes first whenever rows are
// marked, so the two keys open one menu rather than two that would drift
// apart. The key only exists because `ctrl+c` is what a terminal user
// reaches for to copy — see wiki/design/grid-multi-row-selection.md.
func (m *Model) copySelectionMenu() tea.Cmd {
	sel := m.data.selectedRows()
	if m.activityFocused() {
		sel = m.activity.grid.selectedRows()
	}
	if len(sel) == 0 {
		return logCmd("-- copy selection skipped: nothing selected")
	}
	return m.copyMenu()
}

// cursorColumnLabel names the column under the cell cursor for a menu
// entry, falling back to the generic word when there is none: a label
// never wants an empty noun in it.
func (m Model) cursorColumnLabel() string {
	if name, ok := m.cursorColumnName(); ok {
		return name
	}
	return "column"
}

// selectionValues is every selected row as the grid shows it — staged
// cell edits included, exactly like rowValues, which is what it is built
// from.
func (m Model) selectionValues() [][]any {
	sel := m.data.selectedRows()
	out := make([][]any, 0, len(sel))
	for _, r := range sel {
		if values, ok := m.rowValues(r); ok {
			out = append(out, values)
		}
	}
	return out
}

// selectionColumns is the columns the selection covers — all of them
// unless `shift+←`/`shift+→` narrowed it to a block — together with the
// indices they sit at, so a row can be cut down to the same shape.
func (m Model) selectionColumns() ([]db.Column, []int) {
	idx := m.data.selectedCols()
	cols := make([]db.Column, 0, len(idx))
	for _, c := range idx {
		if c >= 0 && c < len(m.data.cols) {
			cols = append(cols, m.data.cols[c])
		}
	}
	return cols, idx
}

// copySelectionRows copies the whole selection in one of the row
// formats. A selection narrowed to a block copies only its columns: the
// CSV header, the JSON keys and the INSERT column list all shrink with
// it.
func (m Model) copySelectionRows(f export.Format) tea.Cmd {
	rows := m.selectionValues()
	if len(rows) == 0 {
		return logCmd("-- copy selection skipped: nothing selected")
	}
	cols, idx := m.selectionColumns()
	if len(cols) == 0 {
		return logCmd("-- copy selection skipped: no columns selected")
	}
	scope := fmt.Sprintf("%d selected rows of %s", len(rows), m.dataSubject())
	if m.data.narrowedToCols() {
		scope = fmt.Sprintf("%d selected rows × %d columns of %s",
			len(rows), len(cols), m.dataSubject())
	}
	return copyRowBlock(f, m.exportOptions(""), scope, m.dataSubject()+"-selection",
		cols, cutColumns(rows, idx))
}

// copySelectionColumn copies the cursor column's value in every selected
// row, one per line.
func (m Model) copySelectionColumn() tea.Cmd {
	rows := m.selectionValues()
	if len(rows) == 0 {
		return logCmd("-- copy selection skipped: nothing selected")
	}
	col := m.data.col
	if col < 0 || col >= len(m.data.cols) {
		return logCmd("-- copy selection skipped: no column under the cursor")
	}
	values := make([]any, 0, len(rows))
	for _, r := range cutColumns(rows, []int{col}) {
		values = append(values, r[0])
	}
	return copyColumnValues(m.dataSubject(), m.data.cols[col].Name, values)
}

// dataSubject names what the Data tab is showing, for log lines, copy
// labels and file names: the open relation, or "query" for a result the
// editor produced.
func (m Model) dataSubject() string {
	if m.data.browsing() {
		return m.data.table
	}
	return "query"
}

// exportOptions is what the serializers need about the open relation.
// ddl is non-empty only for the CREATE TABLE + INSERTs variant.
func (m Model) exportOptions(ddl string) export.Options {
	o := export.Options{Database: m.data.database, Table: m.data.table, DDL: ddl}
	if m.driver != nil {
		o.Dialect = m.driver.Dialect()
	}
	return o
}

// copyTextCmd hands text to the clipboard off the update loop; the
// clipboard library shells out, which is not something Update may wait
// for. When the copy has to go out as OSC 52 instead, the message it
// returns says so and the root issues tea.SetClipboard for it — the
// escape sequence belongs to the program's tty, not to a command
// running on its own goroutine.
func copyTextCmd(subject, filename, text string) tea.Cmd {
	return func() tea.Msg { return copyOut(subject, filename, text) }
}

// ---------- whole table ----------

// copyTable streams the whole table — the grid's filter and sort
// included — into a buffer and copies it. It is capped at copyRowLimit:
// unlike a file export, a clipboard copy cannot be streamed anywhere,
// so the cap is what keeps `y` from trying to hold a 100k-row table in
// memory. Reaching it is reported, never silent.
func (m *Model) copyTable(f export.Format, withDDL bool) tea.Cmd {
	if !m.data.browsing() {
		return logCmd("-- copy table skipped: no relation open")
	}
	if m.driver == nil {
		return logCmd("-- copy table skipped: not connected")
	}
	if withDDL && !m.meta.loaded {
		// The DDL has never been fetched. Park the copy and replay it
		// when the metadata lands, the same way `y` on a cold DDL tab
		// already works.
		return m.deferUntilMeta(actCopyTableSchema)
	}
	ddl := ""
	if withDDL {
		if m.meta.ddl == "" {
			return logCmd("-- copy table skipped: %s", m.ddlProblem())
		}
		ddl = m.meta.ddl
	}

	d := m.data
	opts := m.exportOptions(ddl)
	pager := pagerFor(m.driver, d)
	label := strings.ToUpper(string(f))
	if withDDL {
		label = "CREATE + INSERTs"
	}
	name := fmt.Sprintf("%s.%s", d.table, f)

	return tea.Batch(
		logCmd("-- copy %s as %s (streaming, max %d rows)…", d.table, label, copyRowLimit),
		func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), copyTimeout)
			defer cancel()

			var b strings.Builder
			w, err := export.NewWriter(&b, f, opts)
			if err != nil {
				return copiedMsg{line: fmt.Sprintf("-- copy %s FAILED: %v", d.table, err)}
			}
			rows, truncated, err := export.Stream(ctx, w, pager, export.StreamOptions{
				PageSize: export.DefaultPageSize,
				MaxRows:  copyRowLimit,
			})
			if err != nil {
				return copiedMsg{line: fmt.Sprintf("-- copy %s FAILED after %d rows: %v", d.table, rows, err)}
			}
			out := copyOut(fmt.Sprintf("%s as %s (%d rows)", d.table, label, rows), name, b.String())
			if truncated {
				out.line += fmt.Sprintf("  -- TRUNCATED at %d rows, press E to export the whole table", copyRowLimit)
			}
			return out
		},
	)
}

// copyQueryPage copies a query result's loaded page — the rows already
// materialized in memory, not the full result the statement might match.
// A whole-table copy can stream past what is on screen because a table
// can be re-read page by page; an arbitrary query result cannot be
// re-paged the same way (see queryRunnerFor), and streaming into a
// clipboard buffer makes no sense regardless — `E` exports the full
// result to a file instead. The log line says so, the same way the
// table-scope copy's truncation notice does.
func (m Model) copyQueryPage(f export.Format) tea.Cmd {
	if !m.data.isQuery() {
		return logCmd("-- copy skipped: no query result open")
	}
	text, err := export.Rows(f, m.exportOptions(""), m.data.cols, m.data.rows)
	if err != nil {
		return logCmd("-- copy page FAILED: %v", err)
	}
	subject := fmt.Sprintf("query page as %s (%d rows, loaded page only — E exports the full result)",
		strings.ToUpper(string(f)), len(m.data.rows))
	return copyTextCmd(subject, fmt.Sprintf("query-page.%s", f), text)
}

// pagerFor closes over the driver and the grid's query shape, so an
// export reads exactly the rows the grid would page through.
func pagerFor(drv db.Driver, d dataView) export.Pager {
	return func(ctx context.Context, limit, offset int) (*db.ResultSet, error) {
		return drv.QueryPage(ctx, d.database, d.table, d.filter, d.sort, limit, offset)
	}
}

// queryRunnerFor closes over the driver and a query result's own
// statement — and, for a run against bound placeholders, the values it
// ran with — so an export re-reads exactly what produced the result on
// screen. Unlike pagerFor it cannot be re-issued a page at a time:
// rewriting arbitrary user SQL to add a LIMIT/OFFSET would risk changing
// what it selects, so the statement runs once and streams straight
// through Driver.QueryStream.
func queryRunnerFor(drv db.Driver, d dataView) export.QueryRunner {
	return func(ctx context.Context, onRow func(cols []db.Column, row []any) error) error {
		return drv.QueryStream(ctx, d.queryExec, d.queryArgs, onRow)
	}
}

// ---------- dispatch ----------

// copyActions handles the copy menu and its entries. Like dataActions
// it runs from runAction, so a key press, the `a` menu and the copy
// menu all reach the same code.
func (m Model) copyActions(id actionID) (Model, tea.Cmd, bool) {
	// The activity report's rows are not the Data tab's: while it has the
	// main view the cell, row, selection and list scopes read its grid.
	// Everything it does not answer — the menu keys themselves, the
	// export flow — falls through to the shared handling below.
	if m.activityFocused() {
		if cmd, ok := m.activityCopy(id); ok {
			return m, cmd, true
		}
	}
	switch id {
	case actCopyMenu:
		cmd := m.copyMenu()
		return m, cmd, true
	case actCopyCell:
		return m, m.copyCell(), true
	case actCopyRowCSV:
		return m, m.copyRow(export.FormatCSV), true
	case actCopyRowJSON:
		return m, m.copyRow(export.FormatJSON), true
	case actCopyRowInsert:
		return m, m.copyRow(export.FormatSQL), true
	case actCopyTableCSV:
		cmd := m.copyTable(export.FormatCSV, false)
		return m, cmd, true
	case actCopyTableJSON:
		cmd := m.copyTable(export.FormatJSON, false)
		return m, cmd, true
	case actCopyTableInsert:
		cmd := m.copyTable(export.FormatSQL, false)
		return m, cmd, true
	case actCopyTableSchema:
		cmd := m.copyTable(export.FormatSQL, true)
		return m, cmd, true
	case actCopyPageCSV:
		return m, m.copyQueryPage(export.FormatCSV), true
	case actCopyPageJSON:
		return m, m.copyQueryPage(export.FormatJSON), true

	case actCopySelectionMenu:
		cmd := m.copySelectionMenu()
		return m, cmd, true
	case actCopySelectionCSV:
		return m, m.copySelectionRows(export.FormatCSV), true
	case actCopySelectionJSON:
		return m, m.copySelectionRows(export.FormatJSON), true
	case actCopySelectionInsert:
		return m, m.copySelectionRows(export.FormatSQL), true
	case actCopySelectionColumn:
		return m, m.copySelectionColumn(), true

	// The file export shares this dispatcher: it is the same feature
	// seen from the other end, and `E` is offered next to `y`.
	case actExportTable:
		cmd := m.startExport()
		return m, cmd, true
	// actExportDDL is not bound to a key of its own — it is what a
	// deferred DDL export (startDDLExport's cold-cache path) replays
	// once the metadata lands, regardless of which tab is open by then.
	case actExportDDL:
		cmd := m.startDDLExport()
		return m, cmd, true
	case actCancelExport:
		cmd := m.cancelExport()
		return m, cmd, true
	}
	return m, nil, false
}
