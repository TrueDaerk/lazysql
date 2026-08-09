---
type: Design Decision
title: Database driver abstraction in internal/db
description: One generic Driver over database/sql plus a Dialect per engine; UI never imports concrete SQL drivers.
tags: [db, architecture, dialect]
generated:
  by: claude-code/fable-5
  at: 2026-08-09T00:00:00Z
---

# Database driver abstraction

## Decision

`internal/db` exposes a single `Driver` interface and exactly one
implementation (`conn`, a thin wrapper over `*sql.DB`). Everything
engine-specific lives in a `Dialect` value: identifier quoting,
placeholder style, pagination clause, and all introspection queries.
The five engines (MySQL, MariaDB, PostgreSQL, SQLite, DuckDB) register
their dialects in `init()` inside `internal/db`; concrete
`database/sql` drivers are imported only there.

Key choices:

- **One conn, many dialects** — instead of five Driver implementations,
  the generic `conn` delegates to dialect methods that receive a
  narrow `querier` interface. Introspection differences stay in one
  file per engine; connection handling, scanning, paging and exec are
  written once.
- **Dialect methods are unexported** (`listRelations`, `tableDDL`, ...)
  and `driverName()` too, so outside packages can use quoting and
  placeholders but cannot bypass the Driver.
- **MariaDB = MySQL dialect** with separate `Engine`/display name — the
  wire protocol and `information_schema` layout are identical.
- **Normalized result sets** — `ResultSet.Rows` cells are only
  nil / string / int64 / float64 / bool / time.Time. `[]byte` is copied
  to string during scan because drivers may reuse the buffer.
- **Cancellation** — every method takes `context.Context`; the row scan
  loop also checks `ctx.Err()` between rows so materialization aborts
  promptly.
- **Relations carry their kind** — the dialect method is
  `listRelations`, returning `[]Relation` (name + table/view). The
  name-only `Driver.ListTables` is derived from it, so the `[3] Tables`
  panel can split its sub-tabs from one query — see
  [design/catalog-browsing](catalog-browsing.md).
- **Namespaces** — `ListDatabases` means: databases (MySQL/MariaDB),
  schemas of the connected database (PostgreSQL, which cannot query
  across databases on one connection), attached databases (SQLite,
  DuckDB).
- **User filters are raw SQL** — `QueryPage`'s filter is the user's own
  WHERE fragment against their own database, so it is interpolated as
  written; sort columns and table names are dialect-quoted, values in
  `Exec`/`Query` are always parameterized.

## Drivers

- MySQL/MariaDB: `go-sql-driver/mysql` (pure Go)
- PostgreSQL: `jackc/pgx/v5/stdlib` (pure Go)
- SQLite: `modernc.org/sqlite` (pure Go, chosen over `mattn/go-sqlite3`
  to avoid CGO)
- DuckDB: `marcboeker/go-duckdb/v2` — **requires CGO** (bundled C++
  engine); documented in the README build section.

See [dialect quirks](../reference/dialect-introspection-quirks.md).
