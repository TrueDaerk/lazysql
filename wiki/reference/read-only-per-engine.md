---
type: Dialect Note
title: Engine-level read-only sessions
description: What each engine takes to open a session read-only, what it costs, and where it silently does not apply.
tags: [db, dsn, dialect, read-only, mysql, mariadb, postgres, sqlite, duckdb]
generated:
  by: claude-code/opus-5
  at: 2026-08-10T00:00:00Z
sources:
  - resource: https://www.sqlite.org/uri.html
  - resource: https://duckdb.org/docs/connect/overview
  - resource: https://www.postgresql.org/docs/current/runtime-config-client.html
  - resource: https://github.com/go-sql-driver/mysql#system-variables
  - resource: https://mariadb.com/kb/en/server-system-variables/
---

# Engine-level read-only sessions

A read-only connection (`read_only = true` on a profile) is enforced by
the driver session in `internal/db` — see
[design/read-only-connections](../design/read-only-connections.md). What
this note describes is the *second* lock: the engine's own read-only
mode, asked for through the DSN in `db.ReadOnlyParams`. It is defence in
depth, never the guarantee, because not every engine or version honours
it and one of them cannot be given it at all.

| Engine | DSN parameter | Applies to |
| --- | --- | --- |
| SQLite | `mode=ro` | the whole connection |
| DuckDB | `access_mode=read_only` | the whole connection |
| PostgreSQL | `default_transaction_read_only=on` | every transaction of the session |
| MySQL / MariaDB | `transaction_read_only=1` | every transaction of every pooled connection |

## SQLite

`mode=ro` is a URI parameter, so it only takes effect in the `file:` form
— `fileDSN` switches to it as soon as any option is present, which a
read-only profile always has. Consequences:

- **The database file must already exist.** `mode=ro` never creates one;
  connecting to a path that is not there fails instead of opening an
  empty database. That is the right failure for a guardrail, but it makes
  a read-only profile useless for a scratch database.
- Writes fail with the engine's own `attempt to write a readonly
  database`, not with `db.ErrReadOnly` — the session guard usually gets
  there first, so the engine error only surfaces if the guard is bypassed.
- The mode is per connection handle, so `database/sql` pooling cannot
  produce a writable connection behind it.

## DuckDB

`access_mode=read_only` needs a database file. An **in-memory** DuckDB
database (empty `file`) cannot be opened read-only — there would be
nothing in it to read — and the driver refuses the combination outright.
`engineReadOnlyParams` therefore drops the parameter for an in-memory
DuckDB profile rather than making the connection fail; the session guard
still refuses every write, so the mode is intact where it matters.

Read-only DuckDB also cannot attach or install extensions, which is
invisible here only because lazysql does neither.

## PostgreSQL

`default_transaction_read_only` travels to the server as a startup
parameter — pgx passes any DSN parameter it does not recognize through as
a runtime parameter — so it applies to every transaction the session
opens, including implicit single-statement ones. A write fails with
`ERROR: cannot execute … in a read-only transaction` (SQLSTATE 25006).

It is a *default*, not a lock: `SET TRANSACTION READ WRITE` inside the
session would undo it. Nothing in lazysql sends that, and the session
guard would refuse the statement, but it is why this cannot be the
enforcement layer.

## MySQL / MariaDB

There is no DSN flag for this; `transaction_read_only` is a system
variable. `go-sql-driver/mysql` sends every unknown DSN parameter as a
`SET <name>=<value>` during connection setup, which is the only spelling
that survives `database/sql` pooling: a `SET SESSION …` executed once
after `Connect` would apply to exactly one pooled connection and silently
not to the next one the pool dials.

Version floor: MySQL knows `transaction_read_only` from 5.7.20 (it
replaced `tx_read_only`, removed in 8.0.3), MariaDB from 10.2.2. An older
server **fails the handshake** with `Unknown system variable` rather than
connecting read-write — the connection is refused, not silently
downgraded, which is the safe direction for a guardrail but is worth
recognizing in a bug report.

`transaction_read_only` does not stop DDL on MySQL — `CREATE TABLE` is
not transactional there and runs regardless. That gap is exactly why the
session guard, which classifies DDL as a write like anything else, is the
layer that actually enforces the mode.
