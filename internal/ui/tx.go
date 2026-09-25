package ui

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/db"
)

// Explicit transactions in the query editor: `B` in panel [3] opens one,
// every statement the editor runs from then on runs inside it, and `C`
// commits or `U` rolls it back — both behind a confirm. The transaction
// lives on a connection of its own (db.Tx), so the grid's page queries,
// the staged changeset and every other Driver call keep using the pool:
// they see committed data only and can neither join the transaction nor
// break it. The state is always on screen — the options bar carries it
// whichever panel is focused, panel [3] and the editor name it too — and
// the command log tags every statement that ran inside it with [tx]. See
// wiki/design/editor-transactions.md.

// editorTx is the open transaction, or the one being opened.
type editorTx struct {
	// handle is nil while BEGIN is still in flight.
	handle db.Tx
	// driver and conn are the session it was opened on: a reply that
	// lands after a disconnect belongs to nothing on screen.
	driver db.Driver
	conn   string
	// state and stmts mirror the handle, updated from the messages that
	// report each statement — never read off the handle in View, which
	// would race with a running statement's bookkeeping for no gain.
	state db.TxState
	stmts int
	// cause is the error that aborted or ended the transaction.
	cause string
	// busy names the BEGIN/COMMIT/ROLLBACK in flight, "" when none.
	busy      string
	startedAt time.Time
}

// ---------- messages ----------

type txBegunMsg struct {
	drv db.Driver
	tx  db.Tx
	err error
}

type txCommittedMsg struct {
	tx    db.Tx
	err   error
	state db.TxState
	stmts int
}

type txRolledBackMsg struct {
	tx    db.Tx
	err   error
	stmts int
	// why is "" for a rollback the user asked for, and names the
	// transaction's fate otherwise.
	why string
}

// ---------- commands ----------

func beginTxCmd(drv db.Driver) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
		defer cancel()
		tx, err := drv.Begin(ctx)
		return txBegunMsg{drv: drv, tx: tx, err: err}
	}
}

func commitTxCmd(tx db.Tx) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
		defer cancel()
		err := tx.Commit(ctx)
		return txCommittedMsg{tx: tx, err: err, state: tx.State(), stmts: tx.Statements()}
	}
}

func rollbackTxCmd(tx db.Tx, why string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
		defer cancel()
		n := tx.Statements()
		err := tx.Rollback(ctx)
		return txRolledBackMsg{tx: tx, err: err, stmts: n, why: why}
	}
}

// ---------- actions ----------

// txOpen reports whether the editor has a transaction, including one
// whose BEGIN is still in flight.
func (m Model) txOpen() bool { return m.query.tx != nil }

// beginTx is `B` in panel [3].
func (m *Model) beginTx() tea.Cmd {
	switch {
	case m.driver == nil:
		return logCmd("-- begin transaction skipped: not connected")
	case m.readOnly():
		return readOnlyBlocked("begin transaction")
	case m.txOpen():
		return logCmd("-- begin transaction skipped: one is already open (%s commits, %s rolls back)",
			m.keys.CommitTx.Help().Key, m.keys.RollbackTx.Help().Key)
	case m.query.run.running:
		return logCmd("-- begin transaction skipped: a query is still running (ctrl+c cancels it)")
	}
	m.query.tx = &editorTx{driver: m.driver, conn: m.active, busy: "begin"}
	return tea.Batch(logCmd("-- begin transaction on %s…", m.active), beginTxCmd(m.driver))
}

// txUnavailable is the reason a commit or rollback cannot start now, ""
// when it can.
func (m Model) txUnavailable(verb string) string {
	tx := m.query.tx
	switch {
	case tx == nil:
		return fmt.Sprintf("-- %s skipped: no open transaction (%s begins one)", verb, m.keys.BeginTx.Help().Key)
	case tx.busy != "":
		return fmt.Sprintf("-- %s skipped: %s is still in flight", verb, tx.busy)
	case m.query.run.running:
		return fmt.Sprintf("-- %s skipped: a query is still running in the transaction (ctrl+c cancels it)", verb)
	}
	return ""
}

// confirmCommitTx is `C` in panel [3]. A transaction that cannot commit
// says why instead of asking — an aborted one must never look like it
// could.
func (m *Model) confirmCommitTx() tea.Cmd {
	if why := m.txUnavailable("commit transaction"); why != "" {
		return logCmd("%s", why)
	}
	tx := m.query.tx
	if tx.state == db.TxAborted || tx.state == db.TxLost {
		m.modal = &confirmModal{
			title:  "Cannot commit — transaction " + tx.state.String(),
			body:   m.txDeadBody(tx),
			danger: true,
		}
		return logCmd("-- commit refused: the transaction is %s", tx.state)
	}
	m.modal = &confirmModal{
		title: "Commit transaction",
		body: fmt.Sprintf("Commit the %s run in the transaction on %s?\n\nOpen for %s. Once committed, the changes are visible to every other session.",
			countStatements(tx.stmts), m.taggedConnName(tx.conn), formatElapsed(time.Since(tx.startedAt))),
		onConfirm: func(mm *Model) tea.Cmd {
			t := mm.query.tx
			if t == nil || t.handle == nil || t.busy != "" {
				return nil
			}
			t.busy = "COMMIT"
			return commitTxCmd(t.handle)
		},
	}
	return nil
}

