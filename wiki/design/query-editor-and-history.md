---
type: Design Decision
title: Query editor, result routing and the persistent history
description: Why free-form results are materialized and paged in memory instead of rewritten with LIMIT/OFFSET, how a script is split and classified without a parser, why DML from the editor is confirmed but never staged, how a run is cancelled, and why the history is JSON Lines under XDG_STATE_HOME.
tags: [tui, query, sql, history, cancellation, persistence, xdg]
generated:
  by: claude-code/opus-5
  at: 2026-08-09T00:00:00Z
---

# Query editor and query history

## Decision

`:` opens a centered modal holding a Bubbles `textarea`. `ctrl+r` (and
`ctrl+enter`, for the terminals that send it) runs the script, `esc`
closes it and keeps the draft on the model, and `ctrl+c` aborts a run.
Results land in the existing main-view **Data** tab, next to the browsing
pages, and every executed statement is appended to `[4] Query history`,
which is backed by a file so it survives a restart.

Four things about the flow needed a decision.

## 1. A free-form result is materialized once and paged in memory

Table browsing re-issues its query with a new `OFFSET` for every page —
`db.PageSQL` builds the statement, so lazysql knows its shape. A
statement the user typed has no known shape. Paging it would mean either
wrapping it (`SELECT * FROM (<user sql>) LIMIT … OFFSET …`, which is not
portable, breaks on multi-statement scripts and changes what `EXPLAIN`,
`SHOW` and DML mean) or re-running it per page (which re-executes side
effects). Both are worse than reading it once.

So a query result is read once into `dataView.all` and the grid slices a
page out of it (`dataView.setPage`). `ctrl+f`/`ctrl+b` then cost nothing
and the row total is exact rather than a separate `COUNT(*)` — which is
why the status line drops the `~` for query results and keeps it for
browsed tables.

The price is memory, so the read is capped. `Driver.QueryLimit` stops
materializing at `maxQueryRows` (10 000) and reports whether the server
still had rows; the grid says `capped at 10000 rows` in red and the
command log repeats it. `Driver.Query` is `QueryLimit` with no cap, which
is what the internal callers (pages, counts, introspection) keep using.

### `dataView` carries both, and they are mutually exclusive

`dataView.table` and `dataView.query` are never both set. That single
invariant is what tells every table-scoped action it has nothing to work
on:

- `open()` — the tab has something to render (a relation, a result, or a
  rows-affected notice).
- `browsing()` — a relation is open. **Editing, staging, inserting,
  introspection, `E` export and the table-scope copies ask this**, not
  `open()`.
- `isQuery()` — the page is a materialized result.

Getting that wrong is how a `DELETE` gets staged against a table named
`""`, so the guards were converted deliberately rather than left on
`open()`.

## 2. Splitting and classifying a script without a SQL parser

`db.SplitStatements(engine, script)` is a small dialect-aware lexer, not
`strings.Split(script, ";")`. A semicolon inside a string literal, a
quoted identifier or a comment is data. The dialect matters for exactly
three constructs:

| Construct | Engines |
|---|---|
| `` `backtick` `` identifiers | MySQL, MariaDB |
| `#` line comments | MySQL, MariaDB |
| `\'` backslash escape inside `'…'` | MySQL, MariaDB (not PostgreSQL/SQLite/DuckDB — see [sql-literal-escaping](../reference/sql-literal-escaping.md)) |
| `$tag$ … $tag$` bodies | PostgreSQL |

An unterminated literal swallows the rest of the script rather than
splitting into nonsense — a half-typed quote should produce one failing
statement, not five.

`db.ClassifyStatement` decides read vs. write from the first keyword
after any leading comments, and **errs towards write**: an unrecognized
statement is confirmed before it runs and executed through `Exec`. Two
cases are not decided by the leading keyword alone:

- `WITH` is a read *unless* the script contains `INSERT`/`UPDATE`/
  `DELETE`/`MERGE` as a whole word — PostgreSQL allows data-modifying
  CTEs.
- `PRAGMA x` reads, `PRAGMA x = y` writes.

`EXPLAIN` is classified as a read by convention even though PostgreSQL's
`EXPLAIN ANALYZE` really executes its statement. Treating every `EXPLAIN`
as a write would put a confirm modal in front of the most common
read-only debugging tool; the narrower case is documented here instead.

## 3. Editor DML is confirmed, never staged

The changeset ([staged-changeset](staged-changeset.md)) exists because
lazysql refuses to guess row identity. A statement the user wrote already
*is* the identity — there is nothing to stage and nothing to reconstruct.
So editor DML/DDL bypasses the changeset entirely and instead goes
through a `confirmModal` that prints the exact write statements, says how
many of the script's statements they are, and says plainly that they run
immediately and cannot be rolled back.

