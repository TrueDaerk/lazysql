---
type: Design Decision
title: Command log panel sourced from a single Driver.Logger choke point
description: Why every executed SQL statement is captured once in internal/db instead of hand-formatted at each UI call site, how the slim panel and the `@` expanded view merge that Logger with the UI's own status notes into one chronological feed, and why the expanded view is a static snapshot like every other modal.
tags: [tui, db, logging, command-log, keybindings]
generated:
  by: claude-code/sonnet-5
  at: 2026-08-09T00:00:00Z
---

# Command log panel

## Decision

Every statement lazysql sends to a server is captured exactly once, in
`internal/db`, not by UI code remembering to log it. `conn` (the one
`Driver` implementation) touches `database/sql` in exactly four places —
`Exec`, `ExecTx`, `QueryLimit` (which `Query`, `QueryPage` and
`CountRows` all funnel through) and `querierAdapter.QueryContext` (the
introspection queries every dialect shares) — and each one calls
`Logger.record` with the SQL, its bound args, a start time and the
resulting error. `Logger` (`internal/db/logger.go`) is a fixed
`LogCapacity`-entry (500) ring buffer; `Driver.Logger()` exposes it.

This is the fix for a real gap: before this change, roughly ninety UI
call sites formatted their own log lines, and several of them
hand-echoed the exact SQL text a driver call was about to run (page/count
queries in `data.go`, `BEGIN`/each statement/`COMMIT` in `model.go`'s
commit handler, the query editor's per-statement text in `query.go`).
Introspection queries (columns, indexes, foreign keys, DDL) were never
logged at all. Moving the capture into `conn` means a future UI code
path gets a correct, deduplicated log entry for free, and the four
introspection queries now show up too.

## Two streams, merged at render time

The UI still has its own `commandLog []logLine` of plain status notes —
"connect X FAILED", "copy skipped: nothing open", export progress — that
never touch the database and so have nothing for `Logger` to record.
`Model.commandLogEntries()` merges that slice with
`m.driver.Logger().Entries()` by timestamp (`sort.SliceStable`) into one
feed both the slim panel and the `@` expanded view render from. At
session scale (≤ ~700 entries total) a sort on every render is not worth
optimizing away; "no noticeable render slowdown" is trivially satisfied.

A `logLine.err` is true either because a `db.LogEntry.Err` is non-nil or,
for a UI note, because its text contains `"FAILED"` — the convention
every existing note already used. Both render in `styles.danger` (red).

## Why the expanded view is a snapshot, not live

`@` opens a `commandLogModal`, matching `cellModal`/`helpModal`: `esc`
returns, `j/k`/`pgup`/`pgdown`/`g`/`G` scroll. Modals in this codebase
render from `view(s styles, maxW, maxH int) string` alone — they never
see the live `Model` — so a statement that lands while the modal is open
is not reflected until it is closed and reopened. Changing that would
mean changing the `modal` interface for every existing modal to thread
the `Model` through `view`, which is a much bigger change than one
feature needs; `esc` then `@` again is a one-keystroke refresh.

## Ring buffer, not unbounded

`LogCapacity` (500) bounds `Logger` the same way the query history caps
at 1000 ([query-editor-and-history](query-editor-and-history.md)) and
the slim panel already capped its old `[]string` at 200: a long session
should not grow the log forever. The merged `commandLogEntries()` also
re-caps its combined output to `LogCapacity` after merging, so the UI
notes half of the feed cannot make the whole thing unbounded either.
