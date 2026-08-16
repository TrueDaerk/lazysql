---
type: Dialect Note
title: Server activity and session kills per engine
description: Where each engine keeps its session list and its lock waits, the version floors behind pg_stat_activity's columns, why MySQL needs a second (best-effort) query for blockers while PostgreSQL answers with pg_blocking_pids, what "duration" means on each, and why KILL takes a validated literal rather than a placeholder.
tags: [db, dialect, activity, processlist, locks, kill, postgres, mysql, mariadb, sqlite, duckdb]
generated:
  by: claude-code/opus-5
  at: 2026-08-16T12:00:00Z
sources:
  - resource: https://dev.mysql.com/doc/refman/8.0/en/information-schema-processlist-table.html
  - resource: https://dev.mysql.com/doc/refman/8.0/en/performance-schema-data-lock-waits-table.html
  - resource: https://dev.mysql.com/doc/refman/8.0/en/kill.html
  - resource: https://mariadb.com/kb/en/information-schema-innodb_lock_waits-table/
  - resource: https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ACTIVITY-VIEW
  - resource: https://www.postgresql.org/docs/current/functions-info.html
  - resource: https://github.com/TrueDaerk/lazysql/issues/151
    title: "Issue #151 — Add server activity view: processes, locks, kill session"
---

# Server activity and session kills per engine

What `Dialect.listProcesses` / `Dialect.killProcessSQL`
(`internal/db/activity.go` and the dialect files) have to absorb so the
UI only ever sees a `[]db.Process`.

## The shared column contract

Every server dialect's `processListSQL` returns the same nine columns, in
this order, and `scanProcesses` reads only that:

```
id, user, database, client, state, duration_seconds, query, blocked_by, is_self
```

`duration_seconds` and `blocked_by` may be `NULL` — "not running
anything" and "not blocked, or the engine cannot say". `blocked_by` is a
**comma-separated string**, not an array: PostgreSQL's `pg_blocking_pids`
returns `int[]`, and `array_to_string` on the server side means no
dialect has to agree with another about array decoding through
`database/sql`.

## MySQL / MariaDB

Sessions come from `information_schema.processlist`. MySQL 8.0.22
deprecated it in favour of `performance_schema.processlist` but has not
removed it, and both views carry the same rows; the
`information_schema` spelling is the one that works on MariaDB and on
MySQL 5.7 as well.

Two columns are shaped rather than passed through:

- **state** is `COMMAND` plus `STATE`: `Query` alone says nothing and
  `Sending data` alone does not say it is a query.
- **duration** is `TIME`, which is *seconds in the current state*. For a
  `Sleep` command that is how long the connection has been idle, which is
  not work — so it is reported as `NULL`, and idle sessions sort to the
  bottom instead of on top of the long query the view exists to surface.

### Lock waits are a second query, and a fragile one

There is no blocker column in the process list. The waiter → blocker
mapping lives in an InnoDB catalog that has moved on both engines:

| Engine | Catalog | Note |
|---|---|---|
| MySQL 8.0+ | `performance_schema.data_lock_waits` joined to `information_schema.innodb_trx` | What `sys.innodb_lock_waits` is built from |
| MySQL 5.7 | `information_schema.innodb_lock_waits` | Removed in 8.0 |
| MariaDB ≤ 10.5 | `information_schema.innodb_lock_waits` | Deprecated in 10.5.2 |
| MariaDB 10.6+ | — | Both `INNODB_LOCKS` and `INNODB_LOCK_WAITS` were removed |

`mysqlDialect.listProcesses` therefore runs the query for its engine
(`mysqlLockWaitsSQL`) as **best-effort**: its error is swallowed and the
process list is returned without blockers. A server whose lock views are
gone, disabled, or not permitted still gets its activity view; only the
`Blocked by` column stays empty. The attempt itself is visible in the
command log like every other statement, so the failure is never silent —
it is just not fatal.

