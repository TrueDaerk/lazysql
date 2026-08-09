package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"lazysql/internal/db"
)

// Inline editing, the staged way: `e` records a CellChange in the
// changeset instead of executing anything. The UPDATEs only run when the
// user confirms the commit modal, all in one transaction. A table
// without a primary key cannot be edited at all — identifying a row by
// anything less than its declared key is guessing, and lazysql does not
// guess what an UPDATE will hit.

// ---------- messages ----------

// changesCommittedMsg reports the outcome of one commit transaction.
type changesCommittedMsg struct {
	stmts []db.Statement
	err   error
}

// ---------- commands ----------

func commitChangesCmd(drv db.Driver, stmts []db.Statement) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
		defer cancel()
		_, err := drv.ExecTx(ctx, stmts)
		return changesCommittedMsg{stmts: stmts, err: err}
	}
}

// ---------- model wiring ----------

// startEdit is `e` on the Data tab. The primary key comes from the
// cached metadata; when no tab has fetched it yet the fetch is started
// and the modal opens when the reply lands, like `y` does for the DDL.
func (m *Model) startEdit() tea.Cmd {
	if !m.data.open() || m.tab.metadata() || m.driver == nil {
		return nil
	}
	if m.onPhantomRow() {
		return logCmd("-- edit skipped: the cursor is on a staged insert (u unstages it)")
	}
	if len(m.data.rows) == 0 {
		return logCmd("-- edit skipped: no rows on this page")
	}
	if m.meta.loaded {
		return m.openEditModal()
	}
	return m.deferUntilMeta(actEditCell)
}

// editIdentity returns the primary key columns of the open table and
// their values in one page row. A non-empty problem means the row cannot
// be identified and editing is off.
func (m Model) editIdentity(row int) (pkCols []string, pkVals []any, problem string) {
	for _, c := range m.meta.cols {
		if c.PrimaryKey {
			pkCols = append(pkCols, c.Name)
		}
	}
	if len(pkCols) == 0 {
		return nil, nil, "the table has no primary key, so a row cannot be identified safely.\n\nlazysql refuses to guess row identity — add a primary key to edit or delete rows here. Inserting is still allowed."
	}
	if row < 0 || row >= len(m.data.rows) {
		return nil, nil, "no row under the cursor."
	}
	vals, ok := m.rowKeyVals(pkCols, row)
	if !ok {
		return nil, nil, "the result set does not contain every primary key column."
	}
	return pkCols, vals, ""
}

// rowKeyVals picks the values of the named columns out of one page row.
func (m Model) rowKeyVals(cols []string, row int) ([]any, bool) {
	if row < 0 || row >= len(m.data.rows) {
		return nil, false
	}
	vals := make([]any, len(cols))
	for i, name := range cols {
		found := false
		for j, c := range m.data.cols {
			if c.Name == name && j < len(m.data.rows[row]) {
				vals[i] = m.data.rows[row][j]
				found = true
				break
			}
		}
		if !found {
			return nil, false
		}
	}
	return vals, true
}

// openEditModal builds the edit modal for the cell under the cursor.
// Editing an already-staged cell resumes from its staged value.
func (m *Model) openEditModal() tea.Cmd {
	pkCols, pkVals, problem := m.editIdentity(m.data.row)
	if problem != "" {
		m.modal = &confirmModal{title: "Editing disabled", body: problem, danger: true}
		return nil
	}
	if m.changes.DeleteStaged(m.data.database, m.data.table, pkVals) {
		return logCmd("-- edit skipped: the row is staged for deletion (u unstages it)")
	}
	if m.data.col >= len(m.data.cols) {
		return nil
	}
	colName := m.data.cols[m.data.col].Name
	old, _ := m.data.cell()

	change := db.CellChange{
		Database: m.data.database,
		Table:    m.data.table,
		PKCols:   pkCols,
		PKVals:   pkVals,
		Column:   colName,
		OldValue: old,
	}
	initial := old
	if staged, ok := m.changes.Lookup(change.Database, change.Table, pkVals, colName); ok {
		change.OldValue = staged.OldValue
		initial = staged.NewValue
	}

	nullable := true
	for _, c := range m.meta.cols {
		if c.Name == colName {
			nullable = c.Nullable
			break
		}
	}
	m.modal = newEditCellModal(change, initial, nullable, rowLabel(pkCols, pkVals))
	return nil
}

