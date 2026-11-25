package ui

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"lazysql/internal/config"
	"lazysql/internal/db"
	"lazysql/internal/session"
)

// sessionFixture is a model over two SQLite files with different tables
// (alpha: users, orders — beta: widgets) plus an in-memory DuckDB profile,
// nothing connected yet and the cursor of panel [1] on alpha.
func sessionFixture(t *testing.T) Model {
	t.Helper()
	dir := t.TempDir()
	alpha := filepath.Join(dir, "alpha.sqlite")
	beta := filepath.Join(dir, "beta.sqlite")
	seedSQLiteFile(t, alpha, []string{
		`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)`,
		`INSERT INTO users (id, name) VALUES (1,'ann'),(2,'bob'),(3,'cyd'),(4,'dee'),(5,'eve')`,
		`CREATE TABLE orders (id INTEGER PRIMARY KEY, total INTEGER)`,
	})
	seedSQLiteFile(t, beta, []string{
		`CREATE TABLE widgets (id INTEGER PRIMARY KEY, label TEXT)`,
		`INSERT INTO widgets (id, label) VALUES (1,'w1'),(2,'w2')`,
	})
	m := sized(120, 40)
	m.cfg = &config.Config{Connections: []config.Connection{
		{Name: "alpha", Engine: db.EngineSQLite, File: alpha},
		{Name: "beta", Engine: db.EngineSQLite, File: beta},
		{Name: "gamma", Engine: db.EngineDuckDB},
	}}
	m.refreshConnections("alpha")
	return m
}

// showConn is `enter` on a profile in panel [1]: it dials one that is not
// connected and switches to one that is.
func showConn(t *testing.T, m Model, name string) Model {
	t.Helper()
	m = send(t, m, press('1'))
	if !m.panels[panelConnections].selectByName(name) {
		t.Fatalf("profile %q is not in panel [1]: %v", name, m.panels[panelConnections].items)
	}
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.active != name {
		t.Fatalf("on screen = %q after enter on %q; log: %v", m.active, name, m.commandLogEntries())
	}
	return m
}

// openRelation opens a table of the session on screen through the tree.
func openRelation(t *testing.T, m Model, name string) Model {
	t.Helper()
	m = send(t, m, press('2'))
	if !m.panels[panelObjects].selectByName(name) {
		t.Fatalf("relation %q is not in the tree: %v", name, m.panels[panelObjects].items)
	}
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.table != name || m.focus != panelMain {
		t.Fatalf("table = %q focus = %v, want %q open in the grid", m.table, m.focus, name)
	}
	return m
}

// twoSessions connects alpha, opens users there, then connects beta and
// opens widgets: beta is on screen, alpha in the background.
func twoSessions(t *testing.T) Model {
	t.Helper()
	m := sessionFixture(t)
	m = openRelation(t, showConn(t, m, "alpha"), "users")
	m = openRelation(t, showConn(t, m, "beta"), "widgets")
	return m
}

func countLog(m Model, want string) int {
	n := 0
	for _, l := range m.commandLogEntries() {
		if strings.Contains(l.text, want) {
			n++
		}
	}
	return n
}

func TestSecondConnectKeepsFirstOpen(t *testing.T) {
	m := sessionFixture(t)
	defer func() { m.closeSessions() }()
	m = showConn(t, m, "alpha")
	alphaDrv := m.driver
	m = showConn(t, m, "beta")

	if m.driver == alphaDrv || m.driver == nil {
		t.Fatal("beta did not get a driver of its own")
	}
	if !m.connected("alpha") || !m.connected("beta") {
		t.Fatalf("connected: alpha=%v beta=%v, want both", m.connected("alpha"), m.connected("beta"))
	}
	// The first connection is still usable: nothing closed it.
	if n := qRows(t, alphaDrv, "users"); n != 5 {
		t.Fatalf("alpha's driver counts %d users, want 5", n)
	}
	if got := m.panels[panelObjects].items; !containsName(got, "widgets") || containsName(got, "users") {
		t.Fatalf("panel [2] = %v, want beta's tree", got)
	}
	if logContains(m, "-- disconnect alpha") {
		t.Fatalf("connecting beta disconnected alpha: %v", m.commandLogEntries())
	}
}

