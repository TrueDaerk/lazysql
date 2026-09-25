package ui

import (
	"context"
	"strings"
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"lazysql/internal/db"
)

// inTx is a connected model with the editor focused in normal mode and a
// transaction open.
func inTx(t *testing.T, m Model) Model {
	t.Helper()
	m = send(t, m, press('3'), press('B'))
	if m.query.tx == nil || m.query.tx.handle == nil {
		t.Fatalf("B did not open a transaction; log: %v", m.commandLogEntries())
	}
	if m.query.tx.state != db.TxActive {
		t.Fatalf("state = %v, want open", m.query.tx.state)
	}
	// The fixture database outlives the test; a transaction a failed
	// test leaves open must not lock it for the next one.
	drv := m.driver
	t.Cleanup(func() { drv.Close() })
	return m
}

// qRows counts the fixture through the pool — what the grid, and every
// other session, sees.
func qRows(t *testing.T, drv db.Driver, table string) int64 {
	t.Helper()
	rs, err := drv.Query(context.Background(), "SELECT COUNT(*) FROM "+table)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	n, ok := rs.Rows[0][0].(int64)
	if !ok {
		t.Fatalf("count = %v (%T)", rs.Rows[0][0], rs.Rows[0][0])
	}
	return n
}

func hasBinding(bs []key.Binding, b key.Binding) bool {
	for _, x := range bs {
		if x.Help() == b.Help() {
			return true
		}
	}
	return false
}

func TestTxCommitPersists(t *testing.T) {
	m := inTx(t, queryable(t))
	if !logContains(m, "transaction open on") || !logContains(m, "[tx] BEGIN") {
		t.Fatalf("BEGIN not logged: %v", m.commandLogEntries())
	}
	m = runQuery(t, m, "INSERT INTO q (id, name) VALUES (9, 'x')")
	if !logContains(m, "[tx] INSERT INTO q") {
		t.Fatalf("the statement is not tagged as inside the transaction: %v", m.commandLogEntries())
	}
	if got := m.query.tx.stmts; got != 1 {
		t.Fatalf("tx statements = %d, want 1", got)
	}
	// The pool — the grid — does not see it yet.
	if n := qRows(t, m.driver, "q"); n != 3 {
		t.Fatalf("pool count = %d before commit, want 3", n)
	}
	// A SELECT from the editor does: it runs in the transaction.
	m = runQuery(t, m, "SELECT * FROM q")
	if len(m.grid.data.all) != 4 {
		t.Fatalf("in-tx SELECT saw %d rows, want 4", len(m.grid.data.all))
	}

	m = send(t, m, press('C'))
	cm, ok := m.modal.(*confirmModal)
	if !ok || !strings.Contains(cm.body, "2 statements") {
		t.Fatalf("C opened %T %+v, want a commit confirmation naming 2 statements", m.modal, m.modal)
	}
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.query.tx != nil {
		t.Fatal("the transaction is still open after the commit")
	}
	if !logContains(m, "[tx] COMMIT") || !logContains(m, "transaction committed") {
		t.Fatalf("COMMIT not logged: %v", m.commandLogEntries())
	}
	if n := qRows(t, m.driver, "q"); n != 4 {
		t.Fatalf("count after commit = %d, want 4", n)
	}
}

func TestTxRollbackDiscards(t *testing.T) {
	m := inTx(t, queryable(t))
	m = runQuery(t, m, "DELETE FROM q WHERE id = 1")
	m = send(t, m, press('U'))
	if _, ok := m.modal.(*confirmModal); !ok {
		t.Fatalf("U on a non-empty transaction opened %T, want a confirmation", m.modal)
	}
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.query.tx != nil {
		t.Fatal("the transaction is still open after the rollback")
	}
	if !logContains(m, "[tx] ROLLBACK") || !logContains(m, "transaction rolled back") {
		t.Fatalf("ROLLBACK not logged: %v", m.commandLogEntries())
	}
	if n := qRows(t, m.driver, "q"); n != 3 {
		t.Fatalf("count after rollback = %d, want 3", n)
	}
	// Statements autocommit again afterwards.
	m = runQuery(t, m, "DELETE FROM q WHERE id = 1")
	if n := qRows(t, m.driver, "q"); n != 2 {
		t.Fatalf("count after an autocommitted delete = %d, want 2", n)
	}
}

func TestTxEmptyRollbackDoesNotAsk(t *testing.T) {
	m := inTx(t, queryable(t))
	m = send(t, m, press('U'))
	if m.modal != nil {
		t.Fatalf("U on an empty transaction opened %T", m.modal)
	}
	if m.query.tx != nil {
		t.Fatal("the empty transaction was not rolled back")
	}
}

