---
type: Design Decision
title: Clipboard strategy — native, OSC 52, spill; and bracketed paste
description: Why a copy tries pbcopy/xclip first, OSC 52 second and a temp file last, why the escape sequence has to travel back through the update loop as copiedMsg.osc52, and why a bracketed paste is routed as its own message instead of as the keys it is made of.
tags: [tui, clipboard, osc52, paste, bubbletea, ssh]
generated:
  by: claude-code/opus-5
  at: 2026-08-10T00:00:00Z
sources:
  - resource: https://github.com/TrueDaerk/lazysql/issues/76
    note: copy/paste reported broken; OSC 52 fallback and bracketed-paste handling
  - resource: https://invisible-island.net/xterm/ctlseqs/ctlseqs.html
    note: OSC 52 (manipulate selection data) and DECSET 2004 (bracketed paste)
---

# Clipboard strategy

Copy and paste are two separate problems that only look like one. Copy
is lazysql asking the outside world to hold some text; paste is the
terminal handing lazysql text it already holds. They fail for unrelated
reasons and are fixed in unrelated places.

## Decision — copy tries three mechanisms in order

`copyOut` (`internal/ui/clipboard.go`) is every copy in the app. It
tries, in order:

1. **The native clipboard** — `github.com/atotto/clipboard`, which
   shells out to `pbcopy` (macOS) or `xclip`/`xsel` (X11). It is first
   because it is the only mechanism that *answers*: a returned error
   means the text is definitely not on the clipboard. It is also the
   only one that works when lazysql's terminal is not the terminal the
   user is looking at — a detached tmux pane, say.
2. **OSC 52** — `ESC ] 52 ; c ; <base64> BEL`, the escape sequence a
   terminal turns into a clipboard write itself. This is the path that
   makes `y` work over SSH and inside containers, where there is no
   `pbcopy` to shell out to and no display to talk to: the sequence
   travels back over the same connection the UI is drawn on, and the
   *local* terminal does the writing.
3. **The temp-file spill** — `spillFile`, unchanged. For a session with
   neither: a bare tty, `TERM=dumb`, output redirected somewhere that
   is not a screen. The log names the path.

Native before OSC 52, not the other way round, because OSC 52 is
unverifiable (see below) and because on a local macOS session `pbcopy`
is both available and exactly what the user means by "the clipboard".
iTerm2 also refuses OSC 52 writes by default, so a terminal-first order
would silently degrade the common case.

### The escape sequence cannot be written where the copy happens

A copy runs in a `tea.Cmd`, off the update loop, because the clipboard
library shells out. But an escape sequence has to leave through the
same tty Bubble Tea is drawing on — a stray write from another
goroutine lands in the middle of a frame.

So `copyOut` does not write it. It returns `copiedMsg{line, osc52}`,
and the root's `copiedMsg` case turns a non-empty `osc52` into
`tea.SetClipboard(text)`, which the runtime executes through its own
output. `copyTextCmd` therefore returns the message copyOut built
rather than wrapping a string, and the whole-table copy path (which
appends its truncation notice) edits `out.line` instead of a local
string.

### An OSC 52 copy is reported as sent, not as confirmed

Nothing answers an OSC 52 write. There is no reply, no error, no way to
ask whether the terminal understood it. The log line says so:

```
-- copy cell orders.id to clipboard via OSC 52 (1 bytes; no local clipboard: exec: "pbcopy": executable file not found in $PATH)
```

It names the mechanism *and* the reason the native path was skipped, so
a user whose terminal drops OSC 52 can tell what happened from the log
instead of from an empty clipboard.

### Two guards on the fallback

- **`osc52Limit` (128 KiB).** Terminals cap the length of an escape
  sequence, and one past the cap is dropped whole, not truncated —
  silently, since nothing answers. A whole-table copy can reach
  megabytes (`copyRowLimit` is 5000 rows), so anything larger skips
  OSC 52 and takes the temp file, where the log gives a path that can
  actually be opened.
- **`osc52Available`** (`detectOSC52`) requires a real terminal on
  stdout (`ModeCharDevice`), a `TERM` that is neither empty nor `dumb`,
  and no `LAZYSQL_NO_OSC52` in the environment. The environment
  variable is the escape hatch for a terminal that prints the sequence
  instead of acting on it. The check is deliberately coarse — support
  cannot be probed — which is safe because a terminal that does not
  understand OSC 52 swallows it without printing anything.

