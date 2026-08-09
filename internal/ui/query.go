package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"lazysql/internal/db"
)

// The query editor is panel [5]: a multi-line textarea that lives in the
// layout, not in a popup. `5` (or `:`) focuses it, ctrl+r runs the
// buffer without ever closing or clearing it, and the result lands in
// the main view's Data tab next to the browsing pages. Three rules shape
// the flow:
//
//   - Nothing that changes data runs unasked. A script holding DML or
//     DDL goes through a confirm modal that shows the exact statements.
//   - A run is always interruptible. The statements execute in one
//     goroutine under a cancellable context and ctrl+c aborts it without
//     touching the connection or the app.
//   - A result set is materialized once and paged in memory. An
//     arbitrary statement cannot be re-issued with a different OFFSET —
//     rewriting user SQL to page it would change what it means — so the
//     rows are read once, capped, and the grid pages the slice.

// maxQueryRows caps what one statement materializes. It is far above any
// result a terminal grid can usefully show and far below what would put
// the process at risk on a `SELECT *` over a large table. Hitting it is
// reported in the grid and in the log, never silently.
const maxQueryRows = 10000

// ---------- in-flight state ----------

// queryRun is the one script a Model may have executing. Only one runs
// at a time: two concurrent runs would race over which result the Data
// tab shows, and nothing about the flow wants a queue.
type queryRun struct {
	running bool
	// id distinguishes the run a message belongs to, so a cancelled
	// run's late reply cannot overwrite its successor's result.
	id     int
	total  int
	cancel context.CancelFunc
	// ch carries one message per finished statement plus the final
	// outcome. Unbuffered, like the export channel: a UI that stopped
	// reading should block the worker rather than let it run ahead.
	ch chan tea.Msg
	// gotRows records that some statement in this run returned a result
	// set, so a trailing DML notice cannot replace it. The issue's rule
	// is that the last SELECT is what stays on screen.
	gotRows bool
	// affected accumulates the rows changed by the run's write
	// statements, for the closing log line.
	affected int64
}

// ---------- messages ----------

// queryStmtMsg reports one finished statement of a run.
type queryStmtMsg struct {
	id        int
	index     int // 1-based, for "statement 2 of 3"
	total     int
	sql       string
	read      bool
	rs        *db.ResultSet
	truncated bool
	affected  int64
	took      time.Duration
	err       error
}

// queryDoneMsg closes a run, successfully or not.
type queryDoneMsg struct {
	id  int
	ran int
	err error
}

// ---------- the worker ----------

// queryJob is everything the worker goroutine needs. It is a value, so
// the goroutine shares nothing mutable with the Model.
type queryJob struct {
	id    int
	ctx   context.Context
	drv   db.Driver
	stmts []string
	ch    chan tea.Msg
}

// startQueryCmd launches the worker and blocks until its first message.
// Later messages are pulled by waitQueryCmd, which the root re-issues
// after each one — the same streaming shape the file export uses.
func startQueryCmd(job queryJob) tea.Cmd {
	return func() tea.Msg {
		go job.run()
		return <-job.ch
	}
}

func waitQueryCmd(ch chan tea.Msg) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg { return <-ch }
}

// run executes the statements in order and stops at the first failure:
// a script is written top to bottom, so running the rest after an error
// would apply changes the user never got to react to.
func (j queryJob) run() {
	var runErr error
	ran := 0
	for i, sql := range j.stmts {
		if err := j.ctx.Err(); err != nil {
			runErr = err
			break
		}
		out := queryStmtMsg{
			id:    j.id,
			index: i + 1,
			total: len(j.stmts),
			sql:   sql,
			read:  db.ClassifyStatement(sql) == db.StatementRead,
		}
		start := time.Now()
		if out.read {
			out.rs, out.truncated, out.err = j.drv.QueryLimit(j.ctx, sql, maxQueryRows)
		} else {
			var res db.ExecResult
			res, out.err = j.drv.Exec(j.ctx, sql)
			out.affected = res.RowsAffected
		}
		out.took = time.Since(start)
		ran++
		// A plain send, not a select on ctx.Done(): the root drains this
		// channel until queryDoneMsg arrives, so there is always a
		// reader, and guarding the send would drop exactly the message
		// that reports the cancelled statement.
		j.ch <- out
		if out.err != nil {
			runErr = out.err
			break
		}
	}
	j.ch <- queryDoneMsg{id: j.id, ran: ran, err: runErr}
}