// txDeadBody explains an aborted or ended transaction and what to do.
func (m Model) txDeadBody(tx *editorTx) string {
	cause := ""
	if tx.cause != "" {
		cause = "\n\nCaused by: " + tx.cause
	}
	rb := m.keys.RollbackTx.Help().Key
	if tx.state == db.TxLost {
		return "The server ended the transaction: nothing run in it was committed." + cause +
			"\n\nPress " + rb + " to close it; statements run after that autocommit again."
	}
	body := "An error aborted the transaction: nothing run in it can be committed." + cause +
		"\n\nPress " + rb + " to roll it back."
	if m.driver != nil && m.driver.Engine() == db.EnginePostgres {
		body += " A ROLLBACK TO SAVEPOINT run from the editor revives it instead."
	}
	return body
}

// confirmRollbackTx is `U` in panel [3]. An empty or already-dead
// transaction rolls back without asking: there is nothing to lose.
func (m *Model) confirmRollbackTx() tea.Cmd {
	if why := m.txUnavailable("rollback"); why != "" {
		return logCmd("%s", why)
	}
	tx := m.query.tx
	start := func(mm *Model) tea.Cmd {
		t := mm.query.tx
		if t == nil || t.handle == nil || t.busy != "" {
			return nil
		}
		t.busy = "ROLLBACK"
		return rollbackTxCmd(t.handle, "")
	}
	if tx.stmts == 0 || tx.state == db.TxLost {
		return start(m)
	}
	m.modal = &confirmModal{
		title: "Roll back transaction",
		body: fmt.Sprintf("Discard the %s run in the transaction on %s?",
			countStatements(tx.stmts), m.taggedConnName(tx.conn)),
		danger:    true,
		onConfirm: start,
	}
	return nil
}

// ---------- reducing ----------

func (m *Model) applyTxBegun(msg txBegunMsg) tea.Cmd {
	tx := m.query.tx
	if tx == nil || tx.driver != msg.drv || tx.handle != nil {
		// The session it was opened for is gone; its Close has already
		// rolled back whatever it could see, and this handle is released
		// the same way.
		if msg.tx != nil {
			return rollbackTxCmd(msg.tx, "stale")
		}
		return nil
	}
	if msg.err != nil {
		m.query.tx = nil
		return logCmd("-- BEGIN FAILED: %v", msg.err)
	}
	tx.handle, tx.busy, tx.state, tx.startedAt = msg.tx, "", db.TxActive, time.Now()
	return logCmd("-- transaction open on %s: editor statements run inside it until %s commits or %s rolls back; the grid shows committed data only",
		tx.conn, m.keys.CommitTx.Help().Key, m.keys.RollbackTx.Help().Key)
}

func (m *Model) applyTxCommitted(msg txCommittedMsg) tea.Cmd {
	tx := m.query.tx
	if tx == nil || tx.handle != msg.tx {
		return nil
	}
	tx.busy = ""
	if msg.err != nil {
		// The handle decides whether a transaction survived the failed
		// COMMIT; the UI only mirrors it.
		tx.state, tx.stmts = msg.state, msg.stmts
		if tx.state != db.TxActive {
			tx.cause = msg.err.Error()
		}
		body := fmt.Sprintf("%v\n\nThe transaction is still open: retry with %s or roll back with %s.",
			msg.err, m.keys.CommitTx.Help().Key, m.keys.RollbackTx.Help().Key)
		if tx.state != db.TxActive {
			body = fmt.Sprintf("%v\n\n%s", msg.err, m.txDeadBody(tx))
		}
		m.modal = &confirmModal{title: "COMMIT failed", body: body, danger: true}
		return logCmd("-- COMMIT FAILED — transaction %s: %v", tx.state, msg.err)
	}
	m.query.tx = nil
	// The committed rows are what the grid reads from now on.
	return tea.Batch(
		logCmd("-- transaction committed on %s: %s", tx.conn, countStatements(msg.stmts)),
		m.reloadPage())
}

