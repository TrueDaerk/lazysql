package ui

import (
	"context"
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

	plan   *db.Plan
	lines  []string
	err    string
	offset int
}

// explainDoneMsg carries one finished plan request.
type explainDoneMsg struct {
	id   int
	stmt string
	plan *db.Plan
	err  error
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

	id := 1
	if m.plan != nil {
		id = m.plan.id + 1
	}
	m.plan = &planView{
		id: id, running: true,
		stmt:   span.SQL,
		engine: m.driver.Dialect().DisplayName(),
	}
	// The plan replaces the editor in the main view, so the buffer stops
	// taking keys while it is up; the text itself is untouched.
	m.setEditing(false)
	m.setFocus(panelQuery)
	return tea.Batch(
		logCmd("-- explain %s on %s…", db.FirstKeyword(span.SQL), m.active),
		explainCmd(id, m.driver, span.SQL),
	)
}

// explainCmd fetches one plan. The statement itself lands in the command
// log through the Driver's Logger, like every other statement lazysql
// runs — nothing here re-formats it.
func explainCmd(id int, drv db.Driver, sql string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), explainTimeout)
		defer cancel()
		plan, err := drv.Explain(ctx, sql)
		return explainDoneMsg{id: id, stmt: sql, plan: plan, err: err}
	}
}

// finishExplain lands the plan (or the failure) in the view.
func (m *Model) finishExplain(msg explainDoneMsg) tea.Cmd {
	if m.plan == nil || msg.id != m.plan.id {
		return nil
	}
	m.plan.running = false
	if msg.err != nil {
		m.plan.err = msg.err.Error()
		return logCmd("-- explain FAILED: %v", msg.err)
	}
	m.plan.plan = msg.plan
	m.plan.lines = msg.plan.Lines()
	return logCmd("-- plan for %s: %d lines", db.FirstKeyword(msg.stmt), len(m.plan.lines))
}

// closePlan is `esc` on an open plan: back to the editor, buffer intact.
func (m *Model) closePlan() { m.plan = nil }

// editorOffset is the caret's rune offset into the buffer. The textarea
// reports a row and a column; the statement splitter works in offsets,
// so the rows above the caret are measured here.
func (m Model) editorOffset() int {
	lines := strings.Split(m.script(), "\n")
	row := m.editor.area.Line()
	if row >= len(lines) {
		row = len(lines) - 1
	}
	off := 0
	for i := 0; i < row; i++ {
		off += len([]rune(lines[i])) + 1 // the newline
	}
	col := m.editor.area.Column()
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
	case p.running:
		lines = append(lines, "", s.pending.Render("planning…"))
	case p.err != "":
		lines = append(lines, "", s.danger.Render(truncate("explain failed: "+p.err, w)))
		lines = append(lines, "", s.keyHint.Render("esc back to the editor"))
	default:
		body := maxInt(h-2, 0) // statement, footer — the header is the border title
		lines = append(lines, scrollLines(p.lines, p.offset, w, body)...)
		hint := fmt.Sprintf("%d lines — j/k scroll · y copy · esc back to the editor", len(p.lines))
		lines = append(lines, s.keyHint.Render(truncate(hint, w)))
	}
	return joinTruncated(lines, w, h)
}

// planTitle is the main view's border title while a plan is open.
func (m Model) planTitle() string {
	s := m.style
	title := s.titleFocused.Render("Query plan") + s.muted.Render(" — "+m.plan.engine)
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
