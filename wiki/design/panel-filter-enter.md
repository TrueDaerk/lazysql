---
type: Design Decision
title: A navigated panel filter's enter opens the selection directly
description: Issue #193 — enter while the inline `/` filter is active confirms the filter and activates the selection in one keypress once the user has moved the cursor with the arrow keys or the mouse wheel; a still-default selection keeps the old confirm-only behavior.
tags: [tui, panels, filtering, keybindings]
generated:
  by: claude-code/sonnet-5
  at: 2026-08-24T00:00:00Z
---

# Panel filter: navigated enter opens the selection

## Problem

[design/catalog-browsing](catalog-browsing.md) documents the inline `/`
filter: `enter` used to always just leave input mode (`filtering = false`)
and hand the panel's normal keys back, whatever the cursor had done since
the filter opened. Arrow keys and the mouse wheel already moved the
selection while filtering, so a user who typed a pattern and picked a row
still needed a *second* `enter` to open it — the first one was spent
confirming the filter alone.

## Decision

`sidePanel` gained a `navigated bool` field: whether the cursor has moved
off the filter's own default selection since filtering was turned on.
It is set by the two ways a filtered list is navigated —
`updateFilter`'s `KeyUp`/`KeyDown` cases in `model.go`, and the wheel's
`zoneSide` branch of `applyScroll` in `mouse.go`, but only when the
target panel is filtering (a wheel over an idle panel does not set it).
It is cleared whenever a filter starts (`actFilter`) or is dropped
(`clearFilter`, which `esc` and a cleared pattern both call).

`updateFilter`'s `KeyEnter` case now branches on it:

- **Not navigated** (the default selection is still whatever the pattern
  put there): `enter` only leaves input mode, same as before — typing a
  pattern that happens to narrow to one obvious row does not surprise the
  user by jumping straight into it.
- **Navigated**: `enter` leaves input mode *and* calls the same
  `activateSelection` helper the focused panel's own `enter` uses
  (`k.Enter` in `updateFocused`), so the filter is confirmed and the
  selection acted on in the same keypress. `activateSelection` is the
  extracted body of the old inline `enter` handling: panel `[1]`
  connects (routed through `runAction(actConnect)` because connecting can
  open a password prompt, which the plain `tea.Cmd` `drillIn` cannot),
  every other panel drills in.

`esc` is unaffected — `clearFilter` always cancels without touching
`navigated`'s target, so a navigated-then-`esc` opens nothing.

## Scope

Only `[2] Objects` binds the `/` action today (`panelActions` in
`keys.go`), so this is the only panel exercised in practice. The
`navigated` field and the `updateFilter`/`applyScroll` routing are on the
generic `sidePanel`, not special-cased to the object tree, so any future
panel that adds a `/` binding gets the same one-step behavior for free.

## Alternatives rejected

- **Always drill in on first enter while filtering.** Rejected: a
  pattern that already narrows to a single row would then open on the
  very keypress that was meant to just confirm the filter, with no way
  to review the narrowed list first.
- **A separate keybinding to "confirm and open".** Rejected: it adds a
  key the options bar and `?` have to document for one narrow case,
  where reusing `enter`'s existing meaning (act on the current selection)
  is closer to how the rest of the panel already behaves once a filter is
  out of the way.
