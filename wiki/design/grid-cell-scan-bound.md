---
type: Design Decision
title: The grid never walks more of a value than it can draw
description: Issue #208's "descending sort is sluggish" was not about the sort at all — the descending page simply held the table's biggest rows, and buildGrid flattened, UTF-8 checked, measured and truncated every cell of the page in full on every frame. gridCellText now works from a bounded prefix (cellScanBytes), so a frame costs what the grid can show, not what the page happens to hold.
tags: [ui, data-grid, performance, rendering, sorting]
generated:
  by: claude-code/opus-5
  at: 2026-09-22T00:00:00Z
sources:
  - resource: https://github.com/TrueDaerk/lazysql/issues/208
    note: issue — UI slowdown when the data grid is sorted descending
  - resource: https://github.com/TrueDaerk/lazysql/issues/78
    note: the per-message View build this concept's cost model rests on
---

# The grid never walks more of a value than it can draw

Sorting the grid descending made the whole UI sluggish: the reload felt
slower than the ascending one, and afterwards plain `j`/`k` kept lagging
for as long as the descending sort was active. Ascending — and unsorted —
navigation of the same 714-row MySQL table was fast (issue #208).

## It is not the sort

Two candidates were ruled out by measurement before anything was changed.

**The generated SQL.** `db.PageSQL` puts one word in a different place
for a descending sort:

```
SELECT * FROM `app`.`orders` ORDER BY `id` ASC  LIMIT 100 OFFSET 200
SELECT * FROM `app`.`orders` ORDER BY `id` DESC LIMIT 100 OFFSET 200
```

There is no wrapping subquery, no window function, no reversed keyset —
for any dialect, since all five share `PageSQL` and differ only in
`QuoteIdent` and `LimitOffset`. `TestPageSQLDescendingIsPlainPerDialect`
now pins that, both as literals per engine and as a shape (one `SELECT`,
no parentheses, and the two statements differing by exactly the
direction word).

**The update path.** Nothing between a key press and a frame reads
`data.sort` except the header's `▲`/`▼` marker and the status line's
`sort <col> <dir>` hint. `applyScroll` starts no round trip, and the
test below confirms that navigating a descending page runs zero
statements. On a fixture whose rows all hold the same values, ascending
and descending navigation measured identically.

## What was actually different: the rows

A descending sort by an `AUTO_INCREMENT` primary key puts the *newest*
hundred rows on page one. An ascending sort — and no sort at all — puts
the oldest hundred there. In a table that grew over time those are not
the same rows: the recent ones carry the long notes, the filled-in JSON,
the accumulated payload. That is the whole asymmetry, and it is a
property of the data, not of the query.

It only became a *slowdown* because `buildGrid` — which runs on every
frame, and Bubble Tea v2 builds a frame per queued message (see
[design/input-coalescing](input-coalescing.md)) — formatted every cell of
the page **in full**:

- `flatten` scanned the whole value for `\n\r\t`;
- `classifyCell` ran `strings.TrimSpace`, `json.Valid` and
  `utf8.ValidString` over the whole value;
- the column-width pass called `lipgloss.Width` — a grapheme-cluster
  walk — over the whole value;
- `truncate` in `gridRow` walked it once more for each visible row.

All of that to produce a cell that is **at most `maxColWidth` (32) cells
wide**. A CPU profile of a page of ~8 KB cells put a third of the frame
in `lipgloss.Width` and another sixth in `ansi.Truncate`.

## The bound

`gridCellText` now works from `cellHead(raw)` — the value's first
`cellScanBytes = 4 * (maxColWidth + 1)` bytes, cut back to a rune
boundary so a rune split in half is not mistaken for binary. Four bytes
per cell is the widest a rune gets, and one cell more than the widest
possible column is all `truncate` needs to still mark the value as cut
short, so nothing past that prefix can change a frame.

The binary check dropped `classifyCell`'s JSON arm with it. That arm
exists for `v`, the cell-detail popup, which pretty-prints JSON; the grid
flattens a JSON cell and a plain one identically, so all the grid ever
needed from the classification was "is this valid UTF-8" — and running
`json.Valid` over a multi-megabyte document on every frame was itself
part of the cost.

Nothing is lost. `v` still classifies and shows the whole value, the
copy scopes and the export read `m.data.rows` rather than the rendered
grid, and a cell whose prefix is all whitespace simply renders as the
short value it appears to be.

## What was deliberately not done

- **No cache of the rendered grid.** A cache would have hidden the cost
  behind an invalidation problem (the changeset stages per cell, so the
  key would be most of the model) instead of removing it. The goal was
  a frame whose cost depends on what is drawn, not on what was fetched.
- **No narrowing of `buildGrid` to the visible row window.** Column
  widths are sized from the whole page on purpose: deriving them from
  the visible rows would make columns jump width while scrolling. With
  the per-cell work bounded, a 100-row page is cheap enough that the
  stable layout is worth keeping.
- **Nothing about the reload itself.** The laggy *reload* the issue also
  reports is issue #206 — superseded page queries were not cancelled —
  and it was fixed separately in
  [design/page-query-cancellation](page-query-cancellation.md).

## Measured

`internal/ui/perf_test.go`, 160×48, M4, per queued navigation key on a
714-row SQLite table whose last hundred rows hold a 64 KB note:

| sort | before | after |
|---|---|---|
| ascending (small rows on page one) | 2.7 ms | 2.7 ms |
| descending (big rows on page one) | 68.6 ms | 2.4 ms |

`TestDescendingSortNavigatesLikeAscending` asserts both halves: zero
statements per keystroke in either direction, and a descending key cost
within 4× of the ascending one.
