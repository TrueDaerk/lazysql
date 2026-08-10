---
type: Design Decision
title: Mouse support as a shortcut layer over the existing keys
description: Cell-motion tracking set on the tea.View; hit-testing recomputed from the layout numbers rather than cached rects; the wheel aimed by the pointer and coalesced into one scroll per frame; every gesture an alias for a binding that already exists.
tags: [ui, bubbletea, mouse, input, layout]
generated:
  by: claude-code/opus-5
  at: 2026-08-10T00:00:00Z
sources:
  - resource: https://github.com/TrueDaerk/lazysql/issues/77
    note: issue — add basic mouse support (click to focus/select, wheel scrolling)
  - resource: https://github.com/TrueDaerk/lazysql/issues/78
    note: related — input backlog while scrolling, which is why the wheel is coalesced
---

# Mouse support as a shortcut layer over the existing keys

lazysql was keyboard-only. It now handles clicks and the wheel
(`internal/ui/mouse.go`), under one rule: **every mouse gesture is an
alias for a key that already exists**. Nothing is mouse-only, the options
bar and `?` are unchanged, and a session driven entirely from the
keyboard behaves exactly as it did.

## Why cell-motion tracking, not all-motion

Tracking is turned on per view, not per program: `View()` sets
`v.MouseMode = tea.MouseModeCellMotion` next to `AltScreen` — Bubble Tea
v2 carries the mode on the `tea.View` and the renderer emits the
enable/disable sequences when it changes. Cell motion delivers clicks,
releases and the wheel. All-motion would additionally deliver a message
for every cell the pointer crosses, which is pure queue pressure for a UI
that has no hover state, no drag and no resize handles. Out of scope by
the same argument: text selection in the editor, drag-and-drop, and
dragging panel borders.

### The cost this imposes

Asking the terminal for mouse events turns off its own wheel handling —
the "alternate scroll mode" that maps the wheel to arrow keys in the
alternate screen — and takes over click-drag text selection. So wheel
handling had to be *complete* before this could merge, not partial:
anything reachable by `j`/`k` that the wheel did not cover would have
been a regression, not a missing feature. Selecting text with the mouse
now needs the terminal's override modifier (`shift`, or `option` in
iTerm2 and Terminal.app); `y`'s copy menu is the in-app answer and needs
no modifier.

## Hit-testing recomputes the layout; it does not cache rects

Panels do not record their screen rects. `Model.hitTest` recomputes the
geometry from the same inputs `View` lays the body out with —
`m.width`/`m.height`, `m.screen`, `m.focus`, then `sideWidth()`,
`panelHeights()` and `commandLogHeight()`. A rect cache would be a second
source of truth for the layout, and it would be written by the renderer
and read by the input path — precisely the pairing that goes stale when a
`WindowSizeMsg` lands between a render and the click that follows it. The
whole computation is a handful of integer divisions, so there is nothing
to gain by storing it.

`boxHit` then resolves a cell inside a bordered box into content
coordinates, with two special cases that fall out of the rendering:

- The **top border line is the title**, because `renderTitledBox` splices
  it there instead of spending a content row on it (see
  [design/border-embedded-panel-titles](border-embedded-panel-titles.md)).
  A hit on it carries `title: true` and a column measured from where the
  title text starts — one corner rune plus `titleBorderPad` fill runes in
  from the box edge.
- The inline `/` filter prompt takes the first content row when it is
  showing, so `clickSide` subtracts it before mapping a row onto
  `sidePanel.offset + row`.

Sub-tab hit-testing works the same way: `sidePanel.tabHit` and
`mainTabHit` walk the label widths in the order `titleLine` and
`mainTabBar` write them (`‹Tables|Views›`), which is cheap enough that
there was no reason to leave it out.

## Second click, not double-click

Clicking a row moves the cursor onto it. Clicking the row the cursor is
*already* on, in the *already focused* panel, is `enter`.

