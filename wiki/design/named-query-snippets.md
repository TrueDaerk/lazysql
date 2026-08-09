---
type: Design Decision
title: Named query snippets
description: Why snippets are a store of their own (internal/snippets) next to the history rather than a flag on a history entry, why the file is rewritten whole while the history appends, how the name is the identity (case-insensitive, overwrite confirmed, creation date kept), and how the Snippets section rides inside the existing floating history pane instead of a second modal.
tags: [tui, snippets, history, state-directory, modal, keybindings]
generated:
  by: claude-code/opus-5
  at: 2026-08-09T22:00:00Z
sources:
  - resource: https://github.com/TrueDaerk/lazysql/issues/45
    title: "Issue #45 — Named query snippets with save/insert/execute"
---

# Named query snippets

## Decision

A **snippet** is a statement the user named and kept. It lives in
`internal/snippets` — `{Name, SQL, Engine, CreatedAt}` as JSON Lines in
`${XDG_STATE_HOME:-~/.local/state}/lazysql/snippets`, mode 600, next to
the history file — and surfaces in the **Snippets section** of the
floating pane that `backspace` already opens, toggled with `tab`.

`ctrl+s` in the query editor saves the buffer under a prompted name;
`s` on a history entry saves that entry. In the Snippets section
`enter` runs, `e` loads into the editor without running, `d` deletes
after a confirm.

## A separate store, not a flag on a history entry

The history is *chronological and self-pruning*: it compacts at
`history.MaxEntries` and every executed statement joins it whether the
user wanted it or not. A snippet is the opposite on all three counts —
kept until deleted, found by name, created deliberately. Marking a
history entry as "favourite" would have made compaction conditional on
the flag and left the name with nowhere to live. Two files, one
purpose each.

The two stores share their format and their reasoning (JSON Lines so a
multi-line statement stays on one line; owner-only because a statement
can name schemas and values the config file never holds), but not their
write strategy: **history appends, snippets rewrite**. A save may
replace an existing name and the list is held sorted by name — neither
is expressible as an append, and the file is small enough that an atomic
temp-file rewrite is the simpler contract. `LoadFrom` still tolerates a
duplicate name (last line wins), which is what a crash between rename
and truncate would leave.

## The name is the identity

`snippets.SameName` folds case and trims, so "Recent orders" cannot sit
next to "recent orders" as two rows the eye reads as one. Saving over an
existing name opens a confirm before anything is written — a snippet is
work the user cannot recover from the history once it has aged out —
and the overwrite **keeps the original `CreatedAt`**: the name is the
identity, and the date says how long the snippet has been in use.

`Put` and `Delete` return new slices rather than mutating: the model
holds the list while the save command runs in its own goroutine.

## The pane gained a section, not a second modal

`historyModal` grew a `section` field (`sectionHistory` /
`sectionSnippets`) with a per-section cursor and scroll offset, rather
than a `snippetsModal` beside it. The two lists want exactly the same
frame — list, detail of the selection, footer — and the same three
verbs; a second modal would have duplicated the windowing arithmetic and
made `tab` a modal swap. Per-section cursors mean toggling back and
forth does not lose either position.

`enter` goes through `Model.submitQuery`, the same as `ctrl+r` and the
history section, so a snippet with `?`/`:name` placeholders prompts via
`paramsModal` and runs prepared, with the values bound as parameters and
never interpolated (see
[history-pane-and-placeholders](history-pane-and-placeholders.md)).

Deleting a snippet confirms; deleting a history entry does not. The
asymmetry is deliberate: the history entry is a record of something that
already happened, the snippet is the only copy of a statement.

## `ctrl+s` is bound in both editor modes

Insert mode swallows almost every key, but `ctrl+s` is not a character,
so it can keep its meaning there. It is a normal panel action
(`actSaveSnippet`, overridable as `save-snippet`) for normal mode and an
explicit case in `updateEditor` plus an entry in `keyMap.editorInsert()`
for insert mode — which is what puts it in the options bar and `?` in
both modes, per
[keybindings-single-source](keybindings-single-source.md). Saving does
**not** end insert mode: the prompt takes the keys while it is open and
the buffer is waiting where it was left.

## Consequences

- The pane's footer is section-dependent and its title is now two tabs;
  `tab` inside the pane is the section toggle, not the panel cycle (a
  modal swallows every key anyway).
- A broken snippet file costs the section its contents and nothing else:
  the load logs a `FAILED` line and the next save rewrites the file from
  scratch.
- `Engine` is advisory. A snippet saved while disconnected has none and
  still runs anywhere; the pane renders it as `(any engine)`.
- Not implemented, deliberately: rename, folders, and a fuzzy filter
  over the snippet list. The list is browsed by name and sorted; those
  can follow if it grows.

## See also

- [history-pane-and-placeholders](history-pane-and-placeholders.md) —
  the pane the Snippets section rides in, and the placeholder path
  `enter` reuses.
- [query-editor-and-history](query-editor-and-history.md) — the history
  file format the snippet file mirrors.
- [keybindings-single-source](keybindings-single-source.md) — why
  `ctrl+s` had to be an action, not a bare key case.
