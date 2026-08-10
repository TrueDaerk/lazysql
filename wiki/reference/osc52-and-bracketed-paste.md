---
type: Reference
title: OSC 52 and bracketed paste across terminals
description: What lazysql actually emits for an OSC 52 copy, which terminals and multiplexers accept it (and which need configuration), why there is a size limit, and what Bubble Tea v2 does about bracketed paste without being asked.
tags: [terminal, osc52, clipboard, bracketed-paste, tmux, bubbletea]
generated:
  by: claude-code/opus-5
  at: 2026-08-10T00:00:00Z
sources:
  - resource: https://invisible-island.net/xterm/ctlseqs/ctlseqs.html
    note: OSC 52 (manipulate selection data), DECSET 2004 (bracketed paste)
  - resource: https://man.openbsd.org/tmux#set-clipboard
    note: tmux set-clipboard on/external/off semantics
  - resource: https://github.com/TrueDaerk/lazysql/issues/76
    note: PTY runs that produced the "verified here" rows below
---

# OSC 52 and bracketed paste across terminals

Background for [design/clipboard-strategy](../design/clipboard-strategy.md).

## What lazysql emits

Captured from a real PTY run of the built binary (`y` → `c` with
`PATH=` so the native clipboard fails):

```
ESC ] 52 ; c ; MQ== BEL
```

- Selection `c` is the system clipboard. Bubble Tea's
  `tea.SetClipboard` uses it; `tea.SetPrimaryClipboard` (selection `p`,
  X11/Wayland only) is not used.
- The payload is standard base64 of the raw text.
- The terminator is BEL (`0x07`), not ST. Terminals accept either.
- **No multiplexer wrapping.** Bubble Tea emits the bare sequence; it
  does not wrap it in tmux's `ESC P tmux; …` passthrough. This is
  correct for tmux's defaults (below) and is why lazysql adds no
  wrapping of its own.

## Who accepts it

Verified here: only that lazysql emits the sequence and that the
terminal is not corrupted by it. Whether a given terminal *acts* on it
is from that terminal's own documentation — OSC 52 has no reply, so
there is nothing to observe from inside the program.

| terminal | OSC 52 clipboard write |
| --- | --- |
| kitty, WezTerm, foot, Windows Terminal | supported, on by default |
| Alacritty | supported; governed by its `osc52` setting, which permits copy by default |
| iTerm2 | supported but **off by default** — "Applications in terminal may access clipboard" must be enabled |
| xterm | supported but restricted by the window-operation allowances; needs explicit configuration |
| macOS Terminal.app | not supported — a copy there falls through to the temp-file spill |

Reads (`ESC ] 52 ; c ; ? BEL`) are disabled far more widely than writes,
because they let anything with tty access exfiltrate the clipboard.
lazysql never issues one.

## Multiplexers

- **tmux** — `set-clipboard` decides. Default `external`: tmux forwards
  the application's sequence to the outer terminal but refuses to let
  it create a tmux buffer. `on` does both. `off` drops it, and a copy
  from inside tmux then reaches nothing. No passthrough wrapping is
  needed for the forwarding cases, which is why lazysql emits the bare
  sequence.
- **GNU screen** — support is version-dependent and was not tested for
  this change. If `y` reports an OSC 52 copy under screen and the
  clipboard stays empty, `LAZYSQL_NO_OSC52=1` forces the temp-file
  spill, whose path the log names.

## Why there is a size limit

A terminal caps how long an escape sequence it will buffer, and one
past the cap is discarded whole rather than truncated — silently, since
nothing answers an OSC 52 write. The cap is not discoverable and
differs per terminal (xterm's is a build-time constant; tmux's is a
buffer size). lazysql therefore refuses to send more than
`osc52Limit` = 128 KiB and spills to a file instead, so a large copy
fails visibly with a path rather than invisibly with an unchanged
clipboard.

## Bracketed paste in Bubble Tea v2

- The mode (`ESC [ ? 2004 h`) is enabled by the renderer for every view
  whose `View.DisableBracketedPasteMode` is false, which is the zero
  value. **No program option, no capability request, nothing to opt
  into** — a v2 app has bracketed paste on unless it turns it off.
  Confirmed from the emitted stream: `ESC[?2004h` on startup,
  `ESC[?2004l` on teardown.
- The bracketed block arrives as one `tea.PasteMsg{Content}`, with the
  content verbatim — newlines included, no per-character key events.
  `PasteStartMsg`/`PasteEndMsg` exist for apps that want the brackets
  themselves; lazysql does not.
- The v2 components already handle the message, so forwarding is enough:
  `textarea` inserts it at the cursor with newlines becoming rows, and
  `textinput` sanitizes tabs and newlines to spaces first (its field is
  one line). Neither does anything while blurred — `Update` returns
  early — which is why the query editor's normal mode focuses the
  textarea for the insertion.
