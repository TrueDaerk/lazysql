---
type: Design Decision
title: The [2] Objects tree panel
description: Why the Databases and Tables panels merged into one expandable tree (database → category → object), how the tree is flattened into the existing list panel, how categories load lazily and cache, what the fuzzy filter means on a tree, and how the panel renumbering was carried through.
tags: [tui, panels, tree, browsing, triggers, keybindings]
generated:
  by: claude-code/opus-5
  at: 2026-08-10T00:00:00Z
---

# The `[2] Objects` tree panel

## Decision

The two panels `[2] Databases` and `[3] Tables` are gone. One panel,
`[2] Objects`, holds an expandable tree with three levels:

```
▾ app_dev
    ▾ Tables
        users
        orders
    ▸ Views
    ▸ Triggers
▸ app_test
```

The panel set is now `[1] Connections`, `[2] Objects`, `[3] Query`, plus
the unnumbered `panelMain`. Digit jumps, the `tab` cycle, the options bar
and `?` all follow from `panelID`/`panelTitles`, so the renumbering was a
single edit in `internal/ui/panel.go` plus the `Jump` binding's key list
(`1`-`3`).

### Why a tree

Relation kinds were previously split across a sub-tab mechanism on `[3]`
(`Tables`/`Views` on `[`/`]`). That mechanism had no room for a third
kind: triggers — and later procedures, functions, sequences — had nowhere
to live, and each new kind would have cost another tab and another
keystroke to reach. A category level makes the kind an ordinary node, so
adding one is a constant in `objectCategory` and a `Dialect` method, not
a UI redesign.

Merging the database level in as well is what makes that affordable in
screen rows: three side panels split the column evenly instead of four,
and the tree only spends rows on the branches that are open.

### The tree is flattened, not rendered as a tree

`sidePanel` stays what it always was — a cursor over a slice of strings
with a scroll window, an inline `/` filter and a selection. The tree
lives in `objtree.go` as a `*treeNode` graph, and `flatten()` walks the
*expanded* nodes into a `[]treeRow` (node + depth). `setTreeRows` pushes
the row names into the panel's `all` and keeps the rows themselves in
`sidePanel.rows`.

Everything that made the old panel work therefore keeps working
unchanged: `visible()` scrolls, `move()` clamps, `sourceIndex()` maps a
visible row back onto the unfiltered list. Only two things branch on
`rows != nil`:

- **rendering** — the row gains an indent + `▸`/`▾` prefix and a trailing
  note (`loading…`, `(none)`, `not supported`, `on <table>`). Like the
  connection color tag, the note is rendered outside the selection style
  so its colour survives the highlight instead of being swallowed by it.
- **filtering** — see below.

`setItemsWithStatus` clears `rows`: assigning a plain list is how a panel
stops being a tree, so the two representations can never disagree.

Selection is by node identity, not by name (`selectNode`): two namespaces
can hold a table called `users`, and matching on text would jump to the
wrong one.

### Lazy loading and caching

Expanding a **database** node costs nothing — the category set is fixed
and built eagerly by `categoryNodes`. Expanding a **category** starts an
introspection query as a `tea.Cmd` (`loadCategory`) and marks the node
`loading`, which the row shows. `Update` never blocks.

`Tables` and `Views` share one round trip: `Driver.ListRelations` returns
both kinds, so expanding either marks both loading and one
`relationsLoadedMsg` fills the two. `Triggers` has its own
`Driver.ListTriggers` / `triggersLoadedMsg`.

A loaded category sets `loaded`, and `expandNode` skips the query while
that flag is set — so collapsing and re-expanding is free. `R` is the
only thing that drops it, and it drops it for the level the cursor is on:

- on a **database** node → re-list the namespaces (`ListDatabases`),
  which rebuilds the tree and therefore drops every cached listing;
- on a **category** or **object** node → re-read that category.

Failures leave the node's previous children on screen with a `failed`
note and a line in the command log; only `db.ErrUnsupported` is different
(see below).

`m.relations` — read by the DDL export, the completion cache and the
namespace-wide foreign-key scan — is a mirror of the tree's cache for the
*browsed* namespace, filled in exactly one place (`syncRelations`).

### Table nodes carry a size annotation (issue #153)

A table row shows what it costs to open, right-aligned and dim:

```
▾ Tables
    users                  ~1.2M rows · 340 MB
    events                     ~2.4K rows · 1 MB
```

The figures come from `Driver.TableStats(ctx, database)` — **one catalog
query for the whole namespace**, never a `COUNT(*)` per table. Which
catalog, and how stale each engine's numbers are, is
[reference/table-size-estimates](../reference/table-size-estimates.md).
Row counts always wear a `~`: every engine's figure is a planner
statistic, not a count.

Three decisions make it fit the tree that already existed:

