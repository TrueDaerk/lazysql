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
- `sqlite_master.type` is already `'table'` / `'view'`, so the
  relation-kind column costs nothing.
- Original DDL comes from `sqlite_master.sql` (NULL for auto-created
  indexes — filter `sql IS NOT NULL`).
- `AUTOINCREMENT` is reported by **nothing**: not `table_info`, not
  `index_list`. It exists only in the stored `sqlite_master.sql` text,
  so `tableColumns` reads the DDL back and marks the single
  `INTEGER PRIMARY KEY` column when the keyword appears. A failure
  there is dropped — it costs a display note, not the column list.
- `PRAGMA foreign_key_list` names no constraint: the columns are
  `(id, seq, table, from, to, on_update, on_delete, match)`, `id`
  groups the columns of one constraint and doubles as its display name.
  `to` is NULL when the reference targets the referenced table's
  primary key implicitly.
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
- Tables and views live in *separate* functions, so a combined listing
  is a `UNION ALL` with **literal** kind columns (`'table'` / `'view'`)
  — there is no catalog column to read the kind from. Each branch needs
  its own copy of the `database_name` argument.
- `duckdb_tables().sql` / `duckdb_views().sql` hold reconstructed DDL.
- Foreign keys come from `duckdb_constraints()` with
  `constraint_type = 'FOREIGN KEY'`, but the **referenced** table and
  columns are only spelled inside `constraint_text`
  (`FOREIGN KEY (user_id) REFERENCES users(id)`) — and the catalog's
  own reference columns have changed names between DuckDB versions, so
  parsing that text is the version-independent route.

## PostgreSQL (`jackc/pgx/v5/stdlib`, driver name `pgx`)

- One connection sees one database — "databases" in the UI are the
  schemas of the connected database (`pg_namespace`).
- `information_schema.tables.table_type` spells kinds as `BASE TABLE`,
  `VIEW`, `FOREIGN`, `LOCAL TEMPORARY` — match on "contains view"
  rather than equality.
- No stored CREATE TABLE text; DDL is synthesized from
  `information_schema.columns` plus the introspected indexes and
  foreign keys. It is a faithful description, not a byte-identical
  replay of the original statement.
- Foreign keys: `pg_constraint` with `contype = 'f'`. `conkey` and
  `confkey` are positionally paired arrays, so unnest them **together**
  — `JOIN LATERAL unnest(c.conkey, c.confkey) WITH ORDINALITY` — or the
  column pairing is lost.
- `confupdtype`/`confdeltype` are single chars (`a` no action,
  `r` restrict, `c` cascade, `n` set null, `d` set default) of the
  internal `"char"` type; cast them `::text` so the pgx decoder stays
  on the plain-string path.
- Column "extra": `is_identity` + `identity_generation` (PG 10+),
  `is_generated` (PG 12+), and the older `serial` types, which show up
  only as a `nextval(...)` column default.
- Primary key / index columns via `pg_index` with
  `to_regclass($1)` on the quoted `schema.table` string — pass the
  qualified name as a **value**, quoting each part with `QuoteIdent`.

## MySQL / MariaDB (`go-sql-driver/mysql`, driver name `mysql`)

- Shared dialect; only `Engine`/display name differ.
- `information_schema` throughout; empty database resolves via
  `table_schema = DATABASE()`.
- `information_schema.tables.table_type` adds `SYSTEM VIEW` to the
  standard set; the same "contains view" rule covers it.
- `SHOW CREATE TABLE` returns two columns (name, DDL) — and a
  different column set for views; scan positionally.
- Primary key detection: `columns.column_key = 'PRI'`; the PK index in
  `statistics` is always named `PRIMARY`.
- `information_schema.columns.extra` is where `auto_increment`,
  `DEFAULT_GENERATED` and `STORED GENERATED` live.
- Foreign keys need **two** tables: `key_column_usage` for the column
  pairs in key order (`referenced_table_name IS NOT NULL` filters out
  plain unique/primary rows) and `referential_constraints` for the
  `update_rule`/`delete_rule`. Join on schema **and** constraint name;
  the extra `table_name` predicate is defensive — InnoDB keeps foreign
  key names unique per schema, but the join costs nothing and pins the
  row to the table being introspected.
- `key_column_usage.referenced_table_schema` is worth selecting: a
  constraint may point at a table in another schema, and the UI shows
  the qualified name only then.
