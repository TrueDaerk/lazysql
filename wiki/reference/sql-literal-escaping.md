---
type: Dialect Note
title: Per-dialect SQL literal escaping for generated INSERTs
description: MySQL and MariaDB read a backslash inside a string literal as an escape character while PostgreSQL, SQLite and DuckDB do not; booleans and timestamps also need per-engine spellings.
tags: [db, dialects, sql, escaping, export, security]
generated:
  by: claude-code/opus-5
  at: 2026-08-09T00:00:00Z
---

# SQL literal escaping

Everything lazysql *executes* is parameterized, so this note applies
only to the SQL text lazysql *generates* for the user: the `INSERT`
statements produced by the copy menu and by a `.sql` export. See
[design/copy-and-export](../design/copy-and-export.md).

`db.QuoteLiteral(dialect, value)` in `internal/db/literal.go` is the one
place these rules live.

## Strings: the backslash is not universal

Doubling the single quote (`'` → `''`) is standard and works everywhere.
The backslash is not:

| engine | `C:\tmp` renders as | why |
| --- | --- | --- |
| MySQL, MariaDB | `'C:\\tmp'` | `NO_BACKSLASH_ESCAPES` is off by default, so `\t` inside a literal is a tab |
| PostgreSQL | `'C:\tmp'` | `standard_conforming_strings` is on by default, so the backslash is data |
| SQLite, DuckDB | `'C:\tmp'` | no backslash escapes at all |

Escaping the backslash unconditionally would corrupt every Windows path
exported from PostgreSQL; not escaping it would corrupt every one
exported from MySQL. Hence the per-dialect branch.

The two passes are order-independent in either direction — doubling a
quote never introduces a backslash, and doubling a backslash never
introduces a quote — but the backslash pass runs first anyway, because
the reverse reads like a bug even when it is not.

A MySQL server started with `sql_mode=NO_BACKSLASH_ESCAPES` will read
the doubled backslash as two characters. lazysql does not probe for it:
the setting is rare, and the export is text a human reviews before
running.

## Booleans

MySQL and MariaDB have no real boolean type — `TRUE`/`FALSE` are
accepted as aliases for `1`/`0` — and the underlying `TINYINT(1)` column
round-trips more predictably as the number it actually stores. Every
other engine gets `TRUE`/`FALSE`.

## Timestamps

MySQL and MariaDB reject the RFC 3339 `T` separator and the zone suffix
in a `DATETIME` literal, so timestamps are converted to UTC and written
`'2026-08-09 12:34:56'`. PostgreSQL, SQLite and DuckDB all parse RFC
3339, so they keep the offset: `'2026-08-09T12:34:56Z'`.

## Non-finite floats

Neither `Infinity` nor `NaN` is a portable numeric literal. Every engine
that accepts them at all accepts the quoted spelling (`'NaN'`), so that
is what is emitted; on an engine that does not, the statement fails
loudly rather than inserting a silently wrong number.

## A nil dialect is allowed

`QuoteLiteral`, `QualifiedTable` and `QuoteIdentifier` are all nil-safe,
falling back to the standard-conforming spelling with unquoted
identifiers. That is what a preview built without a live connection
wants, and it keeps the serializers in `internal/export` usable in tests
that have no driver.
