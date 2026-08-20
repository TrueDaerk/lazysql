---
type: Reference
title: Per-dialect SQL function catalog for autocomplete
description: What internal/sqlhl's function catalog (coreFunctions/dialectFunctions in functions.go) covers per dialect, why it is a separate list from the keyword set rather than an addition to it, which names were deliberately dropped because they already reach the popup as keywords, and the source documentation each dialect's extras were drawn from.
tags: [tui, query, editor, completion, autocomplete, sql, functions, mysql, postgres, sqlite, duckdb]
generated:
  by: claude-code/sonnet-5
  at: 2026-08-20T00:00:00Z
sources:
  - resource: https://github.com/TrueDaerk/lazysql/issues/184
    title: "Issue #184 — Query editor autocomplete: add per-dialect SQL function catalog"
  - resource: https://dev.mysql.com/doc/refman/8.4/en/built-in-function-reference.html
    title: "MySQL 8.4 built-in function reference"
  - resource: https://www.postgresql.org/docs/current/functions.html
    title: "PostgreSQL functions and operators"
  - resource: https://www.sqlite.org/lang_corefunc.html
    title: "SQLite core functions"
  - resource: https://www.sqlite.org/lang_datefunc.html
    title: "SQLite date and time functions"
  - resource: https://www.sqlite.org/json1.html
    title: "SQLite JSON functions"
  - resource: https://duckdb.org/docs/stable/sql/functions/overview
    title: "DuckDB function overview"
---

# Per-dialect SQL function catalog for autocomplete

## What this is

`internal/sqlhl/functions.go` exports `Functions(dialect) []string`, the
same shape as `Keywords(dialect)` in `keywords.go`
([sql-syntax-highlighting](../design/sql-syntax-highlighting.md)): a
shared `coreFunctions` list plus a `dialectFunctions` map of extras,
built once and cached. The query editor's completion popup
(`internal/ui/complete.go`) offers it as its own suggestion kind,
`completeFunction`, tagged `fn` — see
[schema-aware-autocomplete](../design/schema-aware-autocomplete.md) for
the popup itself.

Accepting a function inserts `NAME()` with the caret placed between the
parens, ready for an argument, rather than `NAME(` with nothing to close
it or `NAME()` with the caret trailing past the close paren.

## Why it is a separate list, not an addition to the keyword set

`sqlhl.Keywords`/`IsKeyword` back two things at once: syntax highlighting
and (until this change) the popup's keyword suggestions. Folding the
function catalog into that same set would have made every function name
color as a keyword too — so a column genuinely named `count` or `length`
would highlight wrong. `coreFunctions`/`dialectFunctions` are their own
lists; `sqlhl.IsKeyword` was not touched, and
`TestFunctionsDoNotFloodTheKeywordSet` (`internal/sqlhl/functions_test.go`)
guards against a future change quietly merging them.

## Deliberate keyword collisions

A handful of function names are *also* keyword syntax in the dialect they
belong to, and those are left out of the function catalog because the
keyword completion already offers them:

- **`CAST`**, **`REPLACE`** — core keywords in every dialect (`CAST(x AS
  int)` is keyword syntax; `REPLACE` doubles as the `INSERT ... OR
  REPLACE`/upsert clause).
- **`LEFT`**, **`RIGHT`** (MySQL, PostgreSQL) — also `LEFT JOIN`/`RIGHT
  JOIN`, core keywords for every dialect.
- **`TRUNCATE`** (MySQL) — also `TRUNCATE TABLE`.
- **`DATE`**, **`TIME`**, **`DATETIME`**, **`GLOB`** (SQLite) — `DATE`/
  `TIME`/`DATETIME` are core type-name keywords; `GLOB` is a SQLite
  keyword operator as well as a function.

The exception is DuckDB's **`LIST`**: it is both a type-name keyword
*and* the aggregate function the issue names as DuckDB's `GROUP_CONCAT`/
`STRING_AGG` equivalent, and dropping it to avoid the collision would
fail the issue's own acceptance criterion. It is the one name the two
lists are allowed to share; `TestFunctionsDoNotFloodTheKeywordSet` bounds
the *total* overlap rather than forbidding it outright, so one
intentional collision like this does not need a special case in the
test.

## Coverage per dialect

`coreFunctions` — offered for every dialect, including `Generic` (no
active connection):