// rowLabel names the row being edited, e.g. "id=3" or "a=1, b=2".
func rowLabel(pkCols []string, pkVals []any) string {
	parts := make([]string, len(pkCols))
	for i, c := range pkCols {
		parts[i] = c + "=" + db.FormatValue(pkVals[i], nullText)
	}
	return strings.Join(parts, ", ")
}

// stageChange records a confirmed edit. Restoring a cell to its original
// value unstages it instead of staging a no-op UPDATE.
func (m *Model) stageChange(c db.CellChange) tea.Cmd {
	if valuesEqual(c.NewValue, c.OldValue) {
		if m.changes.Unstage(c.Database, c.Table, c.PKVals, c.Column) {
			return logCmd("-- unstage %s.%s (original value restored)", c.Table, c.Column)
		}
		return logCmd("-- not staged: %s.%s is unchanged", c.Table, c.Column)
	}
	m.changes.Stage(c)
	st := db.UpdateSQL(m.driver.Dialect(), c)
	return logCmd("-- stage: %s;  -- args %v", st.SQL, st.Args)
}

// unstageAtCursor is `u`: drop the staged change under the cursor. A
// phantom row unstages its whole INSERT, a row staged for deletion
// unstages the DELETE, and anything else unstages the cursor cell —
// the row-level operations come first because on those rows there is no
// cell change to remove anyway.
func (m *Model) unstageAtCursor() tea.Cmd {
	if !m.data.open() || m.tab.metadata() {
		return nil
	}
	if ins, ok := m.phantomAtCursor(); ok {
		m.changes.UnstageInsert(ins.Database, ins.Table, ins.ID)
		m.clampCursor()
		return logCmd("-- unstage insert into %s", ins.Table)
	}
	pkCols := m.pkColumns()
	if pkCols == nil {
		return logCmd("-- nothing staged for %s", m.data.table)
	}
	pkVals, ok := m.rowKeyVals(pkCols, m.data.row)
	if !ok {
		return nil
	}
	if m.changes.UnstageDelete(m.data.database, m.data.table, pkVals) {
		return logCmd("-- unstage delete of %s (%s)", m.data.table, rowLabel(pkCols, pkVals))
	}
	if m.data.col >= len(m.data.cols) {
		return nil
	}
	colName := m.data.cols[m.data.col].Name
	if m.changes.Unstage(m.data.database, m.data.table, pkVals, colName) {
		return logCmd("-- unstage %s.%s", m.data.table, colName)
	}
	return logCmd("-- no staged change under the cursor")
}

// pkColumns names the primary key of the open table. The metadata is
// the source of truth, but a changeset that already holds a change of
// the table knows them too — which is how the grid keeps highlighting
// staged rows after the metadata cache was dropped.
func (m Model) pkColumns() []string {
	if m.meta.loaded && m.meta.table == m.data.table {
		var cols []string
		for _, c := range m.meta.cols {
			if c.PrimaryKey {
				cols = append(cols, c.Name)
			}
		}
		if cols != nil {
			return cols
		}
	}
	return m.changes.PKColsFor(m.data.database, m.data.table)
}

// confirmDiscard is `U`: throw the whole changeset away, after asking.
func (m *Model) confirmDiscard() tea.Cmd {
	n := m.changes.Len()
	if n == 0 {
		return logCmd("-- no staged changes to discard")
	}
	m.modal = &confirmModal{
		title:  "Discard staged changes",
		body:   fmt.Sprintf("Discard all %s without executing anything?", countChanges(n)),
		danger: true,
		onConfirm: func(mm *Model) tea.Cmd {
			mm.changes.Clear()
			return logCmd("-- discard %s", countChanges(n))
		},
	}
	return nil
}

