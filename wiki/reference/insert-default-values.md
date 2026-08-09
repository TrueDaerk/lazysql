---
type: Dialect Note
title: INSERT with no columns is spelled two different ways
description: PostgreSQL, SQLite and DuckDB accept the standard DEFAULT VALUES; MySQL and MariaDB reject it and want an empty column list instead.
tags: [db, dialect, mysql, mariadb, postgres, sqlite, duckdb, insert]
generated:
  by: claude-code/fable-5
  at: 2026-08-09T00:00:00Z
sources:
  - resource: https://dev.mysql.com/doc/refman/8.4/en/insert.html
  - resource: https://www.postgresql.org/docs/current/sql-insert.html
  - resource: https://www.sqlite.org/lang_insert.html
---

# INSERT with no columns

lazysql's insert form lets every column stay on its default, so
`db.InsertSQL` has to be able to render an INSERT that names no columns
at all. There is no single spelling for it.

| Engine | Statement |
| --- | --- |
| PostgreSQL | `INSERT INTO "t" DEFAULT VALUES` |
| SQLite | `INSERT INTO "t" DEFAULT VALUES` |
| DuckDB | `INSERT INTO "t" DEFAULT VALUES` |
| MySQL | ``INSERT INTO `t` () VALUES ()`` |
| MariaDB | ``INSERT INTO `t` () VALUES ()`` |

`DEFAULT VALUES` is the SQL-standard form and is a syntax error in
MySQL/MariaDB; the empty-column form is a MySQL extension and is a
syntax error in PostgreSQL. `defaultValuesClause` in
`internal/db/dialect_helpers.go` picks between them from
`Dialect.Engine()`.

Two things worth knowing before relying on it:

- The row it inserts is only valid if every column has a default, is
  nullable, or is auto-assigned. The insert form checks that before
  staging (see
  [design/staged-changeset](../design/staged-changeset.md)), so the
  clause is normally reached only when the check passed.
- MySQL in strict mode still rejects a NOT NULL column with no default
  here, which is the same error it gives for an explicit column list —
  the transaction rolls back and the changeset survives.

Related: [reference/dialect-introspection-quirks](dialect-introspection-quirks.md).