// txDuck is the DuckDB fixture — the engine whose errors abort a
// transaction — with a keyed table to violate.
func txDuck(t *testing.T) Model {
	t.Helper()
	m := duckBrowsing(t)
	if _, err := m.driver.Exec(context.Background(),
		"CREATE TABLE d (id INTEGER PRIMARY KEY, name TEXT)"); err != nil {
		t.Fatal(err)
	}
	m.commandLog = nil
	return m
}

// An error inside the transaction must not look like success: on DuckDB
// it aborts the transaction, the badge says so, commit is refused with an
// explanation, and rollback is the way out.
func TestTxErrorMidTransactionIsSurfaced(t *testing.T) {
	m := inTx(t, txDuck(t))
	m = runQuery(t, m, "INSERT INTO d VALUES (1, 'a')")
	m = runQuery(t, m, "INSERT INTO d VALUES (1, 'dup')")
	if m.grid.data.err == "" {
		t.Fatal("the failing statement did not show its error")
	}
	if m.query.tx == nil || m.query.tx.state != db.TxAborted {
		t.Fatalf("tx = %+v, want an aborted transaction", m.query.tx)
	}
	if !strings.Contains(m.txBadge(), "ABORTED") || !strings.Contains(m.renderOptionsBar(), "ABORTED") {
		t.Fatalf("the aborted state is not on screen: badge %q", m.txBadge())
	}
	if !logContains(m, "transaction ABORTED") {
		t.Fatalf("the log does not say the transaction aborted: %v", m.commandLogEntries())
	}
	// The next statement is refused before it reaches the server.
	m = runQuery(t, m, "INSERT INTO d VALUES (2, 'b')")
	if !strings.Contains(m.grid.data.err, "aborted") {
		t.Fatalf("Data tab error = %q, want the aborted refusal", m.grid.data.err)
	}

	m = send(t, m, press('C'))
	cm, ok := m.modal.(*confirmModal)
	if !ok || !strings.Contains(cm.title, "Cannot commit") || cm.onConfirm != nil {
		t.Fatalf("C on an aborted transaction opened %T %+v, want a refusal", m.modal, m.modal)
	}
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.query.tx == nil || logContains(m, "[tx] COMMIT") {
		t.Fatal("an aborted transaction was committed")
	}

	m = send(t, m, press('U'), special(tea.KeyEnter, 0))
	if m.query.tx != nil {
		t.Fatal("rollback did not close the aborted transaction")
	}
	if n := qRows(t, m.driver, "d"); n != 0 {
		t.Fatalf("count = %d, want 0", n)
	}
}

// On SQLite a failed statement undoes itself only: the transaction stays
// open, the error is shown, and the rest still commits.
func TestTxStatementErrorKeepsTransactionOnSQLite(t *testing.T) {
	m := inTx(t, queryable(t))
	m = runQuery(t, m, "INSERT INTO q (id, name) VALUES (9, 'x')")
	m = runQuery(t, m, "INSERT INTO q (id, name) VALUES (9, 'dup')")
	if m.grid.data.err == "" || !logContains(m, "FAILED") {
		t.Fatal("the failing statement was not surfaced")
	}
	if m.query.tx.state != db.TxActive || !strings.Contains(m.txBadge(), "TX open") {
		t.Fatalf("state = %v, badge %q; want still open", m.query.tx.state, m.txBadge())
	}
	m = send(t, m, press('C'), special(tea.KeyEnter, 0))
	if n := qRows(t, m.driver, "q"); n != 4 {
		t.Fatalf("count = %d, want 4", n)
	}
}

func TestTxRefusesTypedTransactionControl(t *testing.T) {
	m := inTx(t, queryable(t))
	m = runQuery(t, m, "COMMIT")
	if !strings.Contains(m.grid.data.err, "transaction control") {
		t.Fatalf("Data tab error = %q, want the refusal", m.grid.data.err)
	}
	if m.query.tx == nil || m.query.tx.state != db.TxActive {
		t.Fatal("a typed COMMIT changed the transaction")
	}
}