// ---------- the editor panel ----------

// queryEditor is panel [5]. It has two modes, the shape the vim-mode
// work will extend: in normal mode the panel's keys are lazysql's own —
// `i` starts editing, ctrl+r runs the buffer, digits still jump — and in
// insert mode every key that is not ctrl+r, ctrl+c or esc types into the
// buffer. Without that split a persistent editor would swallow `q`, `?`
// and the panel numbers for as long as it had focus.
type queryEditor struct {
	area    textarea.Model
	editing bool
}

func newQueryEditor() queryEditor {
	ta := textarea.New()
	ta.CharLimit = 0
	// The textarea is the input model only: internal/ui/highlight.go
	// draws the buffer, its gutter and its cursor, so the component's own
	// chrome is turned off and its width is pinned wide enough that it
	// never soft-wraps — see the note at the top of that file for why the
	// two must not both wrap.
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.SetWidth(editorWrapWidth)
	return queryEditor{area: ta}
}

// script is the buffer's text. It is the only copy of the draft the
// model keeps: the editor is never torn down, so there is nothing to
// save it into and nothing that can go out of sync with it.
func (m Model) script() string { return m.editor.area.Value() }

// setScript replaces the buffer and leaves the cursor at its end.
func (m *Model) setScript(sql string) {
	m.editor.area.SetValue(sql)
	m.editor.area.MoveToEnd()
}

// setEditing switches the editor between normal and insert mode. The
// textarea's own focus follows, so a blurred buffer cannot swallow a key
// even if one reached it.
func (m *Model) setEditing(on bool) {
	m.editor.editing = on
	if on {
		m.editor.area.Focus()
		return
	}
	// The completion popup belongs to insert mode: it is anchored on a
	// caret that is no longer taking input.
	m.completion = completion{}
	m.editor.area.Blur()
}

// openQueryEditor is `:`: focus panel [5] and start typing. The buffer
// survives every focus change, so `:` never costs the user a draft.
func (m *Model) openQueryEditor() tea.Cmd {
	m.setFocus(panelQuery)
	m.setEditing(true)
	return nil
}

// loadIntoEditor puts a statement from the history panel in the buffer,
// replacing whatever was there. The old text is not worth a confirm: it
// is one keystroke away in the history too. The editor stays in normal
// mode — a recalled statement is meant to be run, not typed over.
func (m *Model) loadIntoEditor(sql string) tea.Cmd {
	m.setScript(sql)
	m.setFocus(panelQuery)
	m.setEditing(false)
	return nil
}

// clearQuery is `D` on panel [5]. An empty buffer goes without a
// confirmation; anything else is work the user typed.
func (m *Model) clearQuery() tea.Cmd {
	if strings.TrimSpace(m.script()) == "" {
		return nil
	}
	m.modal = &confirmModal{
		title:  "Clear the query buffer",
		body:   "Discard the editor's contents?",
		danger: true,
		onConfirm: func(mm *Model) tea.Cmd {
			mm.setScript("")
			return logCmd("-- clear the query buffer")
		},
	}
	return nil
}

