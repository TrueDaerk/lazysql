---
type: Dialect Note
title: DDL spellings and gaps per engine
description: How each engine spells the staged schema operations lazysql renders — where the index name vs the table is qualified, RENAME TABLE vs ALTER … RENAME TO, MySQL's whole-column CHANGE and how its COLUMN_DEFAULT is reported, DuckDB's one-action ALTER and its refusal to alter an indexed table, SQLite's missing TRUNCATE/ALTER COLUMN — and which engines run DDL inside a transaction.
tags: [db, dialects, ddl, mysql, mariadb, postgres, sqlite, duckdb]
generated:
  by: claude-code/opus-5.5
  at: 2026-09-25T00:00:00Z
sources:
  - resource: https://www.postgresql.org/docs/current/sql-altertable.html
  - resource: https://dev.mysql.com/doc/refman/8.0/en/alter-table.html
  - resource: https://dev.mysql.com/doc/refman/8.0/en/implicit-commit.html
  - resource: https://mariadb.com/kb/en/information-schema-columns-table/
  - resource: https://www.sqlite.org/lang_altertable.html
  - resource: https://duckdb.org/docs/sql/statements/alter_table
---

# DDL per dialect

Rendered by `internal/db/ddl_dialects.go`; the design is in
[design/staged-ddl](../design/staged-ddl.md). The lifecycle tests in
`internal/db/ddl_test.go` execute every SQLite and DuckDB spelling below
in-process; the PostgreSQL and MySQL spellings are asserted as text.

## Where the namespace goes

| Statement | PostgreSQL | MySQL / MariaDB | SQLite | DuckDB |
|---|---|---|---|---|
| `CREATE INDEX` | `ix ON s.t` | `ix ON db.t` | **`s.ix ON t`** | `ix ON c.t` |
| `DROP INDEX` | `s.ix` | **`ix ON db.t`** | `s.ix` | `c.ix` |
| rename relation | `ALTER TABLE/VIEW s.t RENAME TO new` | **`RENAME TABLE db.t TO db.new`** | `ALTER TABLE s.t RENAME TO new` | `ALTER TABLE/VIEW c.t RENAME TO new` |

- SQLite qualifies the *index* with the attached database and leaves the table
  bare — a qualified table in `CREATE INDEX … ON` is a syntax error.
- A MySQL index belongs to its table; there is no schema-level index name.
- `ALTER … RENAME TO` takes an unqualified new name everywhere it exists; MySQL's
  `RENAME TABLE` needs the qualified target or it would move the table into
  the session's current database. It renames views too.
- A DuckDB "database" in lazysql is a catalog; `catalog.name` resolves for
  tables, views and indexes alike.

## Altering a column

| | PostgreSQL | MySQL / MariaDB | SQLite | DuckDB |
|---|---|---|---|---|
| rename | `RENAME COLUMN` (own statement) | `RENAME COLUMN` (MySQL 8.0, MariaDB 10.5) | `RENAME COLUMN` (3.25) | `RENAME COLUMN` |
| type | `ALTER COLUMN c TYPE t` | `CHANGE COLUMN` — whole definition | **unsupported** | `ALTER COLUMN c TYPE t` |
| NULL-ness | `SET/DROP NOT NULL` | `CHANGE COLUMN` — whole definition | **unsupported** | `SET/DROP NOT NULL` |
| default | `SET/DROP DEFAULT` | `ALTER COLUMN c SET/DROP DEFAULT` | **unsupported** | `SET/DROP DEFAULT` |
| actions per statement | many, comma-separated | many | one | **one** |

- PostgreSQL folds type, NULL-ness and default into one `ALTER TABLE` and
  renames last in a statement of its own, so no action names a column that
  does not exist yet.
- DuckDB: one statement per aspect, rename last.
- **DuckDB refuses any `ALTER TABLE` on a table that has an index**
  ("Dependency Error: Cannot alter entry … because there are entries that
  depend on it"). lazysql does not work around it; the engine's error reaches
  the commit, which rolls back.
- SQLite's `ALTER TABLE` knows `RENAME TO`, `RENAME COLUMN`, `ADD COLUMN` and
  (3.35+) `DROP COLUMN` — nothing else. The other column changes answer
  `ErrUnsupported`; the documented table-rebuild dance is deliberately not
  automated.

### MySQL rewrites the whole column

`MODIFY`/`CHANGE COLUMN` replace the column definition; whatever the new one
leaves out is gone. lazysql therefore:

- uses `ALTER COLUMN … SET/DROP DEFAULT` and `RENAME COLUMN` for default-only
  and rename-only changes — they touch nothing else;
- uses one `CHANGE COLUMN old new type NULL|NOT NULL …` for a type or NULL-ness
  change, restating the kept default, `AUTO_INCREMENT` and `ON UPDATE` from
  what introspection reported. Comment, character set and collation are not
  restated; the form warns.

Restating the default is where the two engines differ in
`information_schema.COLUMNS.COLUMN_DEFAULT`:

- **MariaDB 10.2.7+** reports SQL: a quoted literal (`'x'`), `NULL`, or an
  expression — restated verbatim.
- **MySQL** reports a literal *unquoted* (`x`) and an expression bare, with
  `DEFAULT_GENERATED` in `EXTRA`. The literal is re-quoted with `QuoteLiteral`;
  the expression is spelled like a typed one.

An expression default on MySQL 8 must be parenthesized — `DEFAULT (uuid())` —
except the `CURRENT_TIMESTAMP` family, which predates expression defaults and
stays bare (`mysqlExprDefault`). MariaDB reads the parenthesized form as the
same expression.

## Relation-level gaps

| | PostgreSQL | MySQL / MariaDB | SQLite | DuckDB |
|---|---|---|---|---|
| `TRUNCATE` | `TRUNCATE TABLE` | `TRUNCATE TABLE` | **none** | `TRUNCATE` |
| rename a view | `ALTER VIEW` | `RENAME TABLE` | **none** | `ALTER VIEW` |

SQLite's missing `TRUNCATE` is not replaced by `DELETE FROM`: different
semantics (triggers fire, `AUTOINCREMENT` is not reset), and row deletes are
already stageable.

## DDL and transactions

PostgreSQL, SQLite and DuckDB run DDL inside the commit's transaction, so a
failing statement rolls every earlier one back (tested for SQLite in
`TestSQLiteDDLCommitIsAtomic`). **MySQL and MariaDB commit implicitly before
and after every DDL statement** — `TRUNCATE` included — so a commit carrying
schema changes is not atomic there. `db.TransactionalDDL` reports it; the
commit preview replaces its "one transaction" promise with a warning on those
engines, and a failed commit re-reads the schema anyway.

## No bound parameters

No engine accepts a placeholder in a DDL statement (a column default, in
particular, must be a constant or an expression in the statement text), so
every DDL `Statement` has empty `Args`: names are quoted, text defaults are
escaped literals, and types and expressions are held to the grammar described
in the design note.
