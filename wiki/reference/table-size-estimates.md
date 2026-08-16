---
type: Dialect Note
title: Table row and size estimates per engine
description: Which catalog each engine's Driver.TableStats reads for a namespace's row counts and on-disk sizes in one query, how stale each figure is (pg_class.reltuples after ANALYZE, InnoDB's sampled table_rows, DuckDB's estimated_size, SQLite's ANALYZE-only sqlite_stat1 and optional dbstat), and why no engine is ever asked for a COUNT(*).
tags: [db, dialect, introspection, statistics, sqlite, postgres, mysql, mariadb, duckdb, objtree]
generated:
  by: claude-code/opus-5
  at: 2026-08-16T00:00:00Z
sources:
  - resource: https://www.postgresql.org/docs/current/catalog-pg-class.html
  - resource: https://www.postgresql.org/docs/current/functions-admin.html
  - resource: https://dev.mysql.com/doc/refman/8.4/en/information-schema-tables-table.html
  - resource: https://www.sqlite.org/fileformat2.html#stat1tab
  - resource: https://www.sqlite.org/dbstat.html
  - resource: https://duckdb.org/docs/stable/sql/meta/duckdb_table_functions
---

# Table row and size estimates per engine

`Driver.TableStats(ctx, database)` returns one `[]db.TableStat`
(`Table`, `Rows`, `Bytes`) for a whole namespace. It is what the
`[2] Objects` tree annotates its table nodes with — see
[design/object-tree-panel](../design/object-tree-panel.md).

## The one rule: never COUNT(\*)

The annotation exists so a 100M-row table can be recognized *before* it
is opened. Counting it to find that out would defeat the purpose: on
PostgreSQL and MySQL a `COUNT(*)` is a scan, and a per-table count would
turn expanding one branch into as many scans as the schema has tables.
`internal/db/conn.go`'s `CountSQL` is for the *opened* relation's exact
total, under the user's own filter, and is deliberately not reused here.

So every figure below comes from a catalog the engine maintains for its
own planner, and every one of them can be wrong. The UI renders row
counts with a leading `~` for exactly that reason.

`Rows` and `Bytes` are `db.StatUnknown` (-1) when an engine has no figure;
a table with neither renders no annotation at all.

## PostgreSQL

```sql
SELECT c.relname, c.reltuples::bigint, pg_total_relation_size(c.oid)
FROM pg_catalog.pg_class c
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND c.relkind IN ('r', 'p')
```

- `reltuples` is written by `ANALYZE`, `VACUUM` and autovacuum. Between
  runs it is as stale as the write volume makes it — a table that has
  just been bulk-loaded can read far too low, and a heavily deleted one
  far too high. Since PostgreSQL 14 a table that has **never** been
  analyzed reports `-1` (older versions reported `0`, which is
  indistinguishable from an empty table); `scanTableStats` maps every
  negative to `StatUnknown`, so the annotation is simply absent rather
  than wrong.
- `pg_total_relation_size` is a real measurement: the table's main fork
  plus its indexes plus its TOAST relation. It needs no ANALYZE.
- `relkind IN ('r','p')` keeps ordinary and partitioned tables. A
  partition is an `'r'` in its own right and is listed — and sized —
  separately, exactly as the tree lists it.

## MySQL / MariaDB

```sql
SELECT table_name, table_rows, data_length + index_length
FROM information_schema.tables
WHERE table_schema = ? AND table_type = 'BASE TABLE'
```

- For InnoDB, `table_rows` is an **estimate derived from sampled index
  dives**, not a stored counter. MySQL's own documentation puts the error
  at up to 40–50%, and two consecutive reads can differ without any write
  in between. It is refreshed by `ANALYZE TABLE` and by InnoDB's
  automatic recalculation (`innodb_stats_auto_recalc`, after ~10% of the
  rows change). MyISAM, by contrast, keeps an exact count.
- `data_length + index_length` is the on-disk footprint including
  indexes. It is page-granular and does not shrink when rows are deleted
  until the tablespace is rebuilt.
- Views have no storage, so only `BASE TABLE` rows are asked for.

## DuckDB

```sql
SELECT table_name, estimated_size, NULL FROM duckdb_tables()
WHERE database_name = ?
```

- `estimated_size` is the optimizer's cardinality estimate. On a
  freshly written in-process database it matches the real count; after
  many deletes it is an upper bound.
- DuckDB's catalog exposes **no per-table byte size** — `pragma_database_size`
  is whole-database only — so the size half stays `StatUnknown` and the
  tree shows a row estimate alone.

## SQLite

SQLite has no statistics catalog that is always there. Both of its
sources are optional, and *naming one that does not exist is a
query-time error*, so the dialect asks first:

```sql
SELECT (SELECT COUNT(*) FROM main.sqlite_master WHERE name = 'sqlite_stat1'),
       (SELECT COUNT(*) FROM pragma_module_list WHERE name = 'dbstat')
```

That probe is a query of its own rather than a failed attempt at the real
one **because every statement lands in the command log**: browsing a
database nobody has ever `ANALYZE`d must not paint a red
`no such table: sqlite_stat1` line there on every expand. With neither
source present the second query is skipped and the tables render
unannotated.

- **`sqlite_stat1`** exists only after an `ANALYZE`. Its `stat` column
  begins with the table's row count (`"1234 5 2"`), which
  `CAST(stat AS INTEGER)` picks off the front; each index of a table
  repeats the same leading count, so `MAX` collapses them. A table with
  no index gets a single row with `idx` NULL. The figure is frozen at the
  moment of the `ANALYZE` — SQLite never refreshes it on its own — so it
  is the stalest of the four engines'.
- **`dbstat`** is a virtual table the build may omit
  (`SQLITE_ENABLE_DBSTAT_VTAB`; the `modernc.org/sqlite` build lazysql
  uses does include it). Its rows are per b-tree, so index pages are
  folded onto their table through `sqlite_master.tbl_name` to match the
  table+indexes footprint the other engines report. Reading it walks the
  whole database file, which is cheap for a local file and is the reason
  it is not attempted at all when the module is missing.

## Consequences for callers

- The two halves are independent: an engine may answer one and not the
  other, and the annotation drops whichever half is missing.
- `TableStats` is a decoration: every caller treats an error as "no
  annotation". The failing statement is already in the command log
  through the `Driver`'s `Logger`, so nothing else reports it.
- Getting an *exact* count still costs a scan and is what opening the
  table does (`Driver.CountRows`), where the user has asked for it.