// updateEditor is insert mode: a handful of keys keep their meaning and
// every other one types.
func (m Model) updateEditor(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := m.keys
	// An open completion popup claims its four keys ahead of everything
	// else. esc is the one that matters: it closes the popup and nothing
	// else, so the buffer and insert mode survive it.
	if m.completion.open {
		switch {
		case key.Matches(msg, k.CloseCompletion):
			m.closeCompletion()
			return m, nil
		case key.Matches(msg, k.CompleteNext):
			m.moveCompletion(1)
			return m, nil
		case key.Matches(msg, k.CompletePrev):
			m.moveCompletion(-1)
			return m, nil
		case key.Matches(msg, k.AcceptCompletion):
			m.acceptCompletion()
			return m, nil
		}
	}

	switch {
	case key.Matches(msg, k.LeaveInsert):
		// The buffer is kept: esc means "give me my keys back", not
		// "throw away what I typed".
		m.closeCompletion()
		m.setEditing(false)
		return m, nil

	case key.Matches(msg, k.RunEditor), msg.String() == "ctrl+enter":
		// A run ends insert mode so the result is immediately
		// navigable — paging, `v`, the tabs — without an extra esc.
		m.closeCompletion()
		m.setEditing(false)
		cmd := m.submitQuery(m.script())
		return m, cmd

	case msg.String() == "ctrl+c":
		// In the editor ctrl+c is the run's abort key, never the app's
		// quit key. With nothing running it leaves insert mode, the
		// closest thing to "stop" it can mean here.
		m.closeCompletion()
		if m.run.running {
			cmd := m.cancelQuery()
			return m, cmd
		}
		m.setEditing(false)
		return m, nil

	case key.Matches(msg, k.Complete):
		// `tab` is also the accept key, so it only completes where there
		// is a word to complete; with nothing but whitespace before the
		// caret it stays an ordinary tab. ctrl+space always completes,
		// including on an empty prefix.
		if msg.String() != "tab" || m.editorContext().completable() {
			cmd := m.refreshCompletion(true)
			return m, cmd
		}
	}

	var cmd tea.Cmd
	m.editor.area, cmd = m.editor.area.Update(msg)
	// The popup follows the buffer: every keystroke re-derives it, which
	// is what narrows it as the word grows and closes it when the word
	// ends. The fetches it wants are batched behind the textarea's own
	// command, never waited on.
	return m, tea.Batch(cmd, m.refreshCompletion(false))
}

// updateQuery is normal mode on panel [5]. Unclaimed keys fall through
// to the data grid whenever the main view is showing this editor's
// result, so paging or inspecting a result never costs the editor its
// focus.
func (m Model) updateQuery(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := m.keys
	switch {
	case key.Matches(msg, k.Down):
		m.editor.area.CursorDown()
		return m, nil
	case key.Matches(msg, k.Up):
		m.editor.area.CursorUp()
		return m, nil
	case key.Matches(msg, k.Back):
		m.focusBack()
		return m, nil
	case key.Matches(msg, k.Actions):
		m.modal = m.actionsMenu()
		return m, nil
	}
	for _, a := range k.panelActions(panelQuery) {
		if key.Matches(msg, a.binding) {
			return m.runAction(a.id)
		}
	}
	if m.data.isQuery() {
		return m.updateData(msg)
	}
	return m, nil
}

// ---------- running ----------

// submitQuery is what ctrl+r hands over: split the script, and either
// run it or ask first. Everything that can be decided without the server
// is decided here, so the worker only ever reports database problems.
//
// It never writes to the buffer. The editor owns its text, and a run
// started from the history panel must not overwrite what is being
// written in panel [5].
func (m *Model) submitQuery(script string) tea.Cmd {
	if m.driver == nil {
		return logCmd("-- run query skipped: not connected")
	}
	if m.run.running {
		return logCmd("-- run query skipped: a query is still running (ctrl+c cancels it)")
	}
	stmts := db.SplitStatements(m.driver.Engine(), script)
	if len(stmts) == 0 {
		return logCmd("-- run query skipped: nothing to run")
	}

	var writes []string
	for _, s := range stmts {
		if db.ClassifyStatement(s) == db.StatementWrite {
			writes = append(writes, s)
		}
	}
	if len(writes) == 0 {
		return m.startQuery(stmts)
	}
	// Staged edits go through the changeset and its commit modal; this
	// path bypasses both, so it says so.
	body := strings.Join(writes, ";\n") + ";"
	if len(writes) != len(stmts) {
		body += fmt.Sprintf("\n\n(%d of %d statements; the rest only read.)", len(writes), len(stmts))
	}
	body += "\n\nThese execute immediately against " + m.active +
		" — they are not staged and there is nothing to roll back."
	m.modal = &confirmModal{
		title:     "Run " + countStatements(len(writes)) + " that change data",
		body:      body,
		danger:    true,
		onConfirm: func(mm *Model) tea.Cmd { return mm.startQuery(stmts) },
	}
	return nil
}

