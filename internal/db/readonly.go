package db

import (
	"errors"
	"strings"
	"time"
)

// A read-only connection is enforced in exactly one place: the driver
// session. Every write lazysql can produce — the query editor's DML/DDL,
// a committed changeset, a staged row operation — reaches the server
// through conn.Exec, conn.ExecTx or one of the Query calls, so guarding
// those three doors covers all of them without a check per call site.
//
// The UI disables its own write entry points on top of this, and the DSN
// asks the engine for a read-only session where it supports one (see
// ReadOnlyParams). Neither is the guarantee: this file is.

// ErrReadOnly is what a read-only session returns instead of running a
// statement that would change something. Its message is the one the UI
// shows verbatim.
var ErrReadOnly = errors.New("connection is read-only")

// IsWrite reports whether a statement would change data or schema on the
// engine, and is what the read-only guard rejects on. It is
// ClassifyStatementFor plus one case the query editor deliberately reads
// the other way: `EXPLAIN ANALYZE` really executes its statement on
// PostgreSQL and MySQL, so a read-only session refuses it even though the
// editor routes it through Query.
func IsWrite(engine Engine, sql string) bool {
	if ClassifyStatementFor(engine, sql) == StatementWrite {
		return true
	}
	toks := significantTokens(engine, sql)
	return len(toks) >= 2 &&
		strings.EqualFold(toks[0].text, "EXPLAIN") &&
		strings.EqualFold(toks[1].text, "ANALYZE")
}

// ContainsWrite reports whether any statement of a script would write.
// The guard asks it rather than IsWrite because what reaches a Query call
// is not always a single statement: a free-text row filter, or any other
// text that ends up in a query, could carry a second statement after a
// `;` that the leading keyword says nothing about.
func ContainsWrite(engine Engine, script string) bool {
	stmts := SplitStatements(engine, script)
	if len(stmts) == 0 {
		// Nothing to run — and nothing to refuse either.
		return false
	}
	return len(WriteStatements(engine, stmts)) > 0
}

// WriteStatements returns the statements of a script that would write, in
// script order. The query editor uses it to reject a run before it starts
// rather than letting the guard fail statement by statement.
func WriteStatements(engine Engine, stmts []string) []string {
	var out []string
	for _, s := range stmts {
		if IsWrite(engine, s) {
			out = append(out, s)
		}
	}
	return out
}

// rejectWrite logs a blocked statement to the command log — as a rejected
// one, with ErrReadOnly as its outcome — and returns the error the caller
// hands back to the UI.
func (c *conn) rejectWrite(query string, args []any) error {
	c.logger.record(rejectedPrefix+query, args, time.Now(), ErrReadOnly)
	return ErrReadOnly
}

// rejectedPrefix marks a command log line as a statement that never ran.
const rejectedPrefix = "-- REJECTED (read-only) "

// rejectTx is rejectWrite for a whole transaction: the changeset commit
// is one user action, so it produces one rejected line naming every
// statement it would have run.
func (c *conn) rejectTx(stmts []Statement) error {
	sql := make([]string, 0, len(stmts))
	for _, s := range stmts {
		sql = append(sql, s.SQL)
	}
	c.logger.record(rejectedPrefix+strings.Join(sql, "; "), nil, time.Now(), ErrReadOnly)
	return ErrReadOnly
}
