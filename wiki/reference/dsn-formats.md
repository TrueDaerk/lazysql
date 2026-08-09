---
type: Dialect Note
title: DSN formats per engine
description: How internal/db turns ConnParams into a driver-specific connection string, and the quirks each engine imposes.
tags: [db, dsn, dialect, mysql, postgres, sqlite, duckdb]
generated:
  by: claude-code/opus-5
  at: 2026-08-09T00:00:00Z
sources:
  - resource: https://github.com/go-sql-driver/mysql#dsn-data-source-name
  - resource: https://pkg.go.dev/modernc.org/sqlite
  - resource: https://github.com/marcboeker/go-duckdb#connecting
---

# DSN formats per engine

`db.BuildDSN(engine, ConnParams)` is the only place a connection string is
assembled. UI and config code never concatenate DSNs.

| Engine | Shape |
| --- | --- |
| MySQL / MariaDB | `user:pass@tcp(host:port)/db?parseTime=true` |
| PostgreSQL | `postgres://user:pass@host:port/db?sslmode=…` |
| SQLite | bare path, or `file:<path>?<opts>` when options are set |
| DuckDB | bare path, or `<path>?<opts>`; empty path = in-memory |

## Quirks

- **MySQL escaping is not URL escaping.** The DSN is built with
  `mysql.NewConfig()` + `FormatDSN()` rather than string concatenation;
  hand-rolling it breaks on passwords containing `@`, `/` or `:`.
- **`parseTime=true` is mandatory, not cosmetic.** Without it the driver
  hands back `[]byte` for `DATE`/`DATETIME`, which violates the
  `ResultSet` contract that cells are `nil`/string/int64/float64/bool/
  `time.Time`.
- **A unix socket is expressed as a host.** A `host` starting with `/`
  switches the MySQL net from `tcp` to `unix` and uses the path as the
  address; there is no separate socket field in the form.
- **SQLite needs the `file:` URI form for options.** A bare path with a
  query string is read as a literal filename containing `?`. Options are
  only wrapped in `file:` when there is at least one, so ordinary paths stay
  readable in the command log.
- **DuckDB uses a bare path plus a query string** — no `file:` prefix — and
  an empty path is a valid in-memory database. That is why
  `config.Connection.Validate` requires a file path for SQLite but not for
  DuckDB.
- **Option order is sorted, not map order.** Go randomizes map iteration;
  without sorting, the DSN written to the command log would change between
  identical connects.

## Redaction

`db.RedactDSN` substitutes the literal `REDACTED` (see `db.PasswordMask`)
before building, so the command log shows a real, readable DSN with no
secret. The mask is alphanumeric on purpose: `***` survives MySQL's format
intact but percent-encodes to `%2A%2A%2A` inside a PostgreSQL URL.

## Related

- [design/db-driver-abstraction](../design/db-driver-abstraction.md)
- [design/connection-secrets](../design/connection-secrets.md)
