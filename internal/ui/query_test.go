package ui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/db"
)

// queryable is a connected model with a small table to query.
func queryable(t *testing.T) Model {
	t.Helper()
	m := browsing(t)
	if _, err := m.driver.Exec(context.Background(),
		`CREATE TABLE IF NOT EXISTS q (id INTEGER PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := m.driver.Exec(context.Background(), `DELETE FROM q`); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		if _, err := m.driver.Exec(context.Background(),
			`INSERT INTO q (id, name) VALUES (?, ?)`, i, "row"); err != nil {
			t.Fatal(err)
		}
	}
	// Start from an empty history so the assertions below count only
	// what the test itself ran.
	m.history = nil
	m.refreshHistory()
	m.commandLog = nil
	return m
}

// runQuery types a script into the editor panel and runs it.
func runQuery(t *testing.T, m Model, script string) Model {
	t.Helper()
	m = send(t, m, press(':'))
	if m.focus != panelQuery || !m.editor.editing {
		t.Fatalf("`:` left focus on %v (editing=%v), want insert mode in panel [5]",
			m.focus, m.editor.editing)
	}
	m.setScript(script)
	return send(t, m, tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
}

func TestColonFocusesTheEditorPanelWithTheBuffer(t *testing.T) {
	m := sized(120, 40)
	m.setScript("SELECT 1")
	m = send(t, m, press(':'))
	if m.modal != nil {
		t.Fatalf("`:` opened %T, want the persistent panel", m.modal)
	}
	if m.focus != panelQuery {
		t.Fatalf("focus = %v, want the query panel", m.focus)
	}
	if !m.editor.editing {
		t.Fatal("`:` did not start insert mode")
	}
	if m.script() != "SELECT 1" {
		t.Fatalf("editor holds %q, want the kept buffer", m.script())
	}
}

func TestDigitFiveFocusesTheEditorPanel(t *testing.T) {
	m := sized(120, 40)
	m = send(t, m, press('5'))
	if m.focus != panelQuery {
		t.Fatalf("focus = %v, want the query panel", m.focus)
	}
	// `5` jumps to the panel, it does not start typing: the panel's own
	// keys have to stay reachable.
	if m.editor.editing {
		t.Fatal("`5` started insert mode")
	}
	if out := m.View().Content; !strings.Contains(out, "[5] Query") {
		t.Fatalf("the layout has no [5] Query panel:\n%s", out)
	}
}

func TestEscLeavesInsertModeAndKeepsTheBuffer(t *testing.T) {
	m := sized(120, 40)
	m = send(t, m, press(':'))
	m = send(t, m, press('S'), press('E'), press('L'))
	if m.script() != "SEL" {
		t.Fatalf("buffer = %q, want the typed text", m.script())
	}
	// Typing opened the completion popup, and the first esc is its: it
	// closes the popup and leaves insert mode alone.
	if !m.completion.open {
		t.Fatal("typing SEL did not open the completion popup")
	}
	m = send(t, m, special(tea.KeyEscape, 0))
	if !m.editor.editing {
		t.Fatal("esc left insert mode instead of only closing the popup")
	}
	m = send(t, m, special(tea.KeyEscape, 0))
	if m.editor.editing {
		t.Fatal("esc did not leave insert mode")
	}
	if m.focus != panelQuery {
		t.Fatalf("focus = %v, want to stay on the panel", m.focus)
	}
	if m.script() != "SEL" {
		t.Fatalf("buffer = %q, want esc to keep it", m.script())
	}
	// A second esc backs out of the panel, still without losing the text.
	m = send(t, m, special(tea.KeyEscape, 0))
	if m.focus == panelQuery {
		t.Fatal("esc in normal mode did not back out of the panel")
	}
	if m.script() != "SEL" {
		t.Fatalf("buffer = %q, want it to survive leaving the panel", m.script())
	}
}

// Insert mode has to swallow the keys that would otherwise quit, jump or
// open a modal — they are ordinary characters in a statement.
func TestInsertModeSwallowsGlobalKeys(t *testing.T) {
	m := sized(120, 40)
	m = send(t, m, press(':'))
	m = send(t, m, press('q'), press('?'), press('2'))
	if m.script() != "q?2" {
		t.Fatalf("buffer = %q, want the global keys typed into it", m.script())
	}
	if m.modal != nil {
		t.Fatalf("a key typed into the buffer opened %T", m.modal)
	}
	if m.focus != panelQuery {
		t.Fatalf("focus = %v, want the query panel", m.focus)
	}
}

func TestRunKeepsTheEditorOpenWithItsContent(t *testing.T) {
	m := runQuery(t, queryable(t), "SELECT id FROM q ORDER BY id")
	if m.script() != "SELECT id FROM q ORDER BY id" {
		t.Fatalf("buffer = %q, want it unchanged by the run", m.script())
	}
	if m.focus != panelQuery {
		t.Fatalf("focus = %v, want to stay in the editor", m.focus)
	}
	// The run ends insert mode so the result is navigable straight away.
	if m.editor.editing {
		t.Fatal("ctrl+r stayed in insert mode")
	}
	if !m.data.isQuery() || len(m.data.rows) != 3 {
		t.Fatalf("the result did not land in the main view: %#v", m.data)
	}
	// The main view shows both the buffer and the result.
	out := m.mainContent(100, 24)
	if !strings.Contains(out, "SELECT id FROM q") {
		t.Fatalf("the editor is missing from the main view:\n%s", out)
	}
	if !strings.Contains(out, "id") {
		t.Fatalf("the result is missing from the main view:\n%s", out)
	}
}

// Running from the history panel must not overwrite what is being
// written in the editor.
func TestRunningFromHistoryLeavesTheBufferAlone(t *testing.T) {
	m := queryable(t)
	m.setScript("SELECT 1")
	m = runQuery(t, m, "SELECT id FROM q")
	m = send(t, m, press('4'), press('x'))
	if m.script() != "SELECT id FROM q" {
		t.Fatalf("buffer = %q, want the history run to leave it alone", m.script())
	}
	if m.focus != panelMain {
		t.Fatalf("focus = %v, want the grid for a run started outside the editor", m.focus)
	}
}

// `D` on the editor panel asks before it throws work away.
func TestClearBufferAsksFirst(t *testing.T) {
	m := sized(120, 40)
	m.setScript("SELECT 1")
	m = send(t, m, press('5'), press('D'))
	if _, ok := m.modal.(*confirmModal); !ok {
		t.Fatalf("D opened %T, want a confirm modal", m.modal)
	}
	m = send(t, m, special(tea.KeyEscape, 0))
	if m.script() != "SELECT 1" {
		t.Fatalf("buffer = %q, want the declined clear to keep it", m.script())
	}
	m = send(t, m, press('D'), special(tea.KeyEnter, 0))
	if m.script() != "" {
		t.Fatalf("buffer = %q, want it cleared", m.script())
	}
}

// Every key the editor panel advertises is documented in `?`, in both
// modes.
func TestEditorKeysAreDocumented(t *testing.T) {
	k := newKeyMap()
	documented := map[string]bool{}
	for _, group := range k.helpGroups(panelQuery) {
		for _, b := range group {
			documented[b.Help().Key] = true
		}
	}
	for _, b := range append(k.optionsBarBindings(panelQuery), k.editorInsert()...) {
		if !documented[b.Help().Key] {
			t.Errorf("the options bar shows %q which `?` omits", b.Help().Key)
		}
	}
}

func TestSelectRendersInTheDataTab(t *testing.T) {
	m := runQuery(t, queryable(t), "SELECT id, name FROM q ORDER BY id")

	if !m.data.isQuery() {
		t.Fatal("the Data tab is not showing a query result")
	}
	if m.data.browsing() {
		t.Fatal("a query result must not look like a browsed relation")
	}
	if len(m.data.rows) != 3 || len(m.data.cols) != 2 {
		t.Fatalf("result = %d rows x %d cols, want 3x2", len(m.data.rows), len(m.data.cols))
	}
	if !logContains(m, "SELECT id, name FROM q ORDER BY id;") {
		t.Fatalf("the statement is missing from the command log: %v", m.commandLog)
	}
	if !logContains(m, "3 rows") {
		t.Fatalf("the row count is missing from the command log: %v", m.commandLog)
	}
	// The grid renders it with the browsing renderer.
	if out := m.dataContent(100, 20); !strings.Contains(out, "name") {
		t.Fatalf("grid did not render the result columns:\n%s", out)
	}
}

func TestQueryResultPaginatesInMemory(t *testing.T) {
	m := queryable(t)
	// A result wider than one page, without touching the server again.
	m = runQuery(t, m, "WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i < 250) SELECT i FROM n")
	if len(m.data.all) != 250 {
		t.Fatalf("materialized %d rows, want 250", len(m.data.all))
	}
	if len(m.data.rows) != dataPageSize {
		t.Fatalf("page 1 has %d rows, want %d", len(m.data.rows), dataPageSize)
	}
	if got := m.data.pageCount(); got != 3 {
		t.Fatalf("pageCount() = %d, want 3", got)
	}
	m = send(t, m, tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	if m.data.page != 1 || m.data.rows[0][0] != int64(dataPageSize+1) {
		t.Fatalf("after ctrl+f: page %d starting at %v, want page 1 starting at 101",
			m.data.page, m.data.rows[0][0])
	}
	m = send(t, m, tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl},
		tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	if m.data.page != 2 {
		t.Fatalf("page = %d, want to stop on the last page", m.data.page)
	}
	if !logContains(m, "already on the last page") {
		t.Fatalf("paging past the end said nothing: %v", m.commandLog)
	}
	m = send(t, m, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	if m.data.page != 1 {
		t.Fatalf("after ctrl+b: page = %d, want 1", m.data.page)
	}
}

func TestGuardedDMLRunsWithoutAsking(t *testing.T) {
	m := queryable(t)
	m = runQuery(t, m, "DELETE FROM q WHERE id = 3")

	if m.modal != nil {
		t.Fatalf("a DELETE with a WHERE clause opened %T, want no modal", m.modal)
	}
	if !logContains(m, "1 row affected") {
		t.Fatalf("the guarded DELETE did not report its row count: %v", m.commandLog)
	}
	if m.data.notice == "" || !strings.Contains(m.data.notice, "1 row affected") {
		t.Fatalf("data notice = %q, want the affected-row count", m.data.notice)
	}
	if !logContains(m, "DELETE FROM q WHERE id = 3;") {
		t.Fatalf("the statement is missing from the command log: %v", m.commandLog)
	}
}

func TestUnguardedDMLAsksBeforeItRuns(t *testing.T) {
	m := queryable(t)
	m = runQuery(t, m, "DELETE FROM q")

	cm, ok := m.modal.(*confirmModal)
	if !ok {
		t.Fatalf("DELETE without WHERE opened %T, want a confirm modal", m.modal)
	}
	if !cm.danger || !strings.Contains(cm.body, "DELETE FROM q") || !strings.Contains(cm.title, "q") {
		t.Fatalf("the confirm modal does not name the table: %#v", cm)
	}
	// Nothing ran yet.
	if logContains(m, "rows affected") {
		t.Fatalf("the statement executed before the confirmation: %v", m.commandLog)
	}

	m = send(t, m, special(tea.KeyEnter, 0))
	if !logContains(m, "3 rows affected") {
		t.Fatalf("the confirmed DELETE did not report its row count: %v", m.commandLog)
	}
	if !logContains(m, "DELETE FROM q;") {
		t.Fatalf("the statement is missing from the command log: %v", m.commandLog)
	}
}

func TestCancelledUnguardedDMLDoesNotRun(t *testing.T) {
	m := queryable(t)
	m = runQuery(t, m, "DELETE FROM q")
	m = send(t, m, special(tea.KeyEscape, 0))
	if m.modal != nil {
		t.Fatalf("esc left %T open", m.modal)
	}
	if m.run.running || logContains(m, "rows affected") {
		t.Fatalf("the declined statement ran anyway: %v", m.commandLog)
	}
}

func TestInsertNeverAsks(t *testing.T) {
	m := queryable(t)
	m = runQuery(t, m, "INSERT INTO q (id, name) VALUES (9, 'x')")

	if m.modal != nil {
		t.Fatalf("INSERT opened %T, want no modal", m.modal)
	}
	if !logContains(m, "1 row affected") {
		t.Fatalf("the INSERT did not report its row count: %v", m.commandLog)
	}
}

func TestMultipleStatementsRunInOrderAndAllAreLogged(t *testing.T) {
	m := queryable(t)
	m = runQuery(t, m,
		"UPDATE q SET name = 'a' WHERE id = 1;\nSELECT id FROM q WHERE id = 1;\nSELECT name FROM q WHERE id = 1;")

	// The UPDATE carries a WHERE clause, so it runs without a confirm modal.
	if m.modal != nil {
		t.Fatalf("guarded UPDATE opened %T, want none", m.modal)
	}
	for _, want := range []string{
		"UPDATE q SET name = 'a' WHERE id = 1;",
		"SELECT id FROM q WHERE id = 1;",
		"SELECT name FROM q WHERE id = 1;",
	} {
		if !logContains(m, want) {
			t.Fatalf("statement %q missing from the command log: %v", want, m.commandLog)
		}
	}
	// The last SELECT is the one on screen.
	if len(m.data.cols) != 1 || m.data.cols[0].Name != "name" {
		t.Fatalf("Data tab shows %v, want the last SELECT's columns", m.data.cols)
	}
	// The write did not replace the rows.
	if m.data.notice != "" {
		t.Fatalf("notice = %q, want the SELECT result to win", m.data.notice)
	}
	if len(m.history) != 3 {
		t.Fatalf("history has %d entries, want one per statement: %v", len(m.history), m.history)
	}
}

func TestFailingStatementStopsTheScript(t *testing.T) {
	m := queryable(t)
	m = runQuery(t, m, "SELECT * FROM nope; SELECT 1")
	if logContains(m, "SELECT 1;") {
		t.Fatalf("the script continued past a failure: %v", m.commandLog)
	}
	if !logContains(m, "FAILED") {
		t.Fatalf("the failure was not reported: %v", m.commandLog)
	}
	if m.run.running {
		t.Fatal("the run is still marked as in flight")
	}
}

func TestCancelKeyIsOnlyBoundWhileAQueryRuns(t *testing.T) {
	m := queryable(t)
	if m.keys.CancelQuery.Enabled() {
		t.Fatal("ctrl+c is bound to cancel with nothing running — it must still quit")
	}
	// Enter the running state without a worker, then cancel it the way
	// the key handler does.
	m.run.running = true
	m.run.cancel = func() {}
	m.keys.CancelQuery.SetEnabled(true)
	handled, next, _ := m.updateGlobal(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if !handled {
		t.Fatal("ctrl+c was not handled while a query was running")
	}
	if _, ok := next.(Model); !ok {
		t.Fatalf("updateGlobal returned %T", next)
	}
	// finishQuery clears it again.
	mm := next.(Model)
	mm.finishQuery(queryDoneMsg{id: mm.run.id})
	if mm.keys.CancelQuery.Enabled() {
		t.Fatal("ctrl+c stayed bound to cancel after the run finished")
	}
}

// longQuery counts far enough that the engine is still working when the
// test cancels it. The driver honours the context mid-statement, which
// is what makes a runaway query interruptible.
const longQuery = `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i < 200000000) SELECT count(*) FROM n`

func TestWorkerAbortsAStatementInFlight(t *testing.T) {
	m := queryable(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := make(chan tea.Msg)
	go queryJob{id: 1, ctx: ctx, drv: m.driver, stmts: []string{longQuery}, ch: ch}.run()

	// Cancel while the statement is running, not before it starts.
	time.AfterFunc(300*time.Millisecond, cancel)

	deadline := time.After(20 * time.Second)
	var sawStmt bool
	for {
		select {
		case msg := <-ch:
			switch msg := msg.(type) {
			case queryStmtMsg:
				sawStmt = true
				if !errors.Is(msg.err, context.Canceled) {
					t.Fatalf("statement error = %v, want context.Canceled", msg.err)
				}
			case queryDoneMsg:
				if !errors.Is(msg.err, context.Canceled) {
					t.Fatalf("run error = %v, want context.Canceled", msg.err)
				}
				if !sawStmt {
					t.Fatal("the run closed without reporting the aborted statement")
				}
				return
			}
		case <-deadline:
			t.Fatal("the worker did not stop after its context was cancelled")
		}
	}
}

func TestQueryRunIsCancellable(t *testing.T) {
	m := queryable(t)
	// A long recursive scan the SQLite driver checks the context in.
	script := "WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i < 20000000) SELECT count(*) FROM n"
	stmts := db.SplitStatements(m.driver.Engine(), script)
	cmd := m.startQuery(stmts)
	if !m.run.running {
		t.Fatal("startQuery did not mark the run as in flight")
	}
	// Cancel before draining, so the worker sees a dead context.
	m.cancelQuery()
	for _, msg := range drain(cmd) {
		next, c := m.Update(msg)
		m = next.(Model)
		for _, msg := range drain(c) {
			next, _ := m.Update(msg)
			m = next.(Model)
		}
	}
	if m.run.running {
		t.Fatal("the cancelled run is still in flight")
	}
	if m.driver == nil {
		t.Fatal("cancelling a query closed the connection")
	}
	// The app is still usable: a plain query works afterwards.
	m = runQuery(t, m, "SELECT 1")
	if len(m.data.rows) != 1 {
		t.Fatalf("the model did not recover from a cancellation: %#v", m.data)
	}
}

func TestQueryResultHasNoTableActions(t *testing.T) {
	m := runQuery(t, queryable(t), "SELECT id FROM q")
	m = send(t, m, press('E')) // export
	if m.modal != nil {
		t.Fatalf("export opened %T for a query result", m.modal)
	}
	if !logContains(m, "export skipped") {
		t.Fatalf("export said nothing: %v", m.commandLog)
	}
	// The introspection tabs describe a relation, so they stay away.
	m = send(t, m, press(']'))
	if m.tab != mainTabData {
		t.Fatalf("tab = %v, want the Data tab to stay", m.tab)
	}
}

func TestQueryStatementsLandInTheHistory(t *testing.T) {
	m := runQuery(t, queryable(t), "SELECT id FROM q")
	if len(m.history) != 1 {
		t.Fatalf("history = %#v, want the statement", m.history)
	}
	e := m.history[0]
	if e.SQL != "SELECT id FROM q" {
		t.Fatalf("stored %q, want the statement without its terminator", e.SQL)
	}
	if e.Engine != string(db.EngineSQLite) {
		t.Fatalf("engine = %q, want the engine it ran on", e.Engine)
	}
	if e.At.IsZero() {
		t.Fatal("the entry has no timestamp")
	}
	// Re-running the newest entry does not duplicate it.
	m = runQuery(t, m, "SELECT id FROM q")
	if len(m.history) != 1 {
		t.Fatalf("history = %#v, want the replay folded into the newest entry", m.history)
	}
}