func (m *Model) applyTxRolledBack(msg txRolledBackMsg) tea.Cmd {
	if msg.why == "stale" {
		return nil
	}
	tx := m.query.tx
	if tx == nil || tx.handle != msg.tx {
		return nil
	}
	m.query.tx = nil
	if msg.err != nil {
		return logCmd("-- ROLLBACK FAILED: %v — the connection was discarded, which ends the transaction on the server", msg.err)
	}
	if tx.state == db.TxLost {
		return logCmd("-- transaction closed on %s (the server had already ended it)", tx.conn)
	}
	return logCmd("-- transaction rolled back on %s: %s discarded", tx.conn, countStatements(msg.stmts))
}

// noteTxStatement mirrors the handle's state after one statement of a run
// and says so when the statement changed what the transaction can do.
func (m *Model) noteTxStatement(msg queryStmtMsg) tea.Cmd {
	tx := m.query.tx
	if tx == nil || msg.tx == nil || tx.handle != msg.tx {
		return nil
	}
	before := tx.state
	tx.state, tx.stmts = msg.txState, msg.txStmts
	if before == tx.state {
		return nil
	}
	rb := m.keys.RollbackTx.Help().Key
	switch tx.state {
	case db.TxAborted:
		tx.cause = errText(msg.err)
		return logCmd("-- transaction ABORTED by that error: nothing in it can be committed — %s rolls it back", rb)
	case db.TxLost:
		tx.cause = errText(msg.err)
		return logCmd("-- the server ENDED the transaction: nothing in it was committed — %s closes it", rb)
	case db.TxActive:
		tx.cause = ""
		return logCmd("-- transaction recovered: open again")
	}
	return nil
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// dropTx forgets the transaction because its session is going away. The
// driver's Close rolls it back; this only says so.
func (m *Model) dropTx(why string) tea.Cmd {
	tx := m.query.tx
	if tx == nil {
		return nil
	}
	m.query.tx = nil
	return logCmd("-- transaction on %s rolled back (%s): %s discarded", tx.conn, why, countStatements(tx.stmts))
}

// guardTx runs next straight away when no transaction is open, and asks
// first when one is: disconnecting, switching connections and quitting
// all roll it back.
func (m *Model) guardTx(title, verb string, next func(*Model) tea.Cmd) tea.Cmd {
	tx := m.query.tx
	if tx == nil {
		return next(m)
	}
	m.modal = &confirmModal{
		title:     title,
		body:      fmt.Sprintf("%s and roll back the open transaction on %s (%s)?", verb, m.taggedConnName(tx.conn), countStatements(tx.stmts)),
		danger:    true,
		onConfirm: next,
	}
	return nil
}

// refuseWithTx is the answer of a write path that runs outside the
// editor's transaction — the staged changeset's commit, a CSV import —
// while one is open. It never interleaves the two: the other path runs on
// another connection, cannot see the transaction's uncommitted rows, and
// could wait forever on the locks it holds.
func (m *Model) refuseWithTx(what string) tea.Cmd {
	tx := m.query.tx
	if tx == nil {
		return nil
	}
	m.modal = &confirmModal{
		title: "Cannot " + what + " — a transaction is open",
		body: fmt.Sprintf("An editor transaction is open on %s (%s).\n\n"+
			"This would run in a transaction of its own on another connection: it could not see the editor transaction's "+
			"uncommitted work, and could wait forever on rows that transaction has locked.\n\n"+
			"Commit (%s) or roll back (%s) the transaction in [3] Query first.",
			m.taggedConnName(tx.conn), countStatements(tx.stmts),
			m.keys.CommitTx.Help().Key, m.keys.RollbackTx.Help().Key),
		danger: true,
	}
	return logCmd("-- %s refused: an editor transaction is open", what)
}

// ---------- rendering ----------

// txBadge is the transaction's one-line state for the options bar, panel
// [3]'s title and the editor's status line; "" when none is open.
func (m Model) txBadge() string {
	tx := m.query.tx
	if tx == nil {
		return ""
	}
	s := m.style
	switch {
	case tx.busy != "":
		return s.pending.Render("⛁ TX " + tx.busy + "…")
	case tx.state == db.TxAborted:
		return s.danger.Render("⛁ TX ABORTED — " + m.keys.RollbackTx.Help().Key + " rolls back")
	case tx.state == db.TxLost:
		return s.danger.Render("⛁ TX ENDED BY SERVER — " + m.keys.RollbackTx.Help().Key + " closes")
	}
	return s.pending.Render(fmt.Sprintf("⛁ TX open · %s · %s",
		countStatements(tx.stmts), formatElapsed(time.Since(tx.startedAt))))
}

// txNote is the short marker the grid's tab bar carries while a
// transaction is open and a relation is being browsed: those rows are the
// committed ones. A query result carries none — the editor ran it inside
// the transaction.
func (m Model) txNote() string {
	if m.query.tx == nil || !m.grid.data.browsing() {
		return ""
	}
	return m.style.pending.Render("⛁ tx open — grid shows committed data")
}
