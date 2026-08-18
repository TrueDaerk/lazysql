package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"lazysql/internal/db"
)

// `A` on panel [1] asks the server what it is doing: one row per
// session, longest-running first, with the sessions that are waiting on
// a lock marked with the ID of whoever holds it. The report opens *in*
// the main view and takes the focus there, the way a trigger definition
// does — not as an overlay on a focused panel [1], which is what the
// schema diff still is. See wiki/design/server-activity-focus.md.
//
// Four rules shape it:
//
//   - The report is focused where it is drawn. Its keys act only while
//     the main view has the focus; `1` or `tab` hands the keyboard back
//     to a side panel, which then behaves exactly as it always does
//     while the list stays readable beside it.
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

	// cursor is the row `K` acts on; off is the first rendered row.
	cursor int
	off    int
}

// selected is the process under the cursor.
func (v *activityView) selected() (db.Process, bool) {
	if v == nil || v.cursor < 0 || v.cursor >= len(v.rows) {
		return db.Process{}, false
	}
	return v.rows[v.cursor], true
}

func (v *activityView) clampCursor() {
	if v.cursor >= len(v.rows) {
		v.cursor = len(v.rows) - 1
	}
	if v.cursor < 0 {
		v.cursor = 0
	}
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
			v.unsupported, v.err, v.rows = true, "", nil
			v.auto = false
			v.gen++
			return logCmd("-- server activity: %s has no server sessions to list", v.engine)
		}
		v.err = msg.err.Error()
		return logCmd("-- server activity FAILED: %v", msg.err)
	}
	v.unsupported, v.err = false, ""
	v.rows = msg.rows
	v.clampCursor()
	return nil
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
	// The client address is not in the table — there is no room for it —
	// and this is the one moment it matters: it says which machine is
	// about to lose its connection.
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
		m.closeActivity()
		// The report was the main view's content; with it gone the box has
		// nothing of its own to show, so the focus goes back where `A` came
		// from rather than sitting on an empty grid.
		if m.focus == panelMain && !m.data.open() && m.trigger == nil {
			m.focusBack()
		}
		return m, nil, true
	case key.Matches(msg, k.Down):
		v.cursor++
		v.clampCursor()
		return m, nil, true
	case key.Matches(msg, k.Up):
		v.cursor--
		v.clampCursor()
		return m, nil, true
	case key.Matches(msg, k.NextPage):
		v.cursor += 10
		v.clampCursor()
		return m, nil, true
	case key.Matches(msg, k.PrevPage):
		v.cursor -= 10
		v.clampCursor()
		return m, nil, true
	case key.Matches(msg, k.Refresh):
		cmd := m.refreshActivity()
		return m, cmd, true
	case key.Matches(msg, k.KillProcess):
		cmd := m.killSelectedProcess()
		return m, cmd, true
	case key.Matches(msg, k.ActivityAuto):
		cmd := m.toggleActivityAuto()
		return m, cmd, true
	case key.Matches(msg, k.ViewCell):
		// The grid's "show me this value in full" key: a server-truncated
		// statement is exactly the cell the list cannot show whole.
		if p, ok := v.selected(); ok {
			m.modal = newCellModal("session "+p.ID, "query", "", p.Query)
		}
		return m, nil, true
	}
	switch msg.String() {
	case "g", "home":
		v.cursor = 0
		return m, nil, true
	case "G", "end":
		v.cursor = len(v.rows) - 1
		v.clampCursor()
		return m, nil, true
	}
	return m, nil, false
}

// scrollActivity is the wheel: the report has a cursor rather than a
// scroll offset, so the wheel walks rows the way j/k does.
func (m *Model) scrollActivity(delta int) {
	if m.activity == nil {
		return
	}
	m.activity.cursor += delta
	m.activity.clampCursor()
}