`osc52Available` is a variable for the same reason `clipboardWrite` and
`spillFile` are: a test decides for itself which mechanisms exist. The
suite additionally sets `LAZYSQL_NO_OSC52=1` in `TestMain`, so a test
run can never write an escape sequence to the developer's terminal even
if a test forgets the seam.

### Reading the clipboard is not attempted

OSC 52 has a read form (`ESC ] 52 ; c ; ? BEL`), and Bubble Tea exposes
it as `tea.ReadClipboard`/`tea.ClipboardMsg`. lazysql does not use it.
Most terminals disable clipboard *reads* by default — it lets any
program that can write to the tty exfiltrate whatever the user last
copied — so a feature built on it would work on almost nobody's
machine. Nothing in lazysql needs it either: pasting is the terminal's
job (below), and vim's `p` pastes the editor's own register.

## Decision — a bracketed paste is one message, routed on its own

The terminal brackets pasted text in `ESC [ 200 ~` … `ESC [ 201 ~`
(DECSET 2004). Bubble Tea v2 turns the mode on for every view whose
`View.DisableBracketedPasteMode` is false — lazysql's is — and delivers
the block as a single `tea.PasteMsg`. **No program option is needed;
the mode is on by default.** The bug was purely that nothing handled
the message.

Without handling, a `tea.PasteMsg` falls off the end of `Update` and the
paste is lost. Handling it as if it were a key would be worse: in the
editor's normal mode a pasted `DROP TABLE dd;` reads as vim commands —
`D` clears the buffer, `dd` deletes a line, `p` pastes the register —
so the paste *edits* the buffer instead of entering it.

`updatePaste` (`internal/ui/paste.go`) therefore mirrors the key
routing, minus the steps that only make sense for a key:

| target | behaviour |
| --- | --- |
| open modal | forwarded if the modal implements `pasteHandler`; ignored otherwise |
| open `/` filter | appended to the pattern, flattened to one line |
| query editor, insert mode | forwarded to the textarea; the completion popup refreshes |
| query editor, normal mode | inserted at the cursor, **mode unchanged** |
| data grid, side panels | dropped in silence |

- **`pasteHandler` is optional, not part of the `modal` interface.** A
  confirm, a menu and the help have nowhere to put text; making them
  implement a no-op method would be ceremony. The popups that do
  implement it — `promptModal`, `formModal` (and `filterModal` through
  it), `editCellModal`, `insertRowModal`, `paramsModal` — hand the
  message straight to the focused `textinput`, which handles
  `tea.PasteMsg` itself and flattens newlines to spaces for its
  one-line field. `editCellModal` and `insertRowModal` additionally
  leave their NULL/DEFAULT mode for the same reason typing does: text
  arriving means the user wants a value.
- **Normal mode inserts and stays in normal mode.** A paste is text
  arriving, not a request to start typing, and switching modes behind
  the user's back would make the next keystroke mean something else
  than they aimed at. The textarea is blurred in normal mode and a
  blurred textarea drops every message, so it is focused for the
  insertion alone; afterwards an unfinished `dd`/`yy` chord is cleared
  (text landed between its two keys) and `applyVim` re-clamps the caret
  off the past-the-end column insertion may have left it on.
- **`p` stays internal.** Vim's `p` pastes `queryEditor.register`, which
  only `x`/`dd`/`yy` fill. It is not tied to the system clipboard: the
  read side of OSC 52 is unavailable (above), and terminal paste is
  already the way system text gets in. The two registers never mix.

## Consequences

- Copying over SSH puts text on the *local* clipboard instead of in a
  temp file on the remote host, which is what a user pressing `y` means.
- A copy still never simply fails: with no terminal and no clipboard the
  spill is unchanged, and the `clipboardWrite`/`spillFile` seams are
  untouched.
- A copy larger than 128 KiB in a headless session goes to a file even
  though a terminal is present. That is deliberate — see the limit
  above — and the log line distinguishes the two cases.
- Pasting works everywhere text is typed, including the query editor in
  either mode, without the user having to know which mode they are in.
- Multi-line paste into a one-line field (a prompt, a form field, the
  `/` filter) collapses to one line rather than losing everything after
  the first newline.
- Terminal-specific behaviour — who honours OSC 52, what tmux and
  `screen` need — is in
  [reference/osc52-and-bracketed-paste](../reference/osc52-and-bracketed-paste.md).

## See also

- [design/copy-and-export](copy-and-export.md) — the copy menu and the
  serializers behind it.
- [design/vim-mode-query-editor](vim-mode-query-editor.md) — the single
  register `p` reads.
- [design/tui-shell-architecture](tui-shell-architecture.md) — the key
  routing order `updatePaste` mirrors.