```
COUNT SUM AVG MIN MAX
COALESCE NULLIF
UPPER LOWER LENGTH TRIM LTRIM RTRIM SUBSTR CONCAT
ABS ROUND CEIL FLOOR POWER SQRT MOD
```

### MySQL / MariaDB

`GROUP_CONCAT`, `IFNULL`, `CONVERT`; date/time (`DATE_FORMAT`,
`DATE_ADD`, `DATE_SUB`, `DATEDIFF`, `NOW`, `CURDATE`, `CURTIME`, `MONTH`,
`DAY`, `HOUR`, `MINUTE`, `SECOND`, `STR_TO_DATE`, `UNIX_TIMESTAMP`,
`FROM_UNIXTIME`); string (`LPAD`, `RPAD`, `LOCATE`, `INSTR`, `REPEAT`,
`REVERSE`, `FORMAT`, `CHAR_LENGTH`); `RAND`, `GREATEST`, `LEAST`; JSON
(`JSON_EXTRACT`, `JSON_OBJECT`, `JSON_ARRAY`, `JSON_VALID`).

### PostgreSQL

`STRING_AGG`, `ARRAY_AGG`, `IFNULL`; date/time (`TO_CHAR`, `TO_DATE`,
`TO_TIMESTAMP`, `TO_NUMBER`, `DATE_TRUNC`, `DATE_PART`, `EXTRACT`, `AGE`,
`NOW`); `GENERATE_SERIES`; string (`LPAD`, `RPAD`, `POSITION`,
`SPLIT_PART`, `REGEXP_REPLACE`, `REGEXP_MATCH`); `GREATEST`, `LEAST`,
`UNNEST`, `ARRAY_LENGTH`; JSON (`JSON_BUILD_OBJECT`,
`JSONB_BUILD_OBJECT`, `JSON_AGG`, `JSONB_AGG`).

### SQLite

`GROUP_CONCAT`, `IFNULL`, `IIF`, `INSTR`; `RANDOM`, `RANDOMBLOB`, `HEX`,
`QUOTE`, `ZEROBLOB`, `TYPEOF`; date/time (`STRFTIME`, `JULIANDAY`,
`UNIXEPOCH` — `DATE`/`TIME`/`DATETIME` themselves are dropped, see
above); JSON1 (`JSON_EXTRACT`, `JSON_ARRAY`, `JSON_OBJECT`,
`JSON_VALID`).

### DuckDB

`LIST`, `STRING_AGG`, `ARRAY_AGG`, `IFNULL`, `LIST_VALUE`, `UNNEST`;
date/time (`STRFTIME`, `STRPTIME`, `DATE_TRUNC`, `DATE_PART`,
`DATEDIFF`, `EPOCH`, `EPOCH_MS`, `MAKE_DATE`, `MAKE_TIME`,
`MAKE_TIMESTAMP`); string/regex (`REGEXP_MATCHES`, `REGEXP_REPLACE`,
`REGEXP_EXTRACT`, `SPLIT_PART`); list (`LIST_EXTRACT`, `LIST_CONTAINS`,
`STRUCT_PACK`); `GREATEST`, `LEAST`; JSON (`TO_JSON`, `JSON_EXTRACT`,
`ARRAY_LENGTH`).

## Ranking

`completionKind` gained `completeFunction` between `completeView` and
`completeKeyword`
([schema-aware-autocomplete](../design/schema-aware-autocomplete.md#3-ranking)'s
rule 2 kind order): schema names still rank first for a short prefix,
functions rank ahead of keywords since a function is closer to "what am
I calling" than "what is the grammar word", and `maxCompletionItems` was
raised from 200 to 300 so that MySQL's widest unfiltered catalog
(keywords + functions, 243 entries) fits under an explicit `ctrl+space`
on an empty word without silently dropping keywords past the old cut —
`TestKeywordSuggestionsDifferPerDriver` caught the regression before this
landed.

## Completeness

This is the commonly used built-ins per dialect, not an exhaustive port
of each reference page: aggregates, string, numeric, date/time,
conditional and JSON/cast where the dialect has it. Extending a dialect's
list is a matter of adding words to its `strings.Fields` block in
`functions.go`; nothing else needs to change.

## See also

- [schema-aware-autocomplete](../design/schema-aware-autocomplete.md) —
  the popup this catalog feeds, its ranking rule and its keyword list.
- [sql-syntax-highlighting](../design/sql-syntax-highlighting.md) — the
  tokenizer and keyword lists this catalog is deliberately kept apart
  from.
