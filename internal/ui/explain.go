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
)

// `ctrl+e` in the query editor asks the server how it would run the
// statement under the cursor. Three rules shape it:
//
//   - It never executes the statement. The dialect adds EXPLAIN, never
//     EXPLAIN ANALYZE — see internal/db/explain.go — so explaining a
//     DELETE is as safe as explaining a SELECT and no confirm modal is
//     needed for either.
//   - It never touches the buffer. The plan takes over the main view
//     while panel [3] keeps the focus, and `esc` puts the editor back
//     with its text, cursor and mode exactly as they were.
//   - One statement at a time. A script has as many plans as statements
//     and no engine explains a batch, so a multi-statement buffer
//     explains the one the caret is in.
//
// `ctrl+a` is the opt-in exception to the first rule: EXPLAIN ANALYZE,
// which does execute the statement. It is its own key, offered only
// where the engine has an analyzing EXPLAIN, refused for anything
// db.IsWrite calls a write, and always behind a confirm modal. The two
// kinds of plan are labelled apart on screen — see planKind.

// explainTimeout bounds one plan request. Planning is cheap — nothing is
// executed — so a request that has not answered by then is a stuck
// connection, not a slow plan.
const explainTimeout = 30 * time.Second

// planView is the EXPLAIN result on screen: the request in flight or
// what it produced. Model.plan is nil when no plan is open.
type planView struct {
	// id distinguishes requests, so a stale reply cannot fill a view the
	// user has already dismissed.
	id      int
	running bool
	// stmt is the statement being explained, as the user wrote it.
	stmt   string
	engine string
	// analyzed marks an EXPLAIN ANALYZE request: the statement runs, and
	// cancel aborts it while it does.
	analyzed bool
	cancel   context.CancelFunc

	plan   *db.Plan
	lines  []string
	err    string
	offset int
}

// explainDoneMsg carries one finished plan request.
type explainDoneMsg struct {
	id       int
	stmt     string
	analyzed bool
	plan     *db.Plan
	err      error
}

// ---------- the flow ----------

// explainQuery is `ctrl+e`: explain the statement the caret is in.
// Everything decidable without the server is decided here, so a failed
// request always means the engine refused the statement.
func (m *Model) explainQuery() tea.Cmd {
	if m.driver == nil {
		return logCmd("-- explain skipped: not connected")
	}
	if m.plan != nil && m.plan.running {
		return logCmd("-- explain skipped: a plan is still being fetched")
	}
	span, ok := db.StatementAt(m.driver.Engine(), m.script(), m.editorOffset())
	if !ok {
		return logCmd("-- explain skipped: nothing to explain")
	}
	// A placeholder statement has no values yet, and binding them would
	// mean running the prompt for a statement that is not going to run.
	// The message names the key that does bind them.
	if phs := db.ExtractPlaceholders(m.driver.Engine(), span.SQL); len(phs) > 0 {
		return logCmd(
			"-- explain skipped: %s has placeholders — ctrl+r prompts for their values",
			db.FirstKeyword(span.SQL))
	}

	return m.startExplain(span.SQL, false)
}

// explainAnalyzeQuery is `ctrl+a`: the analyzed plan of the statement the
// caret is in. Every refusal is decided here, before the confirm modal —
// the modal is only ever asked about a statement that may run.
func (m *Model) explainAnalyzeQuery() tea.Cmd {
	if m.driver == nil {
		return logCmd("-- explain analyze skipped: not connected")
	}
	if m.plan != nil && m.plan.running {
		return logCmd("-- explain analyze skipped: a plan is still being fetched")
	}
	if m.query.run.running {
		return logCmd("-- explain analyze skipped: a query is running")
	}
	// An engine without an analyzing EXPLAIN says so instead of offering
	// a modal that could only end in an error.
	if err := m.driver.ExplainAnalyzeSupport(); err != nil {
		m.modal = &confirmModal{title: "EXPLAIN ANALYZE unavailable", body: err.Error()}
		return logCmd("-- explain analyze unavailable: %v", err)
	}
	engine := m.driver.Engine()
	span, ok := db.StatementAt(engine, m.script(), m.editorOffset())
	if !ok {
		return logCmd("-- explain analyze skipped: nothing to explain")
	}
	if phs := db.ExtractPlaceholders(engine, span.SQL); len(phs) > 0 {
		return logCmd(
			"-- explain analyze skipped: %s has placeholders — ctrl+r prompts for their values",
			db.FirstKeyword(span.SQL))
	}
	// The same classification the read-only guard refuses on: a write is
	// never analyzed, on any connection. The driver refuses it too; this
	// only says so before a modal offers it.
	if db.IsWrite(engine, span.SQL) {
		m.modal = &confirmModal{
			title:  "EXPLAIN ANALYZE refused",
			body:   db.FirstKeyword(span.SQL) + " is a write.\n\n" + db.ErrAnalyzeWrite.Error() + ".",
			danger: true,
		}
		return logCmd("-- explain analyze refused: %s is a write", db.FirstKeyword(span.SQL))
	}
	sql := span.SQL
	m.modal = &confirmModal{
		title:  "EXPLAIN ANALYZE — executes the statement",
		body:   analyzeConfirmBody(sql, m.taggedConnName(m.active)),
		danger: true,
		onConfirm: func(mm *Model) tea.Cmd {
			return mm.startExplain(sql, true)
		},
	}
	return nil
}

