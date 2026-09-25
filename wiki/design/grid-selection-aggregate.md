---
type: Design Decision
title: Count, sum, avg, min and max of the selected block
description: Why the grid's status line aggregates a column block (and not whole rows), why it reads typed values plus DECIMAL/NUMERIC strings, how it keeps the page scope and the skipped cells visible at every width, and how its per-frame cost stays bounded.
tags: [ui, data-grid, selection, status-line, performance]
generated:
  by: claude-code/opus-5.5
  at: 2026-09-25T00:00:00Z
sources:
  - resource: https://github.com/TrueDaerk/lazysql/issues/223
    note: issue — show sum, average, min and max for the selected block
---

# Count, sum, avg, min and max of the selected block

With a block selected ([design/grid-multi-row-selection](grid-multi-row-selection.md)),
the status line adds an aggregate of its numeric cells:

```
rows 1–100 of ~714 (page 1/8)  3 rows × 2 columns selected · this page: count 5  sum 37  avg 7.4  min 1  max 20 · 1 skipped
```

`Model.selectionAggregate` (`internal/ui/aggregate.go`) computes it and
`Model.dataStatusFit` places it.

## Decisions

- **Blocks only.** A whole-row selection (`ctrl+v`, `shift+↓` without a
  column span) is not aggregated: it would add the primary key to the
  amount next to it — a number, not an answer. Narrowing with
  `shift+←`/`shift+→` or `C` is the deliberate "these values" gesture.
- **Typed values, not rendered text.** `int64` and `float64` count
  (NaN/Inf do not); NULL, booleans, dates and text are skipped and
  counted as skipped. The one exception is the exact numerics — MySQL
  `DECIMAL`, PostgreSQL `NUMERIC`, DuckDB `HUGEINT` — which the drivers
  deliver as strings so no digit is lost: a string is parsed only when
  the column's `DataType` names one of those, so a `TEXT` column of
  digits stays text, as it renders. Figures are float64, so a DECIMAL
  sum is exact only to ~15 significant digits.
- **What the grid shows.** A staged cell edit counts with its new value,
  through the same `rowValues` the copy scopes read.
- **Page scope is always said.** The selection is page-bound (it is
  dropped on every page turn), and every form of the aggregate names the
  page, so it is never read as a total over the filtered table.
- **Skipped cells are always said.** Every form that shows a figure also
  shows the skipped count, so a mixed column is never read as a clean
  total. A block with no numeric cells says `nothing numeric selected`,
  never `sum 0`.
- **Width.** `dataStatusFit(w)` tries the full form, then
  `page: sum … avg …`, then `page sum …`, and finally drops the
  aggregate — it never pushes the sort and filter markers off the line.
  The ordinary `truncate` still guards whatever remains.

## Cost

Nothing runs without a narrowed selection, so an unselected grid pays
nothing per frame. With one, the walk is a type switch per selected cell,
bounded by `aggregateCellLimit` (50 000 cells): a larger block — only
reachable with a big configured page size — shows `too many cells to sum`
instead of costing a frame proportional to it. This follows
[design/grid-cell-scan-bound](grid-cell-scan-bound.md): a frame's cost
depends on what is drawn, not on what was fetched. No cache was added,
for the same reason that concept gives.
