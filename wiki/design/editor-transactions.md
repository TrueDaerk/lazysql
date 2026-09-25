---
type: Design Decision
title: Explicit transactions in the query editor
description: Why the editor's BEGIN/COMMIT/ROLLBACK is a db.Tx handle on a dedicated connection with BEGIN and COMMIT issued as SQL, how each engine reports an aborted or ended transaction, how the state stays on screen, and why the staged changeset and CSV import are refused while one is open.
tags: [db, tui, query-editor, transactions, dialect, postgres, duckdb, sqlite, mysql]
generated:
  by: claude-code/opus-5.5
  at: 2026-09-25T00:00:00Z
sources:
  - resource: https://www.postgresql.org/docs/current/protocol-message-formats.html
  - resource: https://dev.mysql.com/doc/refman/8.0/en/implicit-commit.html
  - resource: https://www.sqlite.org/c3ref/commit_hook.html
  - resource: https://www.sqlite.org/c3ref/interrupt.html
  - resource: https://pkg.go.dev/modernc.org/sqlite
---

# Explicit transactions in the query editor

Issue #226. `B` in panel [3] (normal mode) begins a transaction, `C`
commits it and `U` rolls it back. Every statement the editor runs while
one is open — `enter`, `ctrl+r`, a re-run, `r` in the history pane — runs
inside it.

## Decision

### A handle that outlives one call

`Driver.ExecTx` owns the whole lifecycle inside one call, which is right
for the staged changeset and useless here. `Driver.Begin(ctx)` returns a
`db.Tx` (`internal/db/tx.go`) with `Exec`, `QueryLimit`, `Commit`,
`Rollback`, `State` and `Statements`. A session holds at most one
(`ErrTxOpen`). A read-only session refuses `Begin` with `ErrReadOnly`,
and the refusal is logged as rejected like any other blocked write.

### A dedicated connection, never shared

The handle takes one connection out of the pool (`*sql.Conn`) and keeps
it. Every other Driver method keeps using the pool: grid pages and
counts, introspection, autocomplete, `ctrl+e` plans, the changeset
commit. So nothing can join the transaction by accident, and nothing can
break it by running a statement on its connection. The catch is that
they all see committed data only. The UI says so: while a transaction is
open, the grid's tab bar carries `⛁ tx open — grid shows committed data`
when it browses a relation. It does not when it shows an editor result,
because that result came from inside the transaction.

Sharing the pool connection was rejected. database/sql would hand it to
whichever query asked next, so a page load could land in the middle of
the transaction, or a cancelled page query could kill its connection.

### BEGIN/COMMIT/ROLLBACK as SQL, not `*sql.Tx`

A `*sql.Tx` is finished the moment `Commit` returns, whatever the server
said. modernc's SQLite driver even runs a `rollback` after a failed
`COMMIT`, to hand database/sql a clean connection. A `COMMIT` refused
with `database is locked` (another connection mid-read) would then
destroy the work. Issuing the statements on the `*sql.Conn` instead lets
a failed `COMMIT` leave the transaction open. The monitor (below) says
in what state, and the user can retry or roll back. A failed `ROLLBACK`
marks the connection bad (`Raw` returning `driver.ErrBadConn`), so the
pool discards it instead of reusing a connection still inside a
transaction.

`conn.Close` rolls back an open transaction before closing the pool.
Disconnect, quit and connection replacement therefore never leave it to
whatever the server does with a dropped connection.

### State, asked of the engine

`TxState` is `TxActive`, `TxAborted` (only ROLLBACK is accepted),
`TxLost` (the server already ended it) or `TxClosed`. Each dialect
supplies a `txMonitor` (`tx_dialects.go`) that is asked after
statements:

| Engine | How the state is known |
|---|---|
| PostgreSQL | `PgConn().TxStatus()` from the ReadyForQuery byte (`T`/`E`/`I`), read through `sql.Conn.Raw`, after every statement. It is exact and costs no round trip, and it also sees `ROLLBACK TO SAVEPOINT` revive an aborted transaction. |
| DuckDB | After a failure it probes with `SELECT 1 /* lazysql: … */`: `Current transaction is aborted` → `TxAborted`. Execution errors (constraints) abort, bind errors (unknown table) do not, and the two look alike from outside. The probe is logged as introspection, tagged `[tx]`. |
| MySQL/MariaDB | Error 1213 (deadlock) → `TxLost`. So is 1205 (lock wait timeout) when `@@innodb_rollback_on_timeout` is on. After either, the session is back in autocommit. |
| SQLite | A rollback hook (`RegisterRollbackHook` on modernc's conn) is registered at `Begin` and removed before the connection is pooled again. SQLite rolls back the whole transaction on its own after an interrupted write, a full disk or an I/O error, and the hook fires on exactly those. `ROLLBACK TO` does not fire it. |
| all | A failure that took the connection (`ErrBadConn`, `ErrConnDone`, `mysql.ErrInvalidConn`, a failing `Ping`) → `TxLost`. Cancelling a statement on pgx or the MySQL driver closes the connection. |

`Commit` is refused with `ErrTxAborted`/`ErrTxLost` without contacting
the server when the state forbids it. On DuckDB this is load-bearing: it
answers `COMMIT` on an aborted transaction with **success** while it
rolls everything back (verified against go-duckdb v2.4.3). Every answer
that is not certain errs towards the state that forbids COMMIT.

### Statements refused inside the transaction

- `BEGIN`, `START`, `COMMIT`, `END`, `ABORT`, a `ROLLBACK` that is not
  `ROLLBACK … TO`, and MySQL's `SET autocommit` (`ErrTxControl`). Run as
  SQL, they would end or nest the transaction behind the handle's back.
  `SAVEPOINT`, `RELEASE` and `ROLLBACK TO` are allowed.
- On MySQL/MariaDB, the statements that commit implicitly: DDL and the
  lock, account and maintenance statements, except `CREATE/DROP
  TEMPORARY TABLE` (`ErrTxImplicitCommit`). After an implicit commit the
  session autocommits while the UI would still say "in transaction".
- In an aborted transaction, everything except `ROLLBACK TO`.

Classification runs on the tokenizer (`significantTokens`), like the
read-only guard, so a keyword in a comment or literal does not count.

### Always on screen

- The options bar's right side carries `⛁ TX open · N statements · 42s`
  whichever panel is focused. It turns red as `TX ABORTED — U rolls
  back` or `TX ENDED BY SERVER — U closes`. The bindings are sized
  around it, and on a terminal too narrow for both sides the version
  string goes first and the badge stays.
- Panel [3]'s title, the editor's main-view title and its status line
  carry the same badge. A run's outcome ends in `(in transaction)`.
- The command log prefixes every statement run through the handle with
  `[tx]`, via `LogEntry.InTx`, including BEGIN, COMMIT and ROLLBACK. The
  UI adds `-- transaction open/committed/rolled back/ABORTED` lines.

The UI mirrors `State`/`Statements` from the worker's messages
(`queryStmtMsg.txState`) rather than reading the handle in `View`.
Everything touching the handle runs in a `tea.Cmd`. The one exception
is the synchronous rollback in `closeSession` on quit, where a batched
command may never run (the same reason the driver is closed
synchronously there).

### Keys

`B`/`C`/`U` are panel [3] normal-mode actions in `panelActions`. They
mirror the grid's `c`/`U` for the changeset. All three stay enabled, so
`?` always documents them. The options bar filters them by state: `B`
when none is open (and not on a read-only connection, via
`writeBindings`), `C`/`U` while one is. `C` always confirms. `U`
confirms unless the transaction is empty or already ended.

### Never interleaved with the changeset

With a transaction open, `c` (commit staged changes) and `I` (CSV
import) are refused with an explanation. Both commit on another
connection: they would not see the transaction's uncommitted rows, and
on PostgreSQL/MySQL they would block forever on row locks it holds,
because lazysql would be waiting on itself. Nesting the changeset as a
savepoint was rejected: the changeset's "all or nothing, then reload"
contract would become "all or nothing until the editor rolls back".

### Leaving with a transaction open

Quit, disconnect (`x`), connecting to a profile (including a
password-prompted one), and removing the active profile all confirm
first through `guardTx`/`quitLosses`/`txDropNote`, naming the statement
count. The rollback runs on confirm. A connection replaced some other
way (an ephemeral file opened) logs the rollback when the new session
lands.

## Consequences

- An editor result streamed to a file by export re-runs its statement on
  the pool, so it sees committed data only. The same goes for `ctrl+e`
  and `ctrl+a`.
- On SQLite a write inside the transaction holds the RESERVED lock.
  Other connections' writes fail with `database is locked` until it
  ends, which is one more reason the changeset and import are refused.
- Tests: `internal/db/tx_test.go` runs against SQLite (a temp file, since
  the default temporary database is private to each pooled connection)
  and in-memory DuckDB. It covers commit persists, rollback discards,
  errors mid-transaction per engine, the bind-error case, savepoints,
  refused control statements, one transaction per session, read-only
  refusal, rollback on Close, and `[tx]` log tagging.
  `internal/ui/tx_test.go` covers the key flows, the aborted-state UI on
  DuckDB, and the quit, disconnect, changeset, read-only and options-bar
  behavior.

See also [design/staged-changeset](staged-changeset.md),
[design/query-editor-panel](query-editor-panel.md),
[design/read-only-connections](read-only-connections.md) and
[reference/query-cancellation-per-dialect](../reference/query-cancellation-per-dialect.md).
