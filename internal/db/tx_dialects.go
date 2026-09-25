package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/stdlib"
	"modernc.org/sqlite"
)

// The per-engine half of interactive transactions (tx.go): how each one
// spells BEGIN, and how it tells lazysql where a transaction stands after
// a statement. Every answer that is not certain errs towards the state
// that forbids COMMIT — a rollback the user did not need costs a re-run,
// a COMMIT of an aborted transaction costs believing work was saved.

// connGone reports whether a statement's failure took the connection with
// it — the one outcome every engine shares, and one that ends any
// transaction on it. A cancelled statement is where this happens most:
// pgx and the MySQL driver both close the connection to interrupt one.
func connGone(ctx context.Context, sc *sql.Conn, err error) bool {
	if errors.Is(err, driver.ErrBadConn) || errors.Is(err, sql.ErrConnDone) ||
		errors.Is(err, mysql.ErrInvalidConn) {
		return true
	}
	return sc.PingContext(ctx) != nil
}

// ---------- PostgreSQL ----------

func (postgresDialect) beginSQL() string { return "BEGIN" }

// pgTxMonitor reads the transaction status PostgreSQL reports with every
// ReadyForQuery message — 'T' in a transaction, 'E' in a failed one, 'I'
// idle — straight off the pgx connection. It is exact and costs no round
// trip, so it is asked after every statement, not only failed ones: a
// ROLLBACK TO SAVEPOINT that revives an aborted transaction shows up too.
type pgTxMonitor struct{ sc *sql.Conn }

func (postgresDialect) txMonitor(sc *sql.Conn, _ *Logger) txMonitor { return pgTxMonitor{sc} }

func (m pgTxMonitor) after(ctx context.Context, err error) TxState {
	st, pgx := TxLost, true
	rawErr := m.sc.Raw(func(dc any) error {
		c, ok := dc.(*stdlib.Conn)
		if !ok {
			pgx = false
			return nil
		}
		pc := c.Conn().PgConn()
		if pc.IsClosed() {
			return nil
		}
		switch pc.TxStatus() {
		case 'T':
			st = TxActive
		case 'E':
			st = TxAborted
		}
		return nil
	})
	switch {
	case rawErr != nil:
		return TxLost
	case !pgx:
		// Not pgx underneath — cannot happen with this dialect's driver;
		// fall back to what every engine can tell.
		return genericAfter(ctx, m.sc, err)
	}
	return st
}

func (m pgTxMonitor) afterCommit(ctx context.Context, err error) TxState { return m.after(ctx, err) }
func (pgTxMonitor) release()                                             {}

// ---------- MySQL / MariaDB ----------

func (mysqlDialect) beginSQL() string { return "START TRANSACTION" }

// mysqlTxMonitor classifies by error number. MySQL keeps a transaction
// open through an ordinary statement error — the failed statement alone is
// undone — but a deadlock (1213) rolls the whole transaction back, and so
// does a lock wait timeout (1205) on a server running
// innodb_rollback_on_timeout. Either way the session is back in autocommit
// mode, so every statement after it would commit on its own: TxLost.
type mysqlTxMonitor struct{ sc *sql.Conn }

func (mysqlDialect) txMonitor(sc *sql.Conn, _ *Logger) txMonitor { return mysqlTxMonitor{sc} }

func (m mysqlTxMonitor) after(ctx context.Context, err error) TxState {
	var me *mysql.MySQLError
	if errors.As(err, &me) {
		switch me.Number {
		case 1213:
			return TxLost
		case 1205:
			var on int
			if m.sc.QueryRowContext(ctx, "SELECT @@innodb_rollback_on_timeout").Scan(&on) != nil || on != 0 {
				return TxLost
			}
		}
	}
	return genericAfter(ctx, m.sc, err)
}

// afterCommit: a COMMIT MySQL rejects has not left a transaction to retry.
func (mysqlTxMonitor) afterCommit(context.Context, error) TxState { return TxLost }
func (mysqlTxMonitor) release()                                   {}

// ---------- SQLite ----------

func (sqliteDialect) beginSQL() string { return "BEGIN" }

