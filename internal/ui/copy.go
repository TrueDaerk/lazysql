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
type copiedMsg struct{ line string }

// ---------- the menu ----------

// copyMenu is `y`: the context-aware copy menu. Which entries it offers
// depends on where the cursor is — the cell and row scopes need a real
// row under it, and the metadata tabs have no row at all.
func (m *Model) copyMenu() tea.Cmd {
	if !m.data.open() {
		return logCmd("-- copy skipped: no relation open")
	}
	var entries []menuEntry
	add := func(k, label string, id actionID) {
		entries = append(entries, menuEntry{key: k, label: label, action: func(mm *Model) tea.Cmd {
			next, cmd := mm.runAction(id)
			*mm = next
			return cmd
		}})
	}

	// Lowercase is the row scope, uppercase the table scope. `j` and `k`
	// are not available here: the menu itself moves with them.
	if m.copyableRow() {
		add("c", "cell — raw value", actCopyCell)
		add("r", "row — CSV line", actCopyRowCSV)
		add("o", "row — JSON object", actCopyRowJSON)
		add("i", "row — INSERT statement", actCopyRowInsert)
	}
	add("C", "table — CSV", actCopyTableCSV)
	add("O", "table — JSON array", actCopyTableJSON)
	add("I", "table — INSERT statements", actCopyTableInsert)
	add("A", "table — CREATE TABLE + INSERTs", actCopyTableSchema)
	add("d", "DDL statement", actCopyDDL)
	entries = append(entries, menuEntry{key: "esc", label: "cancel"})

	m.modal = &menuModal{title: "Copy — " + m.data.table, entries: entries}
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

// copyCell copies the raw value under the cursor. A NULL copies as an
// empty string: the cell holds no text, and pasting the four letters
// "NULL" into a form would be a silent lie. The log says which it was.
func (m Model) copyCell() tea.Cmd {
	values, ok := m.rowValues(m.data.row)
	if !ok || m.data.col < 0 || m.data.col >= len(values) {
		return logCmd("-- copy cell skipped: no cell under the cursor")
	}
	name := m.data.cols[m.data.col].Name
	v := values[m.data.col]
	subject := fmt.Sprintf("cell %s.%s", m.data.table, name)
	if v == nil {
		subject += " (NULL → empty)"
	}
	text := db.FormatValue(v, "")
	return copyTextCmd(subject, m.data.table+"-"+name+".txt", text)
}

// copyRow copies the row under the cursor in one of the three row
// formats.
func (m Model) copyRow(f export.Format) tea.Cmd {
	values, ok := m.rowValues(m.data.row)
	if !ok {
		return logCmd("-- copy row skipped: no row under the cursor")
	}
	text, err := export.Row(f, m.exportOptions(""), m.data.cols, values)
	if err != nil {
		return logCmd("-- copy row FAILED: %v", err)
	}
	return copyTextCmd(
		fmt.Sprintf("row of %s as %s", m.data.table, strings.ToUpper(string(f))),
		fmt.Sprintf("%s-row.%s", m.data.table, f),
		text,
	)
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
// for.
func copyTextCmd(subject, filename, text string) tea.Cmd {
	return func() tea.Msg { return copiedMsg{line: copyOut(subject, filename, text)} }
}

// ---------- whole table ----------

// copyTable streams the whole table — the grid's filter and sort
// included — into a buffer and copies it. It is capped at copyRowLimit:
// unlike a file export, a clipboard copy cannot be streamed anywhere,
// so the cap is what keeps `y` from trying to hold a 100k-row table in
// memory. Reaching it is reported, never silent.
func (m *Model) copyTable(f export.Format, withDDL bool) tea.Cmd {
	if !m.data.open() {
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
			line := copyOut(fmt.Sprintf("%s as %s (%d rows)", d.table, label, rows), name, b.String())
			if truncated {
				line += fmt.Sprintf("  -- TRUNCATED at %d rows, press E to export the whole table", copyRowLimit)
			}
			return copiedMsg{line: line}
		},
	)
}

// pagerFor closes over the driver and the grid's query shape, so an
// export reads exactly the rows the grid would page through.
func pagerFor(drv db.Driver, d dataView) export.Pager {
	return func(ctx context.Context, limit, offset int) (*db.ResultSet, error) {
		return drv.QueryPage(ctx, d.database, d.table, d.filter, d.sort, limit, offset)
	}
}

// ---------- dispatch ----------

// copyActions handles the copy menu and its entries. Like dataActions
// it runs from runAction, so a key press, the `a` menu and the copy
// menu all reach the same code.
func (m Model) copyActions(id actionID) (Model, tea.Cmd, bool) {
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

	// The file export shares this dispatcher: it is the same feature
	// seen from the other end, and `E` is offered next to `y`.
	case actExportTable:
		cmd := m.startExport()
		return m, cmd, true
	case actCancelExport:
		cmd := m.cancelExport()
		return m, cmd, true
	}
	return m, nil, false
}