// startQuery launches the worker for an already-vetted statement list.
func (m *Model) startQuery(stmts []string) tea.Cmd {
	if m.driver == nil || len(stmts) == 0 {
		return nil
	}
	// A new result replaces whatever the tab showed; the notice and the
	// old rows belong to the previous run.
	m.data.notice = ""

	ctx, cancel := context.WithCancel(context.Background())
	m.run = queryRun{
		running: true,
		id:      m.run.id + 1,
		total:   len(stmts),
		cancel:  cancel,
		ch:      make(chan tea.Msg),
	}
	m.keys.CancelQuery.SetEnabled(true)

	job := queryJob{id: m.run.id, ctx: ctx, drv: m.driver, stmts: stmts, ch: m.run.ch}
	return tea.Batch(
		logCmd("-- run %s on %s…", countStatements(len(stmts)), m.active),
		startQueryCmd(job),
	)
}

// rerunQuery is `enter`/`R` on a query result: execute the same script
// again. It goes through submitQuery so a re-run of DML asks again.
func (m *Model) rerunQuery() tea.Cmd {
	if !m.data.isQuery() {
		return nil
	}
	return m.submitQuery(m.data.query)
}

// cancelQuery is ctrl+c while a run is in flight. The context reaches
// the driver, which aborts the statement server-side where the engine
// supports it; the connection itself stays open.
func (m *Model) cancelQuery() tea.Cmd {
	if !m.run.running {
		return nil
	}
	m.run.cancel()
	return logCmd("-- cancelling the running query…")
}

// ---------- reducing the worker's messages ----------

// applyQueryStmt reduces one finished statement: the statement itself is
// already in the command log — the worker ran it through the Driver,
// whose Logger caught it — so this only records it in the history and
// routes its outcome into the Data tab.
func (m *Model) applyQueryStmt(msg queryStmtMsg) tea.Cmd {
	cmds := []tea.Cmd{
		m.recordHistory(msg.sql),
	}
	where := ""
	if msg.total > 1 {
		where = fmt.Sprintf(" [%d/%d]", msg.index, msg.total)
	}
	switch {
	case errors.Is(msg.err, context.Canceled):
		cmds = append(cmds, logCmd("-- cancelled%s after %s", where, formatTook(msg.took)))
	case msg.err != nil:
		cmds = append(cmds, logCmd("-- FAILED%s: %v", where, msg.err))
		m.showQueryError(msg.sql, msg.err)
	case msg.read:
		rows := 0
		if msg.rs != nil {
			rows = len(msg.rs.Rows)
		}
		note := ""
		if msg.truncated {
			note = fmt.Sprintf(" (capped at %d)", maxQueryRows)
		}
		cmds = append(cmds, logCmd("-- %d rows%s%s in %s",
			rows, note, where, formatTook(msg.took)))
		if msg.rs != nil {
			m.showQueryResult(msg.sql, msg.rs, msg.truncated)
			m.run.gotRows = true
		}
	default:
		m.run.affected += msg.affected
		cmds = append(cmds, logCmd("-- %s%s in %s",
			countAffected(msg.affected), where, formatTook(msg.took)))
		// A run that only writes still has to show something; a run
		// that already produced rows keeps them, because the last
		// SELECT is what the user asked to see.
		if !m.run.gotRows {
			m.showQueryNotice(msg.sql, msg.affected)
		}
	}
	return tea.Batch(cmds...)
}

