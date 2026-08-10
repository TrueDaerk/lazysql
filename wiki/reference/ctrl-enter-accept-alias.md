---
type: Reference
title: ctrl+enter / cmd+enter as an accept alias
description: Why `internal/ui/keys.go`'s `acceptKeys` needs no explicit Bubble Tea keyboard-enhancements opt-in, and where the alias is wired in.
tags: [tui, keybindings, bubbletea]
generated:
  by: claude-code/sonnet-5
  at: 2026-08-10T00:00:00Z
sources:
  - resource: charm.land/bubbletea/v2@v2.0.8 cursed_renderer.go (keyboardEnhancementsFlags)
---

# ctrl+enter / cmd+enter as an accept alias

`internal/ui/keys.go` defines `acceptKeys = []string{"ctrl+enter", "super+enter"}`
and folds it into every binding that means "accept this change": `AcceptChanges`
(edit cell modal, insert row form, generic form, confirm modals), `CommitChanges`
(table view, aliasing `c`) and `RunEditor` (aliasing `ctrl+r`, pre-existing).
Every one of those contexts still answers to plain `enter` on its own — the
alias only adds a key, it never replaces one.

## No `tea.WithKeyboardEnhancements()` needed

Bubble Tea v2 (as of v2.0.8) has no such program option. Keyboard enhancements
are requested per-`View` via `tea.View.KeyboardEnhancements`, but the kitty
keyboard protocol's *basic key disambiguation* flag — the one that makes a
terminal report `ctrl+enter` as distinct from plain `enter` in the first
place — is sent unconditionally by the renderer:

```go
// cursed_renderer.go
func keyboardEnhancementsFlags(ke KeyboardEnhancements) int {
	flags := 1 // always enable basic key disambiguation
	...
```

`View.KeyboardEnhancements` only layers on *additional* features
(`ReportEventTypes`, `ReportAlternateKeys`, …) that this app does not need.
So `acceptKeys` works out of the box on any terminal that supports the kitty
protocol (or an equivalent, e.g. iTerm2, Ghostty, WezTerm, Kitty itself) —
nothing in `main.go` had to change.

## The plain-`\r` fallback

Terminals without kitty-protocol support collapse `ctrl+enter` to the same
`\r` byte sequence as plain `enter` — indistinguishable at the application
layer. Every `acceptKeys` binding is additive for exactly this reason: the
terminal that cannot tell them apart still gets `enter`, and the shortcut is
best-effort rather than required. `cmd+enter` on macOS is weaker still — some
terminal emulators never forward it to the running program at all, in which
case it will not exist as `super+enter` or anything else.

See also [Keybindings have one source of truth](keybindings-single-source.md).
