---
type: Design Decision
title: Copy menu and streaming file export
description: Why serialization lives in its own package behind a three-method incremental Writer, why a whole-table copy is capped while a file export streams, how NULL is spelled in each format, and how the export is cancelled.
tags: [tui, export, csv, json, sql, clipboard, streaming]
generated:
  by: claude-code/opus-5
  at: 2026-08-09T00:00:00Z
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

### NULL, three times

There is no single right answer, so each format gets the one its readers
expect:

| format | SQL NULL renders as |
| --- | --- |
| CSV | an empty field |
| JSON | `null` |
| SQL | `NULL` |

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
- A copy never simply fails. With no clipboard — an SSH session, a bare
  tty, a container — the text spills to a temp file and the log names
  the path. `clipboardWrite` and `spillFile` are both variables so a
  test run can never touch the developer's clipboard or temp directory.
- A whole-table copy or export of a relation the user is mid-edit on
  reads the *server's* rows. Only the single-cell and single-row copies
  apply the staged changeset, because those are built from the page the
  user is looking at.
- Offset paging without an explicit sort inherits the engine's
  unspecified row order, so a large export can in principle skip or
  repeat a row if the table is being written concurrently. Sorting the
  grid before exporting makes the walk deterministic.
