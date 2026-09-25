---
type: Design Decision
title: The command log strip collapses with a key and persists like the screen mode
description: Why the strip under the main view can be hidden with `T`, how the main view reclaims its full height rather than leaving an empty box, and why the choice is stored in config.State next to the screen mode instead of a new file.
tags: [tui, keybindings, command-log, layout, persistence]
generated:
  by: claude-code/sonnet-5
  at: 2026-09-25T00:00:00Z
---

# Collapsible command log strip

## Problem

The always-on strip under the main view (see
[command-log-panel](command-log-panel.md)) takes a fixed share of the
main column's height — `h/4`, clamped to 5–10 rows. On an 80×24
terminal that is 5 of 24 rows spent on a panel the user is usually not
reading, while the data grid is squeezed. `@`/`L` already opens the log
in full as a modal, so the strip is a convenience rather than the only
way to read it (issue #218).

## Decision

`Model.logCollapsed` (a plain `bool`) gates
`commandLogHeight` (`internal/ui/view.go`), which is now a method on
`Model` rather than a free function: collapsed, it returns `0` before
computing anything else. `renderMainColumn` already treated `logH <= 0`
as "no strip" — the main view box was given the column's full height and
`JoinVertical` was skipped — so collapsing needed no new branch there,
only a way to reach `logH == 0` outside of a terminal too short for one.

`T` (a new global binding, `keyMap.ToggleCommandLog`) flips the flag in
`updateGlobal`. It sits next to `CommandLog` (`@`/`L`, which still opens
the expanded modal either way — that handler is untouched) rather than
overloading it, so `?` can name the two actions separately. `T` is a
plain letter with no AltGr concern on QWERTZ/AZERTY (see
[keyboard-layout-portability](../reference/keyboard-layout-portability.md))
and was free in every context before this change, so it needs no
layout-neutral alias.

Every other reader of the strip's height —
`completionLayer`'s `editorAnchor`/`filterAnchor`, `gridViewport`,
`hitMainColumn` (mouse hit-testing) and `editorBlockRows` — already
called `commandLogHeight` rather than recomputing the split by hand, so
turning it into a method that consults `m.logCollapsed` fixed the
completion popup's placement and the grid/editor row budget for free;
none of those call sites needed their own collapsed check.

## Persistence

The screen mode (`config.State.ScreenMode`) was already the pattern for
"a layout choice survives a restart": stored by name, loaded once in
`initialModel`, saved once in `quit`. `LogCollapsed bool` was added next
to it in the same `State` struct rather than a new file — the state file
is documented as exactly this kind of disposable UI state, and one more
field costs nothing a reader of `quit`'s save line does not already see.
