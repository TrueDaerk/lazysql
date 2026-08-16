package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/db"
)

// fixtureProcesses is a small server: one long query, one session blocked
// behind it, one idle, and lazysql's own connection.
func fixtureProcesses() []db.Process {
	return []db.Process{
		{ID: "42", User: "app", Database: "shop", State: "Query: Sending data",
			Duration: 95 * time.Second, HasDuration: true, Query: "SELECT * FROM orders"},
		{ID: "43", User: "app", Database: "shop", State: "Query: updating",
			Duration: 12 * time.Second, HasDuration: true,
			Query: "UPDATE orders SET total = 1 WHERE id = 7", BlockedBy: []string{"42"}},
		{ID: "44", User: "reporting", Database: "shop", State: "Sleep"},
		{ID: "45", User: "app", Database: "shop", State: "Query", Self: true,
			Duration: time.Second, HasDuration: true, Query: "SELECT id, user FROM processlist"},
	}
}

// serverModel is a model whose active connection is a MySQL profile: the
// report needs an engine that has sessions at all, and the driver is
// never dialed — every assertion here is about what the UI does before
// (and instead of) touching a server.
func serverModel(t *testing.T, readOnly bool) Model {
	t.Helper()
	drv, err := db.OpenOpts(db.EngineMySQL, db.Options{ReadOnly: readOnly})
	if err != nil {
		t.Fatal(err)
	}
	m := sized(120, 40)
	m.driver = drv
	m.active = "local-mysql"
	t.Cleanup(func() { drv.Close() })
	return m
}

// openReport opens the activity view and fills it with rows, the way a
// finished read would. The read the `A` press itself starts cannot reach
// a server, so its failure is overwritten here.
func openReport(t *testing.T, m Model, rows []db.Process) Model {
	t.Helper()
	m = send(t, m, press('1'), press('A'))
	if m.activity == nil {
		t.Fatal("`A` opened no activity report")
	}
	m.commandLog = nil
	return send(t, m, activityLoadedMsg{id: m.activity.id, conn: m.active, rows: rows})
}

func TestActivityOpensFromTheConnectionsPanel(t *testing.T) {
	m := serverModel(t, false)
	m = send(t, m, press('1'), press('A'))
	if m.activity == nil {
		t.Fatal("`A` opened no activity report")
	}
	if m.focus != panelConnections {
		t.Fatalf("focus = %v, want panel [1]", m.focus)
	}
	if !strings.Contains(m.mainTitle(100), "Server activity") {
		t.Fatalf("the main view is not titled for the report:\n%s", m.mainTitle(100))
	}
	if !logHas(m, "server activity on local-mysql") {
		t.Fatalf("the open is missing from the log:\n%v", m.commandLogEntries())
	}
}

func TestActivityListsSessionsAndMarksBlockedOnes(t *testing.T) {
	m := openReport(t, serverModel(t, false), fixtureProcesses())
	out := m.activityContent(120, 20)
	for _, want := range []string{"PID", "Blocked by", "42", "43", "SELECT * FROM orders"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report is missing %q:\n%s", want, out)
		}
	}
	// The blocked session names who it waits for, in its own column.
	if !strings.Contains(out, "1m 35s") {
		t.Errorf("the longest runtime is not rendered:\n%s", out)
	}
	if !strings.Contains(out, "4 sessions") || !strings.Contains(out, "1 blocked") {
		t.Errorf("the footer does not summarize the list:\n%s", out)
	}
	// An idle session has no duration to show, and says so rather than
	// claiming zero.
	if strings.Contains(out, "0.0s") {
		t.Errorf("an idle session was given a runtime:\n%s", out)
	}
}

// The driver sorts; the view must not re-order what it was handed.
func TestActivityKeepsTheDriversOrder(t *testing.T) {
	m := openReport(t, serverModel(t, false), fixtureProcesses())
	if got := m.activity.rows[0].ID; got != "42" {
		t.Fatalf("first row = %s, want the list as ListProcesses sorted it", got)
	}
}

func TestActivityCursorMoves(t *testing.T) {
	m := openReport(t, serverModel(t, false), fixtureProcesses())
	m = send(t, m, press('j'))
	if m.activity.cursor != 1 {
		t.Fatalf("cursor after j = %d, want 1", m.activity.cursor)
	}
	m = send(t, m, press('k'))
	if m.activity.cursor != 0 {
		t.Fatalf("cursor after k = %d, want 0", m.activity.cursor)
	}
	m = send(t, m, press('G'))
	if m.activity.cursor != 3 {
		t.Fatalf("cursor after G = %d, want the last row", m.activity.cursor)
	}
	// The cursor never leaves the list, however hard the key is held.
	m = send(t, m, press('j'), press('j'))
	if m.activity.cursor != 3 {
		t.Fatalf("cursor = %d, want it clamped to the last row", m.activity.cursor)
	}
	m = send(t, m, press('g'))
	if m.activity.cursor != 0 {
		t.Fatalf("cursor after g = %d, want 0", m.activity.cursor)
	}
}