// finishQuery clears the in-flight state and renders the outcome.
func (m *Model) finishQuery(msg queryDoneMsg) tea.Cmd {
	m.run.running = false
	m.run.cancel = nil
	m.run.ch = nil
	m.keys.CancelQuery.SetEnabled(false)

	switch {
	case errors.Is(msg.err, context.Canceled):
		return logCmd("-- query cancelled after %s", countStatements(msg.ran))
	case msg.err != nil:
		// The per-statement message already named the error; this line
		// says how much of the script got as far as running.
		return logCmd("-- query stopped at statement %d of %d", msg.ran, m.run.total)
	case m.run.total > 1:
		return logCmd("-- %s ok, %s total", countStatements(msg.ran), countAffected(m.run.affected))
	default:
		return nil
	}
}

// showQueryResult puts a result set in the Data tab. It replaces the
// browsed relation: the tab has one grid, and a query result is not a
// relation, so nothing table-scoped (editing, export, introspection)
// applies to it afterwards.
func (m *Model) showQueryResult(sql string, rs *db.ResultSet, truncated bool) {
	m.table = ""
	m.resetMeta()
	m.tab = mainTabData
	m.focusResult()
	m.data = dataView{
		conn:      m.active,
		database:  m.database,
		query:     sql,
		all:       rs.Rows,
		truncated: truncated,
		cols:      rs.Columns,
		total:     int64(len(rs.Rows)),
		hasTotal:  true,
		// A bumped req invalidates any page or count still in flight
		// for the relation this result replaced.
		req: m.data.req + 1,
	}
	m.data.setPage(0)
	m.clampCursor()
}

// showQueryError puts a failed statement's message in the Data tab. It
// replaces whatever was there, result or relation: the error is the news
// and leaving stale rows under it invites reading them as the answer.
func (m *Model) showQueryError(sql string, err error) {
	m.table = ""
	m.resetMeta()
	m.tab = mainTabData
	m.focusResult()
	m.data = dataView{
		conn:     m.active,
		database: m.database,
		query:    sql,
		err:      err.Error(),
		req:      m.data.req + 1,
	}
}

// showQueryNotice puts a rows-affected notice in the Data tab for a
// statement that returns no result set.
func (m *Model) showQueryNotice(sql string, affected int64) {
	m.table = ""
	m.resetMeta()
	m.tab = mainTabData
	m.focusResult()
	m.data = dataView{
		conn:     m.active,
		database: m.database,
		query:    sql,
		notice:   db.FirstKeyword(sql) + " — " + countAffected(affected),
		req:      m.data.req + 1,
	}
}

// formatTook renders how long a statement took. Sub-millisecond runs
// are rounded to microseconds rather than to a bare "0s", which reads
// like the statement never ran.
func formatTook(d time.Duration) string {
	if d < time.Millisecond {
		return d.Round(time.Microsecond).String()
	}
	return d.Round(time.Millisecond).String()
}

// countStatements spells "1 statement" / "3 statements".
func countStatements(n int) string {
	if n == 1 {
		return "1 statement"
	}
	return fmt.Sprintf("%d statements", n)
}

// countAffected spells the rows-affected notice.
func countAffected(n int64) string {
	if n == 1 {
		return "1 row affected"
	}
	return fmt.Sprintf("%d rows affected", n)
}

// ---------- rendering ----------

// focusResult decides who owns the keyboard once a result lands. A run
// started in panel [5] leaves the focus there: its main view shows the
// buffer and the result together, which is the whole point of an editor
// that stays open. Every other origin — `x` on the history panel, a
// re-run from the grid — hands the grid the focus, the way it always did.
func (m *Model) focusResult() {
	if m.focus == panelQuery {
		return
	}
	m.setFocus(panelMain)
}

