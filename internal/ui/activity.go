package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"lazysql/internal/db"
	"lazysql/internal/export"
)

// `A` on panel [1] asks the server what it is doing: one row per
// session, longest-running first, with the sessions that are waiting on
// a lock marked with the ID of whoever holds it. The report opens *in*
// the main view and takes the focus there, the way a trigger definition
// does — not as an overlay on a focused panel [1], which is what the
// schema diff still is. See wiki/design/server-activity-focus.md.
//
// Five rules shape it:
//
//   - The report is focused where it is drawn. Its keys act only while
//     the main view has the focus; `1` or `tab` hands the keyboard back
//     to a side panel, which then behaves exactly as it always does
//     while the list stays readable beside it.
//
//   - It is the data grid, read-only. The list is an roGrid, so the cell
//     cursor, the sliding column window, the multi-row and block
//     selection and the copy scopes are the grid's own code rather than
//     a second implementation — and everything that would stage or apply
//     a change is absent from its key table rather than inert. See
//     wiki/design/read-only-grid.md.
//
//   - Reading is free, killing is not. The listing is a plain SELECT and
//     runs on read-only connections too; `K` never executes anything
//     without a confirm modal in between, and a read-only session refuses
//     it in the driver as well as in the UI.
//   - Nothing is refreshed behind the user's back. Auto-refresh is off
//     until `t` turns it on, because every refresh is a real statement in
//     the command log and a view left open would otherwise fill it.
//   - The list is a snapshot, not a live cursor. Rows are re-read whole
//     and re-sorted by db.SortProcesses, whose tie-break keeps equal rows
//     in a stable order so the cursor does not jump between refreshes.

const (
	// activityTimeout bounds one process-list read. It is a catalog query
	// against an in-memory view: one that has not answered by then is a
	// stuck connection, not a slow list.
	activityTimeout = 20 * time.Second
	// killTimeout bounds one kill. The server either signals the backend
	// or it does not; there is nothing slow about it.
	killTimeout = 20 * time.Second
	// activityInterval is how often auto-refresh re-reads the list. It is
	// named in the footer, so the user can see what `t` turned on.
	activityInterval = 5 * time.Second
)

// activityView is the server activity report on screen: the request in
// flight or what it produced. Model.activity is nil when none is open.
type activityView struct {
	// id distinguishes requests, so a stale reply cannot fill a view the
	// user has already dismissed or refreshed past.
	id int
	// conn and engine name what the list belongs to; a reply for another
	// connection is dropped.
	conn   string
	engine string

	loading bool
	// auto is the periodic refresh, off until `t`. gen invalidates the
	// ticks of a schedule that has been turned off or restarted, the way
	// wheelState.gen invalidates a stale wheel flush.
	auto bool
	gen  int

	rows []db.Process
	err  string
	// unsupported marks an engine that has no server to ask — SQLite and
	// DuckDB run in this process, so there is no session but ours.
	unsupported bool
	// at is when the list on screen landed, shown in the footer so a
	// stale list cannot be mistaken for a live one.
	at time.Time

	// grid is the list on screen: the cell cursor `K` and the copy scopes
	// read, its scroll windows and its selection. rows and grid always
	// describe the same list — setRows is the only thing that replaces
	// either.
	grid roGrid
}

// selected is the process under the cursor.
func (v *activityView) selected() (db.Process, bool) {
	if v == nil || v.grid.row < 0 || v.grid.row >= len(v.rows) {
		return db.Process{}, false
	}
	return v.rows[v.grid.row], true
}

// setRows lands a freshly read list. The cursor and the selection are
// re-anchored by session ID rather than kept by index: an auto-refresh
// re-reads the list whole, and a session that ended above the cursor
// would otherwise slide a different one under it — with `K` pointing at
// it. A session that is no longer listed cannot be re-anchored: the
// cursor then stays at the index it had (clamped into the new list), and
// a selection whose anchor or whose cursor row is gone is dropped rather
// than silently re-cut over different sessions.
func (v *activityView) setRows(rows []db.Process) {
	cursorID, anchorID := "", ""
	if p, ok := v.selected(); ok {
		cursorID = p.ID
	}
	if a := v.grid.sel.anchor; v.grid.sel.active && a >= 0 && a < len(v.rows) {
		anchorID = v.rows[a].ID
	}

	v.rows = rows
	cells := make([][]string, len(rows))
	kinds := make([]rowKind, len(rows))
	for i, p := range rows {
		cells[i] = activityCells(p)
		switch {
		case p.Blocked():
			kinds[i] = rowAlert
		case p.Self:
			kinds[i] = rowFaded
		}
	}
	v.grid.setCells(activityHeaders, cells, kinds)

	cursorAt, cursorOK := indexOfProcess(rows, cursorID)
	if cursorOK {
		v.grid.row = cursorAt
	}
	if v.grid.sel.active {
		anchorAt, anchorOK := indexOfProcess(rows, anchorID)
		if anchorOK && cursorOK {
			v.grid.sel.anchor = anchorAt
		} else {
			v.grid.clearSelection()
		}
	}
	v.grid.clampCursor()
}