// `K` never kills anything by itself: it opens a confirm modal that
// spells out the statement, and only that modal runs it.
func TestKillAsksBeforeItKills(t *testing.T) {
	m := openReport(t, serverModel(t, false), fixtureProcesses())
	m = send(t, m, press('K'))
	cm, ok := m.modal.(*confirmModal)
	if !ok {
		t.Fatalf("`K` opened %T, want a confirm modal", m.modal)
	}
	if !cm.danger {
		t.Error("the kill confirm is not marked dangerous")
	}
	if !strings.Contains(cm.body, "KILL CONNECTION 42") {
		t.Fatalf("the confirm does not show the statement:\n%s", cm.body)
	}
	if !strings.Contains(cm.body, "SELECT * FROM orders") {
		t.Fatalf("the confirm does not say what is running:\n%s", cm.body)
	}
	if logHas(m, "kill session 42 on") {
		t.Fatalf("the kill ran before it was confirmed:\n%v", m.commandLogEntries())
	}

	m = send(t, m, special(tea.KeyEnter, 0))
	if m.modal != nil {
		t.Fatalf("the confirm stayed open: %T", m.modal)
	}
	if !logHas(m, "kill session 42 on local-mysql") {
		t.Fatalf("the confirmed kill was not attempted:\n%v", m.commandLogEntries())
	}
}

// esc on the confirm leaves the session alone.
func TestKillCancelled(t *testing.T) {
	m := openReport(t, serverModel(t, false), fixtureProcesses())
	m = send(t, m, press('K'), special(tea.KeyEscape, 0))
	if m.modal != nil {
		t.Fatalf("esc left %T open", m.modal)
	}
	if logHas(m, "kill session 42 on") {
		t.Fatalf("a cancelled confirm still ran the kill:\n%v", m.commandLogEntries())
	}
}

// Killing our own backend would drop the connection the report is read
// through, so the key refuses instead.
func TestKillRefusesLazysqlsOwnSession(t *testing.T) {
	m := openReport(t, serverModel(t, false), fixtureProcesses())
	m = send(t, m, press('G'), press('K')) // the Self row is last
	if m.modal != nil {
		t.Fatalf("killing our own session opened %T", m.modal)
	}
	if !logHas(m, "lazysql's own session") {
		t.Fatalf("no skip reason in the log:\n%v", m.commandLogEntries())
	}
}

func TestKillRefusedOnAReadOnlyConnection(t *testing.T) {
	m := openReport(t, serverModel(t, true), fixtureProcesses())
	m = send(t, m, press('K'))
	if m.modal != nil {
		t.Fatalf("a read-only connection opened %T", m.modal)
	}
	if !logHas(m, "read-only") {
		t.Fatalf("no skip reason in the log:\n%v", m.commandLogEntries())
	}
	// The bar must not offer a key that could only ever refuse.
	bar := m.renderOptionsBar()
	if strings.Contains(bar, "kill session") {
		t.Fatalf("the options bar offers the kill on a read-only connection:\n%s", bar)
	}
}

// A file engine has no server to ask, and the view says so instead of
// showing an empty table.
func TestActivityUnsupportedOnAFileEngine(t *testing.T) {
	m := browsing(t)
	m = send(t, m, press('1'), press('A'))
	if m.activity == nil {
		t.Fatal("`A` opened no report")
	}
	if !m.activity.unsupported {
		t.Fatalf("the report is not marked unsupported: err=%q rows=%d",
			m.activity.err, len(m.activity.rows))
	}
	out := m.activityContent(120, 20)
	if !strings.Contains(out, "no server sessions") {
		t.Fatalf("the view does not explain itself:\n%s", out)
	}
	// And `K` there has nothing to act on.
	m = send(t, m, press('K'))
	if m.modal != nil {
		t.Fatalf("`K` opened %T on an engine with no sessions", m.modal)
	}
}

func TestEscClosesTheActivityReport(t *testing.T) {
	m := openReport(t, serverModel(t, false), fixtureProcesses())
	m = send(t, m, special(tea.KeyEscape, 0))
	if m.activity != nil {
		t.Fatal("esc did not close the report")
	}
	if m.focus != panelConnections {
		t.Fatalf("focus = %v, want panel [1]", m.focus)
	}
	// With the report gone the bar is the panel's again — the keys it
	// claimed are not offered any more.
	if bar := m.renderOptionsBar(); strings.Contains(bar, "kill session") {
		t.Fatalf("the closed report still owns the options bar:\n%s", bar)
	}
}