// The round trip: everything the user built up on A is there again after a
// detour to B, and getting back costs no dial and no query.
func TestSwitchRestoresSessionExactly(t *testing.T) {
	m := sessionFixture(t)
	defer func() { m.closeSessions() }()
	m = openRelation(t, showConn(t, m, "alpha"), "users")

	m = applyWhereFilter(t, m, "id > 1")
	m = send(t, m, press('l'), press('s'), press('s')) // sort by name, descending
	m = send(t, m, press('j'), press('j'))
	cmd := m.setMainTab(mainTabStructure)
	m = send(t, m, drain(cmd)...)
	m = send(t, m, press('2'))
	if !m.panels[panelObjects].selectByName("Views") {
		t.Fatalf("no Views category in %v", m.panels[panelObjects].items)
	}
	m = send(t, m, special(tea.KeyEnter, 0))
	if !m.tree.category("", catViews).expanded {
		t.Fatal("the Views category did not expand")
	}

	type snapshot struct {
		table, filter, sortCol, treeSel string
		sortDesc                        bool
		row, col, rows                  int
		tab                             mainTab
		tree                            *objectTree
		driver                          db.Driver
	}
	snap := func(m Model) snapshot {
		s := snapshot{
			table: m.table, row: m.grid.data.row, col: m.grid.data.col, rows: len(m.grid.data.rows),
			tab: m.tab, tree: m.tree, driver: m.driver, treeSel: m.panels[panelObjects].selected(),
		}
		if f := m.grid.data.filter; f != nil {
			s.filter = f.Raw
		}
		if so := m.grid.data.sort; so != nil {
			s.sortCol, s.sortDesc = so.Column, so.Desc
		}
		return s
	}
	want := snap(m)
	if want.filter != "id > 1" || want.sortCol != "name" || !want.sortDesc || want.row != 2 ||
		want.col != 1 || want.rows != 4 || want.tab != mainTabStructure || want.treeSel != "Views" {
		t.Fatalf("setup did not land where the test expects: %+v", want)
	}
	statements := len(want.driver.Logger().Entries())

	m = openRelation(t, showConn(t, m, "beta"), "widgets")
	if m.grid.data.filter != nil || m.grid.data.sort != nil || m.tab != mainTabData {
		t.Fatalf("alpha's grid state leaked into beta: filter=%v sort=%v tab=%v",
			m.grid.data.filter, m.grid.data.sort, m.tab)
	}

	m = showConn(t, m, "alpha")
	if got := snap(m); got != want {
		t.Fatalf("alpha after the round trip:\n got %+v\nwant %+v", got, want)
	}
	// The keyboard is back in the panel alpha was left in, so the session
	// is what the screen shows, not the profile's detail view.
	if m.focus != panelObjects {
		t.Fatalf("focus after the switch = %v, want the object tree alpha was left in", m.focus)
	}
	if !m.tree.category("", catViews).expanded || !m.tree.category("", catTables).expanded {
		t.Fatal("the tree lost its expansion state")
	}
	if got := len(m.driver.Logger().Entries()); got != statements {
		t.Fatalf("switching back ran %d statement(s) on alpha, want none", got-statements)
	}
	if n := countLog(m, "-- connect alpha"); n != 1 {
		t.Fatalf("alpha was dialled %d times, want once", n)
	}
	if !logContains(m, "-- switch to alpha") {
		t.Fatalf("the switch is not in the log: %v", m.commandLogEntries())
	}
}

func TestStagedChangesSurviveRoundTrip(t *testing.T) {
	m := sessionFixture(t)
	defer func() { m.closeSessions() }()
	m = openRelation(t, showConn(t, m, "alpha"), "users")
	m = send(t, m, press('l'))
	m = stageEdit(t, m, "anna")
	if m.grid.changes.Len() != 1 {
		t.Fatalf("changeset = %d, want the edit staged", m.grid.changes.Len())
	}
	staged := m.grid.changes

	m = openRelation(t, showConn(t, m, "beta"), "widgets")
	if m.grid.changes == staged || m.grid.changes.Len() != 0 {
		t.Fatalf("beta shows alpha's staged changes (%d)", m.grid.changes.Len())
	}
	if out := ansi.Strip(m.View().Content); strings.Contains(out, "1 staged change") {
		t.Fatalf("beta's screen carries a staged-changes badge:\n%s", out)
	}
	// Committing in beta has nothing to commit, and must not touch alpha.
	m = send(t, m, press('c'))
	if _, ok := m.modal.(*confirmModal); ok {
		t.Fatal("beta offered to commit a changeset it does not have")
	}

	m = showConn(t, m, "alpha")
	if m.grid.changes != staged || m.grid.changes.Len() != 1 {
		t.Fatalf("alpha's changeset after the round trip = %d, want 1", m.grid.changes.Len())
	}
	m.setFocus(panelMain)
	m = send(t, m, press('c'))
	if _, ok := m.modal.(*confirmModal); !ok {
		t.Fatalf("c opened %T, want the commit confirmation", m.modal)
	}
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.grid.changes.Len() != 0 {
		t.Fatalf("changeset = %d after commit; log: %v", m.grid.changes.Len(), m.commandLogEntries())
	}
	rs, err := m.driver.Query(context.Background(), "SELECT name FROM users WHERE id = 1")
	if err != nil {
		t.Fatal(err)
	}
	if rs.Rows[0][0] != "anna" {
		t.Fatalf("users.name = %v, want the staged edit committed", rs.Rows[0][0])
	}
}

