---
type: Dialect Note
title: EXPLAIN per engine
description: What EXPLAIN looks like on PostgreSQL, MySQL/MariaDB, SQLite and DuckDB — the prefix, the answer's shape, which forms are machine-readable, why plain Explain never adds ANALYZE, and how each engine spells the opt-in analyzing form (or lacks one).
tags: [db, dialect, explain, query-plan, postgres, mysql, sqlite, duckdb]
generated:
  by: claude-code/opus-5
  at: 2026-08-09T23:30:00Z
sources:
  - resource: https://www.postgresql.org/docs/current/sql-explain.html
  - resource: https://dev.mysql.com/doc/refman/8.0/en/explain.html
  - resource: https://mariadb.com/kb/en/analyze-and-explain-statements/
  - resource: https://www.sqlite.org/eqp.html
  - resource: https://duckdb.org/docs/guides/meta/explain
  - resource: https://github.com/TrueDaerk/lazysql/issues/46
    title: "Issue #46 — EXPLAIN view for the current query"
  - resource: https://dev.mysql.com/doc/refman/8.0/en/explain.html#explain-analyze
  - resource: https://mariadb.com/kb/en/analyze-statement/
  - resource: https://duckdb.org/docs/guides/meta/explain_analyze
  - resource: https://github.com/TrueDaerk/lazysql/issues/227
    title: "Issue #227 — opt-in EXPLAIN ANALYZE guarded against writes"
---

# EXPLAIN per engine

The four engines agree on the keyword and on nothing else. This is what
`Dialect.explain` (`internal/db/explain.go`) has to absorb so the UI only
ever sees a `db.Plan`.

## The rule that holds for `Explain`: never ANALYZE

`EXPLAIN ANALYZE` **executes** the statement on PostgreSQL, MySQL 8.0+
and DuckDB. For a `DELETE` that means the rows are gone by the time the
plan is on screen. lazysql therefore only ever sends the planning form,
which makes explaining a write exactly as safe as explaining a `SELECT` —
and is why `ctrl+e` needs no confirm modal, unlike `ctrl+r`.

Note that `db.ClassifyStatement` already counts a leading `EXPLAIN` as
`StatementRead`; a user who types `EXPLAIN ANALYZE DELETE …` by hand and
presses `ctrl+r` will therefore not be asked. That is a pre-existing
sharp edge of the classifier, not something the explain view introduces.

## PostgreSQL

- `EXPLAIN (FORMAT JSON) <stmt>` — one row, one column, a JSON **array**
  of objects each holding a `"Plan"` key. The array has more than one
  entry only for a multi-statement utility command; lazysql renders all
  of them.
- Costs are the estimator's units, not milliseconds:
  `Startup Cost`/`Total Cost` (floats), `Plan Rows` (float, but always a
  whole number in practice), `Plan Width` (bytes per row).
- Children live in `"Plans"`; the node's role in its parent is
  `"Parent Relationship"` (`Outer`/`Inner`/`SubPlan`/`InitPlan`).
- Conditions are separate keys, not part of the node type:
  `Index Cond`, `Recheck Cond`, `Filter`, `Hash Cond`, `Merge Cond`,
  `Join Filter`, plus the array-valued `Sort Key` and `Group Key`.
- The key set grows every release. Parsing into a typed struct and
  ignoring unknown keys is deliberate: a newer server must degrade to a
  thinner plan, never to an error.

## MySQL / MariaDB

- `EXPLAIN FORMAT=JSON <stmt>` — note `FORMAT=JSON`, **not** PostgreSQL's
  parenthesised `(FORMAT JSON)`. One row, one column.
- The JSON has **no fixed schema**: `query_block` nests `table`,
  `ordering_operation`, `nested_loop` (an array), `grouping_operation`
  and more depending on the plan shape, and the keys inside a node differ
  by access method and server version. lazysql walks it generically —
  scalars become notes on their node, objects and arrays become children.
- Key **order matters** for readability (`table_name` before
  `access_type` before `key`), and `encoding/json`'s `map[string]any`
  destroys it. The plan is therefore parsed with a `json.Decoder` token
  walk that keeps document order.
- MySQL < 5.7 and MariaDB < 10.1 have no JSON format at all, and MariaDB
  spells parts of the JSON differently from MySQL. A failed JSON attempt
  therefore falls back to the classic tabular `EXPLAIN`, rendered as a
  grid with a note saying so — the fallback's error is reported, the
  JSON attempt's is swallowed.

## SQLite

