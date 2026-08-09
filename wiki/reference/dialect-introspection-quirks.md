---
type: Dialect Note
title: Introspection quirks per engine
description: Engine-specific gotchas hit while implementing internal/db introspection.
tags: [db, dialect, introspection, sqlite, duckdb, postgres, mysql]
generated:
  by: claude-code/fable-5
  at: 2026-08-09T00:00:00Z
sources:
  - resource: https://www.sqlite.org/pragma.html
  - resource: https://duckdb.org/docs/sql/duckdb_table_functions
  - resource: https://www.postgresql.org/docs/current/catalog-pg-index.html
---

# Introspection quirks per engine

## SQLite (`modernc.org/sqlite`, driver name `sqlite`)

- `PRAGMA` takes **no placeholders** — schema and table names must be
  interpolated; always pass them through `QuoteIdent` first.
- Schema-qualified pragma form: `PRAGMA "main".table_info("users")`.
- `PRAGMA index_list` marks the implicit primary-key index with
  `origin = 'pk'`; `INTEGER PRIMARY KEY` (rowid alias) produces **no**
  index_list entry at all.
- Original DDL comes from `sqlite_master.sql` (NULL for auto-created
  indexes — filter `sql IS NOT NULL`).
- In-memory DSN: `:memory:`.

## DuckDB (`marcboeker/go-duckdb/v2`, driver name `duckdb`)

- Requires CGO; empty DSN opens an in-memory database.
- Use `duckdb_tables()`, `duckdb_views()`, `duckdb_columns()`,
  `duckdb_constraints()`, `duckdb_indexes()`. All carry
  `database_name`, so scope by `current_database()` when no database
  given; `duckdb_views()` needs `NOT internal` or system views flood
  the list.
- Primary-key columns come from `duckdb_constraints()` where
  `constraint_type = 'PRIMARY KEY'`; `constraint_column_names` is a
  `VARCHAR[]` — `unnest()` it server-side rather than scanning a list
  type through `database/sql`.
- `duckdb_indexes().expressions` is a list too; cast to VARCHAR and
  parse for plain column indexes. Primary keys do not appear in
  `duckdb_indexes()`.
- `duckdb_tables().sql` / `duckdb_views().sql` hold reconstructed DDL.

## PostgreSQL (`jackc/pgx/v5/stdlib`, driver name `pgx`)

- One connection sees one database — "databases" in the UI are the
  schemas of the connected database (`pg_namespace`).
- No stored CREATE TABLE text; DDL is synthesized from
  `information_schema.columns`.
- Primary key / index columns via `pg_index` with
  `to_regclass($1)` on the quoted `schema.table` string — pass the
  qualified name as a **value**, quoting each part with `QuoteIdent`.

## MySQL / MariaDB (`go-sql-driver/mysql`, driver name `mysql`)

- Shared dialect; only `Engine`/display name differ.
- `information_schema` throughout; empty database resolves via
  `table_schema = DATABASE()`.
- `SHOW CREATE TABLE` returns two columns (name, DDL) — and a
  different column set for views; scan positionally.
- Primary key detection: `columns.column_key = 'PRI'`; the PK index in
  `statistics` is always named `PRIMARY`.
