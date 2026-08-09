package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"lazysql/internal/db"
)

// The query editor: `:` opens a multi-line textarea, ctrl+r runs it, and
// the result lands in the main view's Data tab next to the browsing
// pages. Three rules shape the flow:
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

// ---------- opening the editor ----------

// openQueryEditor is `:`. The draft survives every close, so an `esc` to
// look something up in a panel costs nothing.
func (m *Model) openQueryEditor() tea.Cmd {
	m.modal = newQueryModal(m.draft)
	return nil
}

// loadIntoEditor opens the editor on a statement from the history panel,
// replacing whatever draft was there. The old draft is not worth a
// confirm: it is one keystroke away in the history too.
func (m *Model) loadIntoEditor(sql string) tea.Cmd {
	m.draft = sql
	m.modal = newQueryModal(sql)
	return nil
}

// ---------- running ----------

// submitQuery is what ctrl+r hands over: split the script, and either
// run it or ask first. Everything that can be decided without the server
// is decided here, so the worker only ever reports database problems.
func (m *Model) submitQuery(script string) tea.Cmd {
	m.draft = script
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

// applyQueryStmt reduces one finished statement: it logs the statement,
// records it in the history and routes its outcome into the Data tab.
func (m *Model) applyQueryStmt(msg queryStmtMsg) tea.Cmd {
	cmds := []tea.Cmd{
		logCmd("%s;", flattenSQL(msg.sql)),
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
	// The result is the point of running the query, so the grid takes
	// focus; `esc` walks back to whatever panel the user came from.
	m.setFocus(panelMain)
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
	m.setFocus(panelMain)
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
	m.setFocus(panelMain)
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

// flattenSQL collapses a multi-line statement onto the single line the
// command log has room for.
func flattenSQL(sql string) string { return flatten(sql) }

// ---------- the editor modal ----------

// queryModal is the `:` popup: a textarea and nothing else. It never
// touches the database itself — ctrl+r hands the text to submitQuery,
// which decides whether it needs a confirmation first.
type queryModal struct {
	area textarea.Model
}

func newQueryModal(initial string) *queryModal {
	ta := textarea.New()
	ta.Placeholder = "SELECT * FROM …"
	ta.ShowLineNumbers = true
	ta.CharLimit = 0
	ta.SetValue(initial)
	ta.MoveToEnd()
	ta.Focus()
	return &queryModal{area: ta}
}

func (q *queryModal) update(msg tea.KeyPressMsg, m *Model) (bool, tea.Cmd) {
	switch msg.String() {
	case "esc":
		// The draft is kept: `esc` means "get out of my way", not
		// "throw away what I typed".
		m.draft = q.area.Value()
		return true, nil
	case "ctrl+c":
		// In the editor ctrl+c is the run's abort key, never the app's
		// quit key. With nothing running it closes the editor, which is
		// the closest thing to "stop" it can mean here.
		if m.run.running {
			return false, m.cancelQuery()
		}
		m.draft = q.area.Value()
		return true, nil
	case "ctrl+r", "ctrl+enter":
		return true, m.submitQuery(q.area.Value())
	}
	var cmd tea.Cmd
	q.area, cmd = q.area.Update(msg)
	return false, cmd
}

func (q *queryModal) view(s styles, maxW, maxH int) string {
	w := min(maxW-10, 80)
	if w < 20 {
		w = 20
	}
	// The box grows with the script instead of always claiming its
	// maximum: a one-line query should not open a half-screen modal.
	h := min(maxH-9, 14)
	if lines := q.area.LineCount() + 1; lines < h {
		h = lines
	}
	if h < 3 {
		h = 3
	}
	q.area.SetWidth(w)
	q.area.SetHeight(h)

	footer := "ctrl+r run · enter newline · esc close (draft kept)"
	if q.area.Value() == "" {
		footer = "ctrl+r run · esc cancel"
	}
	return s.modal.Render(lipgloss.JoinVertical(lipgloss.Left,
		s.modalTitle.Render("Query editor"),
		"",
		q.area.View(),
		"",
		s.muted.Render(footer),
	))
}
