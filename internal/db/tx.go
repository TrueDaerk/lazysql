package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// An interactive transaction is the query editor's BEGIN … COMMIT: a
// transaction the user opens, runs statements in one run at a time,
// inspects, and only then commits or rolls back. ExecTx cannot be it —
// ExecTx owns the whole lifecycle inside one call — so Begin hands out a
// handle that outlives every call made through it.
//
// Three decisions shape it:
//
//   - The handle owns a dedicated connection taken out of the pool
//     (*sql.Conn), never shared. The grid's page and count queries, the
//     staged changeset's commit, introspection and every other Driver
//     method keep using the pool, so none of them can join the
//     transaction by accident, and none of them can break it by issuing a
//     statement on its connection. They see committed data only.
//
//   - BEGIN, COMMIT and ROLLBACK are issued as SQL on that connection
//     rather than through database/sql's *sql.Tx. A *sql.Tx is finished
//     the moment Commit returns, whatever the server said — modernc's
//     SQLite driver even forces a rollback after a failed COMMIT — so a
//     COMMIT that fails with "database is locked" would lose the work.
//     Here a failed COMMIT leaves the transaction open, in whatever state
//     the server reports, for the user to retry or roll back.
//
//   - The handle tracks the transaction's state, asked of the engine after
//     every statement that fails (and after every statement at all on
//     PostgreSQL, where asking is free). What an error does to a
//     transaction is the dialect difference that matters most: PostgreSQL
//     and DuckDB abort the whole transaction and then accept nothing but
//     ROLLBACK — DuckDB even answers a later COMMIT with success while it
//     rolls everything back — MySQL ends it outright on a deadlock, SQLite
//     rolls it back when a write is interrupted, and a lost connection
//     ends it everywhere. A COMMIT in any of those states is refused
//     rather than sent, so an aborted transaction can never look like a
//     committed one.

// TxState is where an interactive transaction stands.
type TxState int

const (
	// TxActive is an open transaction that accepts statements.
	TxActive TxState = iota
	// TxAborted is a transaction an error has aborted: the server rejects
	// every statement until ROLLBACK (PostgreSQL also accepts ROLLBACK TO
	// SAVEPOINT, which revives it). Nothing in it can be committed.
	TxAborted
	// TxLost is a transaction the server has already ended — rolled back
	// on a deadlock or an interrupt, or gone with its connection. Nothing
	// in it was committed; Rollback only releases the handle.
	TxLost
	// TxClosed is a transaction committed or rolled back through the
	// handle. The handle is spent.
	TxClosed
)

// String is the state as the UI names it.
func (s TxState) String() string {
	switch s {
	case TxActive:
		return "open"
	case TxAborted:
		return "aborted"
	case TxLost:
		return "ended by the server"
	default:
		return "closed"
	}
}

var (
	// ErrTxOpen is Begin on a session that already has an interactive
	// transaction: one per session, the way the editor has one buffer.
	ErrTxOpen = errors.New("a transaction is already open on this connection")
	// ErrTxAborted refuses a statement or a COMMIT in a transaction an
	// error aborted.
	ErrTxAborted = errors.New("the transaction was aborted by an error — only a rollback is possible")
	// ErrTxLost refuses a statement or a COMMIT in a transaction the server
	// already ended.
	ErrTxLost = errors.New("the server ended the transaction — nothing in it was committed")
	// ErrTxClosed is any call on a committed or rolled back handle.
	ErrTxClosed = errors.New("the transaction is already closed")
	// ErrTxControl refuses a transaction-control statement typed into the
	// open transaction: BEGIN, COMMIT or ROLLBACK run as SQL would end or
	// nest it behind the handle's back, so the keys do it instead.
	ErrTxControl = errors.New("transaction control statements are not run inside the open transaction — use the transaction keys")
	// ErrTxImplicitCommit refuses a statement the engine would silently
	// commit the open transaction for (MySQL and MariaDB DDL). Running it
	// would leave every later statement autocommitted while the UI still
	// said "in transaction".
	ErrTxImplicitCommit = errors.New("the statement would commit the open transaction implicitly")
)

