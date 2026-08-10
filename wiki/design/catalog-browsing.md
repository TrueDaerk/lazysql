---
type: Design Decision
title: Catalog browsing in the object panels
description: How catalog reads load asynchronously, drop stale replies, filter fuzzily, and collapse single-namespace engines into one pseudo-database. The panel layout it describes was superseded by the [2] Objects tree.
tags: [tui, db, panels, filtering]
generated:
  by: claude-code/opus-5
  at: 2026-08-09T00:00:00Z
---

# Catalog browsing

> **Superseded in part (issue #79).** Panels `[2] Databases` and `[3]
> Tables` were merged into one expandable tree, `[2] Objects`, and the
> `Tables`/`Views` sub-tabs were removed with them — see
> [design/object-tree-panel](object-tree-panel.md). Everything below
> about the async load, the stale-reply rule, the pseudo-database and the
> inline fuzzy filter still holds; read panel `[3]`/"sub-tab" as the
> tree's `Tables`/`Views` categories.

## Decision

Panels `[2] Databases` and `[3] Tables` are filled from the driver layer
through `tea.Cmd`s only; `Update` never touches the database. Every load
is a message round trip:

- `loadDatabasesCmd` → `databasesLoadedMsg`
- `loadRelationsCmd` → `relationsLoadedMsg`

Both commands carry the connection name (and, for relations, the
namespace) they were started for. The reducer drops a reply whose
`conn`/`database` no longer matches the model, so a slow listing from a
connection the user has since left cannot overwrite the current panel.

Key choices:

- **Loading never clears the panel.** `loading` is a flag on
  `sidePanel`; the previous rows stay on screen with a `loading…` marker
  in the title. A failed load only appends to the command log — see
  [design/tui-shell-architecture](tui-shell-architecture.md) for the
  message-reduction rule this follows.
- **One round trip fills both sub-tabs.** `[3]` has `Tables` and `Views`
  sub-tabs on `[`/`]`. The driver returns `[]db.Relation` (name + kind)
  once; switching tabs re-filters the cached slice and never hits the
  server. This is why `Driver` grew `ListRelations` next to the
  name-only `ListTables`, and why the `Dialect` interface now has
  `listRelations` instead of `listTables` (see
  [design/db-driver-abstraction](db-driver-abstraction.md)).
- **Kind normalization is central.** `scanRelations` maps every engine's
  kind spelling with one rule: anything containing `view` is a view,
  everything else (`BASE TABLE`, `LOCAL TEMPORARY`, …) is a table. Only
  DuckDB needs literal kind columns, because `duckdb_tables()` and
  `duckdb_views()` are separate functions.
- **Single-namespace engines collapse to a pseudo entry.** For
  file-based engines (SQLite, DuckDB) whose listing has at most one
  entry, panel `[2]` shows `(default)` instead of the engine's own name
  (`main`, `memory`, the file stem …) — that name is not a choice the
  user makes. `databaseArg` maps the pseudo entry back to `""`, which
  every dialect already reads as "the connection's current namespace".
  On connect, a single-entry list is drilled into automatically and
  focus lands on `[3]`. Attached databases still list normally.

## Fuzzy filter

`/` opens an **inline** filter inside the panel, not a modal: the
pattern renders under the panel title and narrows the list on every
keystroke. `fuzzyMatch` is a case-insensitive subsequence test (`usr`
matches `users` and `user_roles`) — no dependency needed, and cheap
enough to re-run per key.

Consequences for key routing (the order in `Model.Update` is now
modal → active filter → global keys → focused panel):

- While the filter is capturing, printable keys are *text*: `2` and `q`
  type into the pattern instead of jumping panels or quitting.
- `enter` leaves input mode but keeps the filter applied, so the panel's
  own keys work on the narrowed list.
- `esc` clears the filter first; only an unfiltered panel passes `esc`
  on to the focus stack.

The panel keeps `all`/`allStatus` (everything) separate from
`items`/`status` (what the filter lets through); the cursor always
indexes the filtered slice, and a reload re-applies the filter and
re-selects the previously selected row by name when it survives.

## Alternatives rejected

- **A filter prompt modal** (what the shell shipped with): it hides the
  list while typing, which defeats filter-as-you-type.
- **Querying per sub-tab**: two catalog queries where one suffices, and
  a visible pause on every `[`/`]` press.
- **Ranking matches by score**: subsequence order is stable and matches
  the server's `ORDER BY`; re-ordering rows under the cursor while
  typing is disorienting in a narrow column.
