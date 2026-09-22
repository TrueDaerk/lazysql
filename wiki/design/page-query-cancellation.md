---
type: Design Decision
title: A superseded page query is cancelled, not just ignored
description: Why the Data tab's page and count queries share one model-held context that every reload cancels, why the reply-side fresh() guard stays anyway, how a cancellation is kept out of the grid and out of the command log's failure column, and why the loading marker had to move down into the status line.
tags: [ui, main-view, data-grid, sorting, context, cancellation, loading]
generated:
  by: claude-code/opus-5
  at: 2026-09-22T00:00:00Z
sources:
  - resource: https://github.com/TrueDaerk/lazysql/issues/206
    note: issue — cancel superseded page queries and show a loading state when sorting a slow table
---

# A superseded page query is cancelled, not just ignored

## The invariant

**At most one page query and one count query for the Data tab are ever
running.** Pressing `s` three times on a slow table leaves one statement
on the server, not three — and the grid says so while it waits.

## The problem

Every reload path — `s` (sort), `/` (filter), `ctrl+f`/`ctrl+b`
(paging), `enter` (reload), a foreign-key jump, a commit — goes through
`Model.reloadPage()`. It bumped `dataView.req` and issued
`loadPageCmd` + `countRowsCmd`, each of which built its *own*
`context.WithTimeout(context.Background(), queryTimeout)` inside the
`tea.Cmd`. A `tea.Cmd` is a goroutine the runtime owns: once it is
handed over, nothing in `Update` can reach the context it made.

So `req` only made the *reply* stale. `Model.fresh(req, conn, table)`
dropped it on arrival, and the query behind it ran to completion or to
the 30 s `queryTimeout`. Three presses of `s` on a non-indexed column
meant three full `SELECT … ORDER BY … LIMIT` scans and three
`COUNT(*)`s, all of them paid for, only the last of them looked at —
and, on a busy table, all of them contending with each other, which is
what made the pile look like it drained one statement at a time.

The presses themselves came from the second half of the bug: the grid's
only reaction to `s` was the `loading…` marker in the *border title* of
the main view, several lines above where the user is looking. It read as
"nothing happened", and the honest response to that is to press the key
again.

## Decision

### One shared, model-held context per request

`pageQueries` (`internal/ui/data.go`) is the cancel handle of the page
and count queries one reload put in flight:

```go
type pageQueries struct {
    req         int
    cancel      context.CancelFunc
    page, count bool
}
```

`start(req)` cancels whatever the previous reload left running and
returns the context the new pair shares; `reloadPage` calls it right
after bumping `req` and passes the context into both commands, which no
longer build one of their own.

**One context for both queries** is deliberate. They are issued
together, superseded together and cancelled together, and a `COUNT(*)`
whose page query is gone annotates nothing. The alternative — a handle
per query — buys the ability to keep a count alive across a re-sort,
which is worth nothing: the count depends on the filter, and every path
that changes the filter changes the page too.

**The handle hangs off the Model through a pointer** (`Model.inflight`),
like `changes` and `tree`. Half of the UI is `func (m Model) …` — value
receivers that take a copy and return it — and a cancel func written
into a copy that is then thrown away is a handle silently lost, leaving
the next request unable to cancel anything. A pointer cannot be lost
that way. It is only ever touched from `Update`, which is
single-threaded, so it carries no lock.

### The reply-side guard stays

`fresh()` was not replaced. Cancellation is a request to stop, not a
guarantee of having stopped: a query that had already produced its rows
when `cancel()` ran still delivers them, and a driver may notice the
cancellation only at its next row fetch. The two guards answer different
questions — *stop working on it* and *do not put it on screen* — and
dropping either one reintroduces a class of bug. The `req` in every
reply, and in `pageQueries.req`, is what ties the two together.

### `context.Canceled` is an outcome, never a failure

Three places had to learn that:

- `pageLoadedMsg` clears `data.loading` and returns without setting
  `data.err`. A fresh-but-cancelled reply can only come from the view
  closing under it (a newer request would have bumped `req`), and the
  grid must not be left with a `loading…` marker for a reply that will
  never arrive — nor with "context canceled" where a row used to be.
- `rowCountMsg` does the same, keeping whatever total it had.
- The command log spells the statement `-- cancelled (superseded)`
  instead of `-- FAILED: context canceled`, and `logLine.err` is false
  for it, so it is not coloured red. The statement itself is still
  logged: the log is the one place where "that press re-issued the page
  query" is visible at all, and it is how the fix is verified by hand.

### Releasing the context

`pageQueries.done(req, page)` records which of the two replies has
landed and calls `cancel()` once both have. A `context.WithTimeout`
holds a timer until its cancel runs, and a page that came back in 40 ms
must not keep one alive for the remaining 30 seconds. `stop()` is the
same thing unconditionally, for leaving the view: `Model.resetBrowse`
(disconnect) and `Model.openTable` (another relation) both call it.

### Immediate feedback: the marker moved down

`dataStatus` — the grid's own bottom line, where the sort and the filter
already say what shaped the page — now carries the `loading…` marker
too. The border title keeps its own; the two are cheap and the status
line is where the eyes are.

The *sort indicator* needed no change and that is the point:
`toggleSort` mutates `data.sort` before it issues the query, and
`buildGrid` derives the `▲`/`▼` on the column header from `data.sort`,
not from the rows. So the header already showed the newly requested
order on the frame after the key press — a pending state, rendered over
the old page, which is exactly right. What was missing was anything
saying *why* the rows underneath had not changed yet.

## Rejected alternatives

**Debounce the key.** Swallow `s` while a load is running, or coalesce
presses over a 200 ms window before issuing anything. It makes the key
lie: the issue's own assumption is that toggling sort while a load runs
still cycles asc → desc → none. A press must always change the state it
names; only the query behind it may be replaced.

**A spinner in the status line.** `Model.spin` already animates the
query editor's running indicator, and reusing it means `reloadPage`
returning `m.spin.Tick`. A spinner tick is a real `tea.Tick` — the tests
drive commands synchronously, so every reload in every test would pay
the frame interval, and the marker would gain nothing a static
`loading…` does not already say. The rest of the app (side panels, the
object tree, the metadata tabs) spells it statically for the same
reason.

**Server-side `KILL QUERY`.** Rejected in the issue and not needed:
`database/sql` propagates context cancellation to the driver, which
sends the engine's own cancel request (`pgx` a CancelRequest, MySQL a
`KILL`-equivalent on its own connection). A second connection issuing
`KILL QUERY` by process ID is a write against the server that needs
privileges the browsing session may not have.

## Notes on the driver side

`internal/db/dial.go` opens every handle with plain `sql.Open` and sets
no pool limits, so `database/sql` uses its defaults: unlimited open
connections, two idle. `tea.Batch` runs its commands in separate
goroutines, so a pile of superseded page queries was never serialised by
lazysql — each had its own connection. The sequential-looking drain in
the report is the server working through work it should never have been
given, plus the engine's own contention on the table. Nothing in the
driver layer had to change for this issue; the fix is entirely that the
UI stops asking for work it has already moved past.

See also [design/data-grid](data-grid.md) for the one-page-at-a-time
main view the request belongs to, and
[design/catalog-browsing](catalog-browsing.md) for the `req`-counter
stale-reply rule this extends.
