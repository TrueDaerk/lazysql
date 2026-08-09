---
type: Design Decision
title: Main-view tabs — Data, Structure, Indexes, DDL, Relations
description: Why the main view has five tabs behind one metadata fetch, why they cycle with [ and ] rather than digits, and what survives when the selected relation changes.
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

## `[` / `]`, not `1` / `2` / `3`

The issue offered digit sub-shortcuts as an alternative to bracket
cycling. Digits are not available: `1`–`4` are **global** panel jumps,
handled in `updateGlobal` before the focused view ever sees the key, and
that ordering is the shell's contract
([design/tui-shell-architecture](tui-shell-architecture.md)). Stealing
them inside the main view would make the same key mean two different
things depending on focus — exactly what the numbered-panel convention
exists to prevent.

So the tabs cycle with `[` (previous) and `]` (next), as two separate
bindings rather than one two-key binding: with five tabs, walking
backwards matters, and every key in the options bar must be individually
bound and documented
([design/keybindings-single-source](keybindings-single-source.md)). The
tab bar at the top of the main view is the discoverability half —
`‹Data|Structure|Indexes|DDL|Relations›` with the selected tab
highlighted, in the
same idiom as the `[3]` panel's Tables/Views sub-tabs.

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
