---
type: Reference
title: Runes, cells and escape bytes — the three lengths a TUI renderer must not mix
description: Why the query editor's caret and the data grid's cursor tint drifted off their characters — a rune index compared against a cell width in the editor's wrap geometry, and SGR escape bytes counted as visible columns in truncate — plus the lineCells mapping and the ansi.Truncate rule that fix them.
tags: [ui, rendering, cursor, unicode, ansi, lipgloss, query-editor, data-grid]
generated:
  by: claude-code/opus-5
  at: 2026-08-12T00:00:00Z
sources:
  - resource: https://github.com/TrueDaerk/lazysql/issues/132
    note: issue — cursor caret rendered offset from actual position in query editor
  - resource: https://pkg.go.dev/github.com/charmbracelet/x/ansi
    note: ansi.FirstGraphemeCluster / ansi.StringWidth / ansi.Truncate — the width tables lipgloss.Width sums
---

# Runes, cells and escape bytes

A string in a TUI has three different lengths, and every one of them is
the right answer to some question:

| length | what it answers | how to measure |
| --- | --- | --- |
| bytes | how much memory | `len(s)` |
| runes | where the *cursor* is — bubbles' textarea and textinput both count in runes | `len([]rune(s))` |
| cells | how many *columns* the terminal spends on it | `lipgloss.Width(s)` / `ansi.StringWidth(s)` |

They coincide for plain ASCII, which is why mixing them survives every
casual test and then shows up as "the highlight is a few characters off"
the first time somebody types an umlaut or opens a table with CJK in it.
Issue #132 was two independent instances of the mix-up.

## 1. A rune index compared against a cell width (query editor)

`internal/ui/highlight.go` wraps a logical line into display rows and
then draws the caret on one of them. The wrap points came out of a
per-cell walk, but the decision *"does the caret still fit on this row,
or does it need a row of its own?"* compared a **rune offset** with the
box's **cell width**:

```go
offset = col - segs[last][0]   // runes
if offset >= width { … }       // cells
```

On a row of CJK the rune count is half the cell count, so the test said
"there is room" for a row that was already full, and `renderTokens`
appended the trailing caret cell one column past the editor's right
edge. The row came out one cell too wide, the layout clipped it — and
what it clipped was the caret. On a real terminal the overflowing cell
landed on the panel border instead.

The fix is a single mapping every caller shares:

```go
// cells[i] is the display column rune i starts at;
// cells[len(runes)] is the line's width — where a trailing caret sits.
func lineCells(runes []rune) []int
```

`wrapSegments`, `cursorSegment` and the caret's own column are all
expressed in it. Because it is built from `ansi.FirstGraphemeCluster`,
which is exactly what `ansi.StringWidth` sums, what the editor measures
and what the surrounding box measures can never disagree.

## 2. A grapheme cluster is not a rune

`lineCells` maps every rune of a cluster onto the cluster's own column,
so the mapping is non-decreasing and a repeated value marks a rune that
must not be split off from the one before it. That matters more than it
sounds:

- **macOS hands over decomposed (NFD) text.** A pasted `ä` is `a` +
  U+0308 — two runes, one cell. Measuring per rune (the old
  `lipgloss.Width(string(r))` walk, with its `if rw < 1 { rw = 1 }`
  floor) counted the accent as a whole column, so the wrap point drifted
  left by one cell per umlaut.
- **The caret has to cover the whole cluster.** Styling only the base
  letter leaves the accent outside the caret; styling only the accent
  draws a zero-width caret, i.e. none at all. `renderTokens` takes the
  cluster with `clusterRunes`, and `cursorSegment` snaps a cursor that
  landed mid-cluster back to its start with `clusterStart`.
- Emoji sequences (ZWJ families, flags) are the same shape and fall out
  for free.

Tabs are *not* on this list: the textarea's sanitizer turns a typed tab
into spaces before it reaches the buffer, so `renderTokens` never sees
one. `internal/ui/highlight_test.go` pins that, because it is a property
of bubbles rather than of lazysql.

## 3. Escape bytes are not columns (`truncate`, data grid)

`internal/ui/view.go`'s `truncate` walked runes accumulating
`lipgloss.Width(string(r))`. For a lone `\x1b` that is 0, but for the
`[`, the digits and the `m` that follow it — the rest of the SGR
sequence — it is 1 each. Most callers hand `truncate` text that is
**already styled**: a grid row, the grid header, the options bar. So a
tinted row was cut three to six columns short of its box, and the cut
threw away the closing `\x1b[m` with the tail, leaving the style open to
bleed into everything drawn after it. On the data grid that is the
cursor cell: its highlight was both too narrow and unbounded.

`truncate` now delegates to `ansi.Truncate(s, w, "…")`, which skips
sequences, counts grapheme clusters and re-closes whatever style it cut
through. One wrinkle worth keeping: `ansi.Truncate` measures a string end
to end, so a **multi-line** block has to be cut row by row — `w` is a box
width, not a budget spent across the block — or every row after the first
is swallowed.

## Rules of thumb

- Never compare a rune index with a width. Convert first.
- Measure with `lipgloss.Width`/`ansi.StringWidth`, never with `len`, and
  never rune by rune — a grapheme cluster is only measurable whole.
- Cut styled text with `ansi.Truncate`, never by slicing.
- A renderer that produces a row wider than its box has a bug even when
  it *looks* fine: the layout clips it silently, and what gets clipped is
  the last thing drawn — which for a caret at the end of a line is the
  caret.

## See also

- [design/query-editor-panel](../design/query-editor-panel.md) — why the
  editor draws the buffer itself instead of letting the textarea render.
- [design/grid-cursor-window](../design/grid-cursor-window.md) — the same
  invariant one layer up: the tinted cell is the cell the actions work on.
- [reference/lipgloss-v2-sizing](lipgloss-v2-sizing.md) — the other
  measurement trap in this codebase.
