---
type: Design Decision
title: Reorder connections with K/J, swapping whole config entries in place
description: issue #158 — panel [1] gains K (move up) / J (move down), bound to shifted K/J rather than ctrl+up/ctrl+down because plain k/j already navigate every panel and shift+letter was free; Config.MoveUp/MoveDown swap adjacent Connection structs by index and reuse cfg.Save()'s whole-file rewrite, so order = slice order = config.toml array order with no second serializer.
tags: [config, connections, keybindings]
generated:
  by: claude-code/sonnet-5
  at: 2026-08-16T00:00:00Z
---

# Reorder connections with K/J, swapping whole config entries in place

Panel `[1] Connections` had no way to change the order connections appear
in — they always rendered in `config.toml` array order, the order
`Config.Load` happened to read them off disk. Fixed by adding `K` (move up)
/ `J` (move down) to `internal/ui/keys.go`'s `panelConnections` action list,
backed by two new `*config.Config` methods.

## Key choice: shifted K/J, not ctrl+up/ctrl+down

The issue suggested either `K`/`J` or `ctrl+up`/`ctrl+down`. Plain `k`/`j`
are `Up`/`Down` in every panel (see `keyMap.navigation()`), so they were
never in play. Shifted `K`/`J` were checked against
[design/keybindings-single-source](keybindings-single-source.md)'s
`panelActions(panelConnections)` and the global bindings
(`keyMap.global()`): neither is bound there, and the two other places `K`/`J`
mean something in this codebase — `ShiftUp`/`ShiftDown` (multi-row selection
extend) and vim's up/down motions — are both scoped to the data grid /
query editor, a different panel entirely, so there is no cross-panel
collision. `ctrl+up`/`ctrl+down` was passed over: it depends on the kitty
keyboard protocol or `modifyOtherKeys` the same way `ctrl+enter` does (see
`acceptKeys` in `keys.go`), which macOS Terminal.app supports neither, while
shifted letters are reported everywhere.

## Config.MoveUp/MoveDown swap whole structs

`internal/config/config.go` gained:

```go
func (c *Config) MoveUp(name string) bool
func (c *Config) MoveDown(name string) bool
```

Both look the name up via the existing `Index`, then swap two adjacent
elements of `[]Connection` in place — the whole struct, not just the name —
so every field (host, options map, SSH section, color tag…) travels with the
entry and a reorder can never mismatch a connection's name against another's
settings. Both report `false` at the list's edge or for an unknown name,
which `internal/ui` uses to skip the persist round trip entirely when the
move was a no-op (top row `K`, bottom row `J`).

## No second serializer

The hint in the issue was to reuse the existing save path rather than write
a second one. `runAction`'s `actMoveConnUp`/`actMoveConnDown` cases call
`m.cfg.MoveUp`/`MoveDown`, `m.refreshConnections(name)` so the panel cursor
follows the moved row, and then a small `reorderCmd` (`internal/ui/connections.go`,
next to `persistCmd`/`forgetCmd`) that does nothing but `cfg.Save()` — the
same atomic whole-file TOML rewrite `Upsert`/`Remove` already go through.
Since only the slice order changed and no secret needs touching, `reorderCmd`
is simpler than `persistCmd`: no keyring calls, no rename handling.
