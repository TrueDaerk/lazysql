---
type: Reference
title: Keyboard layout portability (QWERTZ / AZERTY)
description: Which lazysql bindings need AltGr on non-US layouts, the layout-neutral aliases added for them, and the rule for new bindings.
tags: [tui, keybindings, i18n, accessibility]
generated:
  by: claude-code/opus-5
  at: 2026-08-10T00:00:00Z
sources:
  - resource: github.com/charmbracelet/ultraviolet@v0.0.0-20260703014108 key_table.go (nul = ctrl+space)
---

# Keyboard layout portability (QWERTZ / AZERTY)

`internal/ui/keys.go` originally assumed a US QWERTY layout. On German
QWERTZ (and French AZERTY) several of the bound punctuation characters
are AltGr chords. AltGr is Ctrl+Alt at the OS level, so terminals either
deliver those presses as alt-modified keys or swallow them — the app may
never see the intended character at all.

## Audit result

Classified against the German QWERTZ layout:

| Binding | QWERTZ | Verdict |
| --- | --- | --- |
| `[` / `]` prev/next main tab | AltGr+8 / AltGr+9 | risky — superseded as primary, kept as legacy alias (see below) |
| `@` expand command log | AltGr+q | risky — aliased |
| `ctrl+@` autocomplete | ctrl+AltGr+q | untypeable — removed |
| `/` filter, `?` help, `:` editor, `_` prev screen, `$` line end | Shift chords | fine |
| `+` next screen | unshifted key | fine |
| digits, letters, `ctrl+…`, `tab`, arrows | — | fine |
| `y` / `z` bindings | swapped positions | typeable, no change |

Everything else is a letter, a digit, a Shift chord or a named key, all
of which reach the program unchanged on both layouts.

## Aliases

- **`[` / `]` → also `,` / `.`** — those two sit on the same physical
  keys on QWERTY and QWERTZ, and are unbound in the main view. This
  alias was later superseded as the *primary* spelling by `<` / `>`
  (issue #135) — `[` / `]` and `,` / `.` remain bound as legacy
  aliases. See
  [design/main-tab-navigation-keys](../design/main-tab-navigation-keys.md)
  for the candidates considered and why `<` / `>` won over the `H`/`L`
  lazygit idiom (it collides with the `@`/`L` alias immediately below).
  Help renders `</[` and `>/]`.
- **`@` → also `L`** ("log"). `L` is free in every panel and in the
  editor's vim normal mode, and the root's global keys are matched
  before any panel's, so the alias cannot be shadowed. The expanded
  command log closes on `key.Matches(msg, keys.CommandLog)` rather than a
  literal `"@"`, so the alias — and any `[keys]` override — toggles it.
- **`ctrl+@` dropped.** It was bound as the "other spelling" of
  ctrl+space, but ultraviolet's key table decodes the NUL byte to
  `Code: KeySpace, Mod: ModCtrl`, whose `String()` is `ctrl+space` — the
  `ctrl+@` string never matched anything. `ctrl+space` and `tab` still
  open the completion popup.

No existing binding was removed apart from `ctrl+@`; the aliases are
additive, so US muscle memory is untouched. The `[keys]` config action
names are unchanged (`prev-main-tab`, `next-main-tab`, `command-log`),
because no keyMap field was added or renamed — see
[design/keybindings-single-source](../design/keybindings-single-source.md)
and [design/configurable-keys-and-theme](../design/configurable-keys-and-theme.md).

## Rule for new bindings

A new binding may only be an AltGr character on some layout if it also
carries a layout-neutral alias in the *same* `key.Binding`. The
`TestNoActionNeedsAltGr` guard in `internal/ui/layout_keys_test.go`
walks `keyMap.slots()` and fails when every key of an action is one of
`[ ] { } \ @ | ~ €`, so the rule is enforced rather than remembered.
