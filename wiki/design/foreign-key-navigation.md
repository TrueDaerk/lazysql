---
type: Design Decision
title: Foreign-key navigation — following a reference from the grid
description: Why the follow-FK filter is built in internal/db rather than the UI, how the jump history restores a whole dataView, why the reverse direction needs a per-namespace scan, and what a NULL key value does.
tags: [tui, main-view, data-grid, foreign-keys, navigation, filter]
generated:
  by: claude-code/opus-5
  at: 2026-08-09T00:00:00Z
---

# Foreign-key navigation

## Decision

The data grid can walk a schema's references in both directions
(issue #40):

- `g` follows the foreign key the cursor column takes part in: the
  referenced table opens filtered to the referenced row.
- `G` goes the other way: it lists the tables whose foreign keys point at
  the row under the cursor and jumps to the matching rows.
- `ctrl+o` — and `esc`, before it leaves the grid — walks the jumps back,
  restoring the table, filter, sort, page and cell cursor.

Columns that take part in a constraint are marked `⇒` in the grid header
(`fkMark`, `internal/ui/foreignkey.go`), so `g` is discoverable without
opening the Indexes tab first.

### The filter is built in `internal/db`, not in the UI

`db.FKFilter` (`internal/db/foreignkey.go`) turns a constraint plus a row's
values into a `db.Filter`: `QuoteIdent` on every referenced column,
`Placeholder(n)` for every value, and the values themselves in `Args`.
That is the same rule as every other statement fragment — identifiers get
dialect quoting, user data travels as parameters — and it is the reason
the builder does not live next to the key handler. A composite key
contributes one `col = ?` term per column pair, joined with `AND`; the
pairing is positional, exactly as `ForeignKey.Columns[i]` /
`RefColumns[i]` define it.

`Filter.Raw` is a display copy (`tenant = 1 AND code = 'b'`) so the grid's
status line can show what is filtered. It never reaches a statement.

### A NULL key value refuses instead of filtering

SQL's `= NULL` matches nothing, so a filter built from a NULL cell would
show an empty grid and look like a broken jump. `Model.cursorRowValues`
therefore fails on the first NULL and names the *local* column, and the
key becomes a no-op with `-- follow FK skipped: customer_id is NULL` in
the command log. `db.FKFilter` refuses NULL a second time, for callers
that did not pre-check.

### The jump history stores a whole `dataView`

`browseState` keeps the `dataView` wholesale plus the selected main-view
tab, rather than a hand-picked list of fields. That is what makes the
filter, the structured conditions behind it, the sort, the page and the
cell cursor all come back together — a partial snapshot would have to
grow every time the grid gains state. The stack is capped at
`browseStackMax` (32): it is a navigation aid, not an undo log, so the
oldest entries fall off while someone walks a deep chain of references.

`browseBack` re-runs the page query rather than re-rendering the stored
rows — the rows may have changed while the user was away — but leaves the
stored ones on screen until the reply lands, so the grid never blinks
empty.

`esc` pops the history *before* the focus stack. Opening a relation from
the `[2]` tree, or switching namespace, clears the history: those are fresh
starts, not steps in a chain.

### The reverse direction scans the namespace once

No engine offers a portable "who references me". `G` therefore reads
`TableForeignKeys` for every table of the browsed namespace
(`loadNamespaceFKsCmd`, one round trip each, bounded by
`namespaceFKTimeout`) and caches the result under an `fkKey` whose table
is empty. The scan fills the per-table cache from the same round trips,
so a `g` afterwards costs nothing. It runs only when `G` is pressed —
paying N round trips on every relation open, for a key most sessions
never press, is not worth it.

### Caching, and who fills the cache

`Model.fkCache` is keyed by `fkKey{conn, database, table}`. Three things
fill it:

- `openTable` and every jump call `ensureFKs`, because the header mark
  must be right before any key is pressed;
- the introspection fetch behind the Structure/Indexes/DDL tabs already
  reads foreign keys, so `metaLoadedMsg` hands them straight over;
- the namespace scan, as above.

A relation with no constraints caches as an empty non-nil slice, so
"known to have none" never re-fetches. `fkLoading` marks the fetches in
flight, so holding `g` down cannot stack round trips, and `fkAfter` parks
the action until the reply lands — the same deferral shape
`metaView.afterLoad` uses for `y`/`e`/`d`. `R` on the grid drops the
entry so a schema change is picked up.

## Consequences

- Following a key across schemas works: `db.SplitQualified` splits a
  `schema.table` target so the page query addresses the right namespace,
  and the `[2]` tree only follows along when the target is in the
  namespace being browsed.
- A column belonging to several constraints opens a menu instead of
  guessing; so does `G` with several referencing tables.
- Both directions read the *fetched* row value, not a staged edit: a jump
  follows what the database currently holds. Staged inserts are phantom
  rows with no server-side identity, so the key refuses on them.

## See also

- [design/data-grid](data-grid.md) — the grid, its cursor and its filter
- [design/main-view-tabs](main-view-tabs.md) — the metadata fetch that
  also feeds the FK cache
- [design/keybindings-single-source](keybindings-single-source.md) — how
  `g`/`G`/`ctrl+o` reach the options bar and `?`

The same namespace scan and its `refsCache` back the `Relations` main-view
tab, which shows both directions per table and walks them without a row
filter — see [design/relations-tab](relations-tab.md).
