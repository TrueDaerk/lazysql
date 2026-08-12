package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
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

// readOnlyBlocked is the status line every write entry point refuses
// with. The wording matches db.ErrReadOnly, so the message is the same
// whether the UI stopped the action or the driver session did.
func readOnlyBlocked(what string) tea.Cmd {
	return logCmd("-- %s blocked: %v", what, db.ErrReadOnly)
}

// startEdit is `e` on the Data tab. The primary key comes from the
// cached metadata; when no tab has fetched it yet the fetch is started
// and the modal opens when the reply lands, like `y` does for the DDL.
func (m *Model) startEdit() tea.Cmd {
	if !m.data.browsing() || m.tab.metadata() || m.driver == nil {
		return nil
	}
	if m.readOnly() {
		return readOnlyBlocked("edit")
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

	col := db.Column{Name: colName, Nullable: true}
	for _, c := range m.meta.cols {
		if c.Name == colName {
			col = c
			break
		}
	}
	// With a multi-row selection up, this one modal stages the same value
	// in every selected row. The cursor row stays first, so the modal's
	// prefill, its "current" line and the type conversion are the ones the
	// user is looking at.
	targets, skipped := m.bulkTargets(change)
	label := rowLabel(pkCols, pkVals)
	if len(targets) > 1 {
		label = countRows(len(targets)) + " selected"
	}
	var cmd tea.Cmd
	if skipped > 0 {
		cmd = logCmd("-- bulk edit: %s left out (staged for deletion, or no primary key value)",
			countRows(skipped))
	}
	// A temporal column gets the calendar instead of a bare text field.
	// `e` inside it comes back to exactly the modal built below, so the
	// raw path — NULL, now(), CURRENT_TIMESTAMP — is never more than one
	// key away.
	if kind := db.ClassifyType(col.DataType); kind.Temporal() {
		m.modal = m.newEditDatePicker(targets, initial, col, kind, label)
		return cmd
	}
	m.modal = newEditCellModal(targets, initial, col, label)
	return cmd
}

// bulkTargets is the set of rows one edit stages into: the cursor row
// alone normally, and every row of the multi-row selection while it is
// up. A row already staged for deletion, and one whose primary key the
// result set does not carry, drop out and are only counted: lazysql does
// not guess which row an UPDATE hits, and that rule does not get weaker
// because several rows are selected at once. (A phantom row cannot be in
// the selection at all — dataView.selectionRange stops at the last
// fetched row.)
func (m Model) bulkTargets(cursor db.CellChange) (targets []db.CellChange, skipped int) {
	targets = []db.CellChange{cursor}
	if !m.data.selecting() {
		return targets, 0
	}
	for _, r := range m.data.selectedRows() {
		if r == m.data.row {
			continue
		}
		pkVals, ok := m.rowKeyVals(cursor.PKCols, r)
		if !ok || m.changes.DeleteStaged(cursor.Database, cursor.Table, pkVals) {
			skipped++
			continue
		}
		c := cursor
		c.PKVals = pkVals
		c.OldValue = nil
		if m.data.col < len(m.data.rows[r]) {
			c.OldValue = m.data.rows[r][m.data.col]
		}
		// An already-staged cell keeps the value the database holds as its
		// OldValue, so restoring it still unstages rather than staging a
		// second edit on top of the first.
		if staged, ok := m.changes.Lookup(c.Database, c.Table, pkVals, c.Column); ok {
			c.OldValue = staged.OldValue
		}
		targets = append(targets, c)
	}
	return targets, skipped
}

// countRows spells "1 row" / "3 rows".
func countRows(n int) string {
	if n == 1 {
		return "1 row"
	}
	return fmt.Sprintf("%d rows", n)
}

// newEditDatePicker builds the picker for one cell edit. Confirming stages
// the ISO-formatted value through the same stageValue path a typed value
// takes; `e` swaps in the plain text modal, prefilled the same way.
func (m *Model) newEditDatePicker(targets []db.CellChange, initial any, col db.Column, kind db.TypeKind, label string) *datePickerModal {
	change := targets[0]
	p := newDatePickerModal(
		fmt.Sprintf("Edit %s.%s — %s", change.Table, change.Column, label),
		col, kind, pickerStart(initial, kind), m.keys)
	p.current = db.FormatValue(change.OldValue, nullText)
	p.onPick = func(mm *Model, value string) tea.Cmd {
		return mm.stageValue(targets, convertInput(value, change.OldValue))
	}
	p.onRaw = func(mm *Model) tea.Cmd {
		mm.modal = newEditCellModal(targets, initial, col, label)
		return nil
	}
	return p
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
	// This previews the single-cell edit just staged, not the merged
	// statement it may end up part of — Changeset.Statements groups it
	// with any other staged edits of the same row only at commit time.
	st := db.UpdateSQL(m.driver.Dialect(), c)
	return logCmd("-- stage: %s;  -- args %v", st.SQL, st.Args)
}

// stageValue records one confirmed edit against every row it targets.
// A single target is the ordinary `e` and takes the single-cell path
// unchanged; several are a bulk edit, which stages the same value in the
// same column of each selected row. Nothing is executed here either —
// the rows land in the changeset like any other staged edit and only the
// commit runs SQL, one parameterized statement per row.
func (m *Model) stageValue(targets []db.CellChange, value any) tea.Cmd {
	if len(targets) == 1 {
		c := targets[0]
		c.NewValue = value
		return m.stageChange(c)
	}
	var first db.CellChange
	staged, unstaged := 0, 0
	for _, c := range targets {
		c.NewValue = value
		// Restoring a row's original value unstages it instead of staging
		// a no-op UPDATE, exactly as it does for a single cell — so a bulk
		// edit back to the old value cleans up after itself.
		if valuesEqual(c.NewValue, c.OldValue) {
			if m.changes.Unstage(c.Database, c.Table, c.PKVals, c.Column) {
				unstaged++
			}
			continue
		}
		if staged == 0 {
			first = c
		}
		m.changes.Stage(c)
		staged++
	}
	// The selection has done its job; leaving it up would aim the next
	// edit at rows the user has stopped thinking about, the way vim
	// leaves visual mode after an operator.
	m.clearSelection()

	var cmds []tea.Cmd
	if staged > 0 {
		// The preview is the first staged row's statement; the others
		// differ only in their key values, and the commit merges each
		// row's edits into one UPDATE anyway.
		st := db.UpdateSQL(m.driver.Dialect(), first)
		line := fmt.Sprintf("-- stage: %s;  -- args %v", st.SQL, st.Args)
		if staged > 1 {
			line += fmt.Sprintf("  (and %d more rows, same column and value)", staged-1)
		}
		cmds = append(cmds, logCmd("%s", line))
	}
	if unstaged > 0 {
		cmds = append(cmds, logCmd("-- unstage %s (original value restored)", countRows(unstaged)))
	}
	if len(cmds) == 0 {
		return logCmd("-- not staged: every selected row already holds that value")
	}
	return tea.Batch(cmds...)
}

// unstageAtCursor is `u`: drop the staged change under the cursor. A
// phantom row unstages its whole INSERT, a row staged for deletion
// unstages the DELETE, and anything else unstages the cursor cell —
// the row-level operations come first because on those rows there is no
// cell change to remove anyway.
func (m *Model) unstageAtCursor() tea.Cmd {
	if !m.data.browsing() || m.tab.metadata() {
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
			// The phantom rows went with the changeset; the cursor may
			// have been standing on one of them.
			mm.clampCursor()
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
	// Nothing can normally be staged on a read-only connection, but a
	// changeset staged before the connection changed must not find a way
	// out either.
	if m.readOnly() {
		return readOnlyBlocked("commit")
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
			"\n\nAll statements run in one transaction: on error nothing is applied and the changeset is kept." +
			"\n\nConnection: " + m.taggedConnName(m.active),
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
	switch o := old.(type) {
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
		// Parsed in the old value's zone: the picker and the text field
		// both spell a wall-clock time with no offset, and re-reading it as
		// UTC would silently shift a value the user never touched.
		if v, ok := db.ParseDateTimeIn(text, o.Location()); ok {
			return v
		}
	}
	return text
}

// ---------- edit modal ----------

// editCellModal is the `e` popup: a prefilled input plus a NULL toggle.
// Submitting stages the change; nothing here touches the database.
//
// targets is what the confirmed value is staged into: one cell for a
// plain edit, one per selected row for a bulk edit. targets[0] is always
// the cursor row, which is what the modal shows and converts against.
type editCellModal struct {
	targets  []db.CellChange
	col      db.Column
	rowLabel string
	input    textinput.Model
	null     bool
}

func newEditCellModal(targets []db.CellChange, initial any, col db.Column, rowLabel string) *editCellModal {
	ti := textinput.New()
	ti.Placeholder = "value"
	if initial != nil {
		ti.SetValue(db.FormatValue(initial, ""))
	}
	ti.CursorEnd()
	ti.Focus()
	ti.SetWidth(40)
	return &editCellModal{
		targets:  targets,
		col:      col,
		rowLabel: rowLabel,
		input:    ti,
		null:     initial == nil,
	}
}

// change is the cell the modal describes: the cursor row's, whether or
// not more rows ride along with it.
func (e *editCellModal) change() db.CellChange { return e.targets[0] }

func (e *editCellModal) update(msg tea.KeyPressMsg, m *Model) (bool, tea.Cmd) {
	switch {
	case msg.String() == "esc":
		return true, nil
	case msg.String() == "ctrl+n":
		e.null = !e.null
		return false, nil
	// The way back into the calendar after `e` dropped out of it. Only
	// offered for a column that has one — everywhere else ctrl+t is a
	// no-op key the footer never mentions.
	case key.Matches(msg, m.keys.OpenPicker) && db.ClassifyType(e.col.DataType).Temporal():
		kind := db.ClassifyType(e.col.DataType)
		var initial any = e.input.Value()
		if e.null {
			initial = nil
		}
		m.modal = m.newEditDatePicker(e.targets, initial, e.col, kind, e.rowLabel)
		return true, nil
	case msg.String() == "enter", key.Matches(msg, m.keys.AcceptChanges):
		var value any
		if !e.null {
			value = convertInput(e.input.Value(), e.change().OldValue)
		}
		return true, m.stageValue(e.targets, value)
	}
	// Typing while NULL is toggled on means the user wants a value again.
	if msg.Text != "" {
		e.null = false
	}
	var cmd tea.Cmd
	e.input, cmd = e.input.Update(msg)
	return false, cmd
}

// paste puts pasted text in the value input. Like typing, it means the
// user wants a value rather than the NULL the toggle may be showing.
func (e *editCellModal) paste(msg tea.PasteMsg, _ *Model) tea.Cmd {
	e.null = false
	var cmd tea.Cmd
	e.input, cmd = e.input.Update(msg)
	return cmd
}

func (e *editCellModal) view(s styles, maxW, maxH int) string {
	e.input.SetWidth(min(50, maxW-8))
	change := e.change()
	title := fmt.Sprintf("Edit %s.%s — %s", change.Table, change.Column, e.rowLabel)

	typeLine := strings.ToLower(e.col.DataType)
	if !e.col.Nullable {
		typeLine += " · NOT NULL"
	}

	value := e.input.View()
	if e.null {
		value = s.pending.Render(nullText) + s.muted.Render("  (ctrl+n for a value)")
	}
	lines := []string{
		s.modalTitle.Render(truncate(title, min(maxW-8, 70))),
		s.muted.Render(truncate(typeLine, min(maxW-8, 70))),
		"",
		s.muted.Render("current: " + truncate(flatten(db.FormatValue(change.OldValue, nullText)), 50)),
		value,
	}
	if n := len(e.targets); n > 1 {
		// "current" above is the cursor row's value; the other selected
		// rows keep theirs until this one value replaces them all.
		lines = append(lines, s.pending.Render(
			fmt.Sprintf("bulk edit — stages %s in all %s", change.Column, countRows(n))))
	}
	if e.null && !e.col.Nullable {
		lines = append(lines, s.danger.Render("column is NOT NULL — the commit will fail"))
	}
	footer := "enter/ctrl+enter stage · ctrl+n NULL · esc cancel"
	if db.ClassifyType(e.col.DataType).Temporal() {
		footer = "enter/ctrl+enter stage · ctrl+n NULL · ctrl+t picker · esc cancel"
	}
	lines = append(lines, "", s.muted.Render(footer))
	return s.modal.Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
}