A run stops at the first failing statement. A script is written top to
bottom; continuing past an error would apply changes the user never got
to react to.

## 4. Cancellation, and the one place a `select` on `ctx.Done()` is wrong

Statements run in one goroutine under a `context.WithCancel`, streaming
one `queryStmtMsg` per finished statement plus a closing `queryDoneMsg` —
the same shape as the file export worker. `ctrl+c` calls the cancel func;
the driver aborts the statement server-side where the engine supports it
(`modernc.org/sqlite` honours the context mid-statement), and the
connection stays open.

The export worker guards its progress sends with
`select { case ch <- msg: case <-ctx.Done(): }`, because a progress line
is optional. The query worker must **not**: the message that reports the
cancelled statement is produced *after* the context is already dead, so
the guard would swallow exactly the message the user needs. A plain
blocking send is correct here because the root drains the channel until
`queryDoneMsg` — it re-issues `waitQueryCmd` after every statement, and
`startQuery` refuses to begin a second run while one is in flight, so
there is always a reader.

`ctrl+c` is also the quit key. `keyMap.CancelQuery` is bound with
`key.WithDisabled()` and enabled only while a run is in flight, and its
case sits *before* `Quit` in `updateGlobal` — so `key.Matches` picks
cancel during a run and quit the rest of the time, with no extra state
check. The options bar and `?` skip disabled bindings, so the key is
advertised only when it means something.

## 5. The history is JSON Lines under `XDG_STATE_HOME`

`internal/history` writes to
`${XDG_STATE_HOME:-~/.local/state}/lazysql/history`. It is state, not
configuration, which is why it is not next to `config.toml`.

One JSON object per line, oldest first:

```json
{"sql":"SELECT * FROM users","engine":"sqlite","at":"2026-08-09T12:00:00Z"}
```

- **JSON, so a multi-line statement stays on one line.** `enter` on the
  panel has to reload the statement exactly as it was typed.
- **Append-only, so recording a statement is O(1).** A delete or a clear
  rewrites the file atomically (temp file + rename). Loading compacts
  anything past `MaxEntries` (1000).
- **Unparsable lines are skipped, not fatal.** A crash mid-write leaves a
  truncated last line; that must not cost the whole history.
- **Mode 0600, directory 0700.** A statement can carry data that
  `config.toml` deliberately never holds.
- **Writes are mutex-guarded.** Appends run in `tea.Cmd` goroutines and
  would otherwise interleave their lines.

Everything lazysql executes goes in through one message,
`historyEntryMsg` — a browsing page, a committed changeset statement, an
editor script. Re-running the newest entry does not duplicate it; only
the timestamp would differ, and `enter`-and-run would otherwise grow the
list on every replay.

### The panel row is not the entry

The panel is a `[]string`, but `enter` needs the statement verbatim, so
the model keeps `[]history.Entry` and derives the rows
(`time  flattened SQL`). The engine goes in the main view's detail pane
rather than the row: the side column is ~24–40 cells wide and the
statement is what the eye scans for.

Mapping a row back to an entry cannot go by text — two runs of the same
statement render identically. `sidePanel.applyFilter` therefore records
`idx`, mapping each visible row onto its position in the unfiltered list,
and `sidePanel.sourceIndex` reads it. That is what makes `d` delete the
entry under the cursor and not the one at that index in the full list
while a `/` filter is active.

## Consequences

- A query result is not a relation: no `e`/`d`/`n`/`D`, no `E` export, no
  Structure/Indexes/DDL tabs, no server-side `f`/`s`. `[`/`]` keeps the
  Data tab instead of cycling to tabs with nothing behind them.
- The copy menu still offers cell and row scopes on a query result; the
  `INSERT` and table scopes need a table and are hidden.
- A result yanks focus to the main view. It is what the user asked to
  look at, and `esc` walks back through the focus stack.
- Adding `QueryLimit` widened the `Driver` interface. There is one
  implementation (`db.conn`) and no fakes, so the cost was a single
  method; a `Query`-plus-manual-truncation approach would have
  materialized the rows it was trying not to materialize.

## See also

- [data-grid](data-grid.md) — the renderer and the page/cursor model the
  results reuse.
- [staged-changeset](staged-changeset.md) — the path editor DML
  deliberately does not take.
- [copy-and-export](copy-and-export.md) — the worker/channel shape this
  one mirrors, and the one place it diverges.
- [tui-shell-architecture](tui-shell-architecture.md) — the update
  routing order the `ctrl+c` case slots into.
