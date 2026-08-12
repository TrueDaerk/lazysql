---
type: Design Decision
title: Copy menu and streaming file export
description: Why serialization lives in its own package behind a three-method incremental Writer, why a whole-table copy is capped while a file export streams, how NULL is spelled in each format, how the export is cancelled, and how a query result's export/copy differ from a browsed table's.
tags: [tui, export, csv, json, markdown, sql, clipboard, streaming]
generated:
  by: claude-code/opus-5
  at: 2026-08-09T00:00:00Z
sources:
  - resource: https://github.com/TrueDaerk/lazysql/issues/48
    note: query-result export/copy, Markdown format
---

# Copy and export

## Decision

Getting data *out* is one feature with two ends. `y` opens a
context-aware copy menu whose result goes to the clipboard; `E` prompts
for a path and writes the same bytes to a file. Both go through
`internal/export`, so a single row and the table it came from can never
disagree about quoting.

`internal/export` knows nothing about the TUI and nothing about
`database/sql`. It depends only on `internal/db` for the normalized cell
types and the `Dialect`, which keeps the driver-abstraction rule intact:
the serializers see `nil / string / int64 / float64 / bool / time.Time`
and never a driver-private type.

### The writer is incremental, not a function over a slice

```go
type Writer interface {
    Begin(cols []db.Column) error
    Row(values []any) error
    End() error
}
```

Three methods rather than `Serialize(rows) string` is the whole reason a
100k-row export costs one page of memory. `Stream` walks pages through
the same `Driver.QueryPage` the grid uses — inheriting its filter and
sort for free — and feeds each row to the writer, which flushes into the
`io.Writer` underneath. Nothing accumulates.

`Begin` is called even for an empty result, so a CSV still gets its
header and a JSON still gets `[]` rather than an empty file.

A short page ends the stream: an engine has no other way to say "that
was all", and asking for a count first would double the round trips for
information the export does not need.

### A clipboard copy is capped; a file export is not

A file export streams to disk, so its size is bounded by the disk. A
clipboard copy has to be materialized in full before the system
clipboard will take it, so a whole-table copy stops at
`copyRowLimit` (5000 rows) and the command log says so, naming `E` as
the way to get the rest. Silently copying a prefix would be the one
outcome the user cannot detect.

### NULL, four times

There is no single right answer, so each format gets the one its readers
expect:

| format | SQL NULL renders as |
| --- | --- |
| CSV | an empty field |
| JSON | `null` |
| SQL | `NULL` |
| Markdown | the literal text `NULL` |