// indexOfProcess finds a session by ID. An empty ID matches nothing, so
// a caller that had no cursor row to remember gets the same answer as one
// whose session has ended.
func indexOfProcess(rows []db.Process, id string) (int, bool) {
	if id == "" {
		return 0, false
	}
	for i, p := range rows {
		if p.ID == id {
			return i, true
		}
	}
	return 0, false
}

// blockedCount is how many of the listed sessions are waiting on another
// one — the number the footer leads with, since it is the reason to open
// the view at all.
func (v *activityView) blockedCount() int {
	n := 0
	for _, p := range v.rows {
		if p.Blocked() {
			n++
		}
	}
	return n
}

// ---------- messages ----------

type activityLoadedMsg struct {
	id   int
	conn string
	rows []db.Process
	err  error
}

type activityKilledMsg struct {
	conn string
	pid  string
	err  error
}

// activityTickMsg is one beat of the auto-refresh. gen drops the beats of
// a schedule that was turned off.
type activityTickMsg struct{ gen int }

// ---------- the flow ----------

// openActivity is `A` on panel [1]: open the report and read the list.
// Pressing it again on an open report refreshes it rather than throwing
// the cursor away.
func (m *Model) openActivity() tea.Cmd {
	if m.driver == nil {
		return logCmd("-- server activity skipped: not connected")
	}
	if m.activity != nil && m.activity.conn == m.active {
		// A second `A` from the panel refreshes the list it left open and
		// takes the keyboard back to it.
		m.setFocus(panelMain)
		return m.refreshActivity()
	}
	m.activity = &activityView{
		conn:   m.active,
		engine: m.driver.Dialect().DisplayName(),
	}
	// The report owns the main view: whatever else was drawn there — the
	// schema diff of panel [1], a trigger definition — is put away rather
	// than stacked behind it, and the focus follows it into the box.
	m.diff = nil
	m.trigger = nil
	m.setFocus(panelMain)
	return tea.Batch(
		logCmd("-- server activity on %s…", m.active),
		m.refreshActivity(),
	)
}

// refreshActivity starts one read. It is `r`, the auto-refresh beat and
// what a finished kill runs, so a refreshed list always comes from the
// same place.
func (m *Model) refreshActivity() tea.Cmd {
	v := m.activity
	if v == nil {
		return nil
	}
	if m.driver == nil {
		v.loading = false
		v.err = "not connected"
		return logCmd("-- server activity skipped: not connected")
	}
	v.id++
	v.loading = true
	return listProcessesCmd(v.id, m.active, m.driver)
}

// listProcessesCmd reads the process list. The statement itself lands in
// the command log through the Driver's Logger, like every other statement
// lazysql runs.
func listProcessesCmd(id int, conn string, drv db.Driver) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), activityTimeout)
		defer cancel()
		rows, err := drv.ListProcesses(ctx)
		return activityLoadedMsg{id: id, conn: conn, rows: rows, err: err}
	}
}

// applyActivity lands the list (or the failure) in the view.
func (m *Model) applyActivity(msg activityLoadedMsg) tea.Cmd {
	v := m.activity
	if v == nil || msg.id != v.id || msg.conn != m.active {
		return nil
	}
	v.loading = false
	v.at = time.Now()
	if msg.err != nil {
		// An engine without a server is not a failure to report: the view
		// says so and stops asking.
		if errors.Is(msg.err, db.ErrUnsupported) {
			v.unsupported, v.err = true, ""
			m.setActivityRows(nil)
			v.auto = false
			v.gen++
			return logCmd("-- server activity: %s has no server sessions to list", v.engine)
		}
		v.err = msg.err.Error()
		return logCmd("-- server activity FAILED: %v", msg.err)
	}
	v.unsupported, v.err = false, ""
	m.setActivityRows(msg.rows)
	return nil
}

// setActivityRows lands a list in the view and keeps the `ctrl+c` binding
// in step with the selection that survived it — the binding is the single
// source the options bar and `?` read, so enabling it is what shows the
// key and disabling it is what gives `ctrl+c` back to quit.
func (m *Model) setActivityRows(rows []db.Process) {
	v := m.activity
	if v == nil {
		return
	}
	v.setRows(rows)
	m.syncCopySelectionKey()
}

