---
type: Design Decision
title: Pinned and hidden columns in the data grid
description: Issue #222's `p`/`z`/`Z` — why pins and hides are stored by column name on the dataView, why the cell cursor stays a data-column index while every sideways gesture walks a separate display order, how the pinned block is laid out beside the scroll window, what the column hint counts, and why copy and export follow the visible columns.
tags: [ui, data-grid, layout, keybindings, selection, copy, export]
generated:
  by: claude-code/opus-5.5
  at: 2026-09-25T00:00:00Z
sources:
  - resource: https://github.com/TrueDaerk/lazysql/issues/222
    note: issue — pin and hide columns in the data grid
---

# Pinned and hidden columns in the data grid

On a wide table the identifying columns scroll off the left edge, and a
single huge `TEXT` column can dominate the viewport. `p` pins the cursor
column to the left edge (and unpins it), `z` hides it, `Z` opens a menu
of the hidden columns that shows them again (one by one, or all).

## Decision

### State: names on the dataView

`dataView.pinned` and `dataView.hidden` are `[]string` of column names,
`pinned` in pin order. Keeping them on the dataView gives the lifetime
the issue asks for with no extra bookkeeping: a page turn, a sort, a
filter and a reload all keep the dataView (and `pageLoadedMsg` only
replaces `cols`/`rows`), while opening another relation — from panel
[2], a foreign-key jump or the Relations tab — builds a fresh one.
`ctrl+o` back from a foreign-key jump restores the saved dataView, pins
included.

Names rather than indices, because a reload after an `ALTER TABLE` may
shift positions; a name that no longer resolves is simply ignored.

### The cursor stays a data index; the display order is separate

`dataView.col` still indexes `cols`. Every action that reads it — edit,
sort, view, follow a foreign key, the bulk edit — keeps meaning the
column it always meant, and none of them needed a change.

What pinning and hiding change is `visibleOrder()`: pinned columns
first (in pin order), then the rest in table order, hidden ones left
out. `h`/`l` and `shift+←`/`shift+→` move through it (`stepCol`), and
`clampCursor` moves a cursor off a hidden column onto its nearest visible
neighbour (`settleCol`). The order is never empty: `z` refuses the last
visible column, and a hidden set that somehow covered every column is
ignored rather than drawing a blank grid.

Pinned columns are excluded from the unpinned run **by index**, not by
name — a query result can carry two columns called `id`, and pinning one
must not make the other vanish.

### Layout: a pinned block plus the old scroll window

`gridLayout` gains `order`, `pinned` and `hidden`; `cs`/`ce` are now
positions in `order`, always at or right of `pinned`. The pinned block's
width (plus its separator) comes off the box and `columnWindow` windows
the remaining columns into what is left, exactly as before. `colOff` is
stored relative to the scrolling part, so pinning a column does not jolt
the window.

If the pinned block leaves no room for the cursor column (a narrow
terminal, or a lot pinned), the frame lays out as if nothing were pinned.
The invariant of [grid-cursor-window](grid-cursor-window.md) — the cell
drawn with the cursor tint is the cell the keys act on — outranks the
pin.

`gridHeader`/`gridRow` take a `gridSpan` (the drawn columns, the column
index each stands for, and the pinned count) instead of `cols[cs:ce]`
plus a first index, because what is drawn is no longer a contiguous run.
The separator after the pinned block is `┃`/`╂`: one cell like `│`, so
`gridColumnAt` and the width math are unchanged, and `clickGrid` maps a
click through `g.shownCols()` — the same run the frame drew. The
read-only grids pass `contiguousSpan` and look exactly as before.

`buildGrid` still sizes every drawn column from the whole page (see
[grid-cell-scan-bound](grid-cell-scan-bound.md)); it skips hidden
columns entirely, so hiding a heavy column also takes it out of the
per-frame cost.

### The hint counts the display order

`columns X–Y of N` counts positions in the display order, pinned columns
included, and N is the number of **visible** columns. Pinned and hidden
columns are named after it:

    columns 5–8 of 20 · 2 pinned · 3 hidden (Z shows) — h/l scrolls

When the window starts right after the pinned block the run is
contiguous and reads `columns 1–6 of 20 · 2 pinned`. The hint is shown
when the grid is scrolled *or* anything is hidden — a hidden column must
never be invisible without saying so — and `gridBodyRows` budgets it
either way.

### Selection block

`sel.colAnchor` stays a data index; `columnRange` translates anchor and
cursor into display positions, so `shift+←`/`shift+→`/`C` span what is on
screen between the two edges. `selectedCols` returns data indices in
display order, which is what the copy scopes cut rows with. A whole-row
selection covers the visible columns only; `narrowedToCols` compares
against the visible count, so hiding a column does not by itself turn a
row selection into a "block". The per-cell selection test is resolved
once per frame (`cellSelector`) instead of walking the display order for
every drawn cell.

### Copy and export follow the visible columns

Every multi-column scope uses the visible columns in display order: the
row copy, the selection copy, the query-page copy, the table copy and
the file export (`E`). The streamed scopes go through
`export.Projection`, which wraps the `Pager`/`QueryRunner` and narrows
each result by position — falling back to the name when the position no
longer matches, and resolving duplicate names by position. With nothing
pinned or hidden the projection is nil and the stream is untouched.

The `CREATE TABLE + INSERTs` copy keeps the full DDL but inserts only the
visible columns, leaving the hidden ones to their defaults — the same
rule a block-selection INSERT copy already follows. The row-detail view
(`x`) and the cell popup (`v`) are not scopes of the grid's layout and
still show everything.

### Keys

`p`, `z` and `Z` were free in the main view and are plain letters, so
they need no AltGr alias under
[keyboard-layout-portability](../reference/keyboard-layout-portability.md).
They are the last entries of `panelActions(panelMain)` — they shape the
view rather than act on the data, so they rank below the keys a
first-time user needs in the truncating options bar. Action names:
`pin-column`, `hide-column`, `hidden-columns`.

## Related

- [data-grid](data-grid.md)
- [grid-cursor-window](grid-cursor-window.md)
- [grid-multi-row-selection](grid-multi-row-selection.md)
- [copy-and-export](copy-and-export.md)