// analyzeConfirmBody says plainly what confirming does: the statement
// runs, for as long as it takes, against the named connection.
func analyzeConfirmBody(sql, conn string) string {
	return "This EXECUTES the statement on " + conn + " to measure it:\n\n" +
		sql + "\n\n" +
		"It is classified as a read and runs in a transaction that is rolled back, " +
		"but it takes as long as the query itself and reads every row it touches. " +
		"ctrl+c cancels it."
}

// startExplain opens the plan view for one statement and fetches its
// plan: the estimated one, or with analyzed the measured one.
func (m *Model) startExplain(stmt string, analyzed bool) tea.Cmd {
	if m.driver == nil {
		return nil
	}
	id := 1
	if m.plan != nil {
		id = m.plan.id + 1
	}
	m.dropPlan()
	m.plan = &planView{
		id: id, running: true,
		stmt:     stmt,
		engine:   m.driver.Dialect().DisplayName(),
		analyzed: analyzed,
	}
	// The plan replaces the editor in the main view, so the buffer stops
	// taking keys while it is up; the text itself is untouched.
	m.setEditing(false)
	m.setFocus(panelQuery)
	verb := "explain"
	var ctx context.Context
	var cancel context.CancelFunc
	if analyzed {
		// An analyzed run takes as long as the query does, so it gets no
		// timeout — ctrl+c, as for any other query, is how it stops.
		verb = "explain analyze"
		ctx, cancel = context.WithCancel(context.Background())
		m.plan.cancel = cancel
		m.keys.CancelQuery.SetEnabled(true)
	} else {
		ctx, cancel = context.WithTimeout(context.Background(), explainTimeout)
	}
	return tea.Batch(
		logCmd("-- %s %s on %s…", verb, db.FirstKeyword(stmt), m.active),
		explainCmd(ctx, cancel, id, m.driver, stmt, analyzed),
	)
}

// explainCmd fetches one plan. The statement itself lands in the command
// log through the Driver's Logger, like every other statement lazysql
// runs — nothing here re-formats it.
func explainCmd(ctx context.Context, cancel context.CancelFunc, id int, drv db.Driver, sql string, analyzed bool) tea.Cmd {
	return func() tea.Msg {
		defer cancel()
		var plan *db.Plan
		var err error
		if analyzed {
			plan, err = drv.ExplainAnalyze(ctx, sql)
		} else {
			plan, err = drv.Explain(ctx, sql)
		}
		return explainDoneMsg{id: id, stmt: sql, analyzed: analyzed, plan: plan, err: err}
	}
}

// finishExplain lands the plan (or the failure) in the view.
func (m *Model) finishExplain(msg explainDoneMsg) tea.Cmd {
	if m.plan == nil || msg.id != m.plan.id {
		return nil
	}
	m.plan.running = false
	m.plan.cancel = nil
	if msg.analyzed && !m.query.run.running {
		m.keys.CancelQuery.SetEnabled(false)
	}
	verb := "explain"
	if msg.analyzed {
		verb = "explain analyze"
	}
	switch {
	case errors.Is(msg.err, context.Canceled):
		m.plan.err = "cancelled"
		return logCmd("-- %s cancelled", verb)
	case msg.err != nil:
		m.plan.err = msg.err.Error()
		return logCmd("-- %s FAILED: %v", verb, msg.err)
	}
	m.plan.plan = msg.plan
	m.plan.lines = msg.plan.Lines()
	return logCmd("-- %s plan for %s: %d lines",
		planKind(msg.plan.Analyzed), db.FirstKeyword(msg.stmt), len(m.plan.lines))
}

// closePlan is `esc` on an open plan: back to the editor, buffer intact.
func (m *Model) closePlan() { m.dropPlan() }

// dropPlan clears the plan view, cancelling an analyzed run still in
// flight: nothing would be left to show its result, and the statement
// should not keep running unseen.
func (m *Model) dropPlan() {
	p := m.plan
	if p == nil {
		return
	}
	if p.running && p.cancel != nil {
		p.cancel()
		if !m.query.run.running {
			m.keys.CancelQuery.SetEnabled(false)
		}
	}
	m.plan = nil
}

