---
type: Design Decision
title: Vim-like modal editing in the query editor
description: Why the vim layer is a hand-rolled pure buffer engine (vim.go) over the Bubbles v2 textarea instead of adopting vimtea, the minimum key set and its deliberate omissions (no visual mode, no counts, no undo), how two-key chords (dd/yy/gg) are held as pending state and reset, which panel keys the vim layer displaced (`a` actions menu, the h/l/y/d fall-through to the data grid), and how the cursor round-trips through Line()/Column()/SetCursorColumn.
tags: [tui, query, vim, modes, keybindings, textarea]
generated:
  by: claude-code/fable-5
  at: 2026-08-09T00:00:00Z
sources:
  - resource: https://github.com/TrueDaerk/lazysql/issues/33
    title: "Issue #33 — Vim-like modal editing in the query editor"
  - resource: https://github.com/kujtimiihoxha/vimtea
    title: vimtea — the community vim editor component that was evaluated and rejected
---

# Vim-like modal editing in the query editor

## Decision

Panel `[5]`'s normal mode speaks a small vim dialect, implemented as a
hand-rolled layer in `internal/ui/vim.go` rather than an adopted
package. The layer is a pure type — `vimBuffer` holds lines and a
cursor, `vimRegister` the single yank register — with one method per
motion or edit and no dependency on the textarea, tea, or the Model.
`updateQuery` reads the textarea into a `vimBuffer` (via `Line()` /
`Column()`), applies exactly one command, and writes the result back
(`SetValue` when the text changed, then `MoveToBegin` + `CursorDown`×row
+ `SetCursorColumn` — the v2 textarea has no "set line" call).

The key set is the issue's minimum: `h j k l` (`j`/`k` reuse the
existing `Up`/`Down` bindings), `w`/`b`, `0`/`$`, `gg`/`G`, `i a o O`
into insert mode with vim's cursor placement, `x`, `dd`, `yy`, `p`,
`esc` back out of the panel. Insert mode is unchanged: the plain
textarea plus the completion popup, `esc` returning to normal mode.
`ctrl+r` runs from both modes.

## Why not vimtea

The one maintained community option,
[vimtea](https://github.com/kujtimiihoxha/vimtea), was evaluated and
rejected on three grounds:

- **Bubbletea v1.** Its go.mod pins `charmbracelet/bubbletea v1.3.4`
  and `bubbles v0.20.0`; this repo is `charm.land/*/v2`, whose message
  and API surface differ. Bridging the two would be its own project.
- **It is a whole editor, not a layer.** vimtea owns rendering
  (chroma highlighting), the clipboard, and its own buffer. lazysql's
  editor already draws itself — `highlight.go` renders the buffer,
  gutter and cursor precisely because the textarea and the highlighter
  must not both wrap — and has its own completion popup anchored on the
  caret. Adopting vimtea would mean discarding or fighting all of that.
- **Scope.** It ships visual mode, command mode and registers; the
  issue wants a deliberate minimum.

A thin translation layer over the existing textarea keeps the
highlighting, completion and run pipeline untouched and is testable as
pure functions.

## Shape of the layer

- **Pending chords, not a timeout.** `dd`, `yy`, `gg` hold their first
  key in `queryEditor.pending`. The next key either completes the chord
  or clears it and acts as itself (vim's own behavior for an aborted
  operator). There is no timer — the issue allowed either, and explicit
  reset avoids a `tea.Tick` raciness for no user-visible gain. Because
  the global key layer runs *before* the panel (digits still jump
  panels mid-chord), `setFocus` also clears `pending`: a `d` typed
  before a panel detour must not pair with a `d` typed after it.
- **The wanted column.** Vertical motion keeps vim's "desired column":
  `j` over a short or empty line and onward lands back on the column
  the cursor came from. The buffer is rebuilt from the textarea on
  every keypress, so the want survives in `queryEditor.want` (−1 =
  none) and is reset whenever insert mode is entered or left.
- **Normal-mode clamp.** Normal mode sits *on* the last character
  (`col ≤ len−1`), insert mode may sit past it. `setEditing(false)`
  re-clamps through the vim layer, so leaving insert at a line end
  pulls the cursor back one column, as vim does.
- **One register.** `x`, `dd`, `yy` fill `queryEditor.register`; the
  `line` flag decides whether `p` pastes a new line below the cursor or
  characters after it. No named registers, no system clipboard tie-in
  (the `y` copy menu on the data grid already owns that, and text comes
  *in* through the terminal's own bracketed paste — see
  [design/clipboard-strategy](clipboard-strategy.md)).

## What was displaced

- **`a` is append, not the actions menu.** The actions menu keys
  (`i`/enter, `ctrl+r`, `D`) are all still directly bound, so panel [5]
  lost only the menu itself. Every other panel keeps `a`.
- **Fewer keys fall through to the data grid.** Normal mode used to
  pass anything unclaimed to the grid when the main view showed a query
  result. `h`/`l` (columns), `y` (copy menu), `d`, `x`, `p`, `w`, `b`,
  `0`, `G`, `g`, `o`, `O` are now vim's. Paging (`ctrl+f`/`ctrl+b`),
  `v` (view cell), `[`/`]` (tabs) and `R` still fall through; for the
  displaced ones the grid is one `tab` away. Staged-edit keys were
  never usable on a query result anyway.

## Omissions, deliberately

- **No undo.** The v2 textarea keeps no edit history and exposes no
  undo API; building a history layer means shadowing every insert-mode
  keystroke, which is a feature of its own. The issue explicitly allows
  omitting `u` if documented — this is that documentation.
- No visual mode, no counts (`3dd`), no `:` commands, no registers —
  per the issue's scope discipline.

## Testing

`vim_test.go` covers the pure engine (motion clamps, want-column
restore, word motion across punctuation/lines/empty lines, x/dd/yy/p
edge cases including the only-line delete) and the mode layer through
`updateQuery` (normal-mode typing never inserts, chord cancel and
cross-panel reset, insert round-trips, `a`/`o`/`O` placement, `?`
completeness). The engine tests need no textarea at all — that is the
point of the pure type.