// queryPanelBody is panel [5] in the side column: the buffer's first
// lines, and what the editor is currently doing. The column is too
// narrow to edit in — the editing surface is the main view — so this is
// a preview, not a second copy of the textarea.
func (m Model) queryPanelBody(w, h int) string {
	s := m.style
	focused := m.focus == panelQuery
	titleStyle := s.title
	if focused {
		titleStyle = s.titleFocused
	}
	line := titleStyle.Render(fmt.Sprintf("[%d] %s", int(panelQuery)+1, panelTitles[panelQuery]))
	switch {
	case m.run.running:
		line += " " + s.pending.Render("running…")
	case focused && m.editor.editing:
		line += " " + s.keyHint.Render("insert")
	case focused:
		line += " " + s.muted.Render("normal")
	}

	var b strings.Builder
	b.WriteString(truncate(line, w))
	rows := h - 1
	if rows <= 0 {
		return b.String()
	}
	lines := strings.Split(m.script(), "\n")
	if strings.TrimSpace(m.script()) == "" {
		b.WriteString("\n" + s.muted.Render(truncate("(empty — press : to write a query)", w)))
		return b.String()
	}
	for i, l := range lines {
		if i >= rows {
			break
		}
		// The last visible row says how much is out of sight rather than
		// letting the preview end mid-script without a word.
		if i == rows-1 && len(lines) > rows {
			b.WriteString("\n" + s.muted.Render(truncate(
				fmt.Sprintf("… %d more lines", len(lines)-rows+1), w)))
			break
		}
		// The preview truncates first and highlights the result: styling
		// then cutting would slice an escape sequence in half. A token
		// cut by the truncation is re-read on its own, which at worst
		// costs the last word on the line its colour.
		b.WriteString("\n" + highlightSQL(s, m.sqlDialect(), truncate(l, w)))
	}
	return b.String()
}

// queryContent is the main view while panel [5] is focused: the editor
// on top, and under it whatever the last run produced. The editor takes
// what it needs up to half the box, so a one-line query does not push
// the result off screen.
func (m Model) queryContent(w, h int) string {
	s := m.style
	header := s.titleFocused.Render("Query editor") + s.muted.Render(" — "+m.editorMode())
	if m.active != "" {
		header += s.muted.Render(" · " + m.active + " / " + displayDatabase(m.database))
	}
	body := []string{truncate(header, w)}
	rows := h - 1
	if rows <= 0 {
		return clipHeight(strings.Join(body, "\n"), h)
	}

	rendered := m.editorBlock(w, m.editorHeight(w, rows))
	body = append(body, rendered)
	rows -= lipgloss.Height(rendered)
	if rows <= 0 {
		return clipHeight(strings.Join(body, "\n"), h)
	}

	body = append(body, s.muted.Render(truncate(m.editorHint(), w)))
	if rows--; rows > 0 && m.data.open() {
		body = append(body, m.dataContent(w, rows))
	}
	return clipHeight(strings.Join(body, "\n"), h)
}

// clipHeight drops whatever does not fit in h lines. The main view is a
// fixed box; a block that overflows it would push the layout apart.
func clipHeight(block string, h int) string {
	lines := strings.Split(block, "\n")
	if h < 0 {
		h = 0
	}
	if len(lines) <= h {
		return block
	}
	return strings.Join(lines[:h], "\n")
}

// editorMode names the mode for the main view's header.
func (m Model) editorMode() string {
	if m.editor.editing {
		return "insert"
	}
	return "normal"
}

// editorHint is the one-line reminder under the buffer. It names the
// keys of the mode the editor is actually in, the same ones the options
// bar shows.
func (m Model) editorHint() string {
	switch {
	case m.completion.open:
		return "↑/↓ select · enter/tab accept · esc close the popup"
	case m.editor.editing:
		return "ctrl+r run · ctrl+space complete · esc normal mode"
	}
	return "i edit · ctrl+r run · D clear · esc back"
}
