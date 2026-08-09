---
type: Reference
title: Lipgloss v2 Width/Height include the border
description: In lipgloss v2.0.5 Style.Width/Height set the total block size (frame included) and do not pad past the content, which breaks layout math written for v1 semantics.
tags: [lipgloss, layout, gotcha]
sources:
  - resource: https://pkg.go.dev/charm.land/lipgloss/v2
    note: Behaviour confirmed empirically against charm.land/lipgloss/v2 v2.0.5.
generated:
  by: claude-code/opus-5
  at: 2026-08-09T00:00:00Z
---

# Lipgloss v2 Width/Height include the border

Guidance written for lipgloss v1 (including the Bubbletea v2 skill reference)
says `Style.Width`/`Height` are *content* dimensions, so a bordered box occupies
`+2` in each direction and layout code should pass `w-2`, `h-2`.

That is wrong for `charm.land/lipgloss/v2` v2.0.5. Measured with
`lipgloss.Width`/`lipgloss.Height` on the rendered block:

| Call | Rendered block |
| --- | --- |
| `border.Width(38).Height(6).Render(body)` | 38 × 7 |
| `border.Width(40).Height(8).Render(body)` | 40 × 8 |

Two consequences:

1. **Width/Height are totals.** Pass the full box size; size the *content*
   string to `w-2` / `h-2` yourself.
2. **Height is a minimum that content can exceed but padding won't reach.**
   A short body inside `Height(8)` came back 7 rows tall in the first row of
   the table above — the block only reached its requested height once the
   requested height matched the frame plus content. Never assume a block is
   exactly as tall as requested; measure when stacking columns.

The symptom of getting this wrong is subtle: the app renders fine, but the side
column ends several rows above the main column, and the mismatch grows with the
number of stacked panels.

`internal/ui/view.go` therefore passes the full `w`/`h` to the border style, and
`TestRenderedBlocksMatchRequestedSize` asserts that each panel block measures
exactly the requested size and that the side column sums to the body height.