func TestTransactionSurvivesRoundTrip(t *testing.T) {
	m := sessionFixture(t)
	defer func() { m.closeSessions() }()
	m = showConn(t, m, "alpha")
	m = inTx(t, m)
	m = runQuery(t, m, "INSERT INTO users (id, name) VALUES (9, 'zed')")
	handle := m.query.tx.handle

	m = showConn(t, m, "beta")
	if m.query.tx != nil || m.txBadge() != "" {
		t.Fatal("beta shows alpha's transaction")
	}
	// B on beta opens beta's own transaction; alpha's is not in the way.
	m = send(t, m, press('3'), press('B'))
	if m.query.tx == nil || m.query.tx.conn != "beta" || m.query.tx.handle == handle {
		t.Fatalf("beta could not open its own transaction: %+v", m.query.tx)
	}
	m = send(t, m, press('U'))
	if m.query.tx != nil {
		t.Fatal("beta's empty transaction did not roll back")
	}

	m = showConn(t, m, "alpha")
	if m.query.tx == nil || m.query.tx.handle != handle || m.query.tx.stmts != 1 {
		t.Fatalf("alpha's transaction after the round trip = %+v", m.query.tx)
	}
	if n := qRows(t, m.driver, "users"); n != 5 {
		t.Fatalf("pool sees %d users before the commit, want 5", n)
	}
	m = send(t, m, press('3'), press('C'), special(tea.KeyEnter, 0))
	if m.query.tx != nil {
		t.Fatalf("the transaction is still open after the commit; log: %v", m.commandLogEntries())
	}
	if n := qRows(t, m.driver, "users"); n != 6 {
		t.Fatalf("users after the commit = %d, want 6", n)
	}

	// The other way out: a second transaction survives the detour and is
	// rolled back afterwards.
	m = inTx(t, m)
	m = runQuery(t, m, "DELETE FROM users WHERE id > 0")
	m = showConn(t, showConn(t, m, "beta"), "alpha")
	m = send(t, m, press('3'), press('U'), special(tea.KeyEnter, 0))
	if m.query.tx != nil {
		t.Fatal("the transaction is still open after the rollback")
	}
	if n := qRows(t, m.driver, "users"); n != 6 {
		t.Fatalf("users after the rollback = %d, want 6", n)
	}
}

func TestEditorBufferAndResultArePerSession(t *testing.T) {
	m := sessionFixture(t)
	defer func() { m.closeSessions() }()
	m = showConn(t, m, "alpha")
	m = runQuery(t, m, "SELECT name FROM users WHERE id <= 3")
	if !m.grid.data.isQuery() || len(m.grid.data.all) != 3 {
		t.Fatalf("alpha's result = %d rows (query=%v)", len(m.grid.data.all), m.grid.data.isQuery())
	}

	m = showConn(t, m, "beta")
	if m.script() != "" || m.grid.data.open() || m.query.run.outcome != "" {
		t.Fatalf("beta starts with alpha's editor state: script=%q open=%v", m.script(), m.grid.data.open())
	}
	m = runQuery(t, m, "SELECT label FROM widgets")
	if len(m.grid.data.all) != 2 {
		t.Fatalf("beta's result = %d rows, want 2", len(m.grid.data.all))
	}

	m = showConn(t, m, "alpha")
	if m.script() != "SELECT name FROM users WHERE id <= 3" || len(m.grid.data.all) != 3 ||
		m.grid.data.cols[0].Name != "name" {
		t.Fatalf("alpha after the round trip: script=%q rows=%d", m.script(), len(m.grid.data.all))
	}
	m = showConn(t, m, "beta")
	if m.script() != "SELECT label FROM widgets" || len(m.grid.data.all) != 2 {
		t.Fatalf("beta after the round trip: script=%q rows=%d", m.script(), len(m.grid.data.all))
	}
}