// syncCopySelectionKey enables `ctrl+c` exactly while the grid that owns
// the main view holds a selection, and disables it otherwise so the key
// means quit again the moment the selection is gone. The report's grid
// wins whenever it is open, because it is what the box is showing.
func (m *Model) syncCopySelectionKey() {
	sel := len(m.data.selectedRows()) > 0
	if m.activity != nil {
		sel = m.activity.grid.selecting()
	}
	m.keys.CopySelection.SetEnabled(sel)
}

// toggleActivityAuto is `t`: the periodic refresh on or off. Turning it
// on refreshes immediately, so the key has a visible effect before the
// first beat.
func (m *Model) toggleActivityAuto() tea.Cmd {
	v := m.activity
	if v == nil {
		return nil
	}
	v.auto = !v.auto
	// Every toggle invalidates the beats already in flight, so turning it
	// off and on again does not double the rate.
	v.gen++
	if !v.auto {
		return logCmd("-- server activity: auto-refresh off")
	}
	return tea.Batch(
		logCmd("-- server activity: auto-refresh every %s", activityInterval),
		m.refreshActivity(),
		activityTickCmd(v.gen),
	)
}

func activityTickCmd(gen int) tea.Cmd {
	return tea.Tick(activityInterval, func(time.Time) tea.Msg {
		return activityTickMsg{gen: gen}
	})
}

// activityTick is one auto-refresh beat: re-read and re-arm. A beat from
// a schedule that has been turned off — or that outlived the view or the
// connection — is dropped rather than chained, which is what stops the
// timer without a separate "stop" message.
func (m *Model) activityTick(msg activityTickMsg) tea.Cmd {
	v := m.activity
	if v == nil || !v.auto || msg.gen != v.gen || m.driver == nil {
		return nil
	}
	// A read that is still out is not chased with another one: the beat
	// only re-arms the timer.
	if v.loading {
		return activityTickCmd(v.gen)
	}
	return tea.Batch(m.refreshActivity(), activityTickCmd(v.gen))
}

// activityFocused reports whether the report owns the keyboard: it is
// open and the main view — where it is drawn — has the focus. Only then
// do its keys, its options bar and its `?` group apply.
func (m Model) activityFocused() bool {
	return m.activity != nil && m.focus == panelMain
}

// activityOwnsMain reports whether the report is what the main view
// draws. It keeps the box while a side panel is focused, so the list
// stays readable while panel [1] is operated — but panel [3] edits in
// the main view, so the editor wins there.
func (m Model) activityOwnsMain() bool {
	return m.activity != nil && m.focus != panelQuery
}

// activityHelpGroups is the `?` listing while the report has the focus:
// its own keys where the grid's would be, plus the navigation and global
// sets every panel lists. The same slice is documented under panel [1]
// as well — `A` opens the report from there, and a key is worth finding
// from the panel that puts it on screen.
func (m Model) activityHelpGroups() []helpGroup {
	k := m.keys
	return []helpGroup{
		{"", k.serverActivity()},
		{"", k.navigationFor(panelMain)},
		{"", k.global()},
	}
}

// closeActivity is `esc` on the report.
func (m *Model) closeActivity() {
	if m.activity != nil {
		// The beats of an auto-refresh outlive the view they were armed
		// for; bumping the generation is what makes them no-ops.
		m.activity.gen++
	}
	m.activity = nil
	// The report took `ctrl+c` over from the grid while it was open; with
	// it gone the key belongs to whatever the grid's own selection says.
	m.syncCopySelectionKey()
}

// ---------- kill ----------

// killSelectedProcess is `K`: end the session under the cursor. Nothing
// is executed here — the statement runs only from the confirm modal's
// onConfirm, which is the one place a kill can come from.
func (m *Model) killSelectedProcess() tea.Cmd {
	v := m.activity
	if v == nil {
		return nil
	}
	if m.driver == nil {
		return logCmd("-- kill session skipped: not connected")
	}
	p, ok := v.selected()
	if !ok {
		return logCmd("-- kill session skipped: no session under the cursor")
	}
	if m.readOnly() {
		return logCmd("-- kill session %s skipped: %v", p.ID, db.ErrReadOnly)
	}
	// Killing our own backend would drop the connection the list is read
	// through, which is a way to lose staged changes, not a way to free a
	// lock.
	if p.Self {
		return logCmd("-- kill session %s skipped: that is lazysql's own session", p.ID)
	}
	stmt, err := db.KillProcessSQL(m.driver.Dialect(), p.ID)
	if err != nil {
		return logCmd("-- kill session %s FAILED: %v", p.ID, err)
	}
	conn, drv := m.active, m.driver
	m.modal = &confirmModal{
		title:  "Kill session " + p.ID,
		body:   m.killConfirmBody(p, stmt.SQL),
		danger: true,
		onConfirm: func(mm *Model) tea.Cmd {
			return tea.Batch(
				logCmd("-- kill session %s on %s…", p.ID, conn),
				killProcessCmd(conn, drv, p.ID),
			)
		},
	}
	return nil
}

