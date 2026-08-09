---
type: Design Decision
title: The query editor as panel [5], and its normal/insert mode split
description: Why the query editor became a permanent numbered panel instead of a modal, how a persistent textarea coexists with single-key global bindings through a normal/insert mode split, where the editor is drawn (side column preview vs. main view), who owns focus after a run, and why unclaimed normal-mode keys fall through to the data grid.
tags: [tui, query, panel, focus, keybindings, textarea, modes]
generated:
  by: claude-code/opus-5
  at: 2026-08-09T00:00:00Z
sources:
  - resource: https://github.com/TrueDaerk/lazysql/issues/28
    title: "Issue #28 — Make query editor a persistent panel [5] instead of a modal"
---

# The query editor as panel [5]

## Decision

The SQL editor is a numbered side panel, `[5] Query`, not a centered
modal. `5` focuses it, `:` focuses it and starts typing, `tab` cycles
through it, and `ctrl+r` runs the buffer **without closing or clearing
it**. There is no `queryModal` any more; the other modals (confirm,
menu, prompt, help, command log) are untouched.

The buffer is the model's only copy of the draft: `Model.draft` is gone
and `Model.editor.area` (a Bubbles v2 `textarea`) took its place, read
through `Model.script()`. Nothing has to be saved out of a popup on
close, because there is no close.

## 1. A persistent textarea needs a mode split

lazygit-style bindings are single keys: `q` quits, `1`–`5` jump, `?`
helps, `/` filters. A textarea that permanently holds focus would eat
every one of them — `q` in the middle of `SELECT * FROM q` cannot quit
the app.

So the editor has two modes, the same shape vim users expect and the one
the follow-up vim-mode work extends:

- **normal** — the panel's keys are lazysql's own. `i`/`enter` starts
  editing, `ctrl+r` runs, `D` clears (with a confirm), `j`/`k` move the
  buffer's cursor between lines, `esc` backs out of the panel. Every
  global key still works.
- **insert** — everything types, except `ctrl+r` (run), `ctrl+space`
  (complete), `ctrl+c` (cancel a running query, or leave insert mode
  when nothing runs) and `esc` (leave insert mode, buffer kept).

With the completion popup open `esc` closes the popup first and insert
mode survives it, so leaving the mode from there takes a second press —
see [schema-aware-autocomplete](schema-aware-autocomplete.md).

`Model.setEditing` keeps the textarea's own focus in step with the mode,
so a blurred buffer cannot swallow a key even if one reached it.

### Where it slots into the update routing

The [routing order](tui-shell-architecture.md) gained one step, ahead of
the global keys and behind the `/` filter:

```
WindowSizeMsg → open modal → open `/` filter → editor in insert mode
              → global keys → focused view
```

Insert mode has to precede the globals for the reason above. It stays
behind modals, so a confirm (a DML run, `D`) still swallows everything,
and behind the filter, which was already a per-keystroke capture.

## 2. Two surfaces: a preview in the column, the editor in the main view

The side column is ~30 cells wide — too narrow to write SQL in. So panel
`[5]` renders a *preview* (`queryPanelBody`): the buffer's first lines,
a `… N more lines` marker when it does not fit, and a mode/`running…`
marker in the title. The editing surface is the main view
(`queryContent`), which is what the focused panel drives anyway —
exactly how `[1]` drives the connection detail and `[4]` the statement
detail.

`queryContent` stacks the textarea (its own line count, capped at half
the box, minimum 3 rows), a one-line key hint for the current mode, and
the Data tab underneath — so the editor and its last result are on
screen together. The textarea is sized on a *copy* of the model:
`View` may not mutate the model, and the box it has to fit is only known
at render time.

`clipHeight` truncates both blocks to the box they were given. lipgloss
v2 `Width`/`Height` are the total block size and do not clip content, so
an overflowing block would push the whole layout apart — see
[lipgloss-v2-sizing](../reference/lipgloss-v2-sizing.md).

The focused-panel rule ("exactly one green border") is kept: the green
border stays on side panel `[5]`, and the main box holding the editor
keeps its blurred border. Full-screen mode (`+`) is the exception —
there the column is gone, so full-screening `[5]` renders the main
column and the border moves with it.

