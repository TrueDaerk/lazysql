package ui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/db"
)

// ctrlA is the explain-analyze key as the terminal reports it.
func ctrlA() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl} }

// duckQueryable is queryable with the session swapped for an in-memory
// DuckDB one: SQLite has no EXPLAIN ANALYZE to exercise.
func duckQueryable(t *testing.T) Model {
	t.Helper()
	m := queryable(t)
	drv, err := db.Open(db.EngineDuckDB)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := drv.Connect(ctx, ""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { drv.Close() })
	for _, s := range []string{
		`CREATE TABLE q (id INTEGER PRIMARY KEY, name TEXT)`,
		`INSERT INTO q VALUES (1, 'a'), (2, 'b'), (3, 'c')`,
	} {
		if _, err := drv.Exec(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	m.driver = drv
	return m
}

// analyze types a script into the editor, leaves insert mode — ctrl+a is
// a normal-mode key — and presses it.
func analyze(t *testing.T, m Model, script string) Model {
	t.Helper()
	m = send(t, m, press(':'))
	m.setScript(script)
	m = send(t, m, special(tea.KeyEscape, 0))
	return send(t, m, ctrlA())
}

func qRowCount(t *testing.T, m Model) int64 {
	t.Helper()
	n, err := m.driver.CountRows(context.Background(), "", "q", nil)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestExplainAnalyzeConfirmsBeforeRunning(t *testing.T) {
	m := analyze(t, duckQueryable(t), "SELECT * FROM q")
	c, ok := m.modal.(*confirmModal)
	if !ok {
		t.Fatalf("ctrl+a opened %T, want a confirm modal", m.modal)
	}
	if !strings.Contains(c.body, "EXECUTES") || !strings.Contains(c.body, "SELECT * FROM q") {
		t.Fatalf("the confirm does not say the statement will be executed:\n%s", c.body)
	}
	if m.plan != nil {
		t.Fatal("a plan was requested before the confirm")
	}
	if logHas(m, "EXPLAIN ANALYZE") {
		t.Fatal("EXPLAIN ANALYZE reached the server before the confirm")
	}

	m = send(t, m, press('y'))
	if m.plan == nil || m.plan.err != "" {
		t.Fatalf("no analyzed plan after confirming: %+v", m.plan)
	}
	if !m.plan.analyzed || !m.plan.plan.Analyzed {
		t.Fatal("the plan is not marked analyzed")
	}
	if !logHas(m, "EXPLAIN ANALYZE SELECT * FROM q") {
		t.Fatalf("the analyzed statement is missing from the command log:\n%v", m.commandLogEntries())
	}
	if m.keys.CancelQuery.Enabled() {
		t.Error("ctrl+c is still the cancel key after the analyzed run finished")
	}
}

func TestExplainAnalyzeEscCancelsTheConfirm(t *testing.T) {
	m := analyze(t, duckQueryable(t), "SELECT * FROM q")
	m = send(t, m, special(tea.KeyEscape, 0))
	if m.modal != nil || m.plan != nil {
		t.Fatal("esc on the confirm did not back out cleanly")
	}
	if logHas(m, "EXPLAIN ANALYZE") {
		t.Fatal("a cancelled confirm still ran EXPLAIN ANALYZE")
	}
}

// A write is refused with an explanation and never offered a confirm.
func TestExplainAnalyzeRefusesAWrite(t *testing.T) {
	m := duckQueryable(t)
	before := qRowCount(t, m)
	m = analyze(t, m, "DELETE FROM q")
	c, ok := m.modal.(*confirmModal)
	if !ok {
		t.Fatalf("a write opened %T, want the refusal", m.modal)
	}
	if c.onConfirm != nil {
		t.Fatal("the refusal can be confirmed")
	}
	if !strings.Contains(c.body, "write") {
		t.Fatalf("the refusal does not explain itself:\n%s", c.body)
	}
	m = send(t, m, press('y'))
	if m.plan != nil {
		t.Fatal("a refused write opened a plan")
	}
	if !logHas(m, "explain analyze refused") {
		t.Fatalf("no refusal in the log:\n%v", m.commandLogEntries())
	}
	if n := qRowCount(t, m); n != before {
		t.Fatalf("rows = %d after a refused analyze, want %d", n, before)
	}
}

// An engine without an analyzing EXPLAIN says so instead of offering it.
func TestExplainAnalyzeUnsupportedEngine(t *testing.T) {
	m := analyze(t, queryable(t), "SELECT * FROM q")
	c, ok := m.modal.(*confirmModal)
	if !ok {
		t.Fatalf("ctrl+a on SQLite opened %T, want the explanation", m.modal)
	}
	if c.onConfirm != nil {
		t.Fatal("an unsupported engine was offered the action")
	}
	if !strings.Contains(c.body, "SQLite has no EXPLAIN ANALYZE") {
		t.Fatalf("the modal does not say why:\n%s", c.body)
	}
	if !logHas(m, "explain analyze unavailable") {
		t.Fatalf("nothing in the log:\n%v", m.commandLogEntries())
	}
}

// The two plans read alike line for line; the view must always say which
// one is on screen.
func TestPlanViewLabelsEstimatedAndAnalyzedApart(t *testing.T) {
	m := explain(t, duckQueryable(t), "SELECT * FROM q")
	estTitle, estBody := m.mainTitle(120), m.mainContent(120, 30)
	if !strings.Contains(estTitle, "(estimated)") || !strings.Contains(estBody, "ESTIMATED") {
		t.Fatalf("the estimated plan is not labelled:\n%s\n%s", estTitle, estBody)
	}
	if strings.Contains(estTitle, "analyzed") || strings.Contains(estBody, "ANALYZED") {
		t.Fatalf("the estimated plan claims to be analyzed:\n%s\n%s", estTitle, estBody)
	}

	m = send(t, m, special(tea.KeyEscape, 0), ctrlA(), press('y'))
	if m.plan == nil || !m.plan.analyzed {
		t.Fatal("no analyzed plan")
	}
	anTitle, anBody := m.mainTitle(120), m.mainContent(120, 30)
	if !strings.Contains(anTitle, "(analyzed)") || !strings.Contains(anBody, "ANALYZED") {
		t.Fatalf("the analyzed plan is not labelled:\n%s\n%s", anTitle, anBody)
	}
	if strings.Contains(anTitle, "estimated") || strings.Contains(anBody, "ESTIMATED") {
		t.Fatalf("the analyzed plan claims to be estimated:\n%s\n%s", anTitle, anBody)
	}
}

// ctrl+c while an analyzed run is in flight cancels it — and does not
// quit the app, which is what ctrl+c means in normal mode otherwise.
func TestExplainAnalyzeIsCancellable(t *testing.T) {
	m := duckQueryable(t)
	m = send(t, m, press(':'), special(tea.KeyEscape, 0))
	cancelled := false
	m.plan = &planView{id: 7, running: true, analyzed: true, stmt: "SELECT 1",
		cancel: func() { cancelled = true }}
	m.keys.CancelQuery.SetEnabled(true)

	next, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m = next.(Model)
	if !cancelled {
		t.Fatal("ctrl+c did not cancel the analyzed run")
	}
	for _, msg := range drain(cmd) {
		if _, quit := msg.(tea.QuitMsg); quit {
			t.Fatal("ctrl+c quit the app instead of cancelling")
		}
	}
	m = send(t, m, explainDoneMsg{id: 7, stmt: "SELECT 1", analyzed: true, err: context.Canceled})
	if m.plan == nil || m.plan.err != "cancelled" {
		t.Fatalf("plan after cancel = %+v", m.plan)
	}
	if m.keys.CancelQuery.Enabled() {
		t.Error("ctrl+c is still the cancel key after the run ended")
	}
	if !logHas(m, "explain analyze cancelled") {
		t.Fatalf("no cancellation in the log:\n%v", m.commandLogEntries())
	}
}

// Leaving the plan while an analyzed run is in flight stops the run too.
func TestDroppingARunningAnalyzedPlanCancelsIt(t *testing.T) {
	m := duckQueryable(t)
	cancelled := false
	m.plan = &planView{running: true, analyzed: true, cancel: func() { cancelled = true }}
	m.setEditing(true)
	if !cancelled {
		t.Fatal("leaving the plan left its statement running")
	}
}

func TestExplainAnalyzeIsDocumented(t *testing.T) {
	m := sized(120, 40)
	m = send(t, m, press('3'))
	m = send(t, m, press('?'))
	if m.modal == nil {
		t.Fatal("`?` opened no modal")
	}
	out := m.modal.view(m.style, 120, 40)
	if !strings.Contains(out, "ctrl+a") || !strings.Contains(out, "explain analyze") {
		t.Fatalf("ctrl+a is missing from the help:\n%s", out)
	}
}
