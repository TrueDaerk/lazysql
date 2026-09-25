---
type: Design Decision
title: CSV import into an existing table
description: Issue #225 — `I` on a table in [2] Objects imports a CSV file through a path form (shared path completion), a settings form that shows the detected delimiter, header and column mapping as editable fields over a typed preview, and a worker in the export's shape; rows go through a new Driver.ImportRows (one transaction, one prepared parameterized INSERT, read-only guard, condensed command log lines); fields are converted by column type and a mismatch is refused, never coerced; the engines' native CSV loaders are deliberately not used.
tags: [import, csv, driver, transactions, read-only, command-log, modal]
generated:
  by: claude-code/opus-5.5
  at: 2026-09-25T00:00:00Z
sources:
  - resource: "GitHub issue TrueDaerk/lazysql#225"
  - resource: "https://pkg.go.dev/encoding/csv"
---

# CSV import into an existing table

The way back in for what [`E` exports](copy-and-export.md): `I` on a table in
`[2] Objects` loads a CSV file into it. The target must already exist;
creating tables is [staged DDL](staged-ddl.md)'s job.

## The flow

1. **Path** — a one-field form with the shared
   [path completion](path-completion-in-forms.md) (`withSuggest`), because the
   plain prompt modal has none. Submitting reads the table's columns and the
   first `csvimport.SampleSize` (64 KiB) of the file in a `tea.Cmd`.
2. **Settings** — the guesses as editable fields: delimiter, "first line is
   a header", the column mapping, and the NULL marker. The form's body block
   shows what was read (delimiter, header, width), the mapping with target
   types, and the first five rows *converted to the target column types* —
   a row that would stop the import shows its reason instead. `enter` is the
   confirmation; nothing runs before it.
3. **Import** — a worker goroutine in the shape of the export's
   (`importJob`, an unbuffered channel, `waitImportCmd`), with progress lines
   every 5000 rows carrying a byte-based percentage, and `X` to cancel.

The job state lives in `exportsModel.csv` next to the file export and the
dump, so closing the connection cancels it through `cancelAll` like the
others.

## Decisions

- **The mapping is a text field**, `email, -, id`: one entry per CSV column,
  a table column name or `-` to skip. A per-column picker would need a
  modal per column; the text form is correctable in one line and validated
  (unknown name, a column mapped twice, more entries than the file has
  columns, nothing mapped) before submit. With a header the default maps by
  name case-insensitively; without one, by position.
- **Delimiter sniffing** tries `,` `;` tab `|` over up to 20 records and keeps
  the one that splits every record into the same number (>1) of fields,
  preferring the most fields. A sample cut at 64 KiB drops its last,
  possibly partial record first, so a cut quoted field does not disqualify
  the right delimiter.
- **Header detection** — a header when any first-line field names a table
  column, or else when some column is numeric on line two but not on line
  one.
- **NULL** — by default an empty field is NULL, the spelling lazysql's own
  CSV export writes, so export → edit → import round-trips. `encoding/csv`
  cannot tell `""` from an empty field, so a table that needs empty strings
  sets a marker (`\N`), after which only the marker is NULL.
- **Types are applied, and a mismatch is an error.** `db.ClassifyValue` maps
  a declared type to a coarse class (integer, float, decimal, bool,
  date/time/datetime, text) and `db.ConvertText` converts a field to the Go
  value bound for it. `"n/a"` in an INTEGER column stops the import with
  `line 3, column id (INTEGER): "n/a" is not an integer` — the engine would
  otherwise coerce it (SQLite stores the text; MySQL in non-strict mode
  stores 0). Unknown types bind as text, never refused on a guess. Decimals
  and temporal values are validated and then bound *as their text*, so a
  `NUMERIC(38,10)` keeps every digit and a zone written in the file is not
  dropped by re-rendering.
- **A record of the wrong width is refused whole** — a missing field means
  every following value would land one column over.

## `Driver.ImportRows`

A new driver method rather than `ExecTx`, which takes a materialized
`[]Statement`: a million-row file would be a million statements in memory,
and it would log every one. `ImportRows` takes a row iterator and:

- refuses on a read-only session before reading the source, logging the
  rejected `INSERT` like every other refused write;
- runs `BEGIN`, prepares one `INSERT INTO t (cols) VALUES (?, …)` with
  dialect quoting and placeholders, executes it per row with the values as
  parameters, and commits;
- on a refused row, a source error (the conversion above), or a cancelled
  context, rolls back and returns — an engine refusal as `*ImportRowError`
  naming the row, its source line and the reason;
- logs **condensed**: `BEGIN`, the `INSERT` once as `… -- ×N rows` (with the
  failing row's values as its arguments when one failed), then `COMMIT` or
  `ROLLBACK`. Logging each row would evict the whole session history from
  the 500-entry ring buffer.

A cancelled context makes `database/sql` roll the transaction back by
itself; the explicit `Rollback` then reports `sql.ErrTxDone`, which is the
asked-for outcome and is logged as a successful `ROLLBACK`.

## Why not the native CSV loaders

DuckDB's `COPY … FROM` / `read_csv` and SQLite's `.import` are far faster,
but neither fits:

- they do their own type coercion, so a mismatch is not reported the way
  the issue demands (DuckDB rejects the whole file with its own message;
  SQLite's shell stores whatever text it gets);
- they would need a second implementation of the mapping, the NULL marker
  and the delimiter per engine, and `.import` is a shell command, not SQL;
- they read the file on the engine side, which for a server engine is the
  wrong machine.

One prepared statement inside one transaction is fast enough for SQLite and
DuckDB in-process (no fsync per row) and identical on every engine. If a
native path is ever added, it must go through `ImportRows` so the read-only
guard and the command log still apply.
