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

// `A` opens the report in the main view and hands it the focus there —
// it is not an overlay on the panel that opened it.
func TestActivityOpensFocusedInTheMainView(t *testing.T) {
	m := serverModel(t, false)
	m = send(t, m, press('1'), press('A'))
	if m.activity == nil {
		t.Fatal("`A` opened no activity report")
	}
	if m.focus != panelMain {
		t.Fatalf("focus = %v, want the main view", m.focus)
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
	if m.activity.grid.row != 1 {
		t.Fatalf("cursor after j = %d, want 1", m.activity.grid.row)
	}
	m = send(t, m, press('k'))
	if m.activity.grid.row != 0 {
		t.Fatalf("cursor after k = %d, want 0", m.activity.grid.row)
	}
	m = send(t, m, press('G'))
	if m.activity.grid.row != 3 {
		t.Fatalf("cursor after G = %d, want the last row", m.activity.grid.row)
	}
	// The cursor never leaves the list, however hard the key is held.
	m = send(t, m, press('j'), press('j'))
	if m.activity.grid.row != 3 {
		t.Fatalf("cursor = %d, want it clamped to the last row", m.activity.grid.row)
	}
	m = send(t, m, press('g'))
	if m.activity.grid.row != 0 {
		t.Fatalf("cursor after g = %d, want 0", m.activity.grid.row)
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

// `v` shows the cell under the cursor in full — on the Query column,
// that is the server-truncated statement the table cannot render whole.
func TestActivityViewsTheStatement(t *testing.T) {
	m := openReport(t, serverModel(t, false), fixtureProcesses())
	m = toColumn(t, m, "Query")
	m = send(t, m, press('v'))
	cm, ok := m.modal.(*cellModal)
	if !ok {
		t.Fatalf("`v` opened %T, want the cell popup", m.modal)
	}
	if cm.rawText != "SELECT * FROM orders" {
		t.Fatalf("the popup shows %q, want the session's statement", cm.rawText)
	}
}

// `x` opens the whole session as a field list, the way it does for a
// grid row too wide to read across.
func TestActivitySessionDetail(t *testing.T) {
	m := openReport(t, serverModel(t, false), fixtureProcesses())
	m = send(t, m, press('x'))
	rd, ok := m.modal.(*rowDetailModal)
	if !ok {
		t.Fatalf("`x` opened %T, want the row detail popup", m.modal)
	}
	if rd.subject != "session 42" {
		t.Fatalf("detail subject = %q, want the session under the cursor", rd.subject)
	}
	if len(rd.fields) != len(activityHeaders) {
		t.Fatalf("detail has %d fields, want one per column", len(rd.fields))
	}
	if rd.fields[len(rd.fields)-1].text != "SELECT * FROM orders" {
		t.Fatalf("the last field is %q, want the statement", rd.fields[len(rd.fields)-1].text)
	}
}

// toColumn walks the cell cursor onto a named column with `l`, the way a
// user would.
func toColumn(t *testing.T, m Model, header string) Model {
	t.Helper()
	for i, h := range activityHeaders {
		if h != header {
			continue
		}
		for m.activity.grid.col < i {
			m = send(t, m, press('l'))
		}
		return m
	}
	t.Fatalf("no column named %q", header)
	return m
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

// The report holds the keyboard in the main view, not over panel [1]:
// `1` takes the focus back and the panel is a plain list again, while
// the list stays on screen beside it. This is issue #174.
func TestActivityReleasesTheKeysToTheFocusedPanel(t *testing.T) {
	m := openReport(t, serverModel(t, false), fixtureProcesses())
	m = send(t, m, press('1'))
	if m.focus != panelConnections {
		t.Fatalf("focus = %v, want panel [1]", m.focus)
	}
	if m.activity == nil {
		t.Fatal("`1` closed the report instead of releasing its keys")
	}

	before := m.panels[panelConnections].cursor
	m = send(t, m, press('j'))
	if got := m.panels[panelConnections].cursor; got != before+1 {
		t.Fatalf("connections cursor = %d, want %d — the report swallowed `j`", got, before+1)
	}
	if m.activity.grid.row != 0 {
		t.Fatalf("activity cursor = %d, want it untouched by the panel's `j`", m.activity.grid.row)
	}
	// `enter` on panel [1] is connect, not a report key.
	m = send(t, m, special(tea.KeyEnter, 0))
	if !logHas(m, "local-postgres") {
		t.Fatalf("`enter` did not act on the connections panel:\n%v", m.commandLogEntries())
	}

	// The list is still what the main view draws, and it says how to get
	// back to it rather than offering keys it no longer owns.
	out := m.activityContent(120, 20)
	if !strings.Contains(out, "4 sessions") {
		t.Fatalf("the report left the main view:\n%s", out)
	}
	if strings.Contains(out, "K kill") {
		t.Fatalf("the blurred report still offers its keys:\n%s", out)
	}
	// And the options bar is the panel's again.
	bar := m.renderOptionsBar()
	if strings.Contains(bar, "kill session") {
		t.Fatalf("the blurred report still owns the options bar:\n%s", bar)
	}
	if !strings.Contains(bar, "new connection") {
		t.Fatalf("the options bar is not panel [1]'s:\n%s", bar)
	}
}

// tab reaches the report the same way it reaches the grid, and `A` from
// the panel takes the focus back to a list it left open.
func TestActivityIsInTheTabOrder(t *testing.T) {
	m := openReport(t, serverModel(t, false), fixtureProcesses())
	m = send(t, m, press('1'))
	for i := 0; i < int(panelCount) && m.focus != panelMain; i++ {
		m = send(t, m, special(tea.KeyTab, 0))
	}
	if m.focus != panelMain {
		t.Fatalf("tab never reached the report: focus = %v", m.focus)
	}
	m = send(t, m, press('j'))
	if m.activity.grid.row != 1 {
		t.Fatalf("cursor = %d, want the refocused report to take `j`", m.activity.grid.row)
	}

	m = send(t, m, press('1'), press('A'))
	if m.focus != panelMain {
		t.Fatalf("focus = %v, want `A` to take the keyboard back to the report", m.focus)
	}
}

// While the report is focused `?` documents its keys, not the grid's.
func TestActivityHelpFollowsTheFocus(t *testing.T) {
	m := openReport(t, serverModel(t, false), fixtureProcesses())
	m = send(t, m, press('?'))
	if m.modal == nil {
		t.Fatal("`?` opened no modal")
	}
	out := m.modal.view(m.style, 120, 40)
	if !strings.Contains(out, "Server activity") {
		t.Fatalf("the help is not the report's:\n%s", out)
	}
	for _, want := range []string{"kill session", "auto-refresh"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q is missing from the help:\n%s", want, out)
		}
	}
	if strings.Contains(out, "edit cell") {
		t.Fatalf("the help still lists the grid's actions:\n%s", out)
	}
}

// A click puts the cursor on the row that was clicked, and the wheel
// walks the list the way j/k does. The main view of a 120x40 model
// starts at x=40; the grid spends its first two content rows on the
// column names and the rule under them.
func TestActivityMouse(t *testing.T) {
	m := openReport(t, serverModel(t, false), fixtureProcesses())
	// The click is mapped through the window the last frame rendered.
	m.activityContent(80, 28)

	// y=1 is the column names, y=2 the rule and y=3 the first session, so
	// y=5 is the third row of the list.
	m = send(t, m, click(60, 5))
	if m.activity.grid.row != 2 {
		t.Fatalf("cursor after the click = %d, want the clicked row", m.activity.grid.row)
	}
	if m.focus != panelMain {
		t.Fatalf("focus = %v, want the click to keep the report focused", m.focus)
	}
	// A click on the header row is not a row: it leaves the cursor alone.
	m = send(t, m, click(60, 1))
	if m.activity.grid.row != 2 {
		t.Fatalf("cursor after clicking the header = %d, want it unmoved", m.activity.grid.row)
	}
	// The wheel walks rows, clamped to the list.
	m = send(t, m, wheelUp(60, 5))
	if m.activity.grid.row != 0 {
		t.Fatalf("cursor after a wheel notch up = %d, want 0", m.activity.grid.row)
	}
	m = send(t, m, wheelDown(60, 5))
	if m.activity.grid.row != 3 {
		t.Fatalf("cursor after a wheel notch down = %d, want the last row", m.activity.grid.row)
	}
	// A click from a focused side panel focuses the report again.
	m = send(t, m, press('1'), click(60, 4))
	if m.focus != panelMain || m.activity.grid.row != 1 {
		t.Fatalf("focus = %v cursor = %d, want the click to refocus and select",
			m.focus, m.activity.grid.row)
	}
}

// A click lands on a cell, not just a row: the column under the pointer
// becomes the cursor column, so `y` copies what was clicked.
func TestActivityClickPicksAColumn(t *testing.T) {
	m := openReport(t, serverModel(t, false), fixtureProcesses())
	m.activityContent(80, 28)

	// The first column is "PID"; the second starts one separator past it.
	x := 40 + 1 + m.activity.grid.cols[0].width + colGap
	m = send(t, m, click(x, 3))
	if m.activity.grid.col != 1 {
		t.Fatalf("column after the click = %d, want the second column", m.activity.grid.col)
	}
}

// Opening a relation takes the main view back: the report and the grid
// cannot both own the box.
func TestActivityClosesWhenARelationOpens(t *testing.T) {
	m := openReport(t, dataBrowsing(t), nil)
	m = send(t, m, press('2'))
	if !m.panels[panelObjects].selectByName("grid") {
		t.Fatalf("fixture table not listed: %v", m.panels[panelObjects].items)
	}
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.activity != nil {
		t.Fatal("the report survived a relation opening in the main view")
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