// A query started on alpha is still out when the user switches to beta:
// its reply is held back here, delivered once beta is on screen, and has
// to land in alpha's session.
func TestBackgroundQueryResultLandsInItsSession(t *testing.T) {
	m := sessionFixture(t)
	defer func() { m.closeSessions() }()
	m = showConn(t, m, "alpha")
	m = showConn(t, m, "beta")
	m = runQuery(t, m, "SELECT label FROM widgets")
	m = showConn(t, m, "alpha")

	m = send(t, m, press(':'))
	m.setScript("SELECT name FROM users WHERE id <= 3")
	next, cmd := m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	m = next.(Model)
	held := drain(cmd)
	if !m.query.run.running {
		t.Fatal("the run did not start")
	}

	m = showConn(t, m, "beta")
	if m.query.run.running {
		t.Fatal("beta shows alpha's run as its own")
	}
	p := m.panels[panelConnections]
	if st := p.allStatus[0]; st != statusBusy {
		t.Fatalf("alpha's row status = %v, want busy", st)
	}
	if out := ansi.Strip(m.View().Content); !strings.Contains(out, backgroundMark+" alpha "+busyTag) {
		t.Fatalf("panel [1] does not mark alpha as busy in the background:\n%s", out)
	}
	// The spinner belongs to the session on screen: a tick that arrives
	// now is dropped rather than chained.
	if _, tick := m.Update(spinner.TickMsg{}); tick != nil {
		t.Fatal("the spinner keeps ticking for a run that is not on screen")
	}
	before, focus := m.View().Content, m.focus

	m = send(t, m, held...)
	if m.active != "beta" || m.script() != "SELECT label FROM widgets" ||
		len(m.grid.data.all) != 2 || m.grid.data.cols[0].Name != "label" || m.query.run.outcome == "" {
		t.Fatalf("alpha's reply disturbed beta: script=%q rows=%d", m.script(), len(m.grid.data.all))
	}
	if strings.Contains(m.query.run.outcome, "3 rows") {
		t.Fatalf("beta's status line reports alpha's run: %q", m.query.run.outcome)
	}
	if m.focus != focus || m.modal != nil {
		t.Fatalf("alpha's reply moved the focus to %v (modal %T)", m.focus, m.modal)
	}
	s := m.parkedByName("alpha")
	if s == nil || s.query.run.running || len(s.grid.data.all) != 3 {
		t.Fatalf("alpha's session did not take its result: %+v", s)
	}
	if st := m.panels[panelConnections].allStatus[0]; st != statusBackground {
		t.Fatalf("alpha's row status = %v after the run, want background", st)
	}
	// Only panel [1] and the log may differ: the run's lines, and alpha no
	// longer being busy.
	if after := m.View().Content; after == before {
		t.Fatal("the screen did not pick up alpha's finished run at all")
	}
	if !logContains(m, "[alpha] SELECT name FROM users") || !logContains(m, "[alpha] -- 3 rows") {
		t.Fatalf("alpha's lines are not attributed to it: %v", m.commandLogEntries())
	}

	m = showConn(t, m, "alpha")
	if !m.grid.data.isQuery() || len(m.grid.data.all) != 3 || !strings.Contains(m.query.run.outcome, "3 rows") {
		t.Fatalf("alpha's finished result is not there: rows=%d outcome=%q", len(m.grid.data.all), m.query.run.outcome)
	}
}

// A page load that is still out when the user leaves comes back into the
// grid it was issued for.
func TestBackgroundPageLoadLandsInItsSession(t *testing.T) {
	m := twoSessions(t)
	defer func() { m.closeSessions() }()
	m = showConn(t, m, "alpha")
	m.setFocus(panelMain)
	m.grid.data.rows = nil
	next, cmd := m.Update(press('R'))
	m = next.(Model)
	held := drain(cmd)
	if !m.grid.data.loading {
		t.Fatal("R did not start a reload")
	}

	m = showConn(t, m, "beta")
	widgets := len(m.grid.data.rows)
	m = send(t, m, held...)
	if m.table != "widgets" || len(m.grid.data.rows) != widgets {
		t.Fatalf("alpha's page landed in beta's grid: table=%q rows=%d", m.table, len(m.grid.data.rows))
	}
	s := m.parkedByName("alpha")
	if s.grid.data.loading || len(s.grid.data.rows) != 5 {
		t.Fatalf("alpha's grid: loading=%v rows=%d, want its 5 users", s.grid.data.loading, len(s.grid.data.rows))
	}
}