// killConfirmBody spells out what is about to end: which session, on
// which connection, running what — and the exact statement, so the
// confirm is not a leap of faith.
func (m Model) killConfirmBody(p db.Process, sql string) string {
	lines := []string{fmt.Sprintf("Kill session %s on %s%s?",
		p.ID, m.tagMarkerFor(m.active), m.active)}
	who := p.User
	if p.Database != "" {
		who += " on " + p.Database
	}
	if strings.TrimSpace(who) != "" {
		lines = append(lines, "", "user: "+who)
	}
	// The client address is a column of the table now, but it is easy to
	// have scrolled past and this is the moment it matters most: it says
	// which machine is about to lose its connection.
	if p.Client != "" {
		lines = append(lines, "client: "+p.Client)
	}
	if p.HasDuration {
		lines = append(lines, "running for: "+formatProcessDuration(p))
	}
	if q := flatten(p.Query); q != "" {
		lines = append(lines, "statement: "+truncate(q, 120))
	}
	lines = append(lines, "",
		"This ends the session immediately; its open transaction rolls back.",
		"", sql)
	return strings.Join(lines, "\n")
}

func killProcessCmd(conn string, drv db.Driver, pid string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), killTimeout)
		defer cancel()
		return activityKilledMsg{conn: conn, pid: pid, err: drv.KillProcess(ctx, pid)}
	}
}

// finishKill reports the outcome and re-reads the list, so the row that
// was killed is gone (or visibly still there) without a second key press.
func (m *Model) finishKill(msg activityKilledMsg) tea.Cmd {
	if msg.err != nil {
		return logCmd("-- kill session %s FAILED: %v", msg.pid, msg.err)
	}
	cmds := []tea.Cmd{logCmd("-- kill session %s: terminated", msg.pid)}
	if m.activity != nil && msg.conn == m.active {
		cmds = append(cmds, m.refreshActivity())
	}
	return tea.Batch(cmds...)
}

// ---------- keys ----------

// updateActivityKeys owns the keyboard while the report is focused in
// the main view. Unlike the schema diff's handler there is no panel
// behind it to fall through to: updateData hands it every key the
// globals did not claim, and a key it has no meaning for is a no-op
// rather than an action on the grid the report is covering.
func (m Model) updateActivityKeys(msg tea.KeyPressMsg) (Model, tea.Cmd, bool) {
	v := m.activity
	k := m.keys
	switch {
	case key.Matches(msg, k.Back):
		// esc leaves selection mode before anything else, exactly as it
		// does in the grid: while a selection is up it is the mode the
		// user is in, and leaving it must not also close the report.
		if v.grid.selecting() {
			m.clearActivitySelection()
			return m, logCmd("-- selection cleared"), true
		}
		m.closeActivity()
		// The report was the main view's content; with it gone the box has
		// nothing of its own to show, so the focus goes back where `A` came
		// from rather than sitting on an empty grid.
		if m.focus == panelMain && !m.data.open() && m.trigger == nil {
			m.focusBack()
		}
		return m, nil, true
	case key.Matches(msg, k.Down):
		v.grid.row++
		v.grid.clampCursor()
		return m, nil, true
	case key.Matches(msg, k.Up):
		v.grid.row--
		v.grid.clampCursor()
		return m, nil, true
	}
	// Everything else the report binds goes through its own action table,
	// so a key press, the options bar and `?` cannot describe different
	// sets. Order matters: `K` is the kill, and activityActions lists it
	// ahead of anything else that could match.
	for _, a := range k.activityActions() {
		if key.Matches(msg, a.binding) {
			mm, cmd := m.runAction(a.id)
			return mm, cmd, true
		}
	}
	switch msg.String() {
	case "g", "home":
		v.grid.row = 0
		return m, nil, true
	case "G", "end":
		v.grid.row = len(v.rows) - 1
		v.grid.clampCursor()
		return m, nil, true
	}
	return m, nil, false
}

