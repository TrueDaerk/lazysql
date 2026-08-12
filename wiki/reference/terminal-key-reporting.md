---
type: Reference
title: Which terminals report shift+arrows, and the unshifted fallbacks that do not need them
description: Why shift+up/shift+down looked broken although nothing in lazysql was wrong, what a real PTY shows Bubble Tea v2 receiving for CSI 1;2A-style sequences, which macOS terminals emit them, and why every shifted binding carries a `V`/`K`/`J`/`<`/`>` fallback.
tags: [tui, keybindings, bubbletea, terminal, selection, portability]
generated:
  by: claude-code/opus-5
  at: 2026-08-12T00:00:00Z
sources:
  - resource: https://github.com/TrueDaerk/lazysql/issues/134
    note: shift+arrow selection reported as not working; shift+left/right added
  - resource: charm.land/bubbletea/v2@v2.0.8 cursed_renderer.go (keyboardEnhancementsFlags)
  - resource: github.com/charmbracelet/ultraviolet decoder.go (CSI parameter parsing)
---

# Terminal key reporting for the grid's shifted bindings

## The finding: the application layer was never the problem

Issue #134 reported that `shift+up`/`shift+down` (issue #124) did not
work in practice. The suspicion was a missing Bubble Tea opt-in —
`tea.WithKeyboardEnhancements()` or similar. It is not that:

- Bubble Tea v2 has **no such program option**, and it does not need
  one. `keyboardEnhancementsFlags` starts at `flags := 1 // always
  enable basic key disambiguation`, and the renderer writes
  `SetModifyOtherKeys2` plus the kitty keyboard push on every screen
  switch, unconditionally. This is the same conclusion
  [reference/ctrl-enter-accept-alias](ctrl-enter-accept-alias.md)
  reached for `ctrl+enter`.
- Modified arrows do not even need the enhancement. They are ordinary
  xterm CSI sequences with a modifier parameter, and ultraviolet's
  decoder parses them regardless: `\x1b[1;2A` → `shift+up`, `;2B` →
  `shift+down`, `;2C` → `shift+right`, `;2D` → `shift+left`. The rxvt
  spellings (`\x1b[a`…`\x1b[d`) decode to the same four.
- Driven through a real PTY (`TERM=xterm-256color`, no terminal
  answering the capability query, so the key report shows *"keyboard
  enhancements: not supported"*), `lazysql --debug-keys` prints all four
  as `shift+up`/`shift+down`/`shift+left`/`shift+right`, and the full app
  reaches `3 rows selected` / `3 rows × 2 columns selected` in the grid's
  status line from those bytes alone.

So `keyMap.ShiftUp`/`ShiftDown` match whenever the sequence arrives. What
decides whether it arrives is the **terminal emulator**, and nothing the
program does can change that.

## What the terminals do

| Terminal | shift+arrow |
|---|---|
| kitty, WezTerm, Ghostty, iTerm2, Alacritty, foot, xterm | sends `CSI 1;2A`-style sequences — works |
| tmux / screen | forwards them if the outer terminal sends them (`xterm-keys on` for older tmux) |
| **macOS Terminal.app** | **does not send them** — shift+arrow is indistinguishable from a bare arrow, or is swallowed by the app's own bindings |
| VS Code / JetBrains embedded terminals | usually send them, but the editor's own shortcuts can claim the chord first |

Terminal.app is the likely explanation for the original report: it
supports neither the kitty protocol nor `modifyOtherKeys`, so the grid
never sees a shifted key at all. There is no crash and no misread — the
binding simply never matches and plain `up`/`down` keep moving the
cursor.

## The consequence: unshifted fallbacks, always

Because "does this terminal report it" is not answerable in advance,
every shifted grid binding carries an unshifted alias in the *same*
`key.Binding`, so the options bar and `?` document both from one source
([design/keybindings-single-source](../design/keybindings-single-source.md)):

| Gesture | Shifted | Fallback |
|---|---|---|
| Start a selection | `ctrl+v` | `V` (vim visual-line) |
| Extend up / down | `shift+↑` / `shift+↓` | `K` / `J` |
| Extend a column left / right | `shift+←` / `shift+→` | `<` / `>` |

`H`/`L` were not available for the sideways pair — they are the
main-view tab keys — so the angle brackets take the "shifted `,`/`.`"
shape instead. All six are rebindable by name (`select-rows`,
`shift-up`, `shift-down`, `shift-left`, `shift-right`) through the
`[keys]` config section.

## Diagnosing it: `lazysql --debug-keys`

`--debug-keys` runs a one-screen program (`internal/ui/keydebug.go`)
that prints what the terminal actually reported for each key —
`msg.String()`, the key code, the modifier bits and the associated text
— plus whether the terminal answered the keyboard-enhancement query. It
runs on the main screen, so the dump survives in the scrollback and can
be pasted into a bug report. `ctrl+q` quits, because every plainer key
is itself worth reporting.

`scripts/ptycheck.py` drives any command in a real PTY, feeds it escape
sequences and renders the result through `pyte`, which is how the above
was verified without a human at a keyboard:

```sh
printf '\\x1b[1;2A\n' | python3 scripts/ptycheck.py 100 30 lazysql --debug-keys
```

See also
[design/grid-multi-row-selection](../design/grid-multi-row-selection.md)
and
[reference/keyboard-layout-portability](keyboard-layout-portability.md).
