---
type: Design Decision
title: Main-view tabs — Data, Structure, Indexes, DDL, Relations
description: Why the main view has five tabs behind one metadata fetch, why they cycle with < and > (with [ / ] and , / . as aliases) rather than digits, and what survives when the selected relation changes.
tags: [ui, main-view, tabs, introspection, ddl, clipboard]
generated:
  by: claude-code/opus-5
  at: 2026-08-09T00:00:00Z
---

# Main-view tabs — Data, Structure, Indexes, DDL, Relations

The main view shows one open relation through five tabs. `Data` is the
paged grid from [design/data-grid](data-grid.md); the others render
introspection results:

| Tab | Contents |
| --- | --- |
| `Structure` | one row per column: position, name, type, nullability, default, key info, extra |
| `Indexes` | the indexes (name, type, unique, columns), then the foreign keys with their referenced table and columns |
| `DDL` | the `CREATE` statement, scrollable, `y` copies it |
| `Relations` | the foreign keys in both directions, walkable with `enter` — see [design/relations-tab](relations-tab.md) |

## One fetch behind three tabs

`Structure`, `Indexes` and `DDL` share a single `metaView` filled by one
`loadMetaCmd`, which calls `TableColumns`, `TableIndexes`,
`TableForeignKeys` and `TableDDL` in one command. Splitting them into
three commands would buy three independent loading states and three sets
of staleness checks for four catalog queries that together cost less
than one page read.

The fetch is lazy: opening a relation loads the Data page, and the
metadata only when a tab that needs it is selected (or `y` is pressed).
Browsing a table list with only the Data tab open never issues an
introspection query.

Two error slots, not one:

- `metaView.err` is fatal for all three tabs — the columns could not be
  read at all.
- `metaView.ddlErr` costs only the DDL tab. An engine can describe a
  relation perfectly well and still refuse to produce a `CREATE`
  statement for it (a view on some engines, a table the user cannot
  `SHOW CREATE`), and that must not blank out `Structure`.

## `<` / `>`, not `1` / `2` / `3`

The issue offered digit sub-shortcuts as an alternative to bracket
cycling. Digits are not available: `1`–`4` are **global** panel jumps,
handled in `updateGlobal` before the focused view ever sees the key, and
that ordering is the shell's contract
([design/tui-shell-architecture](tui-shell-architecture.md)). Stealing
them inside the main view would make the same key mean two different
things depending on focus — exactly what the numbered-panel convention
exists to prevent.

So the tabs cycle with two separate bindings rather than one two-key
binding: with five tabs, walking backwards matters, and every key in the
options bar must be individually bound and documented
([design/keybindings-single-source](keybindings-single-source.md)). The
primary spelling is `<` (previous) / `>` (next) — see
[reference/keyboard-layout-portability](../reference/keyboard-layout-portability.md)
for why, and for the `[` / `]` and `,` / `.` aliases kept for muscle
memory. The tab bar at the top of the main view is the discoverability
half — `‹Data|Structure|Indexes|DDL|Relations›` with the selected tab
highlighted, in the
same idiom the `[3]` panel's Tables/Views sub-tabs used before the
object tree replaced them ([design/object-tree-panel](object-tree-panel.md)).

## What resets when the relation changes

Selecting another table keeps the **selected tab** and drops
**everything else**: cached columns/indexes/foreign keys/DDL, the
per-tab cursor and scroll offsets, and any error. Walking down a table
list with `Structure` open and watching each table's columns is the
reason to have tabs at all, so the tab is the one piece of state worth
carrying across relations. Switching connection or namespace resets the
tab to `Data` too, because there is no longer a relation the tab could
describe.

Stale replies are dropped the same way the data grid drops them: a
`metaLoadedMsg` is applied only when its `req`, connection and table all
still match.

## `j`/`k` mean different things per tab

- `Structure` has a row cursor; the window follows it.
- `Indexes` and `DDL` scroll by offset — their rows are not selectable
  targets, and the DDL is free text.
- `Relations` has a row cursor over both halves of its edge list, and
  `enter` there walks to the selected table instead of reloading.

`R` (and `enter`) re-read whatever the visible tab shows: the metadata
on the three introspection tabs, the page on `Data`.

## Copying the DDL

`y` copies through `clipboardWrite` in `internal/ui/clipboard.go` — a
package-level variable wrapping `atotto/clipboard`, so the copy/export
work has one seam to grow from and tests can replace it (a test run must
never overwrite the developer's real clipboard).

`y` works from any tab, including `Data`. When no metadata is cached
yet, it sets `copyAfterLoad`, starts the fetch and copies when the reply
lands, rather than telling the user to visit the DDL tab first. Success
and failure both land in the command log.

## Shortening the strip before the name (issue #217)

`renderTitledBox` truncates whatever `mainTitle` hands it from the right,
with no idea which part of the string matters. At narrow widths that ate
the relation name — the one piece of information the tab strip's
highlighting cannot convey — while leaving the fully redundant tab list
(`‹Data|Structure|Indexes|DDL|Relations›`) untouched, since it sits first.

`mainTabBar` now decides how much of the strip to draw *before*
concatenating the relation name, instead of relying on `renderTitledBox`'s
blind right-truncation to sort it out:

1. `tabStripFull` — every tab name (unchanged, wide terminals).
2. `tabStripFocusedName` — only the focused tab's name, e.g. `‹Structure›`.
3. `tabStripFocusedLetter` — only its first letter, e.g. `‹S›`.

`mainTabLevel` picks the least-shortened level whose width plus the
relation-name suffix still fits the title's room (`w-2`, matching the
padding `renderTitledBox` reserves around the title). The focused tab
keeps its emphasis style at every level, so which tab is open stays
readable even down to a single letter. This mirrors why the full strip
was ever considered droppable: the highlight — not the neighbor labels —
is what identifies the open tab, so collapsing to "highlighted single
label" loses nothing a sighted user was reading from the list.

Collapsing the strip changes what a click can mean: `mainTabHit` (in
`internal/ui/mouse.go`) re-derives the same level from the same width
before hit-testing, so a click never lands on a label that was not
actually drawn. Once collapsed to one label, every click on it resolves
to the tab that is already focused — there is nothing else on screen to
switch to.

The other titles sharing this box (`queryTitle`, `diffTitle`,
`activityTitle`, `planTitle`) are all `label — value` pairs with no
redundant list in front of the value, so none of them had this failure
mode.