// activityDispatch is the report's half of runAction: the grid actions it
// shares with the main view, answered against its own read-only grid
// instead of the Data tab's page. It runs ahead of dataActions whenever
// the report has the focus, and reports handled=false for everything it
// does not claim — the kill, the auto-refresh toggle and the copy scopes
// are dispatched elsewhere.
func (m Model) activityDispatch(id actionID) (Model, tea.Cmd, bool) {
	v := m.activity
	switch id {
	case actRefresh:
		// `R` means "read it again from the server" here too — but from
		// the report's own read, not the page of a relation the main view
		// is not currently showing.
		return m, m.refreshActivity(), true
	case actColLeft:
		v.grid.col--
		v.grid.clampCursor()
	case actColRight:
		v.grid.col++
		v.grid.clampCursor()
	case actNextPage:
		v.grid.row += activityPageStep
		v.grid.clampCursor()
	case actPrevPage:
		v.grid.row -= activityPageStep
		v.grid.clampCursor()
	case actSelectRows:
		return m, m.toggleActivitySelection(), true
	case actSelectColumns:
		return m, m.toggleActivityColumnSelection(), true
	case actExtendSelectionUp:
		return m, m.extendActivitySelection(-1), true
	case actExtendSelectionDown:
		return m, m.extendActivitySelection(1), true
	case actExtendSelectionLeft:
		return m, m.extendActivityColumnSelection(-1), true
	case actExtendSelectionRight:
		return m, m.extendActivityColumnSelection(1), true
	case actViewCell:
		// The grid's "show me this value in full" key: a server-truncated
		// statement is exactly the cell the list cannot show whole.
		if p, ok := v.selected(); ok {
			name := v.grid.header(v.grid.col)
			text, _ := v.grid.cellAt(v.grid.row, v.grid.col)
			m.modal = newCellModal("session "+p.ID, name, "", text)
		}
	case actRowDetail:
		if p, ok := v.selected(); ok {
			m.modal = newProcessDetailModal(p)
		}
	default:
		return m, nil, false
	}
	return m, nil, true
}

// activityPageStep is how far ctrl+f/ctrl+b jump. The report is one list
// rather than a paged relation, so its page keys move the cursor a screen
// at a time instead of turning anything.
const activityPageStep = 10

// ---------- selection ----------

// toggleActivitySelection is `ctrl+v`: it starts a selection anchored at
// the cursor row, or ends the one that is running.
func (m *Model) toggleActivitySelection() tea.Cmd {
	v := m.activity
	if v.grid.selecting() {
		m.clearActivitySelection()
		return logCmd("-- selection cleared")
	}
	if !v.grid.startSelection() {
		return logCmd("-- selection skipped: no session under the cursor")
	}
	m.syncCopySelectionKey()
	p, _ := v.selected()
	return logCmd(
		"-- selection started at session %s (j/k extend, shift+←/→ narrows it to columns, ctrl+c copies, esc clears)",
		p.ID)
}

// extendActivitySelection is `shift+up`/`shift+down`: it anchors a
// selection at the cursor row if none is up and then moves the cursor,
// which is the selection's other edge.
func (m *Model) extendActivitySelection(delta int) tea.Cmd {
	v := m.activity
	if !v.grid.startSelection() {
		return nil
	}
	m.syncCopySelectionKey()
	v.grid.row += delta
	v.grid.clampCursor()
	return nil
}

// extendActivityColumnSelection is `shift+left`/`shift+right`, the
// sideways half of the same gesture: it anchors the column span at the
// cursor column the first time it runs and then moves the cell cursor.
func (m *Model) extendActivityColumnSelection(delta int) tea.Cmd {
	v := m.activity
	if !v.grid.startSelection() {
		return nil
	}
	m.syncCopySelectionKey()
	if !v.grid.sel.cols {
		v.grid.sel.cols = true
		v.grid.sel.colAnchor = v.grid.col
	}
	v.grid.col += delta
	v.grid.clampCursor()
	return nil
}

// toggleActivityColumnSelection is `C`: it anchors the column span at the
// cursor column without moving anything, or drops it again — the way in
// for terminals that never report shift+arrows, since plain `h`/`l` then
// move the span's open edge.
func (m *Model) toggleActivityColumnSelection() tea.Cmd {
	v := m.activity
	if v.grid.selecting() && v.grid.sel.cols {
		v.grid.sel.cols = false
		return logCmd("-- column selection cleared (the sessions stay selected)")
	}
	if !v.grid.startSelection() {
		return logCmd("-- column selection skipped: no session under the cursor")
	}
	m.syncCopySelectionKey()
	v.grid.sel.cols = true
	v.grid.sel.colAnchor = v.grid.col
	return logCmd("-- column selection anchored at %s (h/l extend, C clears it)",
		v.grid.header(v.grid.col))
}

func (m *Model) clearActivitySelection() {
	m.activity.grid.clearSelection()
	m.syncCopySelectionKey()
}

// scrollActivity is the wheel: the report has a cursor rather than a
// scroll offset, so the wheel walks rows the way j/k does.
func (m *Model) scrollActivity(delta int) {
	if m.activity == nil {
		return
	}
	m.activity.grid.row += delta
	m.activity.grid.clampCursor()
}