Double-click was the alternative and was rejected: it needs a timing
threshold, which means a click can mean different things depending on how
fast the user's hand is, and it makes the first-click-focuses case
ambiguous. "Click again on what is selected" is the same gesture without
a clock — a slow, deliberate second click means exactly what a fast one
does — and it composes with the focus rule: a click into an unfocused
panel focuses and selects, so the drill-in always takes two clicks from
anywhere.

The main view follows the same shape: the first click focuses it, and
only once it has the focus does a click pick a cell (row *and* column,
mapped through the same `columnWindow`/`rowWindow` the grid renders
with). With nothing open the main view refuses the focus entirely, rather
than replacing the focused panel's summary with "no relation open".

## The wheel is aimed by the pointer; a click is aimed by focus

A click is "global" in the routing order — it may move the focus. The
wheel deliberately is not: it scrolls the panel **under the pointer**,
focused or not, because hovering is the only aiming a wheel has and
requiring a click first would make the wheel useless for glancing at a
neighbouring list. `sidePanel.move` and the grid's row cursor both work
without focus, and their scroll windows are derived from the cursor at
render time, so this needed no new state.

Two consequences worth naming:

- **The grid and the editor have no scroll offset of their own.** The
  grid's row window comes from `rowWindow(…, d.row, …)` and the editor's
  from the caret (`renderEditor`). "Scrolling" them therefore *is*
  moving the cursor/caret, which is also what `j`/`k` do there.
- **The wheel never issues a round trip.** In the grid it clamps at the
  page boundary instead of turning the page: a page turn is a `SELECT`,
  and a burst of wheel notches must not become a burst of queries.
  `ctrl+f`/`ctrl+b` still page.

The main view stacks blocks (the editor over its own result), so the
scroll target carries the content row: below the buffer and its hint line
the wheel belongs to the grid underneath. Panel `[4]`'s side box is inert
— it previews the buffer from line 1 and has no window to move.

## Wheel coalescing

A modal swallows every mouse event, exactly as it swallows every key, so
the wheel can never scroll the view behind an open popup. A popup with a
scroll offset opts in through `wheelHandler` — the optional half of the
modal contract, mirroring `pasteHandler` (see
[design/clipboard-strategy](clipboard-strategy.md)). A popup without one
still swallows the event.

Wheel events are **coalesced, never applied one-for-one** (issue #78):

1. The first event of a burst is applied immediately, so the scroll
   starts without a frame of delay, and arms a `tea.Tick` flush.
2. Every event that arrives before the tick only adds to `pending` — a
   single integer add, no scroll, no re-render.
3. The tick applies the sum and re-arms while more has queued.

Per-event work is therefore O(1) however fast the wheel turns, and the
scroll stops within one frame (16ms) of the hand stopping instead of
draining a queue of scrolls afterwards. A `gen` counter invalidates a
tick that outlived its burst, the same trick `queryRun.id` uses to drop a
cancelled run's late reply, and retargeting mid-burst flushes what is
queued to the panel it was aimed at rather than to the new one. Targets
with nothing to scroll are dropped before the coalescer sees them, so
they do not even cost a tick.

## Where it sits in the update routing order

`Update` gained one case, keeping the documented order (see
[design/tui-shell-architecture](tui-shell-architecture.md)):

    WindowSizeMsg → modal → global → focused panel

`updateMouse` reproduces it: an open modal first, then a click that moves
the focus (the "global" step), then the view under the pointer. There is
no `/`-filter step and no editor-insert step — a click is not a
character, so a filter being typed neither swallows it nor is disturbed
by it.

## Related

- [design/tui-shell-architecture](tui-shell-architecture.md) — the
  routing order this slots into.
- [design/border-embedded-panel-titles](border-embedded-panel-titles.md) —
  why the top border line is a hit target.
- [design/data-grid](data-grid.md) — the derived row/column windows the
  click and the wheel both walk.
- [design/query-editor-panel](query-editor-panel.md) — why the editor
  lives in the main view, which is where the wheel scrolls it.
- [design/clipboard-strategy](clipboard-strategy.md) — `pasteHandler`,
  the pattern `wheelHandler` copies, and the copy path that replaces
  terminal text selection.