func TestDisconnectBackgroundSession(t *testing.T) {
	m := twoSessions(t)
	defer func() { m.closeSessions() }()
	alpha := m.parkedByName("alpha")
	alphaDrv, betaDrv := alpha.driver, m.driver

	m = send(t, m, press('1'))
	m.panels[panelConnections].selectByName("alpha")
	m = send(t, m, press('x'))
	if m.modal != nil {
		t.Fatalf("x on a session with nothing to lose opened %T", m.modal)
	}
	if m.connected("alpha") || !m.connected("beta") || m.active != "beta" || m.driver != betaDrv {
		t.Fatalf("after x on alpha: alpha=%v beta=%v on screen=%q", m.connected("alpha"), m.connected("beta"), m.active)
	}
	if m.table != "widgets" || len(m.grid.data.rows) != 2 {
		t.Fatalf("beta's grid was disturbed: table=%q rows=%d", m.table, len(m.grid.data.rows))
	}
	if _, err := alphaDrv.Query(context.Background(), "SELECT 1"); err == nil {
		t.Fatal("alpha's driver is still open")
	}
	if len(m.parked) != 0 {
		t.Fatalf("%d session(s) left in the background", len(m.parked))
	}
	if !logContains(m, "-- disconnect alpha") {
		t.Fatalf("command log = %v", m.commandLogEntries())
	}
	if st := m.connState["alpha"].status; st != statusIdle {
		t.Fatalf("alpha's status = %v, want idle", st)
	}
}

func TestDisconnectBackgroundSessionAsksAboutLosses(t *testing.T) {
	m := sessionFixture(t)
	defer func() { m.closeSessions() }()
	m = openRelation(t, showConn(t, m, "alpha"), "users")
	m = send(t, m, press('l'))
	m = stageEdit(t, m, "anna")
	m = inTx(t, m)
	m = runQuery(t, m, "DELETE FROM users WHERE id = 5")
	m = showConn(t, m, "beta")

	m = send(t, m, press('1'))
	m.panels[panelConnections].selectByName("alpha")
	m = send(t, m, press('x'))
	cm, ok := m.modal.(*confirmModal)
	if !ok {
		t.Fatalf("x opened %T, want a confirmation", m.modal)
	}
	body := ansi.Strip(cm.body)
	for _, want := range []string{"Disconnect alpha", "roll back the open transaction on alpha (1 statement)", "discard 1 staged change"} {
		if !strings.Contains(body, want) {
			t.Fatalf("confirmation %q does not say %q", body, want)
		}
	}
	// esc keeps everything.
	m = send(t, m, special(tea.KeyEscape, 0))
	if !m.connected("alpha") || m.parkedByName("alpha").grid.changes.Len() != 1 {
		t.Fatal("esc on the confirmation closed the session or dropped its changes")
	}

	m = send(t, m, press('x'), special(tea.KeyEnter, 0))
	if m.connected("alpha") || m.active != "beta" {
		t.Fatalf("after the confirmed x: alpha=%v on screen=%q", m.connected("alpha"), m.active)
	}
	if !logContains(m, "transaction on alpha rolled back (disconnect)") {
		t.Fatalf("the rollback is not in the log: %v", m.commandLogEntries())
	}
}

// x on the session on screen leaves the "no connection" state there; the
// other sessions stay open and one enter away.
func TestDisconnectOnScreenKeepsBackgroundSessions(t *testing.T) {
	m := twoSessions(t)
	defer func() { m.closeSessions() }()
	m = send(t, m, press('1'), press('x'))
	if m.active != "" || m.driver != nil || len(m.panels[panelObjects].items) != 0 {
		t.Fatalf("screen after x: active=%q items=%v", m.active, m.panels[panelObjects].items)
	}
	if !m.connected("alpha") {
		t.Fatal("x on beta closed alpha too")
	}
	m = showConn(t, m, "alpha")
	if m.table != "users" || len(m.grid.data.rows) != 5 {
		t.Fatalf("alpha after the switch: table=%q rows=%d", m.table, len(m.grid.data.rows))
	}
}

func TestQuitListsLossesOfEverySessionAndClosesAll(t *testing.T) {
	removeState(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := sessionFixture(t)
	defer func() { m.closeSessions() }()
	m = openRelation(t, showConn(t, m, "alpha"), "users")
	m = send(t, m, press('l'))
	m = stageEdit(t, m, "anna")
	alphaDrv := m.driver
	m = showConn(t, m, "beta")
	m = inTx(t, m)
	m = runQuery(t, m, "DELETE FROM widgets WHERE id > 0")
	betaDrv := m.driver

	m = send(t, m, press('q'))
	cm, ok := m.modal.(*confirmModal)
	if !ok {
		t.Fatalf("q opened %T, want a confirmation", m.modal)
	}
	body := ansi.Strip(cm.body)
	for _, want := range []string{
		"alpha: 1 staged change discarded",
		"beta: open transaction rolled back (1 statement)",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("quit confirmation does not say %q:\n%s", want, body)
		}
	}
	if m.driver == nil || !m.connected("alpha") {
		t.Fatal("q tore sessions down before the confirmation")
	}

	m = send(t, m, special(tea.KeyEnter, 0))
	if m.driver != nil || len(m.parked) != 0 || m.connected("alpha") || m.connected("beta") {
		t.Fatalf("sessions left after quit: on screen=%q parked=%d", m.active, len(m.parked))
	}
	for name, drv := range map[string]db.Driver{"alpha": alphaDrv, "beta": betaDrv} {
		if _, err := drv.Query(context.Background(), "SELECT 1"); err == nil {
			t.Fatalf("%s's driver is still open after quit", name)
		}
	}
	// The transaction was rolled back, not committed, on the way out.
	seedSQLiteFile(t, m.cfg.Connections[1].File, []string{`INSERT INTO widgets (id, label) VALUES (3,'w3')`})
	check, err := db.Open(db.EngineSQLite)
	if err != nil {
		t.Fatal(err)
	}
	dsn, _ := db.BuildDSN(db.EngineSQLite, db.ConnParams{File: m.cfg.Connections[1].File})
	if err := check.Connect(context.Background(), dsn); err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	if n := qRows(t, check, "widgets"); n != 3 {
		t.Fatalf("widgets after quit = %d, want the DELETE rolled back", n)
	}
}

