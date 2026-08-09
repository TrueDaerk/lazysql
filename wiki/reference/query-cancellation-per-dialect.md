---
type: Dialect Note
title: Query cancellation per engine
description: How each driver actually reacts to a cancelled context mid-statement — server-side abort vs. client-side connection drop.
tags: [db, dialect, cancellation, context, sqlite, duckdb, postgres, mysql]
generated:
  by: claude-code/sonnet-5
  at: 2026-08-09T00:00:00Z
sources:
  - resource: https://pkg.go.dev/modernc.org/sqlite
  - resource: https://pkg.go.dev/github.com/marcboeker/go-duckdb/v2
  - resource: https://pkg.go.dev/github.com/go-sql-driver/mysql
  - resource: https://pkg.go.dev/github.com/jackc/pgx/v5
---

# Query cancellation per engine

`ctrl+c` (`m.cancelQuery`, `internal/ui/query.go`) calls the run's
`context.CancelFunc`. Every `Driver` method takes that `ctx` straight
through to `database/sql`'s `*Context` calls, so what actually happens
next is up to the driver — and it is not the same shape for all four
engines. Verified by reading each driver's source (versions pinned in
`go.mod`), not just its docs.

## SQLite (`modernc.org/sqlite`, driver name `sqlite`)

Genuine engine-level interrupt. `interruptOnDone` (`sqlite.go`) spawns a
goroutine per statement that waits on `ctx.Done()` and calls
`sqlite3_interrupt()` on the connection. SQLite's own execution loop
checks for the interrupt flag between opcodes, so a long scan or a
recursive CTE aborts within one VM step, not at the next network
round trip — there is no network round trip. The connection is fully
usable for the next statement immediately after. This is the driver
the in-repo test (`TestWorkerAbortsAStatementInFlight`,
`TestQueryRunIsCancellable`) exercises directly with a 20-second
recursive CTE.

## DuckDB (`marcboeker/go-duckdb/v2`, driver name `duckdb`)

Also a genuine engine-level interrupt, but through a different C API.
`Stmt.executeBound` (`statement.go`) starts the query via DuckDB's
*Pending Result* interface (`duckdb_pending_prepared` +
`duckdb_execute_pending`) instead of running it to completion in one
call, and races a goroutine on `ctx.Done()` against each execution
step; a cancellation calls `mapping.Interrupt()`, DuckDB's native
abort. Same shape as SQLite: no network hop, connection reusable
right after.

## PostgreSQL (`jackc/pgx/v5` via `stdlib`, driver name `pgx`)

Server-side abort over the wire, but through Postgres's own protocol
rather than an in-process interrupt: pgx's `pgconn` sends a
`CancelRequest` (`pgproto3.CancelRequest`) on a **second** connection
to the same backend, which is exactly what `psql`'s own `ctrl+c` does.
The server responds by aborting the running statement with `ERROR:
canceling statement due to user request`, which the original
connection surfaces as `context.Canceled` once the driver notices.
The original connection stays valid.

## MySQL / MariaDB (`go-sql-driver/mysql`, driver name `mysql`)

**Not** a server-side `KILL QUERY` — the driver instead severs the
client connection. `mysqlConn.startWatcher` (`connection.go`) races a
per-connection watcher goroutine against every context; on
cancellation it calls `mc.cancel()`, which marks the connection
canceled and runs `mc.cleanup()` (closes the socket). The caller sees
`context.Canceled` immediately — the UI is just as responsive as with
the other three engines — but the query itself keeps running
server-side until MySQL's own dead-socket detection notices and kills
it, which is not immediate. `database/sql`'s pool absorbs this
transparently (`sql.DB` dials a fresh connection for the next query),
so nothing in `internal/db` has to special-case it, but a MySQL
connection under heavy load can carry a few extra seconds of
now-orphaned work after `ctrl+c` returns.

## What this means for `internal/db`

No driver-specific code was needed: `context.Context` cancellation
propagates correctly through `database/sql`'s `*Context` methods for
all four engines, and all four report `context.Canceled` (checked with
`errors.Is`, never a string match — `mysqlConn.cancel` wraps the same
sentinel). The one thing worth remembering when debugging a "why did
this UPDATE still apply after I cancelled it" report against MySQL is
the paragraph above: cancellation there is a promise about the
*client*, not the server.
