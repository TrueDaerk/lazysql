package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// ctrlE is the explain key as the terminal reports it.
func ctrlE() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl} }

// explain types a script into the editor and explains it.
func explain(t *testing.T, m Model, script string) Model {
	t.Helper()
	m = send(t, m, press(':'))
	m.setScript(script)
	return send(t, m, ctrlE())
}

func TestExplainShowsAPlanAndKeepsTheBuffer(t *testing.T) {
	script := "SELECT id FROM q ORDER BY id"
	m := explain(t, queryable(t), script)

	if m.plan == nil {
		t.Fatal("ctrl+e opened no plan")
	}
	if m.plan.running {
		t.Fatal("the plan is still marked running after the reply landed")
	}
	if m.plan.err != "" {
		t.Fatalf("explain failed: %s", m.plan.err)
	}
	if len(m.plan.lines) == 0 {
		t.Fatal("the plan has no lines")
	}
	if m.script() != script {
		t.Fatalf("buffer = %q, want it unchanged by the explain", m.script())
	}
	if m.focus != panelQuery {
		t.Fatalf("focus = %v, want to stay in the editor panel", m.focus)
	}
	if m.editor.editing {
		t.Fatal("ctrl+e stayed in insert mode — the plan would swallow no keys")
	}
	// The plan replaces the editor in the main view; it names itself in
	// the box's top border, not in a content row.
	out := m.mainContent(100, 24)
	if !strings.Contains(m.mainTitle(100), "Query plan") {
		t.Fatalf("the main view is not titled for the plan:\n%s", m.mainTitle(100))
	}
	if !strings.Contains(strings.ToLower(out), "q") {
		t.Fatalf("the plan does not mention the relation:\n%s", out)
	}
}

func TestExplainAppearsInTheCommandLog(t *testing.T) {
	m := explain(t, queryable(t), "SELECT id FROM q")
	var found bool
	for _, e := range m.commandLogEntries() {
		if strings.Contains(e.text, "EXPLAIN QUERY PLAN") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the EXPLAIN statement is missing from the command log:\n%v",
			m.commandLogEntries())
	}
}

func TestEscClosesThePlanAndReturnsToTheEditor(t *testing.T) {
	script := "SELECT id FROM q"
	m := explain(t, queryable(t), script)
	m = send(t, m, special(tea.KeyEscape, 0))
	if m.plan != nil {
		t.Fatal("esc did not close the plan")
	}
	if m.focus != panelQuery {
		t.Fatalf("focus = %v, want the editor panel", m.focus)
	}
	if m.script() != script {
		t.Fatalf("buffer = %q, want it intact", m.script())
	}
	if title := m.mainTitle(100); !strings.Contains(title, "Query editor") {
		t.Fatalf("the editor did not come back: title = %q", title)
	}
}

// The plan scrolls with the same keys every other report uses, and j/k
// must not reach the vim layer while it is up.
func TestPlanScrolls(t *testing.T) {
	m := explain(t, queryable(t), "SELECT id FROM q ORDER BY id")
	m = send(t, m, press('j'))
	if m.plan.offset != 1 {
		t.Fatalf("offset after j = %d, want 1", m.plan.offset)
	}
	m = send(t, m, press('k'))
	if m.plan.offset != 0 {
		t.Fatalf("offset after k = %d, want 0", m.plan.offset)
	}
	m = send(t, m, press('G'))
	if m.plan.offset != len(m.plan.lines) {
		t.Fatalf("offset after G = %d, want %d", m.plan.offset, len(m.plan.lines))
	}
}

// Typing puts the editor back: the plan was covering it, not replacing it.
func TestInsertModeClosesThePlan(t *testing.T) {
	m := explain(t, queryable(t), "SELECT id FROM q")
	m = send(t, m, press('i'))
	if m.plan != nil {
		t.Fatal("entering insert mode left the plan on screen")
	}
	if !m.editor.editing {
		t.Fatal("`i` did not start insert mode")
	}
}

// A multi-statement buffer explains the statement the caret is in.
func TestExplainPicksTheStatementUnderTheCursor(t *testing.T) {
	m := queryable(t)
	m = send(t, m, press(':'))
	m.setScript("SELECT id FROM q;\nSELECT name FROM q;")
	// setScript leaves the caret at the end, which is inside the second
	// statement.
	m = send(t, m, ctrlE())
	if m.plan == nil {
		t.Fatal("no plan")
	}
	if m.plan.stmt != "SELECT name FROM q" {
		t.Fatalf("explained %q, want the statement under the cursor", m.plan.stmt)
	}

	// With the caret moved back into the first statement, that one is
	// explained instead.
	m.plan = nil
	m.editor.area.MoveToBegin()
	m = send(t, m, ctrlE())
	if m.plan == nil {
		t.Fatal("no plan for the first statement")
	}
	if m.plan.stmt != "SELECT id FROM q" {
		t.Fatalf("explained %q, want the first statement", m.plan.stmt)
	}
}

func TestExplainWithoutAConnection(t *testing.T) {
	m := sized(120, 40)
	m = send(t, m, press(':'))
	m.setScript("SELECT 1")
	m = send(t, m, ctrlE())
	if m.plan != nil {
		t.Fatal("explaining without a connection opened a plan")
	}
	if !logHas(m, "explain skipped: not connected") {
		t.Fatalf("no skip reason in the log:\n%v", m.commandLogEntries())
	}
}

func TestExplainEmptyBuffer(t *testing.T) {
	m := explain(t, queryable(t), "   ")
	if m.plan != nil {
		t.Fatal("an empty buffer opened a plan")
	}
	if !logHas(m, "nothing to explain") {
		t.Fatalf("no skip reason in the log:\n%v", m.commandLogEntries())
	}
}

// A statement with placeholders has no values to plan with; the message
// names the key that binds them.
func TestExplainPlaceholderStatement(t *testing.T) {
	m := explain(t, queryable(t), "SELECT * FROM q WHERE id = ?")
	if m.plan != nil {
		t.Fatal("a placeholder statement opened a plan")
	}
	if !logHas(m, "has placeholders") {
		t.Fatalf("no skip reason in the log:\n%v", m.commandLogEntries())
	}
}

func TestExplainFailureIsReported(t *testing.T) {
	m := explain(t, queryable(t), "SELECT * FROM no_such_relation")
	if m.plan == nil {
		t.Fatal("a failed explain closed the view")
	}
	if m.plan.err == "" {
		t.Fatal("the failure was not recorded in the view")
	}
	out := m.mainContent(100, 24)
	if !strings.Contains(out, "explain failed") {
		t.Fatalf("the main view does not report the failure:\n%s", out)
	}
	if !logHas(m, "explain FAILED") {
		t.Fatalf("no failure in the log:\n%v", m.commandLogEntries())
	}
}

// Every key in the options bar is documented in `?`, ctrl+e included.
func TestExplainIsDocumented(t *testing.T) {
	m := sized(120, 40)
	m = send(t, m, press('3'))
	m = send(t, m, press('?'))
	if m.modal == nil {
		t.Fatal("`?` opened no modal")
	}
	out := m.modal.view(m.style, 120, 40)
	if !strings.Contains(out, "ctrl+e") {
		t.Fatalf("ctrl+e is missing from the help:\n%s", out)
	}
}

// logHas reports whether any command-log line contains want.
func logHas(m Model, want string) bool {
	for _, e := range m.commandLogEntries() {
		if strings.Contains(e.text, want) {
			return true
		}
	}
	return false
}