// With one connection open the question keeps its short form.
func TestQuitWithOneSessionKeepsShortQuestion(t *testing.T) {
	m := sessionFixture(t)
	defer func() { m.closeSessions() }()
	m = openRelation(t, showConn(t, m, "alpha"), "users")
	m = send(t, m, press('l'))
	m = stageEdit(t, m, "anna")
	if got := m.quitLosses(); got != "Quit and discard 1 staged change? They are not saved on exit." {
		t.Fatalf("quit question = %q", got)
	}
}

func TestPanelMarksSessionStates(t *testing.T) {
	m := twoSessions(t)
	defer func() { m.closeSessions() }()
	out := ansi.Strip(m.View().Content)
	for _, want := range []string{onScreenMark + " beta", backgroundMark + " alpha"} {
		if !strings.Contains(out, want) {
			t.Fatalf("panel [1] does not show %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "gamma "+busyTag) || strings.Contains(out, onScreenMark+" gamma") ||
		strings.Contains(out, backgroundMark+" gamma") {
		t.Fatalf("an idle profile carries a session mark:\n%s", out)
	}
	p := m.panels[panelConnections]
	if p.allStatus[0] != statusBackground || p.allStatus[1] != statusOK || p.allStatus[2] != statusIdle {
		t.Fatalf("row statuses = %v", p.allStatus)
	}
	// The detail view spells the state out for the selected profile.
	m = send(t, m, press('1'))
	m.panels[panelConnections].selectByName("alpha")
	if d := ansi.Strip(m.connectionDetail(100, 20)); !strings.Contains(d, "connected · in background") {
		t.Fatalf("detail of a background session:\n%s", d)
	}
	m.panels[panelConnections].selectByName("beta")
	if d := ansi.Strip(m.connectionDetail(100, 20)); !strings.Contains(d, "connected · on screen") {
		t.Fatalf("detail of the session on screen:\n%s", d)
	}

	// enter and x are documented with what they do now, in the bar and
	// in `?` alike.
	if !helpMentions(m, panelConnections, "connect / switch to") || !helpMentions(m, panelConnections, "disconnect selected") {
		t.Fatal("`?` does not document enter and x for several connections")
	}
	bar := ansi.Strip(m.renderOptionsBar())
	if !strings.Contains(bar, "enter connect / switch to") || !strings.Contains(bar, "x disconnect selected") {
		t.Fatalf("options bar = %q", bar)
	}
}

// enter on the profile that is already on screen dials nothing: it takes
// the keyboard back to where it was in that session.
func TestEnterOnTheSessionOnScreenDrillsIn(t *testing.T) {
	m := twoSessions(t)
	defer func() { m.closeSessions() }()
	drv := m.driver
	m = showConn(t, m, "beta")
	if m.driver != drv || m.focus != panelMain || m.table != "widgets" {
		t.Fatalf("enter on beta: focus=%v table=%q same driver=%v", m.focus, m.table, m.driver == drv)
	}
	if n := countLog(m, "-- connect beta"); n != 1 {
		t.Fatalf("beta was dialled %d times, want once", n)
	}
}

// A connection that dies in the background is closed and marked failed
// without touching what is on screen.
func TestLostBackgroundConnectionIsMarkedAndClosed(t *testing.T) {
	m := twoSessions(t)
	defer func() { m.closeSessions() }()
	s := m.parkedByName("alpha")
	alphaDrv := s.driver
	lost := fmt.Errorf("read tcp: %w", io.EOF)
	m = send(t, m, sessionMsg{id: s.id, msg: pageLoadedMsg{
		req: s.grid.data.req, conn: "alpha", table: "users", err: lost,
	}})

	if m.connected("alpha") || len(m.parked) != 0 {
		t.Fatalf("alpha is still open: connected=%v parked=%d", m.connected("alpha"), len(m.parked))
	}
	if st := m.connState["alpha"]; st.status != statusError || !strings.Contains(st.lastErr, "EOF") {
		t.Fatalf("alpha's state = %+v, want the error", st)
	}
	if !logContains(m, "connection alpha lost") {
		t.Fatalf("the loss is not in the log: %v", m.commandLogEntries())
	}
	if _, err := alphaDrv.Query(context.Background(), "SELECT 1"); err == nil {
		t.Fatal("alpha's driver was left open")
	}
	if m.active != "beta" || m.table != "widgets" || len(m.grid.data.rows) != 2 || m.modal != nil {
		t.Fatalf("the session on screen was disturbed: %q %q modal=%T", m.active, m.table, m.modal)
	}
	if out := ansi.Strip(m.View().Content); strings.Contains(out, backgroundMark+" alpha") {
		t.Fatalf("panel [1] still marks alpha as open:\n%s", out)
	}

	// An ordinary statement error is not a lost connection.
	m = openRelation(t, showConn(t, m, "alpha"), "users")
	m = showConn(t, m, "beta")
	s = m.parkedByName("alpha")
	m = send(t, m, sessionMsg{id: s.id, msg: pageLoadedMsg{
		req: s.grid.data.req, conn: "alpha", table: "users", err: fmt.Errorf("no such table: users"),
	}})
	if !m.connected("alpha") {
		t.Fatal("a failed statement closed the background session")
	}
}

// A reply for a session that no longer exists is dropped.
func TestReplyForClosedSessionIsDropped(t *testing.T) {
	m := twoSessions(t)
	defer func() { m.closeSessions() }()
	id := m.parkedByName("alpha").id
	m = send(t, m, press('1'))
	m.panels[panelConnections].selectByName("alpha")
	m = send(t, m, press('x'))
	rows := len(m.grid.data.rows)
	m = send(t, m, sessionMsg{id: id, msg: pageLoadedMsg{
		req: m.grid.data.req, conn: "beta", table: "widgets", result: &db.ResultSet{},
	}})
	if len(m.grid.data.rows) != rows {
		t.Fatal("a reply of a closed session was reduced into the one on screen")
	}
}

func TestSessionSaveCoversOnlyTheOneOnScreen(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := twoSessions(t)
	defer func() { m.closeSessions() }()
	m.saveSession()
	sess, err := session.Load()
	if err != nil || sess == nil {
		t.Fatalf("session.Load = %v, %v", sess, err)
	}
	if sess.Connection != "beta" || sess.Table != "widgets" {
		t.Fatalf("saved session = %+v, want the one on screen (beta/widgets)", sess)
	}

	// With nothing on screen the file keeps the last real session, even
	// though a connection is still open in the background.
	m = send(t, m, press('1'), press('x'))
	m.saveSession()
	sess, err = session.Load()
	if err != nil || sess == nil || sess.Connection != "beta" {
		t.Fatalf("session after a quit from the bare screen = %+v, %v", sess, err)
	}
}

// A connection that finishes dialling while a dialog is up opens in the
// background: the dialog's answer must act on the session it was opened
// for.
func TestConnectWhileModalIsOpenStaysInBackground(t *testing.T) {
	m := sessionFixture(t)
	defer func() { m.closeSessions() }()
	m = openRelation(t, showConn(t, m, "alpha"), "users")

	m = send(t, m, press('1'))
	m.panels[panelConnections].selectByName("beta")
	next, cmd := m.Update(special(tea.KeyEnter, 0))
	m = next.(Model)
	dialled := drain(cmd)

	m.setFocus(panelMain)
	m = send(t, m, press('l'), press('e'))
	edit, ok := m.modal.(*editCellModal)
	if !ok {
		t.Fatalf("e opened %T, want the edit modal", m.modal)
	}
	m = send(t, m, dialled...)
	if m.active != "alpha" || m.modal != edit {
		t.Fatalf("beta took the screen from under the dialog: on screen=%q modal=%T", m.active, m.modal)
	}
	if !m.connected("beta") {
		t.Fatalf("beta did not open at all; log: %v", m.commandLogEntries())
	}
	edit.input.SetValue("anna")
	edit.null = false
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.grid.changes.Len() != 1 || m.parkedByName("beta").grid.changes.Len() != 0 {
		t.Fatal("the edit was not staged on alpha")
	}

	m = showConn(t, m, "beta")
	if got := m.panels[panelObjects].items; !containsName(got, "widgets") {
		t.Fatalf("beta's tree was not loaded in the background: %v", got)
	}
}

func TestEphemeralConnectionOpensNextToSavedOnes(t *testing.T) {
	m := sessionFixture(t)
	defer func() { m.closeSessions() }()
	m = openRelation(t, showConn(t, m, "alpha"), "users")

	file := filepath.Join(t.TempDir(), "scratch.sqlite")
	seedSQLiteFile(t, file, []string{`CREATE TABLE notes (id INTEGER PRIMARY KEY)`})
	e, err := newEphemeralConn(file, m.nameTaken)
	if err != nil {
		t.Fatal(err)
	}
	cmd := m.openEphemeral(e)
	m = send(t, m, drain(cmd)...)
	if m.active != "scratch" || !m.connected("alpha") {
		t.Fatalf("on screen=%q alpha connected=%v", m.active, m.connected("alpha"))
	}

	// Back to the saved one and out again: the ephemeral session is an
	// ordinary one.
	m = showConn(t, m, "alpha")
	if m.table != "users" || !m.connected("scratch") {
		t.Fatalf("after switching back: table=%q scratch connected=%v", m.table, m.connected("scratch"))
	}
	// x on the ephemeral row in the background closes it and drops the row.
	m = send(t, m, press('1'))
	m.panels[panelConnections].selectByName("scratch")
	m = send(t, m, press('x'))
	if m.ephem != nil || m.connected("scratch") || m.active != "alpha" {
		t.Fatalf("after x on the ephemeral row: ephem=%v on screen=%q", m.ephem, m.active)
	}
}

// Renaming or removing a profile treats a background session the way it
// treats the one on screen.
func TestRenameAndRemoveFollowBackgroundSessions(t *testing.T) {
	m := twoSessions(t)
	defer func() { m.closeSessions() }()
	alphaDrv := m.parkedByName("alpha").driver

	m = send(t, m, press('1'))
	m.panels[panelConnections].selectByName("alpha")
	m = send(t, m, press('e'))
	form, ok := m.modal.(*formModal)
	if !ok {
		t.Fatalf("e opened %T, want the connection form", m.modal)
	}
	form.field("name").input.SetValue("alpha2")
	m = send(t, m, special(tea.KeyEnter, 0))
	if !m.connected("alpha2") || m.connected("alpha") {
		t.Fatalf("the session did not follow the rename: alpha2=%v alpha=%v", m.connected("alpha2"), m.connected("alpha"))
	}
	if st := m.panels[panelConnections].allStatus[0]; st != statusBackground {
		t.Fatalf("renamed row status = %v, want background", st)
	}
	m = showConn(t, m, "alpha2")
	if m.table != "users" || m.driver != alphaDrv {
		t.Fatalf("renamed session lost its state: table=%q", m.table)
	}

	// Now beta is in the background: removing its profile closes it.
	betaDrv := m.parkedByName("beta").driver
	m = send(t, m, press('1'))
	m.panels[panelConnections].selectByName("beta")
	m = send(t, m, press('d'))
	if _, ok := m.modal.(*confirmModal); !ok {
		t.Fatalf("d opened %T, want the remove confirmation", m.modal)
	}
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.connected("beta") || len(m.parked) != 0 {
		t.Fatal("removing the profile left its session open")
	}
	if _, err := betaDrv.Query(context.Background(), "SELECT 1"); err == nil {
		t.Fatal("the removed profile's driver is still open")
	}
	if m.active != "alpha2" || m.table != "users" {
		t.Fatalf("the session on screen was disturbed: %q %q", m.active, m.table)
	}
}

// With one connection open no line carries a prefix; with two, every
// statement says whose it is.
func TestCommandLogNamesTheConnection(t *testing.T) {
	m := sessionFixture(t)
	defer func() { m.closeSessions() }()
	m = openRelation(t, showConn(t, m, "alpha"), "users")
	for _, l := range m.commandLogEntries() {
		if strings.HasPrefix(l.text, "[alpha]") {
			t.Fatalf("a single connection's line carries a prefix: %q", l.text)
		}
	}
	m = openRelation(t, showConn(t, m, "beta"), "widgets")
	if !logContains(m, `[alpha] SELECT * FROM "users"`) || !logContains(m, `[beta] SELECT * FROM "widgets"`) {
		t.Fatalf("statements are not attributed: %v", m.commandLogEntries())
	}
	if logContains(m, "[beta] -- connect") || logContains(m, "[alpha] -- switch") {
		t.Fatalf("a panel [1] note was attributed to a session: %v", m.commandLogEntries())
	}
}