## 3. Focus after a run depends on where the run started

`focusResult` replaced the unconditional `setFocus(panelMain)` of the
three `showQuery*` reducers:

- Started in panel `[5]` → focus **stays** in the editor. Its main view
  already shows the result under the buffer, and jumping away would undo
  the point of a persistent editor.
- Started anywhere else (`x` on the history panel, `enter`/`R` re-run in
  the grid) → the grid takes focus, exactly as before.

A run from insert mode also drops back to normal mode. The result is
what the user just asked to see, and normal mode is what makes it
navigable without an extra `esc`.

## 4. Unclaimed normal-mode keys fall through to the grid

With focus parked in `[5]`, `ctrl+f`, `v`, `[`/`]` and the rest of the
grid's keys would otherwise be unreachable without leaving the editor —
which is the modal problem again in a different costume. So
`updateQuery` ends with: if the main view is showing a query result,
hand the key to `updateData`.

Since issue #33 normal mode is a vim layer, which claims `h`/`l`, `y`,
`d`, `x`, `p` and friends before the fall-through — see
[design/vim-mode-query-editor](vim-mode-query-editor.md) for the key
set and what it displaced.

The panel's own actions are matched first, so `D` is "clear the buffer"
here and not the grid's "duplicate row". That shadowing is deliberate
and is why `D` confirms before it discards anything.

## 5. `submitQuery` never writes to the buffer

It used to set `m.draft = script`. With a persistent editor that would
mean `x` on the history panel silently overwriting a half-written
statement in `[5]`. The editor owns its text; only `:`-adjacent flows
(`loadIntoEditor`, `D`) change it. `enter` on the history panel still
loads an entry into the buffer — deliberately, and in normal mode: a
recalled statement is meant to be run, not typed over.

## Keybindings

New bindings, all in the one `keyMap` the options bar, the `a` menu and
`?` are built from, and all overridable in `[keys]`:

| Action name | Default | Meaning |
|---|---|---|
| `edit-query` | `i`, `enter` | start insert mode |
| `run-editor` | `ctrl+r` | run the buffer |
| `clear-query` | `D` | clear the buffer (confirms) |
| `leave-insert` | `esc` | leave insert mode |

`leave-insert` is a binding of its own rather than a second meaning of
`back`, so `?` can name what `esc` does while the buffer has the
keyboard, and so the two can be rebound apart.

Insert mode is documented through `keyMap.editorInsert()`, which is what
the options bar renders while editing *and* what `?` lists as an extra
group for `panelQuery` — `?` is unreachable from insert mode (it types),
so the mode's keys have to be visible from normal mode. `navigationFor`
drops `enter` from the navigation group on this one panel: `enter` is
not "drill in" here, and the panel's action list already names it.

`jump` grew to `1`–`5`.

## Consequences

- The side column now holds five panels. The tiny-terminal guard
  (60×18) still fits: `panelHeights` gives every panel its 3-row minimum
  at `bodyH = 17`, and half-screen mode's `collapsed*panelCount+2`
  threshold is exactly met. Verified in a real PTY down to 50×12, where
  the "terminal too small" notice takes over and recovers on resize.
- `Model.panels[panelQuery]` exists but is never filled: `panelQuery` is
  a `panelID` for focus, keys and titles, not a list. `renderPanel`
  special-cases it, and `updateQuery`/`updateEditor` intercept its keys
  before `updateFocused` would index the empty list.
- The `[4]` history panel is untouched here; its own restructure is a
  separate issue.
- `ctrl+enter` still runs, for terminals that send it, but only from
  insert mode — in normal mode it is an ordinary unbound key.

## See also

- [query-editor-and-history](query-editor-and-history.md) — what a run
  does once it starts: statement splitting, DML confirmation,
  cancellation, result materialization, the history file.
- [tui-shell-architecture](tui-shell-architecture.md) — the routing
  order this inserts a step into, and the one-focused-panel rule.
- [keybindings-single-source](keybindings-single-source.md) — the
  `key.Binding` table the new actions and the insert-mode group live in.
- [data-grid](data-grid.md) — the renderer the editor's main view stacks
  under the buffer, and the handler unclaimed keys fall through to.