Markdown gets the odd one out for a reason specific to it: a Markdown
table cell has no equivalent of CSV's empty field. A blank cell in a
rendered table reads as an empty value, not as "no value at all" — CSV's
convention would be silently wrong here — so the literal word is the only
unambiguous spelling. `markdownEscape` also neutralizes `\`, `|` and
newlines (the last becomes `<br>`) before a data value's own backslash
could be mistaken for one of the writer's escapes.

The CSV choice is deliberate: MySQL's own tooling writes `\N`, which
nothing outside MySQL reads, and an empty field at least round-trips
through every spreadsheet. It does mean an empty string and a NULL are
indistinguishable in CSV — which is a property of CSV, not of lazysql,
and the reason the JSON and SQL variants exist next to it.

JSON is the only one of the three that can say NULL exactly, so it also
keeps numbers as numbers and booleans as booleans rather than
stringifying everything. Infinity and NaN have no JSON spelling at all
and degrade to `null`.

### Generated SQL is literal, executed SQL is parameterized

`db.QuoteLiteral` and `db.InsertStatement` (in `internal/db/literal.go`)
exist only for text handed *to the user*. Everything lazysql executes
still goes through the parameterized statements of
[design/staged-changeset](staged-changeset.md). Keeping the two apart is
what lets the escaping rules live in one small, well-tested file — see
[reference/sql-literal-escaping](../reference/sql-literal-escaping.md)
for the per-dialect rules.

`.sql` exports INSERTs only. The `CREATE TABLE + INSERTs` variant is a
copy-menu entry rather than an extension, because inferring it from the
extension would make the same `.sql` path mean two different things
depending on whether the DDL happened to be fetchable.

### A query result exports by re-running, not re-paging

A browsed table's `E` re-issues `Driver.QueryPage` with a growing
`OFFSET`, one streaming page at a time — `tableSource` in
`internal/ui/export.go`. A query-editor result cannot be paged the same
way: [design/query-editor-and-history](query-editor-and-history.md)
already established that rewriting an arbitrary statement to add a
`LIMIT`/`OFFSET` risks changing what it selects, which is why the grid
materializes a query result once, capped at `maxQueryRows` (10000), and
pages that in memory instead of re-querying.

An export needs the *whole* result, not the capped copy the grid is
showing, so it cannot reuse `all` either. Instead `internal/db` grew
`Driver.QueryStream(ctx, query, args, onRow)`: it runs the statement
exactly once and hands each row to `onRow` as `*sql.Rows.Next()` yields
it, never materializing more than one row. `querySource` in
`internal/ui/export.go` wraps it as the query-result counterpart of
`tableSource`, and `export.StreamQuery` is `Stream`'s counterpart on the
`internal/export` side — same `Writer` contract, same progress and
`MaxRows` behaviour, fed by a `QueryRunner` closure instead of a `Pager`.

Re-running only works if it runs the *same* statement. A single
placeholder-bound run stores its display text (`?`/`:name`, for the
prompt and the log) separately from the driver-executable form
`db.BindPlaceholders` produced (dialect-native markers) and the values it
bound — `queryStmtMsg.exec`/`.args`, threaded into `dataView.queryExec`/
`.queryArgs` by `showQueryResult`. The export runs `queryExec` with
`queryArgs`, not the display text, so a result filtered by a bound `?`
exports the same filtered rows rather than erroring on a literal `?` sent
to the server with no parameter to fill it.

### SQL/INSERT needs the query to map onto one table

An INSERT names one table and a fixed column list; a table export always
knows both, but a query result's columns only correspond to a table's
own columns when the statement is a plain `SELECT <cols> FROM <table>` —
a join blends two tables' columns, `GROUP BY`/`DISTINCT` breaks the
row-to-row correspondence, and a computed column or alias has no real
column behind it at all. `db.SingleTableSelect` (`internal/db/singletable.go`)
checks this with a small hand-rolled tokenizer — reusing
`skipQuoted`/`skipBlockComment`/`skipDollarQuoted` from the statement
splitter — rather than a real SQL parser: it looks for exactly one `FROM`
table with every selected item a bare (optionally qualified) column or
`*`, and reports false the moment it sees a `JOIN`, a comma-joined
`FROM`, a subquery, a `UNION`/`INTERSECT`/`EXCEPT`, a `GROUP BY`, a
`DISTINCT`, or a computed/aliased select item. It is deliberately
conservative — a query it cannot prove safe just does not get offered,
rather than risk an INSERT into the wrong table or the wrong column.
`runQueryExport` calls it only when the chosen extension is `.sql`; CSV,
JSON and Markdown never need to know which table (or whether there even
is one).

### A query result's clipboard copy is page-scoped, not table-scoped

The `y` menu's uppercase scope re-reads a browsed table from the server,
capped at `copyRowLimit`. A query result has no relation to re-read — the
same reason its export re-runs instead of re-pages — so `y` on a query
result offers `page — CSV`/`page — JSON` instead: `copyQueryPage`
serializes exactly `m.data.rows`, the page already in memory, with no
round trip at all. The log line says the copy is page-limited and names
`E` for the rest, the same shape `copyTable`'s truncation notice already
used for the table scope's cap.

### Progress and cancellation

The export worker is a goroutine that pushes `exportProgressMsg` and one
`exportDoneMsg` into an unbuffered channel; the root re-issues
`waitExportCmd` after every progress line. The unbuffered channel is
backpressure on purpose — a UI that has stopped reading should stall the
worker rather than let it run ahead — and every progress send also
selects on `ctx.Done()`, so a cancelled export cannot deadlock on a
channel nobody is reading.

`X` cancels by cancelling that context; `Stream` checks it between pages
*and* between rows. A cancelled or failed export removes its partial
file: a half-written export is worse than none, because it looks
complete.

Only one export runs at a time. `X` is bound to `key.WithDisabled()` and
enabled only while one is running, so the options bar and `?` — which
both skip disabled bindings — never advertise a key that would do
nothing. That is the
[design/keybindings-single-source](keybindings-single-source.md) rule
applied to a conditional action.

### Menu keys avoid `j` and `k`

The copy menu is a `menuModal`, which moves its own cursor with `j`/`k`.
Row-scope JSON is therefore `o` (object), not `j`. Lowercase is the row
scope, uppercase the table scope.

## Consequences

- `y` no longer copies the DDL directly; it opens the menu, whose `d`
  entry does. The DDL tab's hint changed to match.
- With a multi-row selection up, the same menu leads with the selection
  scopes and drops the single-row ones, and `ctrl+c` opens it as well as
  `y` does. See
  [design/grid-multi-row-selection](grid-multi-row-selection.md).
- A copy never simply fails. With no native clipboard — an SSH session,
  a container — the text goes out as an OSC 52 escape sequence, and
  with no terminal to take that either it spills to a temp file whose
  path the log names. `clipboardWrite`, `osc52Available` and `spillFile`
  are all variables so a test run can never touch the developer's
  clipboard, terminal or temp directory. See
  [design/clipboard-strategy](clipboard-strategy.md).
- A whole-table copy or export of a relation the user is mid-edit on
  reads the *server's* rows. Only the single-cell and single-row copies
  apply the staged changeset, because those are built from the page the
  user is looking at.
- Offset paging without an explicit sort inherits the engine's
  unspecified row order, so a large export can in principle skip or
  repeat a row if the table is being written concurrently. Sorting the
  grid before exporting makes the walk deterministic.
- A query result re-run for export can likewise return a different set of
  rows than the grid is showing if the underlying data changed between
  the original run and the export — there is no snapshot isolation here,
  same as the table path above.
- `y`/`yy` belongs to the query editor's own vim engine while focus stays
  on panel `[5]` (copying the *query text*, not a row), so the copy menu
  for a query result is reached from the grid — `tab` moves focus there,
  the same as after any other run whose result the user wants to act on.
  This is not new: it already applied to the cell/row scopes before this
  issue, and continues to for the page scope.
- `E` on a query result and `E` on a browsed table share one
  `exportState`/`exportJob`, distinguished only by which `exportSource`
  the job holds (`tableSource` vs. `querySource`), so `X` cancels either
  the same way and the two can never run concurrently against each
  other's progress line.
