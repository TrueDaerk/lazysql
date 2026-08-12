---
type: Design Decision
title: One grid layout behind the highlight, the hit test and the cursor
description: Why the data grid's scroll window is a clamped stored offset rather than a value derived from the cursor, why rendering, clicking and cursor clamping all go through one gridLayout, and why every line the grid draws has to be budgeted.
tags: [ui, main-view, data-grid, cursor, layout, mouse]
generated:
  by: claude-code/opus-5
  at: 2026-08-12T00:00:00Z
sources:
  - resource: https://github.com/TrueDaerk/lazysql/issues/118
    note: issue — data grid, rendered cursor desyncs from actual position
---

# One grid layout behind the highlight, the hit test and the cursor

## The invariant

**The cell rendered with the cursor tint is the cell `enter`, `e`, `v`,
`y` and `x` operate on.** The cursor is `dataView.row`/`col`; the
highlight is whatever `gridRow`/`cellStyle` tinted; the click target is
whatever `clickGrid` resolved. Three pieces of geometry, one truth — and
before issue #118 they could each be right on their own while
disagreeing with each other.

## Decision

### The window is stored and clamped, not derived

`dataView` carries `rowOff`/`colOff`: the top-left cell the grid last
settled on. `rowWindow(n, cursor, rows, off)` and
`columnWindow(cols, cursor, w, off)` clamp that offset into the range
that keeps the cursor visible and scroll no further than they must.

A derived window — anchored at the top until the cursor passes the last
visible slot — needs no state and cannot go stale, which is why the grid
started with one. But it also has no memory, so the window is a function
of the cursor rather than of where the user last was:

- `k` from a row below the fold moved the cursor up one and re-derived
  `start = cursor - rows + 1`. The highlight stayed on the last visible
  line and the *rows* slid instead — then jumped the moment the cursor
  fell inside the top window.
- Clicking near the top of a scrolled page set the cursor to the clicked
  row, which then fit in the top-anchored window: the page scrolled back
  to row 0 and the highlight landed several lines below the pointer.
- `h` had the horizontal version of the same behaviour.

Clamping a stored offset keeps the useful half of the derived rule —
nothing has to be invalidated when the terminal resizes or the page comes
back shorter, because the offset is *checked* on every use rather than
trusted — while the window keeps its place between frames.

`View` still has a value receiver and writes nothing back. The offsets
are settled in `Model.clampCursor` (`internal/ui/rowops.go`), which every
cursor move, page change and changeset edit already funnels through; if
the grid is not on screen it quietly does nothing and the next render's
clamp covers it.

### One `gridLayout`, three callers

`Model.gridLayout(w, h)` (`internal/ui/datagrid.go`) is the single
function that decides what is on screen: the formatted page, the column
window, whether the horizontal-scroll hint is shown, and the row window.

- `dataBody` renders it.
- `clickGrid` (`internal/ui/mouse.go`) maps a click back through it.
- `Model.clampCursor` stores the offsets it settled on.

The click path used to re-implement the same arithmetic against
`mainColumnRect`/`commandLogHeight`. Two copies of a layout calculation
are two chances to drift; `gridViewport` now hands all three callers the
same content box, including the smaller one the result gets while panel
`[3]` is focused and the editor sits above it.

### Every line the grid draws is budgeted

`gridBodyRows(h, hint)` subtracts the three header lines, the status
line, **and** the `columns 3–5 of 9 — h/l scrolls` hint when the columns
do not all fit. The hint was previously appended without a budget, so a
sideways-scrolled grid on a full page rendered one line more than its box
held: the main view box grew a row and pushed the command log and the
options bar off the bottom of the screen.

### The changeset owns the phantom rows, so it has to clamp

Staged inserts render as phantom rows after the page, and the cursor can
sit on one. Anything that drops them has to bring the cursor back:
`u` and staging already did, `U` (discard) and a successful commit did
not, which left the cursor pointing past the last rendered row —
no highlight anywhere, and every cell action silently working on nothing
until the reload landed. Both now call `Model.clampCursor`.

## Consequences

- Regression tests read the highlight out of the rendered frame (its SGR
  background) and compare it with what `dataView.cell()` /
  `phantomAtCursor` return, so they assert the invariant itself rather
  than an implementation detail —
  `TestHighlightFollowsTheCursorThroughEverySequence`,
  `TestClickKeepsTheHighlightOnTheClickedLine` and their neighbours in
  `internal/ui/data_test.go` / `mouse_test.go`.
- `Model.clampCursor` now builds the grid once per cursor move. That is
  the same work one render already does and does not show up in
  `BenchmarkNavKeyDataGrid`, which is dominated by `View`.
- The Structure and Relations tabs keep no offset of their own and pass
  `0`, which is exactly the old top-anchored window. Giving them the
  same treatment is a separate change.

## Related

- [data-grid](data-grid.md) — the grid itself, its paging and its filter
- [mouse-support](mouse-support.md) — why hit-testing recomputes the
  layout instead of caching rects
- [staged-changeset](staged-changeset.md) — where the phantom rows come
  from