- **A second, independent round trip.** `loadCategory` batches
  `loadTableStatsCmd` beside `loadRelationsCmd` rather than folding the
  sizes into `relationsLoadedMsg`. The names are what the user is waiting
  for; the sizes may arrive late, arrive never, or fail, and the tree is
  complete without them. `tableStatsLoadedMsg` with an error — or from a
  connection the user has already left — is dropped silently, leaving
  exactly the unannotated rendering. The failed statement is in the
  command log like every other one.
- **The cache is the relation listing's.** `objectTree.stats` is keyed by
  namespace and refetched with it, so `R` refreshes both. Because either
  reply may land first, both `applyRelations` and `applyTableStats` end
  in `attachStats`, which re-points the (possibly brand-new) table nodes
  at the cached figures. `treeNode.stat` is a pointer: a zero-row table
  is a real answer and the struct's zero value is not.
- **The annotation is the row's least important part.** It is a
  `noteStyle` of its own (`noteStats`) purely so `sidePanel.render` can
  treat it as elastic: `fitStatNote` drops the size half, then the whole
  note, before the *table name* loses a single cell — the opposite of the
  fixed notes (`loading…`, `failed`), which are the row's message and
  keep their space. While it does fit, the name is padded to the panel
  width so the annotation sits at the right edge instead of hugging a
  short name.

Views and triggers are never annotated: a view has no storage, and
`attachStats` only walks the `Tables` category.

### Keys

- `enter` toggles a branch, opens a table/view in the main view, and
  shows a trigger's definition there.
- `l` / `→` expands; on an already-open branch it steps onto the first
  child. `h` / `←` collapses; on a leaf or a closed branch it steps out
  to the parent. This is the lazygit idiom and it makes both keys safe to
  hold down.
- `R` reloads, `/` filters, `E` exports the selected database's DDL, `B`
  opens dump/restore.

All of them come from `keyMap.panelActions(panelObjects)`, so the
dispatch, the options bar, the `a` menu and `?` cannot drift apart — see
[design/keybindings-single-source](keybindings-single-source.md).

### The filter narrows the expanded level, not the whole catalog

`/` keeps a row when its own name fuzzy-matches **or when any row of its
subtree does** (`sidePanel.treeKeep`). Since the flattened rows are only
the expanded ones, that means:

- a match never loses the category and database it hangs under, so the
  result still reads as a tree;
- collapsed contents are **not** searched. They have not been read from
  the server yet, so there is nothing to match against — searching them
  would mean issuing an introspection query per category per keystroke,
  which is exactly what the lazy loading exists to avoid.

Expand a category first, then filter it. A future "search the whole
catalog" would be a different feature with a different cost model (one
listing per namespace up front), not a change to this key.

### Triggers in the main view

`enter` on a trigger opens `triggerView` in the main view: the
`CREATE TRIGGER` statement, read-only, scrolled with `j`/`k` and closed
with `esc`. It takes over the main view the way the EXPLAIN plan does for
`[3]`, rather than becoming a modal, so the tree stays visible next to
it. While it is up the grid's own actions would all be no-ops, so
`updateData` routes to `updateTriggerKeys` and the options bar shows the
three keys that still act — all of them already documented under `?`'s
navigation group.

`triggerView.req` drops a reply for a trigger the cursor has already
left, the same way `dataView.req` drops a stale page. It is a counter of
its own rather than a reuse of `dataView.req`: opening a trigger must not
invalidate an in-flight page load of the table underneath.

## Consequences

- The `[keys]` action `toggle-tab` is **gone** and `expand-node` /
  `collapse-node` replace it. A config that still sets `toggle-tab` fails
  startup with the "unknown key action" error that lists every valid
  name — a deliberate hard failure, per
  [design/configurable-keys-and-theme](configurable-keys-and-theme.md).
- `relationTab`, `relationTabNames`, `sidePanel.tabs/tab` and
  `sidePanel.tabHit` are removed; the panel title line no longer renders
  sub-tabs. The main view's own tab bar is untouched — see
  [design/main-view-tabs](main-view-tabs.md).
- A multi-namespace connection no longer auto-focuses a database picker:
  it lands on the collapsed tree with the namespaces as roots. A
  single-namespace connection skips the database level entirely and opens
  its `Tables` category straight away, which is what the old "go straight
  to tables" behaviour becomes.
- `treeKeep` is O(subtree) per row, so filtering is O(n²) in the number
  of *visible* rows. That is a side column's worth of rows, and the
  alternative (a precomputed match set invalidated on every keystroke)
  buys nothing at this size.

## See also

- [design/catalog-browsing](catalog-browsing.md) — the async load, stale-reply
  and pseudo-database rules this panel inherited.
- [reference/trigger-introspection](../reference/trigger-introspection.md) —
  what each engine reports for a trigger, and what DuckDB does not.
- [reference/table-size-estimates](../reference/table-size-estimates.md) —
  which catalog each engine's row and size figures come from, and how stale
  each one is.
- [design/tui-shell-architecture](tui-shell-architecture.md) — the panel set
  and the update routing order.
