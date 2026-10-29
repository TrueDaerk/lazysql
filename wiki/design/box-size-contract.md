---
type: Design Decision
title: Every box is exactly the size it was allocated
description: Issue #246's rendering rule — panel content is fitted to its box in one place (fitBlock, called by renderTitledBox), the options bar and the full frame go through the same helper, the command log strip folds each entry onto one row, and the expanded log scrolls by display line.
tags: [tui, layout, rendering, lipgloss, command-log]
generated:
  by: claude-code/opus-5.5
  at: 2026-10-29T00:00:00Z
---

# Box size contract

## Rule

A box handed `w × h` renders exactly `w × h` cells — `lipgloss.Width` of
every row is `w`, `lipgloss.Height` is `h` — whatever its renderer
produced. Content wider than the box is cut (ANSI-aware, grapheme- and
wide-rune-aware, with `…`), content taller is clipped from the bottom,
content shorter is padded with spaces.

The rule is enforced in **one place**: `fitBlock(block, w, h)` in
`internal/ui/styles.go`. `renderTitledBox` fits the body to the border's
content area with it before rendering, so every side panel, the main view
and the command log strip get the contract without any renderer having to
get truncation right. Renderers still cut their own lines where they want a
specific shape (a scroll window, a pinned status line, newest-rows-last),
but forgetting to no longer breaks the layout.

The same helper caps the two other pieces of the frame:

- **Options bar** — `fitBlock(bar, m.width, 1)`.
- **Full frame** — `View` runs `fitBlock(full, m.width, m.height)` after
  compositing. `lipgloss.Compositor.Render` sizes its canvas to the union of
  its layers, so a modal or popup reaching past the screen edge would
  otherwise grow the frame by however far it overhangs.

## Why lipgloss alone does not keep a box in its rectangle

- `Style.Width(n)` **wraps** an overlong line onto new rows rather than
  cutting it — one long SQL statement in the log strip became three rows
  and pushed the options bar off screen.
- `Style.Height(n)` is a minimum: it pads short content but never drops
  rows.
- A tab and a carriage return both measure 0 cells in `ansi.StringWidth`,
  but lipgloss renders a tab as 4 spaces and a terminal moves the cursor on
  `\r`. A line containing them passes a width check and still overflows.
  `fitBlock` expands tabs to 4 spaces and drops `\r` before measuring.

`MaxWidth`/`MaxHeight` cap a block but neither pad it nor mark the cut, so
on their own they give "at most w × h", not "exactly".

## Command log

- **Strip** — each entry is exactly one row: the entry's text is folded
  onto one line (`oneLine` collapses every whitespace run, newlines
  included, into a single space) and cut to the box. Before, a multi-line
  statement took one row per line and the newest-rows clip let one long
  statement crowd every other entry out of a 3-row strip.
- **Expanded view (`@`)** — keeps the statement's own line breaks, so it
  scrolls by *display line*: entries are expanded to lines before the row
  budget is applied. Counting a multi-line entry as one row made the modal
  taller than the terminal. The "jump to the newest line" offset is a
  sentinel (`logTail`) that the view clamps, because the entry count is no
  longer the line count.

## Layout arithmetic

`panelHeights` sums to the body height in every screen mode,
`commandLogHeight` leaves the main box at least 5 rows, and `sideWidth`
leaves the main column at least 30 cells — at the 60×18 tiny-terminal
guard and above. `TestLayoutPiecesSumToTheTerminal` checks each piece
before the frame's final clip, so a layout bug is not hidden by it;
`TestViewIsExactlyTheTerminal` checks the whole frame across sizes, screen
modes, focus, the collapsed log and open modals.

Related: [border-embedded-panel-titles](border-embedded-panel-titles.md)
(the box helper itself), [command-log-panel](command-log-panel.md),
[../reference/lipgloss-v2-sizing](../reference/lipgloss-v2-sizing.md).
