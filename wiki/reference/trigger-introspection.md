---
type: Dialect Note
title: Trigger introspection per engine
description: Where each engine keeps its triggers, why PostgreSQL reads pg_trigger instead of information_schema.triggers, why MySQL/MariaDB synthesize the CREATE TRIGGER text instead of running SHOW CREATE TRIGGER, and why DuckDB answers ErrUnsupported.
tags: [db, dialect, introspection, triggers, sqlite, postgres, mysql, mariadb, duckdb]
generated:
  by: claude-code/opus-5
  at: 2026-08-10T00:00:00Z
sources:
  - resource: https://www.sqlite.org/schematab.html
  - resource: https://www.postgresql.org/docs/current/catalog-pg-trigger.html
  - resource: https://dev.mysql.com/doc/refman/8.4/en/triggers-table.html
  - resource: https://duckdb.org/docs/stable/sql/statements/overview
---

# Trigger introspection per engine

`Driver.ListTriggers(ctx, database)` returns `[]db.Trigger` (name + the
relation it fires on) and `Driver.TriggerDDL(ctx, database, name)`
returns one `CREATE TRIGGER` statement. Both go through the `Dialect`
interface (`listTriggers` / `triggerDDL`), so no UI code knows any of the
below.

## SQLite

Triggers live in `sqlite_master` next to tables and views:

```sql
SELECT name, tbl_name FROM "main".sqlite_master
 WHERE type = 'trigger' ORDER BY name
```

- `tbl_name` is the relation the trigger fires on.
- `sqlite_master.sql` holds the original `CREATE TRIGGER` text verbatim,
  so the DDL needs no synthesis — the same property `tableDDL` relies on.
- Like every other `sqlite_master` read, the schema is interpolated
  (quoted) rather than bound: an attached-database prefix is an
  identifier, not a parameter.

## PostgreSQL

Read `pg_catalog.pg_trigger`, **not** `information_schema.triggers`:

- the `information_schema` view has **one row per event**, so a
  `BEFORE INSERT OR UPDATE` trigger appears twice and has to be
  de-duplicated by hand;
- it also hides triggers on tables the caller does not own, which turns a
  permissions difference into a silently short listing.

`pg_trigger` has exactly one row per trigger. Filter `NOT tgisinternal`:
PostgreSQL creates internal trigger rows to enforce foreign keys, and
those are not user objects.

The definition comes from the server's own deparser:

```sql
SELECT pg_catalog.pg_get_triggerdef(t.oid, true)
```

`pg_get_triggerdef` renders the `WHEN` clause and the trigger function's
arguments, neither of which any `information_schema` column carries.
Passing `true` pretty-prints it.

## MySQL / MariaDB

`information_schema.triggers` is keyed on `trigger_schema`, not on the
table: triggers are named per schema, so the listing needs no table
predicate.

```sql
SELECT trigger_name, event_object_table FROM information_schema.triggers
 WHERE trigger_schema = DATABASE() ORDER BY trigger_name
```

The definition is **synthesized** from
`action_timing`, `event_manipulation`, `event_object_table`,
`action_orientation` and `action_statement` rather than read from
`SHOW CREATE TRIGGER`, because the `SHOW` form:

- requires the `TRIGGER` privilege on the schema, which a read-only
  browsing account often does not have; and
- returns a **different number of columns** across MySQL and MariaDB
  versions (the `sql_mode`, character-set and `Created` columns came and
  went), so a positional scan cannot be written once.

The catalog carries every part that matters, so the synthesized statement
is a faithful description — the same trade-off `tableDDL` makes for
PostgreSQL.

Caveat inherited from the catalog: `information_schema.triggers` only
shows triggers the caller has the `TRIGGER` privilege for, so a restricted
account sees a short list rather than an error.

## DuckDB

DuckDB **has no triggers at all** — no `CREATE TRIGGER` statement and no
catalog function to list one. Both entry points return
`db.ErrUnsupported` rather than an empty slice, so "this namespace
defines none" and "this engine has no such concept" stay distinguishable;
the `[2] Objects` tree renders the sentinel as a `not supported` note on
the Triggers node instead of a misleading `(none)`.

Any future object category an engine lacks should answer the same way.
