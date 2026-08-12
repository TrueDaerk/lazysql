---
type: Design Decision
title: Multi-row selection in the data grid, its clipboard copy and its bulk column edit
description: Why the grid's selection is an anchor plus the cell cursor, why every query-shape change drops it, why `ctrl+v` is free to mean visual selection, why `ctrl+c` copies without ever costing the app its quit key, and how one edit modal stages a pending change per selected row.
tags: [tui, data-grid, selection, keybindings, clipboard, copy, editing, staged-changeset]
generated:
  by: claude-code/opus-5
  at: 2026-08-12T00:00:00Z
sources:
  - resource: https://github.com/TrueDaerk/lazysql/issues/120
    note: multi-row copy to the clipboard
  - resource: https://github.com/TrueDaerk/lazysql/issues/119
    note: the selection mode this state was introduced for, plus the bulk column edit
---

# Multi-row selection in the data grid

## Decision

The data grid carries one piece of selection state — `dataView.sel`, a
`rowSelection{active, anchor}` — and every action that can mean "these
rows" reads it. It is deliberately not copy-specific: the bulk column
edit of issue #119 is meant to stage from the same range.

### Anchor plus cursor, not a set of marked rows

Only the anchor is stored. The other end of the range is the cell cursor
itself, so `j`/`k` — and every other vertical movement, including the
wheel — extend and shrink the selection without a second movement path
to keep in sync. `selectionRange` orders the two ends, so anchoring
below the cursor and moving up selects exactly as anchoring above and
moving down does.

The range is clamped to `len(rows)`: the phantom rows of staged inserts
are rendered after the page but have no fetched values behind them, so
they never take part in a selection.

### The selection never outlives the page it was made on

The anchor is a page-row index, which is only meaningful for the rows
that were on screen when it was set. A sort, a filter, a page turn or a
reload puts different rows under the same indices, so all of them drop
the selection (`dataView.clearSelection`, called from `setPage` and
`Model.reloadPage`, plus `Model.clampCursor` for a page that came back
shorter). Remapping a selection onto the rows it "meant" would need a
primary key the grid does not always have — a query result has none at
all — and a silently wrong remap would stage or copy the wrong rows.

### `ctrl+v` is free; `ctrl+c` is not

`ctrl+v` is vim's visual-block key and nothing in lazysql reads it:
bracketed paste arrives as a `tea.PasteMsg`, not as the key chord (see
`internal/ui/paste.go`), so the grid can claim it outright.

`ctrl+c` is the opposite case — it is half of `keyMap.Quit` and, during
a run, `keyMap.CancelQuery`. It is bound as `keyMap.CopySelection` with
`key.WithDisabled()` and enabled only while a selection is up, the same
pattern `CancelQuery` and `CancelExport` already use. Because the global
keys are matched before the focused view ever sees a key, the copy is
matched inside `updateGlobal`, ahead of `Quit` and behind
`CancelQuery`: cancelling a running query stays the more urgent reading
of `ctrl+c`, and the copy is one key press away afterwards. With no
selection the binding does not match at all, so `ctrl+c` quits exactly
as it always did — and disabling it is also what takes it back out of
the options bar and `?`, since both skip disabled bindings.

`esc` leaves selection mode before it does anything else in the grid:
while a selection is up that is the mode the user is in, so leaving it
must not also unwind a foreign-key jump or the focus stack.

### The copy menu is extended, not duplicated

`ctrl+c` opens `copyMenu` — the same menu `y` opens. When rows are
marked, `copyMenu` puts the selection scopes first and drops the
single-row scopes: with N rows selected, "row" is no longer what the
user means by a copy. The selection takes the row scope's own keys
(`r` CSV, `o` JSON, `i` INSERTs) so the muscle memory carries over, and
adds one scope only a selection has:

- **`c` — column values of the selection**: the cursor column's value in
  every selected row, one per line. This is the list-of-ids case: paste
  it straight into an `IN (…)` or another tool. NULL copies as an empty
  line for the same reason `copyCell` copies it as an empty string — the
  cell holds no text, and the four letters `NULL` would be a silent lie.
  The command log says how many there were.

The table scopes are untouched: they do not depend on the cursor or the
selection. The row formats go through `export.Rows`, so a selection copy
is a small table — CSV with its header, JSON as one array — rather than
several single-row copies glued together, and staged cell edits are
applied by the same `rowValues` a single-row copy uses.

Delivery is unchanged: everything goes through `copyTextCmd` →
`copyOut`, i.e. native clipboard → OSC 52 → temp-file spill (see
[design/clipboard-strategy](clipboard-strategy.md)). A page holds at
most `dataPageSize` rows, so a selection copy can never approach the
`copyRowLimit` a whole-table copy needs.

### The bulk edit is the ordinary edit, aimed at more rows

`e` with a selection up opens the **same** modal it always did, for the
cursor column. What changed is what the confirmed value is staged into:
`openEditModal` builds a `[]db.CellChange` (`bulkTargets`) instead of
one, and `stageValue` is the confirm path for both cases — one target
takes the single-cell `stageChange` unchanged, several stage the same
value in the same column of every selected row. The date picker gets the
same slice, so a temporal column bulk-edits too.

The cursor row is always `targets[0]`: it is what the modal's prefill,
its `current:` line and `convertInput`'s type guidance come from, so the
value is converted once, against the cell the user is actually looking
at, and bound identically everywhere.

Two per-row rules carry over rather than being decided once for the
batch: a row that already holds the new value is not staged (and an
existing staged edit of it is *unstaged*, so a bulk edit back to the
original value cleans up after itself), and `Changeset.Stage` keeps the
database's own value as `OldValue` when it replaces an earlier edit.

Row identity is resolved **when the modal opens**, not when it is
confirmed: `bulkTargets` turns each selected row index into its primary
key values there and then, and it is those keys that reach the
changeset. A page reply landing while the modal is up therefore cannot
redirect a staged edit — the indices are already spent. Rows that cannot
be identified safely never reach the changeset at all: one already
staged for deletion, and one whose primary key the result set does not
carry. They are counted and reported in the command log rather than
silently skipped, because "lazysql does not guess row identity" does not
get weaker when several rows are selected at once.

Nothing executes: N staged changes are N ordinary changeset entries —
shown in the grid, counted by the status badge, previewed as
parameterized SQL by the commit modal and run in the one transaction `c`
opens ([design/staged-changeset](staged-changeset.md)). The command log
gets one staging line for the batch, carrying the first row's statement
and how many more rows share it, rather than N near-identical lines.

The selection is consumed by the edit, the way vim leaves visual mode
after an operator: leaving it up would aim the next `e` at rows the user
has stopped thinking about.

## Consequences

- The selection is visible in two places: selected rows are tinted with
  the selection background (the cursor row keeps its own highlight, so
  it stays findable inside the block) and the status line reads
  `N rows selected`, next to the sort and filter markers.
- `ctrl+v` and `ctrl+c` are entries in `panelActions(panelMain)` like
  every other grid key, so dispatch, the options bar, the `a` menu and
  `?` all still read one table — see
  [design/keybindings-single-source](keybindings-single-source.md). Both
  are rebindable as `select-rows` and `copy-selection`.
- The bulk column edit builds on `dataView.selectedRows()` and touches
  none of the copy path; nothing in the selection state is specific to
  either consumer.
- `e` now means "edit this column in every selected row" while a
  selection is up. It is the only grid key that changes meaning with the
  selection — `d`, `n` and `D` stay single-row, because a bulk delete is
  a different confirmation problem and an insert has no rows to spread
  over.