// sqliteTxMonitor registers a rollback hook on the connection. SQLite
// keeps a transaction open through ordinary statement errors — a
// constraint failure undoes the statement alone — but rolls the whole
// transaction back on its own after some of them: an interrupted write
// (a cancelled statement), a full disk, an I/O error. The hook fires on
// exactly those, which no error code alone reliably tells. ROLLBACK TO a
// savepoint does not fire it.
type sqliteTxMonitor struct {
	sc         *sql.Conn
	rolledBack *atomic.Bool
	hooked     bool
}

// sqliteHooks is the part of modernc's connection the monitor uses.
type sqliteHooks interface {
	RegisterRollbackHook(sqlite.RollbackHookFn)
}

func (sqliteDialect) txMonitor(sc *sql.Conn, _ *Logger) txMonitor {
	m := sqliteTxMonitor{sc: sc, rolledBack: new(atomic.Bool)}
	flag := m.rolledBack
	_ = sc.Raw(func(dc any) error {
		if h, ok := dc.(sqliteHooks); ok {
			h.RegisterRollbackHook(func() { flag.Store(true) })
			m.hooked = true
		}
		return nil
	})
	return m
}

func (m sqliteTxMonitor) after(ctx context.Context, err error) TxState {
	if m.rolledBack.Load() {
		return TxLost
	}
	return genericAfter(ctx, m.sc, err)
}

// afterCommit: a COMMIT SQLite refuses — "database is locked" while
// another connection reads — keeps the transaction, and retrying it is
// exactly what the user should be able to do. The hook still says if it
// did not.
func (m sqliteTxMonitor) afterCommit(ctx context.Context, err error) TxState {
	return m.after(ctx, err)
}

func (m sqliteTxMonitor) release() {
	if !m.hooked {
		return
	}
	_ = m.sc.Raw(func(dc any) error {
		if h, ok := dc.(sqliteHooks); ok {
			h.RegisterRollbackHook(nil)
		}
		return nil
	})
}

// ---------- DuckDB ----------

func (duckdbDialect) beginSQL() string { return "BEGIN TRANSACTION" }

// duckdbTxMonitor probes after a failure. DuckDB aborts the transaction
// on an error raised while executing — a constraint violation — but not
// on one raised while binding (an unknown table), and from the outside
// the two look alike. So a trivial SELECT is sent: an aborted
// transaction refuses it with "Current transaction is aborted". The probe
// matters doubly here because DuckDB answers COMMIT on an aborted
// transaction with success while it rolls everything back.
type duckdbTxMonitor struct {
	sc     *sql.Conn
	logger *Logger
}

func (duckdbDialect) txMonitor(sc *sql.Conn, logger *Logger) txMonitor {
	return duckdbTxMonitor{sc: sc, logger: logger}
}

func (m duckdbTxMonitor) after(ctx context.Context, err error) TxState {
	if err == nil {
		return TxActive
	}
	if connGone(ctx, m.sc, err) {
		return TxLost
	}
	if isDuckDBAborted(err) {
		return TxAborted
	}
	const probe = "SELECT 1 /* lazysql: is the transaction still usable? */"
	start := time.Now()
	rows, perr := m.sc.QueryContext(ctx, probe)
	if rows != nil {
		rows.Close()
	}
	m.logger.add(LogEntry{SQL: probe, At: start, Duration: time.Since(start), Err: perr,
		Introspection: true, InTx: true})
	if perr != nil && isDuckDBAborted(perr) {
		return TxAborted
	}
	if perr != nil {
		return TxLost
	}
	return TxActive
}

// afterCommit: a COMMIT DuckDB rejects — a write-write conflict — has
// rolled the transaction back already.
func (duckdbTxMonitor) afterCommit(context.Context, error) TxState { return TxLost }
func (duckdbTxMonitor) release()                                   {}

func isDuckDBAborted(err error) bool {
	return err != nil && strings.Contains(err.Error(), "transaction is aborted")
}

// genericAfter is the answer for an engine that keeps its transaction
// through statement errors: open unless the connection went away.
func genericAfter(ctx context.Context, sc *sql.Conn, err error) TxState {
	if err == nil {
		return TxActive
	}
	if connGone(ctx, sc, err) {
		return TxLost
	}
	return TxActive
}