func TestQuitWithOpenTxPrompts(t *testing.T) {
	removeState(t)
	m := inTx(t, queryable(t))
	m = runQuery(t, m, "INSERT INTO q (id, name) VALUES (9, 'x')")
	m = send(t, m, press('q'))
	cm, ok := m.modal.(*confirmModal)
	if !ok || !strings.Contains(cm.body, "roll back the open transaction") {
		t.Fatalf("q opened %T %+v, want a rollback confirmation", m.modal, m.modal)
	}
	// esc keeps everything.
	m = send(t, m, special(tea.KeyEscape, 0))
	if m.query.tx == nil || m.driver == nil {
		t.Fatal("esc on the quit confirmation dropped the transaction")
	}
	drv := m.driver
	m = send(t, m, press('q'), special(tea.KeyEnter, 0))
	if m.driver != nil || m.query.tx != nil {
		t.Fatal("confirmed quit did not tear down the session")
	}
	var rolledBack bool
	for _, e := range drv.Logger().Entries() {
		rolledBack = rolledBack || (e.InTx && e.SQL == "ROLLBACK")
	}
	if !rolledBack {
		t.Fatal("quit did not roll the transaction back")
	}
	again := browsing(t)
	if n := qRows(t, again.driver, "q"); n != 3 {
		t.Fatalf("count after quit = %d, want 3", n)
	}
}

func TestDisconnectWithOpenTxPrompts(t *testing.T) {
	m := inTx(t, queryable(t))
	m = runQuery(t, m, "INSERT INTO q (id, name) VALUES (9, 'x')")
	m = send(t, m, press('1'), press('x'))
	if _, ok := m.modal.(*confirmModal); !ok {
		t.Fatalf("x opened %T, want a rollback confirmation", m.modal)
	}
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.driver != nil || m.query.tx != nil {
		t.Fatal("confirmed disconnect left the session or the transaction")
	}
	if !logContains(m, "rolled back (disconnect)") {
		t.Fatalf("the rollback is not in the log: %v", m.commandLogEntries())
	}
}

func TestChangesetCommitRefusedWhileTxOpen(t *testing.T) {
	m := dataBrowsing(t)
	m = send(t, m, press('l'))
	m = stageEdit(t, m, "renamed")
	m = inTx(t, m)
	m.setFocus(panelMain)
	m = send(t, m, press('c'))
	cm, ok := m.modal.(*confirmModal)
	if !ok || !strings.Contains(cm.title, "a transaction is open") || cm.onConfirm != nil {
		t.Fatalf("c opened %T %+v, want a refusal", m.modal, m.modal)
	}
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.grid.changes.Len() != 1 {
		t.Fatalf("changeset = %d, want it kept", m.grid.changes.Len())
	}
	// The grid's tab bar says what its rows are.
	if !strings.Contains(m.mainTabBar(200), "committed data") {
		t.Fatalf("tab bar %q does not flag the open transaction", m.mainTabBar(200))
	}
}

func TestReadOnlyRefusesBeginTx(t *testing.T) {
	m := readOnlyGrid(t)
	m = send(t, m, press('3'), press('B'))
	if m.query.tx != nil {
		t.Fatal("a read-only connection opened a transaction")
	}
	if !logContains(m, "connection is read-only") {
		t.Fatalf("log = %v, want the read-only refusal", m.commandLogEntries())
	}
	if hasBinding(m.optionsBarBindings(), m.keys.BeginTx) {
		t.Fatal("the options bar offers B on a read-only connection")
	}
}

// The three keys are documented in `?` at all times, and the options bar
// offers the ones the transaction's state allows.
func TestTxKeysInHelpAndOptionsBar(t *testing.T) {
	m := queryable(t)
	m = send(t, m, press('3'))
	var help []key.Binding
	for _, g := range m.keys.helpGroups(panelQuery) {
		help = append(help, g.bindings...)
	}
	for _, b := range []key.Binding{m.keys.BeginTx, m.keys.CommitTx, m.keys.RollbackTx} {
		if !hasBinding(help, b) {
			t.Errorf("`?` does not list %q", b.Help().Desc)
		}
	}
	bar := m.optionsBarBindings()
	if !hasBinding(bar, m.keys.BeginTx) || hasBinding(bar, m.keys.CommitTx) || hasBinding(bar, m.keys.RollbackTx) {
		t.Fatal("with no transaction the bar should offer B only")
	}
	m = inTx(t, m)
	bar = m.optionsBarBindings()
	if hasBinding(bar, m.keys.BeginTx) || !hasBinding(bar, m.keys.CommitTx) || !hasBinding(bar, m.keys.RollbackTx) {
		t.Fatal("with a transaction open the bar should offer C and U")
	}
	// The state stays on screen with another panel focused.
	m = send(t, m, press('1'))
	if !strings.Contains(m.renderOptionsBar(), "TX open") {
		t.Fatalf("options bar %q does not show the open transaction", m.renderOptionsBar())
	}
}