// Tx is an interactive transaction on a connection of its own. Every
// method takes a context and aborts with it; statements run one at a time
// — a second call waits for the first. State and Statements never wait,
// so a UI can read them while a statement is running.
type Tx interface {
	// Exec runs a statement that returns no rows inside the transaction.
	Exec(ctx context.Context, query string, args ...any) (ExecResult, error)
	// QueryLimit is Driver.QueryLimit inside the transaction.
	QueryLimit(ctx context.Context, query string, max int, args ...any) (*ResultSet, bool, error)
	// Commit commits. It is refused with ErrTxAborted or ErrTxLost without
	// contacting the server when the transaction cannot commit; a COMMIT
	// the server rejects leaves the transaction open (State says in what
	// shape) rather than discarding it.
	Commit(ctx context.Context) error
	// Rollback rolls back and releases the connection. It always spends
	// the handle, even when the ROLLBACK itself fails: the connection is
	// then discarded instead of returned to the pool, which ends the
	// transaction server-side.
	Rollback(ctx context.Context) error
	// State reports where the transaction stands now.
	State() TxState
	// Statements counts the statements that ran inside it successfully,
	// BEGIN not included.
	Statements() int
}

// txMonitor is a dialect's way of telling how a transaction stands. It is
// attached to the connection once at Begin — SQLite registers a rollback
// hook there — and asked after every statement.
type txMonitor interface {
	// after reports the state once a statement returned err (nil on
	// success). It may run a probe of its own on the connection.
	after(ctx context.Context, err error) TxState
	// afterCommit reports the state after a COMMIT the server rejected
	// with err — for some engines a different question than after a
	// statement, since a failed COMMIT may already have ended the
	// transaction while the connection itself is idle and healthy.
	afterCommit(ctx context.Context, err error) TxState
	// release detaches whatever the monitor attached to the connection
	// before it goes back to the pool.
	release()
}

// interactiveTx is the one Tx implementation.
type interactiveTx struct {
	c   *conn
	sc  *sql.Conn
	mon txMonitor

	// mu serializes statements; the state fields are atomics so State and
	// Statements can be read without waiting for a running statement.
	mu    sync.Mutex
	state atomic.Int32
	n     atomic.Int32
}

// txProbeTimeout bounds the state probes and the rollback on Close: none
// of them may hang the caller on a connection that went away.
const txProbeTimeout = 5 * time.Second

func (c *conn) Begin(ctx context.Context) (Tx, error) {
	if c.db == nil {
		return nil, errNotConnected
	}
	begin := c.dialect.beginSQL()
	// BEGIN writes nothing by itself, but everything a transaction exists
	// for does; a read-only session has no use for one and refuses it
	// like any other write, logged as rejected.
	if c.readOnly {
		return nil, c.rejectWrite(begin, nil)
	}
	c.txMu.Lock()
	defer c.txMu.Unlock()
	if c.tx != nil {
		return nil, ErrTxOpen
	}
	sc, err := c.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	_, err = sc.ExecContext(ctx, begin)
	c.logger.recordTx(begin, nil, start, err)
	if err != nil {
		sc.Close()
		return nil, err
	}
	tx := &interactiveTx{c: c, sc: sc, mon: c.dialect.txMonitor(sc, c.logger)}
	c.tx = tx
	return tx, nil
}

func (t *interactiveTx) State() TxState  { return TxState(t.state.Load()) }
func (t *interactiveTx) Statements() int { return int(t.n.Load()) }

// admit decides, before anything reaches the server, whether a statement
// may run in the transaction as it stands.
func (t *interactiveTx) admit(query string) error {
	engine := t.c.Engine()
	switch t.State() {
	case TxClosed:
		return ErrTxClosed
	case TxLost:
		return ErrTxLost
	case TxAborted:
		// ROLLBACK TO SAVEPOINT is the one statement an aborted
		// PostgreSQL transaction accepts, and it revives it.
		if !isRollbackToSavepoint(engine, query) {
			return ErrTxAborted
		}
	}
	if IsTxControl(engine, query) {
		return ErrTxControl
	}
	if CommitsImplicitly(engine, query) {
		return fmt.Errorf("%w: %s commits before %s — commit or roll back first",
			ErrTxImplicitCommit, t.c.dialect.DisplayName(), FirstKeyword(query))
	}
	return nil
}