// openCommitModal is `c`: show the exact SQL the commit will run, then
// execute it all in one transaction on confirm.
func (m *Model) openCommitModal() tea.Cmd {
	if m.driver == nil {
		return nil
	}
	n := m.changes.Len()
	if n == 0 {
		return logCmd("-- no staged changes to commit")
	}
	stmts := m.changes.Statements(m.driver.Dialect())
	lines := make([]string, 0, len(stmts))
	for _, s := range stmts {
		lines = append(lines, fmt.Sprintf("%s;  -- args %v", s.SQL, s.Args))
	}
	m.modal = &confirmModal{
		title: "Commit " + countChanges(n),
		body: strings.Join(lines, "\n") +
			"\n\nAll statements run in one transaction: on error nothing is applied and the changeset is kept.",
		danger:    true,
		onConfirm: func(mm *Model) tea.Cmd { return commitChangesCmd(mm.driver, stmts) },
	}
	return nil
}

// countChanges spells "1 staged change" / "3 staged changes".
func countChanges(n int) string {
	if n == 1 {
		return "1 staged change"
	}
	return fmt.Sprintf("%d staged changes", n)
}

// valuesEqual compares two normalized cell values. The normalized types
// (nil, string, int64, float64, bool, time.Time) are all comparable.
func valuesEqual(a, b any) bool { return a == b }

// convertInput turns the modal's text back into a typed value, guided by
// the type the cell held before. Text that does not parse stays a
// string and the engine gets the final say on commit.
func convertInput(text string, old any) any {
	switch old.(type) {
	case int64:
		if v, err := strconv.ParseInt(text, 10, 64); err == nil {
			return v
		}
	case float64:
		if v, err := strconv.ParseFloat(text, 64); err == nil {
			return v
		}
	case bool:
		if v, err := strconv.ParseBool(text); err == nil {
			return v
		}
	case time.Time:
		if v, err := time.Parse(time.RFC3339, text); err == nil {
			return v
		}
	}
	return text
}

// ---------- edit modal ----------

// editCellModal is the `e` popup: a prefilled input plus a NULL toggle.
// Submitting stages the change; nothing here touches the database.
type editCellModal struct {
	change   db.CellChange
	rowLabel string
	input    textinput.Model
	null     bool
	nullable bool
}

func newEditCellModal(change db.CellChange, initial any, nullable bool, rowLabel string) *editCellModal {
	ti := textinput.New()
	ti.Placeholder = "value"
	if initial != nil {
		ti.SetValue(db.FormatValue(initial, ""))
	}
	ti.CursorEnd()
	ti.Focus()
	ti.SetWidth(40)
	return &editCellModal{
		change:   change,
		rowLabel: rowLabel,
		input:    ti,
		null:     initial == nil,
		nullable: nullable,
	}
}

func (e *editCellModal) update(msg tea.KeyPressMsg, m *Model) (bool, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return true, nil
	case "ctrl+n":
		e.null = !e.null
		return false, nil
	case "enter":
		c := e.change
		if e.null {
			c.NewValue = nil
		} else {
			c.NewValue = convertInput(e.input.Value(), c.OldValue)
		}
		return true, m.stageChange(c)
	}
	// Typing while NULL is toggled on means the user wants a value again.
	if msg.Text != "" {
		e.null = false
	}
	var cmd tea.Cmd
	e.input, cmd = e.input.Update(msg)
	return false, cmd
}

func (e *editCellModal) view(s styles, maxW, maxH int) string {
	e.input.SetWidth(min(50, maxW-8))
	title := fmt.Sprintf("Edit %s.%s — %s", e.change.Table, e.change.Column, e.rowLabel)

	value := e.input.View()
	if e.null {
		value = s.pending.Render(nullText) + s.muted.Render("  (ctrl+n for a value)")
	}
	lines := []string{
		s.modalTitle.Render(truncate(title, min(maxW-8, 70))),
		"",
		s.muted.Render("current: " + truncate(flatten(db.FormatValue(e.change.OldValue, nullText)), 50)),
		value,
	}
	if e.null && !e.nullable {
		lines = append(lines, s.danger.Render("column is NOT NULL — the commit will fail"))
	}
	lines = append(lines, "", s.muted.Render("enter stage · ctrl+n NULL · esc cancel"))
	return s.modal.Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
}
