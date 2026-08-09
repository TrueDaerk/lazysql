---
type: Design Decision
title: Row detail view — psql `\x`-style expanded row
description: Why `x` opens a two-column name/type/value list instead of a modal grid, how it reuses the grid's staged-change and cell-detail machinery, and why it has no modal-stacking of its own.
tags: [tui, main-view, modal, data-grid, staged-changeset]
generated:
  by: claude-code/sonnet-5
  at: 2026-08-09T00:00:00Z
---

# Row detail view

## Decision

`x` on the data grid opens `rowDetailModal` (`internal/ui/rowdetail.go`)
over the row under the cursor: every column as one line, name and
declared type on the left, value on the right — psql's `\x` for a table
too wide to read a row of across [data-grid](data-grid.md)'s columns.
`j`/`k` move a field cursor and scroll once the row has more fields than
the modal has room for; `esc`/`q` close back to the grid, which is a
no-op on the cursor because opening the modal never touches
`dataView.row`/`col` in the first place.

### One field builder, not two renderers

`newRowDetailModal` builds the same `[]rowDetailField` regardless of
what the cursor sits on — a fetched page row or the phantom row of a
staged INSERT (`Model.phantomAtCursor`) — mirroring the two branches
`buildGrid` already has for the same reason. A phantom row's unbound
column becomes `isDefault` (rendered as `DEFAULT`, muted, same as the
grid); everything else looks up `m.changes.Lookup` by primary key the
way the grid's per-cell staged detection does, so a pending edit shows
its new value with the staged tint before it is committed. A row staged
for deletion sets the whole modal's `status` to `rowDeleted` and every
value renders struck through — the same all-or-nothing precedence
`cellStyle` uses in the grid, because a going-away row makes a
per-field tint meaningless.

It refuses to open (returns `false`) on the metadata tabs and on a
cursor with no row to show, the same guard `copyableRow` uses for `y`.

### `v` on a field is the grid's cell modal, unchanged

There is no second cell-rendering path: `v` on a field calls the same
`newCellModal` (`v`'s own popup, [cell-detail-popup](cell-detail-popup.md))
with the field's raw value. Because the shell has exactly one modal slot
(`Model.modal`, nil = none — [tui-shell-architecture](tui-shell-architecture.md)),
opening it replaces the row detail modal rather than stacking on top of
it; closing the cell modal lands back on the grid, not back on the row
detail. This was accepted rather than adding a modal stack for one
call site — the row detail view itself stays open for every other key,
and `v` already meant "leave this list for the full value" on the grid.

## Consequences

- The left column's width is derived from the widest name and the
  widest type across the row's own columns, capped at 24/16 cells, so a
  table with a handful of short column names does not waste half the
  modal on padding.
- Long values are flattened the same way the grid does (`flatten`,
  embedded newlines/tabs collapsed to single spaces) and truncated with
  an ellipsis — no independent truncation width or logic was added.

## See also

- [data-grid](data-grid.md) — the grid `x` is opened from, `buildGrid`'s
  per-cell staged/NULL detection this reuses.
- [cell-detail-popup](cell-detail-popup.md) — what `v` opens from a
  field.
- [staged-changeset](staged-changeset.md) — the changeset `isStaged`,
  `isDefault` and `status` read.