// settle records the state after a statement and counts it if it ran.
func (t *interactiveTx) settle(ctx context.Context, err error) {
	if err == nil {
		t.n.Add(1)
	}
	// A cancelled statement's own context is done; the probe gets one of
	// its own.
	pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), txProbeTimeout)
	defer cancel()
	t.state.Store(int32(t.mon.after(pctx, err)))
}

// settleCommit is settle for a failed COMMIT.
func (t *interactiveTx) settleCommit(ctx context.Context, err error) {
	pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), txProbeTimeout)
	defer cancel()
	t.state.Store(int32(t.mon.afterCommit(pctx, err)))
}

func (t *interactiveTx) Exec(ctx context.Context, query string, args ...any) (ExecResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.admit(query); err != nil {
		return ExecResult{}, err
	}
	start := time.Now()
	res, err := t.sc.ExecContext(ctx, query, args...)
	t.c.logger.recordTx(query, args, start, err)
	t.settle(ctx, err)
	if err != nil {
		return ExecResult{}, err
	}
	out := ExecResult{}
	if n, err := res.RowsAffected(); err == nil {
		out.RowsAffected = n
	}
	if id, err := res.LastInsertId(); err == nil {
		out.LastInsertID = id
	}
	return out, nil
}

func (t *interactiveTx) QueryLimit(ctx context.Context, query string, max int, args ...any) (*ResultSet, bool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.admit(query); err != nil {
		return nil, false, err
	}
	start := time.Now()
	rows, err := t.sc.QueryContext(ctx, query, args...)
	if err != nil {
		t.c.logger.recordTx(query, args, start, err)
		t.settle(ctx, err)
		return nil, false, err
	}
	rs, capped, err := scanResultSetLimit(ctx, rows, max)
	rows.Close()
	t.c.logger.recordTx(query, args, start, err)
	t.settle(ctx, err)
	return rs, capped, err
}

func (t *interactiveTx) Commit(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch t.State() {
	case TxClosed:
		return ErrTxClosed
	case TxAborted:
		return ErrTxAborted
	case TxLost:
		return ErrTxLost
	}
	start := time.Now()
	_, err := t.sc.ExecContext(ctx, "COMMIT")
	t.c.logger.recordTx("COMMIT", nil, start, err)
	if err == nil {
		t.finish(false)
		return nil
	}
	// The COMMIT failed. Whether the transaction survived it is the
	// engine's to say — SQLite keeps it open on "database is locked",
	// PostgreSQL ends it on a deferred constraint — so it is asked, and
	// the handle stays usable for as long as there is a transaction left.
	t.settleCommit(ctx, err)
	return err
}

func (t *interactiveTx) Rollback(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.rollbackLocked(ctx)
}

func (t *interactiveTx) rollbackLocked(ctx context.Context) error {
	if t.State() == TxClosed {
		return nil
	}
	start := time.Now()
	_, err := t.sc.ExecContext(ctx, "ROLLBACK")
	t.c.logger.recordTx("ROLLBACK", nil, start, err)
	// A transaction the server already ended has nothing to roll back,
	// and some engines say so with an error ("no transaction is active").
	// That is the outcome the user asked for, not a failure.
	lost := t.State() == TxLost
	t.finish(err != nil)
	if lost {
		return nil
	}
	return err
}

// finish spends the handle and returns the connection. bad discards the
// connection instead of pooling it: after a failed ROLLBACK it may still
// hold the transaction, and closing it is what makes the server end it.
func (t *interactiveTx) finish(bad bool) {
	t.state.Store(int32(TxClosed))
	t.mon.release()
	if bad {
		_ = t.sc.Raw(func(any) error { return driver.ErrBadConn })
	}
	t.sc.Close()
	t.c.txMu.Lock()
	if t.c.tx == t {
		t.c.tx = nil
	}
	t.c.txMu.Unlock()
}

