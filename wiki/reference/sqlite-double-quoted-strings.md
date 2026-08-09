---
type: Dialect Note
title: SQLite treats unresolvable double-quoted identifiers as string literals
description: Why a quick filter naming a column that does not exist returns zero rows on SQLite instead of raising an error.
tags: [db, dialect, sqlite, filtering, quoting]
generated:
  by: claude-code/opus-5
  at: 2026-08-09T00:00:00Z
sources:
  - resource: https://www.sqlite.org/quirks.html#double_quoted_string_literals_are_accepted
---

# SQLite: double-quoted strings

For compatibility with old applications, SQLite accepts a double-quoted
token that resolves to no identifier as a **string literal** instead of
raising `no such column`. The standard-conforming behaviour is
compile-time optional (`SQLITE_DQS`) and is off in the build behind
`modernc.org/sqlite`.

Consequence for lazysql's quick filter: `db.ParseFilter` rewrites
`no_such_column = 1` into `"no_such_column" = ?`, and SQLite then
evaluates `'no_such_column' = 1`, which is false for every row. The user
sees an empty grid rather than an error naming the typo.

This is a diagnosis quirk, not a safety one — the *value* is still bound
as a parameter, so nothing about it is injectable. Engines that follow
the standard (PostgreSQL, DuckDB, and MySQL with `ANSI_QUOTES`) raise a
real error for the same fragment, which the grid surfaces normally.

Worth knowing when writing tests: a fragment naming a missing column is
**not** a way to provoke a query failure on SQLite. Use something the
parser keeps verbatim instead, e.g. `no_such_column IN (1, 2)`.

## See also

- [../design/data-grid](../design/data-grid.md) — the filter parser.
- [dialect-introspection-quirks](dialect-introspection-quirks.md)