// clickActivity moves the cell cursor onto the clicked cell. row and col
// are content coordinates of the main view box, mapped back through the
// windows the last frame settled — the grid's own click hit test, so what
// is drawn and what a click selects cannot drift apart.
func (m *Model) clickActivity(row, col int) {
	v := m.activity
	if v == nil || len(v.rows) == 0 {
		return
	}
	v.grid.clickRow(row, col)
}

// ---------- rendering ----------

// activityHeaders are the report's columns. Query is last because it is
// the widest and the least often compared across rows; Client is a column
// of its own now that the grid scrolls sideways, where it used to only
// reach the kill confirmation.
var activityHeaders = []string{
	"PID", "User", "Database", "Client", "State", "Duration", "Blocked by", "Query"}

// activityCells renders one process as the report's columns. A column the
// engine reported nothing for shows a dash rather than an empty cell, so
// an unreported value and an empty string do not look the same — that is
// a rendering choice, and activityValues is what a copy carries instead.
func activityCells(p db.Process) []string {
	return []string{
		p.ID,
		orNone(p.User),
		orNone(p.Database),
		orNone(p.Client),
		orNone(p.State),
		formatProcessDuration(p),
		orDash(p.BlockedByText()),
		orNone(flatten(p.Query)),
	}
}

// activityValues is one process as a copy carries it: the same columns,
// but the values themselves. The dash of an unreported column copies as
// an empty string — it is a placeholder in the table, not something the
// server said — and the statement copies unflattened, because a copied
// query is meant to be read and run, not to fit on one line.
func activityValues(p db.Process) []any {
	return []any{
		p.ID,
		p.User,
		p.Database,
		p.Client,
		p.State,
		formatProcessDuration(p),
		p.BlockedByText(),
		p.Query,
	}
}

// activityColumns are the report's columns as the copy scopes name them:
// the header row, with no declared types behind it — the server hands
// these out as text.
func activityColumns() []db.Column {
	cols := make([]db.Column, len(activityHeaders))
	for i, h := range activityHeaders {
		cols[i] = db.Column{Name: h}
	}
	return cols
}

// orNone is the placeholder for a column the engine reported nothing
// for. It is a dash rather than an empty cell so an unreported value and
// an empty string do not look the same.
func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

// formatProcessDuration renders how long a session has been working, at
// the precision the number is worth: sub-second runtimes to a tenth,
// minutes and hours in the units people say them in.
func formatProcessDuration(p db.Process) string {
	if !p.HasDuration {
		return "—"
	}
	d := p.Duration
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Hour:
		return fmt.Sprintf("%dm %02ds", int(d/time.Minute), int((d%time.Minute)/time.Second))
	default:
		return fmt.Sprintf("%dh %02dm", int(d/time.Hour), int((d%time.Hour)/time.Minute))
	}
}

// activityTitle is the main view's border title while the report is
// open. The name follows the box: green while the report has the focus,
// muted while a side panel does and the list is only on display.
func (m Model) activityTitle() string {
	v := m.activity
	s := m.style
	name := s.title
	if m.mainFocused() {
		name = s.titleFocused
	}
	title := name.Render("Server activity") + s.muted.Render(" — "+v.engine)
	if v.conn != "" {
		title += s.muted.Render(" · "+m.tagMarkerFor(v.conn)) + s.muted.Render(v.conn)
	}
	if v.loading {
		title += " " + s.pending.Render("reading…")
	}
	return title
}

// activityContent is the main view while the report is open: the state
// of the read, or the table and its footer.
func (m Model) activityContent(w, h int) string {
	v := m.activity
	s := m.style
	switch {
	case v.unsupported:
		return joinTruncated([]string{"",
			s.pending.Render(v.engine + " runs inside lazysql — it has no server sessions to show."),
			"",
			s.muted.Render("Server activity is available on MySQL, MariaDB and PostgreSQL connections."),
			"",
			s.keyHint.Render("esc close"),
		}, w, h)
	case v.err != "":
		return joinTruncated([]string{"",
			s.danger.Render(truncate("server activity failed: "+v.err, w)),
			"",
			s.keyHint.Render("R retry · esc close"),
		}, w, h)
	case v.loading && len(v.rows) == 0:
		return joinTruncated([]string{"", s.pending.Render("reading the process list…")}, w, h)
	}

	body := maxInt(h-1, 0) // the footer; the header is the border title
	lines := m.activityTable(w, body)
	lines = append(lines, s.keyHint.Render(truncate(m.activityFooter(), w)))
	return joinTruncated(lines, w, h)
}

// activityTable renders the list through the shared read-only grid: the
// same header, the same column and row windows, the same cursor and
// selection tints the Data tab draws. The cursor row's tint wins over the
// row's own colour, or the session `K` acts on would be the one that
// stands out least.
func (m Model) activityTable(w, h int) []string {
	v := m.activity
	if h <= 0 {
		return nil
	}
	if len(v.rows) == 0 {
		return []string{"", m.style.muted.Render("no sessions — the server reports nothing running")}
	}
	return m.roGridLines(&v.grid, m.activityCursor(), w, h)
}