// clickActivity moves the cursor onto the clicked row. row is a content
// row of the main view box, and activityContent spends the first one on
// the column header — everything under it is the window activityTable
// last rendered, which is why the click is mapped through v.off rather
// than through the cursor.
func (m *Model) clickActivity(row int) {
	v := m.activity
	if v == nil || len(v.rows) == 0 || row < 1 {
		return
	}
	idx := v.off + row - 1
	if idx < 0 || idx >= len(v.rows) {
		return
	}
	v.cursor = idx
}

// ---------- rendering ----------

// activityHeaders are the report's columns. Query is last because it is
// the one that gets whatever width is left.
var activityHeaders = []string{"PID", "User", "Database", "State", "Duration", "Blocked by", "Query"}

// activityWidths caps each column except the last, which takes the rest
// of the box. The caps are generous enough for real values (a pid, a
// role name, "idle in transaction (Lock: transactionid)") and small
// enough to leave the statement room on an 80-column terminal.
var activityWidths = []int{7, 12, 14, 24, 9, 11}

// activityCells renders one process as the report's columns.
func activityCells(p db.Process) []string {
	return []string{
		p.ID,
		orNone(p.User),
		orNone(p.Database),
		orNone(p.State),
		formatProcessDuration(p),
		orDash(p.BlockedByText()),
		orNone(flatten(p.Query)),
	}
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

// activityTable renders the header row and the visible window of the
// list. The cursor row is tinted the way the data grid tints its own, so
// what `K` would act on is never in doubt.
func (m Model) activityTable(w, h int) []string {
	v := m.activity
	s := m.style
	if h <= 0 {
		return nil
	}
	if len(v.rows) == 0 {
		return []string{"", s.muted.Render("no sessions — the server reports nothing running")}
	}

	rows := make([][]string, len(v.rows))
	for i, p := range v.rows {
		rows[i] = activityCells(p)
	}
	widths := activityColumnWidths(rows, w)

	out := []string{s.gridHeader.Render(truncate(activityRowText(activityHeaders, widths), w))}
	start, end := rowWindow(len(rows), v.cursor, maxInt(h-1, 1), v.off)
	v.off = start
	for i := start; i < end; i++ {
		line := truncate(activityRowText(rows[i], widths), w)
		style := m.style.plain
		switch {
		case v.rows[i].Blocked():
			style = s.danger
		case v.rows[i].Self:
			style = s.muted
		}
		if i == v.cursor {
			// The cursor tint has to win over the row's own colour, or the
			// row `K` acts on would be the one that stands out least.
			line = s.rowCursor.Render(pad(line, w))
		} else {
			line = style.Render(line)
		}
		out = append(out, line)
	}
	return out
}

// activityColumnWidths sizes the fixed columns to their content within
// their caps and hands the rest of the box to the statement.
func activityColumnWidths(rows [][]string, w int) []int {
	widths := make([]int, len(activityHeaders))
	for i := range widths {
		widths[i] = len(activityHeaders[i])
	}
	for _, r := range rows {
		for i, cell := range r {
			if n := lipgloss.Width(cell); n > widths[i] {
				widths[i] = n
			}
		}
	}
	used := 0
	for i, max := range activityWidths {
		if widths[i] > max {
			widths[i] = max
		}
		used += widths[i] + colGap
	}
	widths[len(widths)-1] = maxInt(w-used, minColWidth)
	return widths
}

// activityRowText joins one row's cells, padded to their columns.
func activityRowText(cells []string, widths []int) string {
	var b strings.Builder
	for i, cell := range cells {
		if i > 0 {
			b.WriteString(" ")
		}
		text := truncate(cell, widths[i])
		if i < len(cells)-1 {
			text = pad(text, widths[i])
		}
		b.WriteString(text)
	}
	return b.String()
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
	// The keys are the report's own, so they are only offered while it
	// has them: with a side panel focused the list is on display and the
	// footer says how to get back to it instead.
	if m.activityFocused() {
		parts = append(parts, "j/k move · R refresh · t auto · K kill · v query · esc close")
	} else {
		parts = append(parts, "tab focuses the report")
	}
	return strings.Join(parts, " · ")
}

func countSessions(n int) string {
	if n == 1 {
		return "1 session"
	}
	return fmt.Sprintf("%d sessions", n)
}
