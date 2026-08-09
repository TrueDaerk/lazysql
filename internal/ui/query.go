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
//   - Ordinary DML/DDL runs unasked — it is logged, not staged, and
//     asking every time trains blind dismissal. The one shape that does
//     ask first is a DELETE or UPDATE with no WHERE and no LIMIT: it
//     would touch every row in its table, and the confirm modal names it.
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
	// args are the bound parameters of a placeholder run. They only ever
	// travel with a single statement — one value set cannot span a
	// script — and display is then the statement as the user wrote it,
	// placeholders and all, for the history and the Data tab.
	args    []any
	display string
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
		if j.display != "" && len(j.stmts) == 1 {
			out.sql = j.display
		}
		start := time.Now()
		if out.read {
			out.rs, out.truncated, out.err = j.drv.QueryLimit(j.ctx, sql, maxQueryRows, j.args...)
		} else {
			var res db.ExecResult
			res, out.err = j.drv.Exec(j.ctx, sql, j.args...)
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

// queryEditor is panel [5]. It has two modes: normal mode speaks the vim
// dialect implemented in vim.go — motions, x/dd/yy/p, i/a/o/O into
// insert — plus lazysql's own keys (ctrl+r runs, D clears, digits still
// jump), and in insert mode every key that is not ctrl+r, ctrl+c or esc
// types into the buffer. Without that split a persistent editor would
// swallow `q`, `?` and the panel numbers for as long as it had focus.
type queryEditor struct {
	area    textarea.Model
	editing bool
	// pending is the first key of an unfinished dd/yy/gg chord, 0 when
	// none. Any other key cancels it and acts as itself; leaving the
	// panel clears it (see setFocus).
	pending rune
	// register is the single yank register x/dd/yy fill and p empties.
	register vimRegister
	// want is the column vertical motions aim for, kept across
	// keypresses so j over a short line and back restores the column.
	// -1 means "wherever the cursor is".
	want int
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
	return queryEditor{area: ta, want: -1}
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
	m.editor.pending = 0
	// Whatever column insert mode ends on is where normal mode starts
	// aiming; a want kept across an editing session would jump the
	// first j/k to a column the user left minutes ago.
	m.editor.want = -1
	if on {
		m.editor.area.Focus()
		return
	}
	// The completion popup belongs to insert mode: it is anchored on a
	// caret that is no longer taking input.
	m.completion = completion{}
	m.editor.area.Blur()
	// Insert mode may leave the caret one past the last character; vim's
	// normal mode sits on it, not after it.
	m.applyVim(m.vimBuffer(), false)
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

	case key.Matches(msg, k.SaveSnippet):
		// Saving does not end insert mode: the name prompt takes the keys
		// while it is open and the buffer is waiting where it was left.
		m.closeCompletion()
		cmd := m.promptSaveSnippet(m.script())
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

// vimBuffer reads the textarea into the pure vim engine: its text, its
// cursor, and the remembered vertical-motion column.
func (m Model) vimBuffer() vimBuffer {
	b := newVimBuffer(m.script(), m.editor.area.Line(), m.editor.area.Column())
	if m.editor.want > b.want {
		b.want = m.editor.want
	}
	return b
}

// applyVim writes a vimBuffer back: the text if an edit changed it, and
// the cursor always. The textarea has no "set line" call, so the row is
// walked from the top — buffers here are a few hundred lines at most.
func (m *Model) applyVim(b vimBuffer, changed bool) {
	if changed {
		m.editor.area.SetValue(b.text())
	}
	m.editor.area.MoveToBegin()
	for i := 0; i < b.row; i++ {
		m.editor.area.CursorDown()
	}
	m.editor.area.SetCursorColumn(b.col)
	m.editor.want = b.want
}

// vimMotion applies one cursor-only command.
func (m *Model) vimMotion(move func(*vimBuffer)) {
	b := m.vimBuffer()
	move(&b)
	m.applyVim(b, false)
}

// vimInsertAt lands the cursor and enters insert mode — the shared tail
// of a/o/O.
func (m *Model) vimInsertAt(b vimBuffer, col int, changed bool) {
	b.col = col
	m.applyVim(b, changed)
	// applyVim's SetCursorColumn clamps to the line, which for `a` at
	// line end is one short; re-set once focused, when the textarea
	// allows the past-the-end column.
	m.setEditing(true)
	m.editor.area.SetCursorColumn(col)
}

// updateQuery is normal mode on panel [5]: the vim layer first, then the
// panel's own actions. Unclaimed keys fall through to the data grid
// whenever the main view is showing this editor's result, so paging or
// inspecting a result never costs the editor its focus.
func (m Model) updateQuery(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := m.keys
	// A started dd/yy/gg chord claims this key: its double completes the
	// command, anything else cancels the chord and acts as itself.
	if p := m.editor.pending; p != 0 {
		m.editor.pending = 0
		if s := msg.String(); len(s) == 1 && rune(s[0]) == p {
			switch p {
			case 'd':
				b := m.vimBuffer()
				m.editor.register = b.deleteLine()
				m.applyVim(b, true)
			case 'y':
				m.editor.register = m.vimBuffer().yankLine()
			case 'g':
				m.vimMotion((*vimBuffer).top)
			}
			return m, nil
		}
	}

	switch {
	// Chord openers. Matching literal first keys — a rebind of the full
	// chord rebinds its opener.
	case key.Matches(msg, k.VimDeleteLine), key.Matches(msg, k.VimYankLine), key.Matches(msg, k.VimTop):
		if s := msg.String(); len(s) == 1 {
			m.editor.pending = rune(s[0])
		}
		return m, nil

	// Motions.
	case key.Matches(msg, k.Down):
		m.vimMotion((*vimBuffer).down)
		return m, nil
	case key.Matches(msg, k.Up):
		m.vimMotion((*vimBuffer).up)
		return m, nil
	case key.Matches(msg, k.VimLeft):
		m.vimMotion((*vimBuffer).left)
		return m, nil
	case key.Matches(msg, k.VimRight):
		m.vimMotion((*vimBuffer).right)
		return m, nil
	case key.Matches(msg, k.VimWordFwd):
		m.vimMotion((*vimBuffer).wordForward)
		return m, nil
	case key.Matches(msg, k.VimWordBack):
		m.vimMotion((*vimBuffer).wordBack)
		return m, nil
	case key.Matches(msg, k.VimLineStart):
		m.vimMotion((*vimBuffer).lineStart)
		return m, nil
	case key.Matches(msg, k.VimLineEnd):
		m.vimMotion((*vimBuffer).lineEnd)
		return m, nil
	case key.Matches(msg, k.VimBottom):
		m.vimMotion((*vimBuffer).bottom)
		return m, nil

	// Insert entry points. `i` (and enter) arrive through the panel's
	// actEditQuery below and keep the cursor where it is.
	case key.Matches(msg, k.VimAppend):
		b := m.vimBuffer()
		m.vimInsertAt(b, b.appendCol(), false)
		return m, nil
	case key.Matches(msg, k.VimOpenBelow):
		b := m.vimBuffer()
		b.openBelow()
		m.vimInsertAt(b, 0, true)
		return m, nil
	case key.Matches(msg, k.VimOpenAbove):
		b := m.vimBuffer()
		b.openAbove()
		m.vimInsertAt(b, 0, true)
		return m, nil

	// Edits.
	case key.Matches(msg, k.VimDeleteChar):
		b := m.vimBuffer()
		if reg, ok := b.deleteChar(); ok {
			m.editor.register = reg
			m.applyVim(b, true)
		}
		return m, nil
	case key.Matches(msg, k.VimPaste):
		b := m.vimBuffer()
		if b.paste(m.editor.register) {
			m.applyVim(b, true)
		}
		return m, nil

	case key.Matches(msg, k.Back):
		m.focusBack()
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

	// A single statement with placeholders — a positional `?` or a named
	// `:name`, detected by the tokenizer so a `?` inside a string or a
	// `::type` cast does not count — prompts for its values first and runs
	// as a prepared statement with them bound as parameters. Multi-statement
	// scripts skip the prompt: one value set cannot span statements, so
	// they run as written.
	if len(stmts) == 1 {
		if phs := db.ExtractPlaceholders(m.driver.Engine(), stmts[0]); len(phs) > 0 {
			stmt := stmts[0]
			m.modal = newParamsModal(stmt, m.sqlDialect(), phs,
				func(mm *Model, values []string) tea.Cmd {
					bound, args, err := db.BindPlaceholders(
						mm.driver.Dialect(), mm.driver.Engine(), stmt, values)
					if err != nil {
						return logCmd("-- run query FAILED: %v", err)
					}
					return mm.vetQuery([]string{bound}, args, stmt)
				})
			return nil
		}
	}
	return m.vetQuery(stmts, nil, "")
}

// vetQuery is the tail of submitQuery once the statements and their bound
// arguments are final: confirm an unguarded write, then start the worker.
// display, when non-empty, is the statement as the user wrote it — with
// its `?`/`:name` placeholders — which is what the history and the Data
// tab show instead of the dialect-rewritten text.
func (m *Model) vetQuery(stmts []string, args []any, display string) tea.Cmd {
	// Ordinary DML/DDL — INSERT, CREATE TABLE, a DELETE or UPDATE that
	// already carries WHERE or LIMIT — just runs: it is logged to the
	// command log like any other statement, and asking every time only
	// trains the user to dismiss the modal blindly. The one shape worth
	// stopping for is a DELETE or UPDATE with neither, which touches
	// every row in its table.
	unguarded := db.FindUnguardedWrites(m.driver.Engine(), stmts)
	if len(unguarded) == 0 {
		return m.startQuery(stmts, args, display)
	}
	m.modal = &confirmModal{
		title:     unguardedWriteTitle(unguarded),
		body:      unguardedWriteBody(unguarded, m.active),
		danger:    true,
		onConfirm: func(mm *Model) tea.Cmd { return mm.startQuery(stmts, args, display) },
	}
	return nil
}

// unguardedWriteTitle names the table for the common case of one
// offending statement, and falls back to a count for a script that has
// several.
func unguardedWriteTitle(ws []db.UnguardedWrite) string {
	if len(ws) == 1 {
		w := ws[0]
		if w.Table != "" {
			return fmt.Sprintf("%s without WHERE or LIMIT — %s", w.Verb, w.Table)
		}
		return w.Verb + " without WHERE or LIMIT"
	}
	return fmt.Sprintf("%s without WHERE or LIMIT", countStatements(len(ws)))
}

// unguardedWriteBody spells out what each offending statement will do
// and asks for the run to be confirmed against the live connection.
func unguardedWriteBody(ws []db.UnguardedWrite, active string) string {
	verb := map[string]string{"DELETE": "delete every row", "UPDATE": "update every row"}
	lines := make([]string, 0, len(ws))
	for _, w := range ws {
		table := w.Table
		if table == "" {
			table = "its table"
		}
		lines = append(lines, fmt.Sprintf("%s;\n  — no WHERE or LIMIT: will %s in %s.",
			w.Statement, verb[w.Verb], table))
	}
	return strings.Join(lines, "\n\n") + "\n\nThis runs immediately against " + active + "."
}

// startQuery launches the worker for an already-vetted statement list.
// args, when non-nil, are the bound parameters of a single-statement run;
// display is the statement as typed, for the history and the Data tab.
func (m *Model) startQuery(stmts []string, args []any, display string) tea.Cmd {
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

	job := queryJob{
		id: m.run.id, ctx: ctx, drv: m.driver, stmts: stmts, ch: m.run.ch,
		args: args, display: display,
	}
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
	return "i/a/o edit · hjkl move · dd/yy/p line ops · ctrl+r run · backspace history · D clear · esc back"
}
