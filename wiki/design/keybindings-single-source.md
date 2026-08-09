---
type: Design Decision
title: Keybindings have one source of truth
description: Why key dispatch, the options bar, the actions menu and the `?` help all read the same key.Binding table, and how the actionID indirection makes that possible.
tags: [tui, keybindings, help]
generated:
  by: claude-code/opus-5
  at: 2026-08-09T00:00:00Z
---

# Keybindings have one source of truth

`CLAUDE.md` requires that every key in the options bar is bound and every
binding appears in `?`. Keeping four renderers in sync by hand is the most
common defect in this UI style, so the shell makes it structural.

## The table

`internal/ui/keys.go` holds a single `keyMap`. Per panel, `panelActions(id)`
returns `[]action`, each pairing an `actionID` with the `key.Binding` that
documents it. Everything derives from that one call:

| Consumer | Derived via |
| --- | --- |
| Key dispatch | `panelActions(focus)` loop in `updateFocused` |
| Options bar | `optionsBarBindings(focus)` → `help.ShortHelpView` |
| Actions menu (`a`) | `panelActions(focus)` → `menuEntry` per action |
| `?` help modal | `helpGroups(focus)` → `help.FullHelpView` |

## Why actionID rather than re-dispatching keys

The actions menu needs to run the same behaviour as the key press. Synthesising
a `tea.KeyPressMsg` for the binding's first key would work but re-enters the
routing layer and depends on stringification of special keys like `space`.
Instead each action has an `actionID`, and `Model.runAction(id)` is the one
implementation both paths call.

`TestOptionsBarAndHelpShareOneSource` and `TestEveryDocumentedKeyIsBound` fail
if a binding is documented but unbound, or shown in the bar but missing from
`?`.

See also [TUI shell architecture](tui-shell-architecture.md).
