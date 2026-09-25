---
type: Design Decision
title: "? help is pinned in the options bar"
description: Issue #215 — the options bar always keeps `? help` visible by rendering it outside bubbles' help.Model truncation, and Data-context actions now list before tab/column navigation.
tags: [tui, keybindings, help, options-bar]
generated:
  by: claude-code/sonnet-5
  at: 2026-09-25T00:00:00Z
---

# `? help` is pinned in the options bar

`wiki/reference/ux-audit-2026-08.md` flagged P1: the options bar truncates
with `…` well before it reaches `? help`, so a first-time user has no visible
route to the full keymap. Fixed in issue #215.

## Ordering

`keyMap.panelActions(panelMain)` in `internal/ui/keys.go` now lists the keys
that act (`e`, `d`, `n`, `c`, `y`, `/`) before tab/column navigation. This is
the single source both the options bar and `?` read (see
[keybindings-single-source](keybindings-single-source.md)), so reordering it
there reorders both — no separate list to keep in sync.

## Pinning `? help`

`bubbles/help.Model.ShortHelpView` truncates by dropping items off the *end*
of the list once the width runs out. Widening the panel's action list (the
whole point of the reorder above) pushes `? help`, which `optionsBarBindings`
appends near the end, further toward the part that gets dropped — reordering
alone does not fix the bug, it just changes which keys survive.

`renderShortHelpPinned` in `internal/ui/view.go` renders the pinned binding
outside `help.Model` entirely: it splits `? help` out of the binding list,
reserves its rendered width (plus one separator), and truncates the
*remaining* bindings itself before appending `sep + pinnedStr`. `?` still
lists everything `help.Model` would have — nothing is hidden from the full
help modal, only the one-line bar changes.

### The bubbles quirk this ran into

`help.Model.shouldAddItem` only stops adding items once the *ellipsis* also
no longer fits the remaining width; if neither the next item nor `…` fits, it
gives up bounding the output and appends every remaining binding
unconditionally (`help@v2.1.1/help.go`, `shouldAddItem`). Reserving width for
the pinned binding shrinks the budget passed to `ShortHelpView` for the rest
of the list, which made this edge case easy to hit at `minWidth` (60 cols) —
the rendered bar overflowed to ~235 visible columns instead of truncating.

The fix does not special-case the library: `renderShortHelpPinned` calls
`h.ShortHelpView` with `SetWidth(0)` (unlimited — `shouldAddItem` no-ops when
`m.width <= 0`) and then truncates the *result* itself with the same
ansi-safe `truncate()` (`ansi.Truncate(s, w, "…")`) the bar's final
gap-fallback already used, rather than trusting `help.Model` to self-limit at
a tight budget.

## Read-only connections

`writeBindings()` filtering (`optionsBarBindings` in `internal/ui/view.go`)
runs after `renderShortHelpPinned` sees the list, unchanged — a read-only
connection's bar still drops write keys while `?` keeps listing them.

## Test coverage

`TestOptionsBarPinsHelpAtMinWidth` (`internal/ui/model_test.go`) renders the
bar for every panel at `minWidth`×`minHeight` and asserts `? help` (after
`ansi.Strip`) is present.

See also [keybindings-single-source](keybindings-single-source.md) and
[ux-audit-2026-08](../reference/ux-audit-2026-08.md).