`performance_schema.data_lock_waits` also requires `performance_schema`
to be on (the default since MySQL 5.6) and the InnoDB instrumentation
enabled.

## PostgreSQL

`pg_stat_activity` carries the lock waits itself:
`pg_blocking_pids(pid)` is the server's own answer to "who is this
backend waiting for", so unlike MySQL there is no second catalog and no
best-effort path.

Version floors, which is the part that bites on old servers:

| Feature | Since |
|---|---|
| `pg_blocking_pids()` | 9.6 |
| `wait_event_type` / `wait_event` | 9.6 |
| `backend_type` | 10 |
| `EXTRACT(EPOCH …)` returns `numeric` (it was `double precision`) | 14 |

lazysql's query uses all four, so it needs **PostgreSQL 10 or newer**; on
9.x it fails with the server's own "column does not exist" message rather
than silently degrading. The `EXTRACT` is cast to `float8` explicitly so
the Go side sees one type on every version.

Three more decisions worth naming:

- **`backend_type` filters the rows** to `client backend` plus the
  autovacuum workers. Without it the list is topped by the checkpointer,
  the walwriter and the background writer — processes that have been
  "running" since server start and would own the top of a
  duration-sorted list forever.
- **An idle backend gets no duration.** `state_change` would give one,
  but "idle for six hours" is not work. `idle in transaction` is *not*
  idle by this rule: it holds locks, so it keeps its duration and stays
  near the top where it belongs.
- **The wait event is only appended to a working backend's state.** Every
  idle backend waits on `Client: ClientRead`, which says nothing;
  `Lock: transactionid` on a working one says everything.

## SQLite / DuckDB

Both run inside the lazysql process. There is no session but ours, no
catalog to list and nothing to kill, so all three dialect entry points
answer `ErrUnsupported` rather than an empty list — the same distinction
`listTriggers` draws for DuckDB (see
[trigger-introspection](trigger-introspection.md)). The UI renders it as
a notice naming the engines that do support it, not as an error.

## Privileges — what a non-admin actually sees

Neither engine errors when the connected user may not see everything; both
answer with less, which is why the view can look wrong rather than broken.

| Engine | Without the privilege | With it |
|---|---|---|
| MySQL / MariaDB | Only the user's own sessions are listed | `PROCESS` shows every session; `SUPER` (MySQL) / `CONNECTION ADMIN` (MariaDB) is needed to kill another user's |
| PostgreSQL | Other backends' `query` reads `<insufficient privilege>` | `pg_read_all_stats` (or superuser) shows the statements; `pg_signal_backend` (or superuser) is needed for `pg_terminate_backend` on another role's backend |

A user may always kill their own sessions on both engines.

## Killing a session

| Engine | Statement | Answers |
|---|---|---|
| MySQL / MariaDB | `KILL CONNECTION <id>` | nothing |
| PostgreSQL | `SELECT pg_catalog.pg_terminate_backend(<id>)` | `true` / `false` |

Two things follow from that table:

- **The id is a validated literal, not a placeholder.** MySQL's `KILL`
  takes no parameter marker outside a stored program, so a
  half-parameterized pair of dialects would be harder to audit than one
  rule that holds for both: `db.processID` accepts a decimal integer and
  nothing else, and anything else is an error before a statement exists.
  Every other part of the statement is a constant in this package.
- **PostgreSQL's `false` is turned into an error.** It means the backend
  was already gone, or this role may not signal it — neither of which is
  a success, and the driver would otherwise report one. MySQL says the
  same thing with an error from the server, so both engines end up
  reporting the same way.

`KILL CONNECTION` rather than plain `KILL` (they mean the same thing) is
deliberate: the other form, `KILL QUERY`, cancels the statement and keeps
the session, and a destructive statement should say which of the two it
is. `pg_terminate_backend` rather than `pg_cancel_backend` is the same
choice — cancelling would leave the transaction, and its locks, in place.