// activityCursor is the report's cursor as the shared renderers want it.
// Like the grid's own, it is only drawn as a cursor while the box it
// lives in has the keyboard.
func (m Model) activityCursor() gridCursor {
	v := m.activity
	return gridCursor{
		row: v.grid.row, col: v.grid.col,
		focused:  m.activityFocused(),
		selected: v.grid.cellSelected,
	}
}

// activityFooter is the line under the table: what the list holds, how
// old it is, and the keys that act on it. The auto-refresh interval is
// spelled out whenever it is on, so a list that moves on its own says
// why.
func (m Model) activityFooter() string {
	v := m.activity
	parts := []string{countSessions(len(v.rows))}
	if n := v.blockedCount(); n > 0 {
		parts = append(parts, fmt.Sprintf("%d blocked", n))
	}
	if !v.at.IsZero() {
		parts = append(parts, "read "+v.at.Format("15:04:05"))
	}
	if v.auto {
		parts = append(parts, fmt.Sprintf("auto-refresh %s", activityInterval))
	} else {
		parts = append(parts, "auto-refresh off")
	}
	// Selection mode is a mode: the footer says so, exactly as the grid's
	// status line does, so it is never on without being visible.
	if n := len(v.grid.selectedRows()); n > 0 {
		sel := fmt.Sprintf("%d sessions selected", n)
		if v.grid.narrowedToCols() {
			sel = fmt.Sprintf("%d sessions × %d columns selected", n, len(v.grid.selectedCols()))
		}
		parts = append(parts, sel)
	}
	// The keys are the report's own, so they are only offered while it
	// has them: with a side panel focused the list is on display and the
	// footer says how to get back to it instead.
	if m.activityFocused() {
		parts = append(parts, "j/k/h/l move · y copy · R refresh · t auto · K kill · esc close")
	} else {
		parts = append(parts, "tab focuses the report")
	}
	return strings.Join(parts, " · ")
}

// ---------- copy ----------

// activityCopyMenu is `y` (and `ctrl+c` with a selection up) on the
// report. It is the grid's copy menu with the scopes that need a relation
// left out: an INSERT statement has no table to insert into and a
// whole-table copy has nothing to re-read, so the report offers the cell,
// the row, the selection and the list it already holds. The keys are the
// grid's own, so `r`, `o`, `c` and `C` mean the same thing in both.
func (m *Model) activityCopyMenu() tea.Cmd {
	v := m.activity
	if v == nil || len(v.rows) == 0 {
		return logCmd("-- copy skipped: no sessions listed")
	}
	var entries []menuEntry
	add := func(k, label string, id actionID) {
		entries = append(entries, menuEntry{key: k, label: label, action: func(mm *Model) tea.Cmd {
			next, cmd := mm.runAction(id)
			*mm = next
			return cmd
		}})
	}

	// A selection outranks the cursor row, exactly as in the grid: with N
	// sessions marked, "row" is no longer what a copy means.
	sel := v.grid.selectedRows()
	scope := fmt.Sprintf("%d selected sessions", len(sel))
	if v.grid.narrowedToCols() {
		scope = fmt.Sprintf("%d selected sessions × %d columns", len(sel), len(v.grid.selectedCols()))
	}
	if len(sel) > 0 {
		add("r", scope+" — CSV", actCopySelectionCSV)
		add("o", scope+" — JSON array", actCopySelectionJSON)
		add("c", "column values of selection — "+v.grid.header(v.grid.col), actCopySelectionColumn)
	} else {
		add("c", "cell — raw value", actCopyCell)
		add("r", "row — CSV line", actCopyRowCSV)
		add("o", "row — JSON object", actCopyRowJSON)
	}
	add("C", "session list — CSV", actCopyPageCSV)
	add("O", "session list — JSON array", actCopyPageJSON)
	entries = append(entries, menuEntry{key: "esc", label: "cancel"})

	title := "Copy — server activity"
	if len(sel) > 0 {
		title = fmt.Sprintf("Copy — server activity (%s)", scope)
	}
	m.modal = &menuModal{title: title, entries: entries}
	return nil
}