- `EXPLAIN QUERY PLAN <stmt>` is the human-useful one. Bare `EXPLAIN`
  dumps the VDBE bytecode program, which is a debugging tool for SQLite
  itself, not a query plan.
- Four columns — `id`, `parent`, `notused`, `detail` — of which only
  `detail` is text (`SCAN users`, `SEARCH users USING INDEX …`,
  `USE TEMP B-TREE FOR ORDER BY`). The tree is rebuilt from `id`/`parent`
  with `parent = 0` at the top; rows arrive in document order, so
  children keep the order SQLite listed them in.
- There are **no cost or row estimates** in the output at all. A plan
  node is a sentence, not a number.
- A statement SQLite plans trivially (`SELECT 1`) yields **zero rows** —
  an empty result is a valid plan, not a failure.

## DuckDB

- `EXPLAIN <stmt>` returns `(explain_key, explain_value)` pairs whose
  value is an already-drawn ASCII box diagram, complete with estimated
  cardinalities inside the boxes.
- Re-parsing that back into nodes would only lose information, so it is
  passed through as preformatted text. DuckDB is the one dialect whose
  plan lazysql does not lay out itself.
- The diagram is wide. The main view truncates rather than wrapping, the
  same as every other long line in the shell.

## The opt-in analyzing form (`ExplainAnalyze`)

`Dialect.explainAnalyze` only ever sees a statement `IsWrite` classified as
a read, inside a transaction that is rolled back (see
[design/explain-view](../design/explain-view.md)).

| Engine | Statement | Answer | Rendering |
|---|---|---|---|
| PostgreSQL | `EXPLAIN (ANALYZE, FORMAT JSON) <stmt>` | the same JSON array plus `Actual Startup Time`/`Actual Total Time`/`Actual Rows`/`Actual Loops` per node and `Planning Time`/`Execution Time` (ms) per root | tree; each node's detail gains `(actual time=a..b rows=r loops=l)`, a node with `Actual Loops = 0` reads `(never executed)`, the timings become the plan's footer |
| MySQL 8.0.18+ | `EXPLAIN ANALYZE <stmt>` | one cell of `FORMAT=TREE` text with `(actual time=… rows=… loops=…)` inline | preformatted text — there is no JSON form of EXPLAIN ANALYZE |
| MariaDB 10.1+ | `ANALYZE FORMAT=JSON <stmt>` | EXPLAIN FORMAT=JSON's JSON with `r_*` keys (`r_rows`, `r_total_time_ms`, `r_filtered`…) beside the estimates | tree, via the same generic ordered-JSON walk as MySQL's plan |
| DuckDB | `EXPLAIN ANALYZE <stmt>` | `(explain_key, explain_value)` with key `analyzed_plan` and a box diagram carrying measured timings and cardinalities | preformatted text |
| SQLite | — | no equivalent: `EXPLAIN` is bytecode, `EXPLAIN QUERY PLAN` never runs; only the shell's `.scanstats` measures, and no driver exposes it | `ErrUnsupported` with that reason |

Quirks worth knowing:

- **MariaDB never adopted MySQL's spelling.** `EXPLAIN ANALYZE` is a
  syntax error there; its analyzing statement is `ANALYZE <stmt>`.
- **MySQL < 8.0.18 has no EXPLAIN ANALYZE.** lazysql does not probe the
  version; the server's syntax error is shown in the plan view.
- **Read-only transactions:** PostgreSQL and MySQL/MariaDB honour
  `sql.TxOptions{ReadOnly: true}` (`BEGIN READ ONLY` / `START TRANSACTION
  READ ONLY`), which makes the server refuse a write the classifier missed.
  go-duckdb v2 returns an error for a read-only `BeginTx`, so DuckDB runs
  in a plain transaction that is rolled back.
- **The read-only guard and a hand-typed `EXPLAIN ANALYZE`.** `IsWrite`
  classifies a statement that *starts* with `EXPLAIN ANALYZE` as a write, so
  `ctrl+r` refuses it on a read-only connection. `ExplainAnalyze` classifies
  the statement *inside* instead, which is what lets a read-only session
  analyze a `SELECT`.

## Shared

- A trailing `;` is stripped before the prefix is prepended: `EXPLAIN
  SELECT 1;` is fine on every engine, but a statement that is *only* a
  semicolon is refused locally rather than sent.
- The EXPLAIN itself runs through the ordinary `querier`, so it lands in
  the command log like any other statement without anything re-formatting
  it by hand.
- Placeholders (`?`, `:name`) are refused before the round trip: there
  are no values to plan with, and prompting for values for a statement
  that will not run reads as if it were about to.
