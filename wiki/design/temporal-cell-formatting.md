---
type: Design Decision
title: A DATE or TIME cell renders only the half of the value it carries
description: Issue #214 (P3 of the 2026-08 UX audit) — a DATE column showed a full RFC3339 timestamp with an invented midnight time-of-day. db.FormatTemporalValue reads the column's already-classified TypeKind and drops the half FormatValue would otherwise invent, everywhere a cell is displayed; copy/export and the date picker are untouched.
tags: [ui, data-grid, dates, coltype]
generated:
  by: claude-code/sonnet-5
  at: 2026-09-25T00:00:00Z
sources:
  - resource: https://github.com/TrueDaerk/lazysql/issues/214
    note: issue — render DATE and TIME columns without an invented time or date part
  - resource: wiki/reference/ux-audit-2026-08.md
    note: recorded as P3, unfixed until this change
---

# A DATE or TIME cell renders only the half of the value it carries

Against a SQLite fixture with `birthday DATE`, the grid showed
`2026-08-02T00:00:00Z` where the stored value was `2026-08-02` — `db.FormatValue`
has exactly one rendering for `time.Time`, `time.Format(time.RFC3339)`, and a
DATE value scanned into a `time.Time` sits at midnight UTC because there is no
time part to put there. `TIME` columns had the mirror problem: a date half
(`0000-01-01`) invented around the clock time.

The information to fix this was already sitting next to the value:
`db.ClassifyType` ([design/date-time-picker](date-time-picker.md)) already
reads a column's declared type into a `TypeKind`, and `db.ResultSet`'s
`Column.DataType` already carries the declared type to every place a cell is
rendered — `gridColumn.typ` in the grid, `colType` in the cell-detail popup,
`rowDetailField.typ` in `x`. Nothing new had to reach the UI; the rendering
path just never consulted it.

## FormatTemporalValue

`db.FormatTemporalValue(v any, kind TypeKind, null string) string`
(`internal/db/coltype.go`) sits next to `FormatValue`, not inside it:
`KindDate`/`KindTime` drop the half the kind does not carry, formatting a
`time.Time` with `kind.Layout()` instead of `FormatValue`'s fixed RFC3339, and
reformatting a string value through `ParseDateTime` when the engine (SQLite,
DuckDB) hands the value back as text rather than a scanned `time.Time`. Every
other kind — `KindOther`, and deliberately `KindDateTime` — is `FormatValue`
unchanged: a `DATETIME`/`TIMESTAMP`/`TIMESTAMPTZ` value genuinely has both
halves, so nothing about it was wrong to begin with.

A value that fails to parse (bad data, or a value the classifier could not
place) passes through untouched rather than disappearing — matching
`FormatValue`'s own behavior for a type it does not recognize, and the
acceptance criterion that unparseable text keeps its raw form.

## Where it is called, and where it deliberately is not

Every *display* path was switched from `FormatValue` to `FormatTemporalValue`,
each already holding the column's type at the call site:

- `gridCellText` (`internal/ui/datagrid.go`) — the grid. `buildGrid` now
  classifies each column's kind once per column, not per cell, keeping the
  per-cell cost the same as before; see
  [design/grid-cell-scan-bound](grid-cell-scan-bound.md) for why that bound
  matters and must not regress.
- `newCellModal` (`internal/ui/modal.go`) — `v`, the cell-detail popup.
- `newRowDetailModal` (`internal/ui/rowdetail.go`) — `x`, both the fetched-row
  branch and the phantom-insert branch.

Copy/export (`y`, CSV/JSON/INSERT via `internal/export` and
`db.InsertStatement`/`QuoteLiteral`) was **not** touched — it never went
through `gridCellText` or `FormatValue` at all. `QuoteLiteral` switches on the
Go value's own type (`time.Time`, `string`, …) and renders a `time.Time`
through `timeLiteral`, the dialect-correct SQL literal, independent of the
declared column type. An `INSERT` generated from a `DATE` cell already
round-tripped before this change and still does; formatting only ever mattered
for what the grid *draws*, not for what leaves it.

The `e` date picker (`internal/ui/edit.go`) is untouched for the same reason:
it already builds its initial value and stages its result through
`kind.Layout()`, the same layout table `FormatTemporalValue` now reads for
display.

## What was deliberately not done

- **No change to `db.FormatValue` itself.** Every other caller (`QuoteLiteral`,
  copy scopes, `edit.go`'s plain-text editor) wants the full value regardless
  of the declared type; overloading `FormatValue` with a `TypeKind` parameter
  would have forced every one of those call sites to either pass `KindOther`
  or start caring about a distinction that was never their problem.
- **No per-cell reclassification.** `ClassifyType` runs once per column in
  `buildGrid`, not once per cell — the column's kind cannot change row to row,
  and the scan-bound performance rule
  ([design/grid-cell-scan-bound](grid-cell-scan-bound.md)) rules out any new
  per-cell parsing of unbounded text. `FormatTemporalValue`'s string branch
  only ever runs on a column already known to be temporal, so the values it
  parses are always short.