// rollbackOpenTx rolls back the session's interactive transaction, if any,
// before the pool closes: Close must never leave one to whatever the
// server does with a dropped connection when it can end it cleanly.
func (c *conn) rollbackOpenTx() {
	c.txMu.Lock()
	tx := c.tx
	c.txMu.Unlock()
	if tx == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), txProbeTimeout)
	defer cancel()
	_ = tx.Rollback(ctx)
}

// ---------- statement classification ----------

// IsTxControl reports whether a statement begins, ends or reconfigures a
// transaction on its own: BEGIN, START TRANSACTION, COMMIT, END, ABORT,
// ROLLBACK (but not ROLLBACK TO SAVEPOINT) and MySQL's SET autocommit.
// SAVEPOINT and RELEASE are not: they nest inside the transaction and
// leave it open.
func IsTxControl(engine Engine, sql string) bool {
	toks := significantTokens(engine, sql)
	if len(toks) == 0 || !toks[0].word {
		return false
	}
	switch strings.ToUpper(toks[0].text) {
	case "BEGIN", "START", "COMMIT", "END", "ABORT":
		return true
	case "ROLLBACK":
		return !isRollbackToSavepoint(engine, sql)
	case "SET":
		if engine != EngineMySQL && engine != EngineMariaDB {
			return false
		}
		for _, t := range toks[1:] {
			name := strings.ToUpper(strings.TrimLeft(t.text, "@"))
			name = strings.TrimPrefix(strings.TrimPrefix(name, "SESSION."), "LOCAL.")
			if name == "AUTOCOMMIT" {
				return true
			}
		}
	}
	return false
}

// isRollbackToSavepoint reports whether a statement is ROLLBACK [WORK |
// TRANSACTION] TO [SAVEPOINT] name — the partial rollback that leaves the
// transaction open.
func isRollbackToSavepoint(engine Engine, sql string) bool {
	toks := significantTokens(engine, sql)
	if len(toks) < 2 || !strings.EqualFold(toks[0].text, "ROLLBACK") {
		return false
	}
	i := 1
	if toks[i].word && (strings.EqualFold(toks[i].text, "WORK") || strings.EqualFold(toks[i].text, "TRANSACTION")) {
		i++
	}
	return i < len(toks) && toks[i].word && strings.EqualFold(toks[i].text, "TO")
}

// implicitCommitKeywords are the leading keywords of the MySQL/MariaDB
// statements that commit an open transaction before they run — DDL and
// the account, lock and table-maintenance statements. See "Statements
// That Cause an Implicit Commit" in the MySQL manual.
var implicitCommitKeywords = map[string]bool{
	"CREATE": true, "ALTER": true, "DROP": true, "RENAME": true, "TRUNCATE": true,
	"GRANT": true, "REVOKE": true, "LOCK": true, "UNLOCK": true,
	"ANALYZE": true, "OPTIMIZE": true, "REPAIR": true, "CACHE": true,
	"FLUSH": true, "RESET": true, "INSTALL": true, "UNINSTALL": true,
}

// CommitsImplicitly reports whether the engine would commit an open
// transaction before running the statement. Only MySQL and MariaDB do:
// the other engines run DDL inside the transaction (see TransactionalDDL).
// CREATE/DROP TEMPORARY TABLE are the documented exception.
func CommitsImplicitly(engine Engine, sql string) bool {
	if TransactionalDDL(engine) {
		return false
	}
	toks := significantTokens(engine, sql)
	if len(toks) == 0 || !toks[0].word {
		return false
	}
	kw := strings.ToUpper(toks[0].text)
	if !implicitCommitKeywords[kw] {
		return false
	}
	if (kw == "CREATE" || kw == "DROP") && secondWordIs(toks, "TEMPORARY") {
		return false
	}
	return true
}
