---
type: Design Decision
title: Input coalescing and per-event render cost
description: Why held-down navigation keys backlogged (a View is built per queued message; the editor re-tokenized and re-styled the whole buffer each time), and the fix — keyboard Down/Up routed through the wheel's frame coalescer, plus a tokenization/wrap/render cache that makes a pure cursor move O(visible rows).
tags: [ui, bubbletea, input, performance, editor]
generated:
  by: claude-code/fable-5
  at: 2026-08-10T00:00:00Z
sources:
  - resource: https://github.com/TrueDaerk/lazysql/issues/78
    note: issue — scroll input backlog; coalesce navigation events, cheap per-event updates
---

# Input coalescing and per-event render cost

Scrolling lagged behind input and ran on after the hand stopped: holding
`j`/`k` (or a terminal's alternate-scroll arrow emulation) queued events
faster than the app consumed them, and a direction reversal waited behind
the whole backlog (issue #78).

## Where the time actually went

Bubble Tea v2 renders at a frame rate and skips identical frames, but
that only bounds the *flush*: `Program.eventLoop` calls
`p.render(model)` — which builds the full `View()` string — **once per
queued message** (`tea.go`, v2.0.7). So the per-event cost of a
navigation key is `Update` *plus* a complete View build, and a backlog
drains at that combined rate.

Measured per queued event (`internal/ui/perf_test.go`, 160×48, M4 Max):

| where | before | after |
|---|---|---|
| query editor, 400-line script | 29.1 ms | 2.6 ms |
| data grid, full 100-row page | 1.9 ms | 2.0 ms |
| side panel, 5000 rows | 0.8 ms | 0.85 ms |

The editor was the culprit, ~29 ms per event — key repeat arrives at
30–40 Hz, so it was saturated the moment a key was held. One View
tokenized and wrapped the whole buffer several times over
(`editorHeight` → `editorRows`, then `renderEditor`, then `editorCaret`
again for the completion popup), styled **every** display row of the
buffer and only then sliced the visible window, split the buffer again
for panel [4]'s preview (plus two `textarea.Value()` re-joins), and ran
a quadratic `truncate` (pop one rune, re-measure the whole prefix) over
each long preview line.

## Fix 1 — cheap per-event work

`editorCache` (`internal/ui/highlight.go`, hanging off the Model as a
shared pointer) memoizes three layers, each invalidated by the layer
above it:

1. **Tokenization** keyed by (dialect, buffer) — a pure cursor move
   reuses the previous `highlightLines`.
2. **Wrap geometry** keyed by content width — segment ranges and total
   display rows.
3. **Rendered block** keyed by (box, caret, focus/mode) — the exact
   `renderEditor` result, so the accumulate-only messages of a coalesced
   burst (state unchanged) cost a struct compare instead of a render.

`renderEditor` itself now works in two passes: a geometry pass over the
cached segments finds the window and the caret row, and only the rows
inside the window are styled — O(visible rows), where it used to style
the whole buffer. Panel [4]'s preview reads the same cached lines
instead of re-splitting the buffer, and `truncate` walks forward
accumulating rune widths (linear) instead of popping-and-remeasuring
(quadratic).

The grid and the side panels were already window-bounded (`buildGrid` is
capped by the 100-row page, `sidePanel` is a slice window); they needed
no per-event work, only coalescing.

## Fix 2 — coalescing, one mechanism for keys and wheel

The message loop offers no queue inspection, so coalescing happens at
the model level — and the mechanism already existed: the wheel's
`wheelState` (see [mouse-support](mouse-support.md)). Keyboard `Down`/
`Up` now route through the same `wheelAt` entry point instead of moving
cursors directly, with the target derived from the focus (a key has no
pointer to aim with): a side panel targets itself, the grid and the
metadata tabs target `zoneMain`, and the editor's `j`/`k` target the
editor block. `applyScroll` lands each delta exactly where the old
direct handlers did.

Behaviour, identical for keys and wheel notches:

- The **first event** of a burst applies immediately — a single `j`
  moves exactly one row with zero added latency — and arms a 16 ms
  flush tick.
- Events arriving before the tick only **accumulate** into a signed
  pending delta; opposite directions cancel arithmetically, so a
  reversal never waits behind queued same-direction events.
- The **flush** applies the net delta and re-arms while more is queued;
  an empty flush disarms. Stopping therefore stops within one frame.

`scrollEditor` applies a flushed delta with one buffer read and one
write-back (the textarea walk is O(buffer)), not one per line.

Tests: `navcoalesce_test.go` drives `Update` raw — no synchronous
command draining, i.e. a real backlog — for the panel, grid and editor
paths, the reversal net-delta, the disarm, and the render memo's
invalidation on caret moves and edits. The existing suite needed no
changes: the shared `send` helper drains commands synchronously, and
draining `wheelFlushCmd` (a `tea.Tick`) blocks the 16 ms and delivers
the flush — sequential test presses are "slow, deliberate" presses by
construction.
