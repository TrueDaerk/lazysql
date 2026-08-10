---
type: Design Decision
title: TUI shell architecture
description: How the lazygit-style shell is structured — one root model, cursor-over-slice side panels, message-based actions, and a fixed update routing order.
tags: [tui, bubbletea, architecture]
generated:
  by: claude-code/opus-5
  at: 2026-08-09T00:00:00Z
---

# TUI shell architecture

> **Renumbered (issue #79).** The panel set is now `[1] Connections`,
> `[2] Objects` (an expandable tree), `[3] Query`, plus the unnumbered
> `panelMain`; `[2] Databases` and `[3] Tables` were merged — see
> [design/object-tree-panel](object-tree-panel.md). The structure below
> is unchanged; read `[4] Query` as `[3] Query` and `1`–`4` as `1`–`3`.

The shell lives in `internal/ui`; `main.go` only constructs `ui.New()` and runs
the Bubbletea program.

## One root model

`ui.Model` owns terminal size, the focused panel, one `sidePanel` per side
panel, the main view state, the open modal (`nil` = none), the screen mode and
the command log. `[4] Query` is the one numbered panel that is not a
`sidePanel`: it is a `panelID` with a textarea behind it, drawn and keyed by
its own code ([query-editor-panel](query-editor-panel.md)). Panels are not independent `tea.Model`s: they are plain
structs with a cursor, rendered by the root. A full child-model interface buys
nothing while panels hold a list and a cursor, and it would force selection
state to be plumbed back up for the main view on every keystroke.

`sidePanel` uses a cursor over a `[]string` rather than the Bubbles `list`
component. `list` renders filtering, pagination and status chrome that fights
the lazygit look, and its own keymap would become a second source of truth for
keys.

## Update routing order

`Model.Update` routes in exactly this order:

1. `tea.WindowSizeMsg` — store the new size; layout is recomputed in `View`,
   never cached.
2. Domain messages (`commandLogMsg`, `historyEntryMsg`, `focusPanelMsg`).
3. An open modal — it swallows **every** key. Nothing reaches panels beneath.
4. An open `/` filter on the focused panel — every printable key narrows the
   list instead of jumping or quitting.
5. The query editor `[4]` in insert mode — it captures every key except
   `ctrl+r`, `ctrl+c` and `esc`, ahead of the globals so `q` cannot quit in
   the middle of a statement. See
   [query-editor-panel](query-editor-panel.md).
6. Global keys (`1`–`4`, `tab`, `shift+tab`, `+`, `_`, `?`, `q`).
7. The focused panel's navigation and context actions.

Because global keys are matched before panel actions, a panel action must never
bind a key the global layer already claims. `TestPanelActionsDoNotShadowGlobalKeys`
enforces that.

## Modal closing rule

The root captures the modal pointer before dispatching:

```go
cur := m.modal
shouldClose, cmd := cur.update(msg, &m)
if shouldClose && m.modal == cur {
    m.modal = nil
}
```

Without the `m.modal == cur` check, an action that opens a replacement modal
(the actions menu launching a confirm dialog) would have it wiped immediately.

## Actions are messages

Panel behaviour returns `tea.Cmd`s that emit domain messages; the root reduces
them. `logCmd` produces the command-log entry for every statement, which keeps
the command-log invariant from `CLAUDE.md` in one place and gives real driver
work an obvious seam: replace `logCmd` with the command that actually executes
and logs.

See also [keybindings single source](keybindings-single-source.md) and
[lipgloss v2 sizing](../reference/lipgloss-v2-sizing.md).
