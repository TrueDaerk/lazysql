---
type: Design Decision
title: Main-tab navigation keys — < / > over H / L or ctrl+arrows
description: Why < / > became the primary prev/next main-tab bindings (issue #135), the candidates rejected and why, and what stays bound for backward compatibility.
tags: [ui, keybindings, i18n, accessibility, main-view, tabs]
generated:
  by: claude-code/sonnet-5
  at: 2026-08-12T00:00:00Z
---

# Main-tab navigation keys — `<` / `>` over `H` / `L` or ctrl+arrows

[design/main-view-tabs](main-view-tabs.md) cycles the main view's five
tabs on two bindings, originally `[` (prev) and `]` (next). Issue #135
reported that on German (and other ISO) keyboards those are AltGr
chords — AltGr+8 / AltGr+9 — which several terminals deliver as
alt-modified keys or drop entirely, making tab switching a fight with
the layout. [reference/keyboard-layout-portability](../reference/keyboard-layout-portability.md)
had already given `PrevMainTab`/`NextMainTab` a `,` / `.` alias for this
reason (issue #97), but the options bar and `?` still advertised `[` /
`]` as the primary spelling, so the fix was invisible unless a user
already knew to try `,` / `.`.

## Candidates considered

| Candidate | Verdict |
| --- | --- |
| `H` / `L` (lazygit's own tab idiom) | **rejected** — `L` is already the global alias for `CommandLog` (`@`, added in #97). Main-tab actions are dispatched ahead of most panel actions but `CommandLog` is a *global* key, matched even earlier ([design/tui-shell-architecture](tui-shell-architecture.md)'s WindowSizeMsg → modal → global → panel order), so binding `L` here would either shadow the command log or never fire. The issue's own constraint against reusing `j`/`k` for tabs — reserved for list navigation — extends to their shifted forms for the same reason: a single letter should not mean two unrelated things depending on modifier state alone once one spelling is already taken. |
| `ctrl+left` / `ctrl+right` | **rejected as primary** — not reliably delivered by every terminal (needs Kitty keyboard protocol or similar CSI-u support to disambiguate from a plain arrow with a stuck modifier), and adds a chord where a bare key already works. Not added as a secondary alternate either: it would be the fourth spelling for the same action, and every extra spelling is another line the options-bar renderer has to fit ([design/keybindings-single-source](keybindings-single-source.md) budgets that space tightly, see also [design/query-editor-ux-rework](query-editor-ux-rework.md)). |
| `shift+tab` variants | **rejected** — `shift+tab` is already `PrevPanel`, a *global* binding; repurposing it per-panel would be the exact ambiguity the numbered-panel convention exists to avoid. |
| `<` / `>` | **chosen** |

## Why `<` / `>` won

- **No AltGr on the layouts that matter.** On German QWERTZ `<` and `>`
  live on a dedicated key next to left shift (unshifted `<`, shifted
  `>`) — not an AltGr chord at all. On US/UK QWERTY they are
  shift+comma / shift+period. Neither requires AltGr or Option, so both
  satisfy the issue's typeability requirement directly, and
  `TestNoActionNeedsAltGr` (`internal/ui/layout_keys_test.go`) confirms
  neither character is in the AltGr set the guard checks.
- **Same physical keys as the existing `,` / `.` alias.** `<` is
  shift+`,` and `>` is shift+`.` on QWERTY, so a user who already found
  the `,` / `.` alias from #97 lands on immediately adjacent keys, and
  the mnemonic (`<` points left/back, `>` points right/forward) reads
  naturally for prev/next.
- **No collision.** Neither character was bound anywhere else in
  `keyMap` before this change (checked by grep across
  `internal/ui/keys.go`), unlike `H`/`L`.

## What stays bound

`PrevMainTab`/`NextMainTab` now carry three spellings each: `<`/`[`/`,`
and `>`/`]`/`.`. `[` / `]` remain bound (never removed — the issue asks
for them to keep working) purely as a legacy alias for US-layout muscle
memory; `,` / `.` remain bound as the #97 alias. Nothing was removed, so
no existing muscle memory breaks. The options bar and `?` help now
render `</[` and `>/]` — leading with the new primary spelling, same
"lead with the newest addition" convention the `@`/`L` command-log
binding already used it the other way around (there, the AltGr key
leads because it was the original spelling).

The `[keys]` config action names are unchanged (`prev-main-tab`,
`next-main-tab`), because no keyMap field was added or renamed.
