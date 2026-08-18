---
type: Design Decision
title: The read-only grid (roGrid) shared by the data grid and the server activity report
description: Why the data grid's cursor, column window, selection and copy scopes were extracted into an roGrid plus a gridCursor and four value-level copy functions instead of being re-implemented in activity.go, why the mutating half stayed behind, and how the activity report re-anchors its cursor and selection by session ID across an auto-refresh.
tags: [tui, data-grid, server-activity, selection, copy, keybindings, read-only]
generated:
  by: claude-code/opus-5
  at: 2026-08-18T00:00:00Z
sources:
  - resource: https://github.com/TrueDaerk/lazysql/issues/176
    note: full grid navigation, copy and multi-select in the server activity view
  - resource: https://github.com/TrueDaerk/lazysql/issues/174
    note: the focus model this builds on — the report is focused in the main view
---

# The read-only grid

## Decision

The server activity report (`internal/ui/activity.go`) no longer renders a
bespoke table. It holds an **`roGrid`** — the data grid's machinery with the
database taken out — and gets column-wise navigation, a sliding column window,
multi-row and block selection, the selection tints and the copy scopes from the
same code the Data tab uses.

Nothing was made generic that did not need to be. The split ran along one line:
**what a grid does with values it was handed** is shared; **what a grid does
because there is a table behind it** stayed in `data.go`/`copy.go`.

### What moved into the shared half

| Shared | Where |
|---|---|
| `gridColumn`, `columnWindow`, `rowWindow`, `gridColumnAt` | already free functions in `datagrid.go`/`mouse.go` — unchanged |
| `gridSelection` and its range math (`rowRange`, `colRange`) | moved to `rogrid.go`; both grids call the same two methods with their own cursor and row/column counts |
| `gridHeader`, `gridRow`, `cellStyle` | now read a `gridCursor` (row, col, focused, `selected(r,c)`) instead of `m.data` and `m.focus` |
| `rowKind` | gained `rowAlert` and `rowFaded` for read-only grids, which have no changeset to tint from |
| the cell / row / selection / column copy scopes | `copyCellValue`, `copyRowValues`, `copyRowBlock`, `copyColumnValues` in `copy.go` — plain functions over columns and values |

`roGrid` itself is cursor + offsets + selection + `[]gridColumn`, plus
`roGridLines` (render, settling the windows) and `clickRow` (hit test through
the windows the render settled). Cells are **strings**: a read-only grid renders
what it was handed rather than formatting values it owns.

### What deliberately stayed behind

`buildGrid`, `gridLayout` and `gridViewport` are not shared. They are about the
*page*: staged cell edits, phantom rows for staged inserts, the foreign-key
column marks, the sort markers in the header, the editor block the query view
stacks above the grid. Threading all of that through an interface so a session
list could ignore it would have made both call sites harder to read than the
one duplicated concept — building `[]gridColumn` — is worth.

The same goes for the copy **menu**. `copyMenu` branches to
`activityCopyMenu` rather than being parameterized, because the two menus offer
genuinely different scope sets, not the same set with holes in it.

### Read-only means absent, not inert

The report's key table is `keyMap.activityActions()`, the single source that
`updateActivityKeys` dispatches, the options bar renders and `?` lists — the
same contract `panelActions` is for a panel. Every mutating action is simply
**not in it**: no edit, delete, insert, duplicate, commit, unstage or discard.
Pressing `e` on the report is a no-op that says nothing, because there is
nothing there to say no to. `runAction` routes the grid actions through
`Model.activityDispatch` first while the report has the focus, so `h`, `l`,
`ctrl+v`, `C` and the shift-arrows act on its grid rather than on the page
behind it.

`K` is the one exception to "read-only", and it is not an exception to the rule
above: it acts on a *server session*, not on a row of data, and it still never
executes anything outside a confirm modal.

### `K`/`J` do not extend the selection here

The data grid binds `shift+↑`/`shift+↓` with `K`/`J` as fallbacks for terminals
that cannot report shifted arrows. In the report `K` kills a session, so the
report binds its own `ActivitySelectUp`/`ActivitySelectDown` — the same shifted
arrows, no letter fallback — rather than listing one key under two meanings in
`?`. Terminals without shifted arrows still get there the way the grid's other
fallback works: `ctrl+v` (or `V`) anchors, plain `j`/`k` extend.

### Copy scopes without a table

The grid's INSERT-statement scopes and its whole-table scopes need a relation to
name and to re-read. The report has neither, so `activityCopyMenu` offers the
cell, the row, the selection (CSV / JSON / cursor-column values) and the whole
list it already holds, and leaves the rest out — degrading by absence, not by
an entry that answers "not here".

What a copy carries is **not** what the table renders. The table shows `—` for a
column the engine reported nothing for and flattens a multi-line statement onto
one row; `activityValues` copies the empty string and the statement as the
server sent it. The dash and the flattening are rendering, and a copied query is
meant to be read and run.

The Client column, which used to reach only the kill confirmation because the
table had no room for it, is a column now that the grid scrolls sideways.

## Auto-refresh: what survives a re-read

`activityTickMsg` replaces the row set wholesale. `activityView.setRows` is the
only thing that ever does, and it **re-anchors by session ID**:

- The **cursor** moves to the row whose session ID it was on. A session that
  ended above the cursor would otherwise slide a different one under it — with
  `K` pointing at it.
- If the cursor's session is no longer listed, the cursor stays at the index it
  had, clamped into the new list.
- A **selection** is kept only if both of its ends — the stored anchor and the
  cursor — are still listed; the anchor is then re-anchored to its session's new
  index. If either end has gone the selection is **dropped**, not silently
  re-cut over sessions the user never marked.
- `ctrl+c` follows the selection through `Model.syncCopySelectionKey`, so the
  key goes back to meaning quit the moment a refresh drops the selection.

This is the opposite of the data grid's rule, and deliberately so: the grid
drops its selection on every reload (see
[design/grid-multi-row-selection](grid-multi-row-selection.md)) because a page
row has no identity across a re-query. A session does — its ID is the identity —
so the report can do better than forget.

## Consequences

- The report's columns are content-sized and capped at `maxColWidth` like every
  other grid column, instead of the old fixed caps with the statement taking
  whatever was left. Wide statements are read with `v` (the cell) or `x` (the
  whole session) rather than by being given the rest of the box.
- `v` now shows the **cell under the cursor**, not always the statement — the
  grid's meaning of the key. `x` opens the whole session in the row detail
  popup, which is the better home for "show me everything about this session".
- A grid whose columns declare no types renders a two-line header (names, rule)
  instead of three. `gridHeaderRows` is the one place that number lives, so the
  render budget and the click hit test cannot disagree.

## Alternatives considered

- **Leave `activity.go` bespoke and add columns/copy/selection to it.** Rejected
  by the issue and on the merits: three of the four behaviours already existed
  and would have drifted.
- **Make the data grid itself read-only-capable behind a flag.** Rejected: the
  flag would have to be consulted in `buildGrid`, `clampCursor`, `dataActions`,
  `copyMenu` and the key table, which is more coupling than the extraction, not
  less.
- **A `copySheet` value threading every scope through one struct.** Rejected as
  more indirection than four functions over `([]db.Column, [][]any)` — the
  scopes take exactly what they need and nothing else.