// planKind names which of the two plans is on screen. An estimated plan
// and an analyzed one look alike line for line, so every place that
// shows one says which it is.
func planKind(analyzed bool) string {
	if analyzed {
		return "analyzed"
	}
	return "estimated"
}

// editorOffset is the caret's rune offset into the buffer. The textarea
// reports a row and a column; the statement splitter works in offsets,
// so the rows above the caret are measured here.
func (m Model) editorOffset() int {
	lines := strings.Split(m.script(), "\n")
	row := m.query.editor.area.Line()
	if row >= len(lines) {
		row = len(lines) - 1
	}
	off := 0
	for i := 0; i < row; i++ {
		off += len([]rune(lines[i])) + 1 // the newline
	}
	col := m.query.editor.area.Column()
	if row >= 0 && row < len(lines) {
		if n := len([]rune(lines[row])); col > n {
			col = n
		}
	}
	return off + col
}

// updatePlanKeys owns the keyboard while a plan is on screen. Keys it
// does not claim fall through to the editor's normal mode, so `i` or
// `ctrl+r` go back to working on the buffer — both of which close the
// plan on their way.
func (m Model) updatePlanKeys(msg tea.KeyPressMsg) (Model, tea.Cmd, bool) {
	p := m.plan
	k := m.keys
	switch {
	case key.Matches(msg, k.Back):
		if p.running && p.analyzed {
			return m, logCmd("-- explain analyze still running — %s cancels it",
				k.CancelQuery.Help().Key), true
		}
		if p.running {
			return m, logCmd("-- plan still loading…"), true
		}
		m.closePlan()
		return m, nil, true
	case key.Matches(msg, k.Down):
		p.offset++
		return m, nil, true
	case key.Matches(msg, k.Up):
		p.offset--
		return m, nil, true
	case key.Matches(msg, k.NextPage):
		p.offset += 10
		return m, nil, true
	case key.Matches(msg, k.PrevPage):
		p.offset -= 10
		return m, nil, true
	case key.Matches(msg, k.CopyMenu):
		if p.plan == nil {
			return m, nil, true
		}
		return m, copyTextCmd("query plan", "query-plan.txt", p.plan.RenderText()), true
	}
	switch msg.String() {
	case "g", "home":
		p.offset = 0
		return m, nil, true
	case "G", "end":
		p.offset = len(p.lines)
		return m, nil, true
	}
	return m, nil, false
}

// ---------- rendering ----------

// planContent is the main view while a plan is open: the request's
// progress line, the failure, or the scrollable plan.
func (m Model) planContent(w, h int) string {
	p := m.plan
	s := m.style
	lines := []string{
		s.muted.Render(truncate(firstLine(p.stmt), w)),
	}
	switch {
	case p.running && p.analyzed:
		lines = append(lines, "", s.pending.Render(truncate(
			"executing and measuring… "+m.keys.CancelQuery.Help().Key+" cancels", w)))
	case p.running:
		lines = append(lines, "", s.pending.Render("planning…"))
	case p.err != "":
		what := "explain"
		if p.analyzed {
			what = "explain analyze"
		}
		lines = append(lines, "", s.danger.Render(truncate(what+" failed: "+p.err, w)))
		lines = append(lines, "", s.keyHint.Render("esc back to the editor"))
	default:
		body := maxInt(h-3, 0) // statement, kind, footer — the header is the border title
		lines = append(lines, m.planKindLine(w))
		lines = append(lines, scrollLines(p.lines, p.offset, w, body)...)
		hint := fmt.Sprintf("%d lines — j/k scroll · y copy · esc back to the editor", len(p.lines))
		lines = append(lines, s.keyHint.Render(truncate(hint, w)))
	}
	return joinTruncated(lines, w, h)
}

// planKindLine is the row under the statement that says which plan this
// is and what its figures mean. The analyzed one is in the danger colour:
// it is the plan whose statement actually ran.
func (m Model) planKindLine(w int) string {
	if m.plan.analyzed {
		return m.style.danger.Render(truncate(
			"ANALYZED — the statement was executed; figures are measured", w))
	}
	return m.style.muted.Render(truncate(
		"ESTIMATED — not executed; figures are the planner's estimates", w))
}

// planTitle is the main view's border title while a plan is open. It
// names the plan's kind, so the two are told apart even at a glance.
func (m Model) planTitle() string {
	s := m.style
	title := s.titleFocused.Render("Query plan ("+planKind(m.plan.analyzed)+")") +
		s.muted.Render(" — "+m.plan.engine)
	if m.active != "" {
		title += s.muted.Render(" · " + m.active + " / " + displayDatabase(m.database))
	}
	return title
}

// firstLine is the statement's first line, marked when it has more —
// the header names what was explained, it does not reprint the script.
func firstLine(sql string) string {
	if i := strings.IndexByte(sql, '\n'); i >= 0 {
		return strings.TrimSpace(sql[:i]) + " …"
	}
	return sql
}