// Auto-refresh is off until `t` turns it on, and the footer says which of
// the two it is — the interval is not a hidden setting.
func TestActivityAutoRefreshIsOptIn(t *testing.T) {
	m := openReport(t, serverModel(t, false), fixtureProcesses())
	if m.activity.auto {
		t.Fatal("auto-refresh is on before it was asked for")
	}
	if !strings.Contains(m.activityContent(120, 20), "auto-refresh off") {
		t.Fatalf("the footer does not say auto-refresh is off:\n%s", m.activityContent(120, 20))
	}
	// The toggle's command chain is driven by the runtime's timer, so it
	// is not sent through the test harness here — only its state is.
	m.toggleActivityAuto()
	if !m.activity.auto {
		t.Fatal("`t` did not turn auto-refresh on")
	}
	if !strings.Contains(m.activityContent(120, 20), "auto-refresh 5s") {
		t.Fatalf("the footer does not name the interval:\n%s", m.activityContent(120, 20))
	}
	// A beat from a schedule that has been turned off (or restarted) is
	// dropped rather than chained, which is what stops the timer.
	stale := activityTickMsg{gen: m.activity.gen - 1}
	if cmd := m.activityTick(stale); cmd != nil {
		t.Error("a stale auto-refresh beat re-armed the timer")
	}
	m.toggleActivityAuto()
	if m.activity.auto {
		t.Fatal("`t` did not turn auto-refresh off again")
	}
	if cmd := m.activityTick(activityTickMsg{gen: m.activity.gen}); cmd != nil {
		t.Error("a beat kept ticking after auto-refresh was turned off")
	}
}

// A reply for a request the view has moved past — or for another
// connection — must not land.
func TestActivityDropsStaleReplies(t *testing.T) {
	m := openReport(t, serverModel(t, false), fixtureProcesses())
	m = send(t, m, activityLoadedMsg{id: m.activity.id - 1, conn: m.active, rows: nil})
	if len(m.activity.rows) != 4 {
		t.Fatalf("a stale reply replaced the list: %d rows", len(m.activity.rows))
	}
	m = send(t, m, activityLoadedMsg{id: m.activity.id, conn: "somewhere-else", rows: nil})
	if len(m.activity.rows) != 4 {
		t.Fatalf("another connection's reply replaced the list: %d rows", len(m.activity.rows))
	}
}

// The sessions belong to the connection they were read from: leaving it
// closes the report rather than leaving a list of dead pids on screen.
func TestActivityClosesWithTheConnection(t *testing.T) {
	m := openReport(t, serverModel(t, false), fixtureProcesses())
	m.resetBrowse()
	if m.activity != nil {
		t.Fatal("the report survived the connection it was read from")
	}
}

// `v` shows a server-truncated statement in full — the one thing the
// table cannot render whole.
func TestActivityViewsTheStatement(t *testing.T) {
	m := openReport(t, serverModel(t, false), fixtureProcesses())
	m = send(t, m, press('v'))
	cm, ok := m.modal.(*cellModal)
	if !ok {
		t.Fatalf("`v` opened %T, want the cell popup", m.modal)
	}
	if cm.rawText != "SELECT * FROM orders" {
		t.Fatalf("the popup shows %q, want the session's statement", cm.rawText)
	}
}

// Every key the report claims is in `?` under panel [1], including the
// two that shadow the panel's own bindings.
func TestActivityKeysAreDocumented(t *testing.T) {
	m := sized(120, 40)
	m = send(t, m, press('1'), press('?'))
	if m.modal == nil {
		t.Fatal("`?` opened no modal")
	}
	out := m.modal.view(m.style, 120, 40)
	for _, want := range []string{"server activity", "kill session", "auto-refresh"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q is missing from the help:\n%s", want, out)
		}
	}
}

// The options bar follows the report: while it is open it offers the
// report's keys, not the panel's.
func TestActivityOptionsBar(t *testing.T) {
	m := openReport(t, serverModel(t, false), fixtureProcesses())
	bar := m.renderOptionsBar()
	for _, want := range []string{"kill session", "auto-refresh", "reload from server"} {
		if !strings.Contains(bar, want) {
			t.Errorf("the options bar is missing %q:\n%s", want, bar)
		}
	}
	if strings.Contains(bar, "move up") {
		t.Errorf("the options bar still offers the panel's `K`:\n%s", bar)
	}
}

func TestFormatProcessDuration(t *testing.T) {
	tests := []struct {
		p    db.Process
		want string
	}{
		{db.Process{}, "—"},
		{db.Process{Duration: 400 * time.Millisecond, HasDuration: true}, "0.4s"},
		{db.Process{Duration: 95 * time.Second, HasDuration: true}, "1m 35s"},
		{db.Process{Duration: 2*time.Hour + 14*time.Minute, HasDuration: true}, "2h 14m"},
	}
	for _, tt := range tests {
		if got := formatProcessDuration(tt.p); got != tt.want {
			t.Errorf("formatProcessDuration(%v) = %q, want %q", tt.p.Duration, got, tt.want)
		}
	}
}
