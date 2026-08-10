---
type: Design Decision
title: Panel titles embedded in the top border line
description: Panel numbers, names, sub-tabs and the loading marker are spliced into the box's top border instead of taking a content row, via one hand-rolled ANSI-aware compositor shared by every box.
tags: [ui, layout, lipgloss, lazygit]
generated:
  by: claude-code/opus-5
  at: 2026-08-10T00:00:00Z
---

# Panel titles embedded in the top border line

lazygit draws a panel's identity inside the box itself:

```
╭─[3]─Local branches - Remotes - ─╮
│ main                            │
╰─────────────────────────────────╯
```

lazysql now does the same (issue #64). Every box — the four side panels,
the main view and the command log strip — names itself in its top border
line rather than spending the first content row on a title:

```
╭─[3] Tables ‹Tables|Views›─────╮
│ users                         │
╰───────────────────────────────╯
```

## renderTitledBox is the one implementation

Lipgloss has no border-title support, so `renderTitledBox` in
`internal/ui/styles.go` does the surgery. It renders the box normally
(`Style.Width(w).Height(h).Render(body)`) and then **rebuilds** the first
line from the style's own `GetBorderStyle()` runes and
`GetBorderTopForeground()`:

```
ink(TopLeft + Top×pad) + title + ink(Top×fill + TopRight)
```

Rebuilding beats splicing into the already-rendered top line: the rendered
line is one styled run of border runes, so cutting a window out of it means
finding cell boundaries inside escape sequences. Rebuilding from the border
values reduces that to arithmetic, and the width invariant becomes exact —
the top line is always `lipgloss.Width(...) == w`, which is what keeps
`JoinHorizontal` from tearing the layout apart.

Two details it must not get wrong:

- **The title is pre-styled and multi-coloured.** `[3]` and the name carry
  the focus colour, the sub-tabs carry three different ones, the loading
  marker is yellow. So truncation is `ansi.Truncate` from
  `github.com/charmbracelet/x/ansi` (already an indirect dep via lipgloss,
  now direct) — `len()` or a rune slice would cut an escape sequence in
  half and bleed colour into the border and the rows below.
- **The fill after the title must be border-coloured again.** The title
  segment and the border segments are rendered by separate styles, so each
  emits its own reset; nothing the title set survives into the corner.

The title's own colour is the caller's business: focused panels pass a
green-bold title into a green border, blurred panels a muted title into a
muted border. Border colour and title colour are deliberately independent,
which is also what lets the main view keep the
[connection colour tag](connection-color-tags.md) on the top border
segment while the title stays focus-coloured.

When the box is too narrow to hold corners plus one padding rune on each
side, `renderTitledBox` returns the untitled box: a clean border with no
name beats a broken one. The tiny-terminal guard still fires before that
for the terminal as a whole.

## Titles moved out of every content renderer

`sidePanel.titleLine` still builds the panel header, but nothing writes it
as a row: `render()` now starts at `rows := h` and every side panel body is
one row taller than it was. The same split happened to each main-view
renderer, so the main view has a `mainTitle(w)` that shadows the switch in
`mainContent`:

| main view state          | border title            | body            |
| ------------------------ | ----------------------- | --------------- |
| relation open            | `mainTabBar` (sub-tabs) | `dataBody`      |
| metadata tab             | `mainTabBar`            | `metaContent`   |
| query editor             | `queryTitle`            | `queryContent`  |
| plan open                | `planTitle`             | `planContent`   |
| schema diff open         | `diffTitle`             | `diffContent`   |
| connection selected      | `Connections — main view` | `connectionDetail` |

`dataContent` survives as tab bar + `dataBody` for the one **nested** case:
the result grid under the editor in the query view, which sits inside a box
whose border title already belongs to the editor. That nesting is why the
tab bar could not simply be deleted from the grid renderer.

Everything that budgeted for a title row lost its `- 1`: `panelHeights`'
`collapsed = 3` now means border + one usable row (it used to mean border +
title + border, i.e. nothing), `dataBody` takes `h - 4` instead of `h - 5`,
`diffContent` `h - 1`, `planContent` `h - 2`, and `completionLayer` no
longer offsets the caret by a header line — the editor is the first content
row of the main box now, so a popup anchored one line too low would have
covered the caret it belongs to.

## Related

- [design/tui-shell-architecture](tui-shell-architecture.md) — the panel
  and focus model these boxes render.
- [reference/lipgloss-v2-sizing](../reference/lipgloss-v2-sizing.md) —
  why `Width`/`Height` here are total block size, borders included.
- [design/main-view-tabs](main-view-tabs.md) — the tab bar that became the
  main view's border title.