// activityCopy answers the copy scopes against the report's own rows. It
// runs ahead of the Data tab's while the report has the focus; ok is
// false for everything it does not claim, which keeps the menu key, the
// export flow and the rest on their shared path.
func (m Model) activityCopy(id actionID) (tea.Cmd, bool) {
	v := m.activity
	opts := export.Options{Table: activitySubject}
	if m.driver != nil {
		opts.Dialect = m.driver.Dialect()
	}
	cols := activityColumns()

	switch id {
	case actCopyCell:
		p, ok := v.selected()
		if !ok || v.grid.col < 0 || v.grid.col >= len(cols) {
			return logCmd("-- copy cell skipped: no cell under the cursor"), true
		}
		return copyCellValue(activitySubject, cols[v.grid.col].Name,
			activityValues(p)[v.grid.col]), true

	case actCopyRowCSV, actCopyRowJSON:
		p, ok := v.selected()
		if !ok {
			return logCmd("-- copy row skipped: no session under the cursor"), true
		}
		return copyRowValues(activityCopyFormat(id), opts, activitySubject, cols, activityValues(p)), true

	case actCopySelectionCSV, actCopySelectionJSON:
		rows, block, ok := m.activitySelectionValues()
		if !ok {
			return logCmd("-- copy selection skipped: nothing selected"), true
		}
		idx := v.grid.selectedCols()
		scope := fmt.Sprintf("%d selected sessions", len(rows))
		if v.grid.narrowedToCols() {
			scope = fmt.Sprintf("%d selected sessions × %d columns", len(rows), len(idx))
		}
		return copyRowBlock(activityCopyFormat(id), opts, scope, activitySubject+"-selection",
			cutColumnHeaders(cols, idx), block), true

	case actCopySelectionColumn:
		rows, _, ok := m.activitySelectionValues()
		if !ok {
			return logCmd("-- copy selection skipped: nothing selected"), true
		}
		if v.grid.col < 0 || v.grid.col >= len(cols) {
			return logCmd("-- copy selection skipped: no column under the cursor"), true
		}
		values := make([]any, 0, len(rows))
		for _, r := range cutColumns(rows, []int{v.grid.col}) {
			values = append(values, r[0])
		}
		return copyColumnValues(activitySubject, cols[v.grid.col].Name, values), true

	case actCopyPageCSV, actCopyPageJSON:
		if len(v.rows) == 0 {
			return logCmd("-- copy skipped: no sessions listed"), true
		}
		all := make([][]any, 0, len(v.rows))
		for _, p := range v.rows {
			all = append(all, activityValues(p))
		}
		scope := fmt.Sprintf("%s of %s", countSessions(len(all)), v.conn)
		return copyRowBlock(activityCopyFormat(id), opts, scope, activitySubject, cols, all), true
	}
	return nil, false
}

// activitySubject names the report in log lines, copy labels and file
// names, the way dataSubject names the open relation.
const activitySubject = "sessions"

// activitySelectionValues is the selected sessions, both whole and cut to
// the columns the selection covers.
func (m Model) activitySelectionValues() (rows, block [][]any, ok bool) {
	v := m.activity
	sel := v.grid.selectedRows()
	if len(sel) == 0 {
		return nil, nil, false
	}
	for _, r := range sel {
		if r >= 0 && r < len(v.rows) {
			rows = append(rows, activityValues(v.rows[r]))
		}
	}
	if len(rows) == 0 {
		return nil, nil, false
	}
	return rows, cutColumns(rows, v.grid.selectedCols()), true
}

// activityCopyFormat is which serializer a copy action asks for. The
// report offers no SQL scope — there is no table to INSERT into.
func activityCopyFormat(id actionID) export.Format {
	switch id {
	case actCopyRowJSON, actCopySelectionJSON, actCopyPageJSON:
		return export.FormatJSON
	}
	return export.FormatCSV
}

// cutColumnHeaders narrows a column list to the indices a block covers,
// so the CSV header and the JSON keys shrink with the values.
func cutColumnHeaders(cols []db.Column, idx []int) []db.Column {
	out := make([]db.Column, 0, len(idx))
	for _, c := range idx {
		if c >= 0 && c < len(cols) {
			out = append(out, cols[c])
		}
	}
	return out
}

// ---------- the session detail ----------

// newProcessDetailModal is `x` on the report: one session as a
// name/value list, in the same popup the grid opens for a row too wide to
// read across. It carries every column the table has plus nothing it
// leaves out — the client address is a column now — so the popup and the
// grid cannot disagree about what a session is.
func newProcessDetailModal(p db.Process) *rowDetailModal {
	rd := &rowDetailModal{subject: "session " + p.ID}
	values := activityValues(p)
	rd.fields = make([]rowDetailField, len(activityHeaders))
	for i, name := range activityHeaders {
		text, _ := values[i].(string)
		rd.fields[i] = rowDetailField{
			name:  name,
			value: values[i],
			text:  flatten(text),
		}
	}
	return rd
}

func countSessions(n int) string {
	if n == 1 {
		return "1 session"
	}
	return fmt.Sprintf("%d sessions", n)
}
