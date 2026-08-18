---
type: Design Decision
title: Disconnect the active connection from panel [1] with x, reusing closeSessionCmd
description: issue #172 — panel [1] gains x (disconnect), scoped to the currently active row via a c.Name != m.active guard rather than a confirm modal; the runAction case swaps m.driver/m.tunnel/m.active to nil/nil/"" before closeSessionCmd tears the driver and any SSH tunnel down off the update loop, and reuses resetBrowse (already used by quit and drop-connection) to put the [2] Objects tree and main view back into their no-connection state.
tags: [connections, keybindings]
generated:
  by: claude-code/sonnet-5
  at: 2026-08-17T00:00:00Z
---

# Disconnect the active connection from panel [1] with x, reusing closeSessionCmd

Panel `[1] Connections` could open a session (`enter`) but not close one
short of connecting elsewhere or quitting. Fixed by adding `x` to the
panel's `key.Binding` slice (`internal/ui/keys.go`, `panelActions(panelConnections)`),
dispatched through the existing `actionID` → `runAction` path like every
other panel action.

## Key choice: x, not d

`d` already means "remove connection" (drop the config entry). The issue
suggested `d`/`x` as candidates; `x` was free in this panel's keymap (it
only means row-detail inside the data grid, a different panel) and reads
as "close" without colliding with delete.

## Scoped to the active row, silently otherwise

`actDisconnect` compares the selected row's name against `m.active`. A
non-active selection just logs a line to the command log — no confirm
modal, unlike `actDropConnection`'s destructive-delete guard — because
closing a connection loses nothing persistent, it only ends a session that
`enter` can reopen in one keystroke.

## Reuses closeSessionCmd and resetBrowse, not new teardown code

The handler mirrors `actDropConnection`'s existing close-on-delete branch
(`internal/ui/model.go`): it captures the live `driver`/`tunnel`, nils out
`m.driver`/`m.tunnel`/`m.active` synchronously, then returns
`closeSessionCmd(driver, tunnel)` as a `tea.Cmd` — the driver `Close()` and
any [SSH tunnel](ssh-tunnels.md) teardown both run off the update loop, in
that order (driver's handles gone before the transport under it closes).
`m.resetBrowse()` — the same helper `quit()` and the drop-connection flow
already call — clears the staged changeset, any running export/backup,
the plan, activity view, and rebuilds `[2] Objects` to an empty tree, so
disconnect and delete-while-connected leave the UI in the identical
no-connection shape. No new teardown path was written.
