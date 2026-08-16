---
type: Design Decision
title: Filesystem path completion in the connection form
description: A pure pathcomplete engine plus a pathSuggest holder on formModal; tab completes the File field while candidates exist and cycles an ambiguous match, ↓/↑ walk the open list instead of changing fields, enter accepts the highlighted candidate instead of submitting, and esc dismisses the list before it cancels the form.
tags: [tui, modal, forms, connections, completion]
generated:
  by: claude-code/opus-5
  at: 2026-08-10T00:00:00Z
updated:
  by: claude-code/sonnet-5
  at: 2026-08-16T00:00:00Z
---

# Filesystem path completion in the connection form

## Decision

The SQLite/DuckDB `File` field of the connection form
([design/connection-form-modal](connection-form-modal.md)) completes against
the filesystem: candidates refresh on every keystroke and render under the
field, and `tab` extends the input to the longest unambiguous prefix.

The logic is split in two, and the split is the point:

- `internal/pathcomplete` — a pure, dependency-free engine. Given a partially
  typed path it returns `Result{Candidates, Completed}`. It imports nothing
  from the UI, so it is testable against tempdir fixtures with no model, no
  terminal and no server.
- `internal/ui/pathsuggest.go` — the presentation glue: a `pathSuggest` value
  embedded in `formModal`, holding the live candidate list for whichever field
  is under the cursor, plus the row rendering (final path component only,
  capped, `… +N more` tail).

The engine was ported from the Ike editor, where it already backs three
different inputs. Keeping it presentation-agnostic is what let it move here
unchanged.

## Engine behaviour worth knowing

- Completion happens in the user's own notation: an input written with a
  leading `~` or a `$VAR`/`${VAR}` reference gets candidates that keep that
  notation — only the directory actually read from disk is expanded.
  `Expand` is the single expansion helper (tilde, then environment
  variables via `os.Expand`) — callers must not keep local copies.
- Hidden entries are offered only when the typed base name explicitly starts
  with a dot, so `~/` does not drown in dotfiles.
- Matching is case-sensitive first and falls back to case-insensitive, so
  `~/dev` still finds `~/Development`.
- Directories carry a trailing separator, which is what makes "accept a
  directory, press tab again" descend rather than stall.
- Candidates rank directories first, then the SQLite/DuckDB extensions
  (`.db`, `.sqlite`, `.sqlite3`, `.duckdb`), then everything else,
  alphabetically within each tier. Nothing is filtered out — a `.txt` file
  still shows, it just sorts last — because the field is free text, not a
  picker restricted to database files.
- `MaxCandidates = 50` caps the list; beyond that the shared-prefix extension
  is the only useful help anyway.

## Why tab completes instead of moving fields

`tab` already meant "next field" in every form. The routing rule
([design/tui-shell-architecture](tui-shell-architecture.md)) says an open modal
swallows every key, so the conflict is entirely `formModal.update`'s to
resolve — there is no root-level fallthrough to lean on.

The resolution is state-dependent rather than a new key:

- While the cursor is on a `withSuggest` field **and** candidates exist, `tab`
  completes — first to the longest prefix every candidate shares (shell-style
  extension), same as before.
- Once that prefix is already the whole input — an ambiguous match with
  nothing left to extend, e.g. `sales.` against `sales.duckdb` and
  `sales.sqlite` — further `tab` presses instead cycle through the
  candidates one at a time (`pathSuggest.cycling`), so the ambiguity can
  still be resolved from the keyboard. The candidate currently applied to
  the field is marked `▸` in the list under it, and the footer swaps to
  `tab/shift+tab cycle path` so the new meaning is visible.
- `shift+tab` reverses an in-progress cycle. Outside a cycle it keeps its old
  meaning of walking to the previous field — `pathSuggest.cycling` is what
  formModal.update checks to decide which one applies.
- Any keystroke that changes the input (typing, pasting, leaving the field)
  cancels a cycle — `pathSuggest.refresh` resets `cycling`, since a fresh
  edit means the user is typing, not continuing the walk through candidates.

A dedicated completion key (`ctrl+space`, as the query editor uses) was
rejected here: a path input is the one place where every shell has trained the
user that `tab` completes, and the fallback keeps the old habit working the
moment there is nothing to complete.

## ↓/↑, enter and esc while the list is open (#154)

Field navigation originally kept `↓`/`↑` regardless of suggestion state, but
that shape was wrong for a list of file candidates: users expect the arrows to
walk the list, the way every shell and picker in the app already behaves
(`enginePickerKeys`, the query editor's own completion popup). So the same
state check `tab` uses now gates `↓`/`↑`, `enter` and `esc` too, each falling
back to its normal form meaning the moment the list is gone:

- `↓`/`↑` move `pathSuggest.selected` by one, wrapping — `pathSuggest.navigate`
  reuses the exact index `complete`'s tab-cycling already tracks rather than
  adding a second one. The first press with nothing highlighted starts
  `cycling` from whichever end the delta points at (`↓` from the top, `↑` from
  the bottom), so a single press always lands on a candidate instead of
  skipping past one. With no candidates, `↓`/`↑` fall through to
  `formModal.move`, unchanged from before.
- `enter` accepts the highlighted candidate into the field (`pathSuggest.
  accept` — the first candidate if nothing has been highlighted yet) instead
  of submitting the form. `ctrl+enter` (`AcceptChanges`) is untouched and
  still submits even with the list open, since it is a dedicated save key, not
  the one enter overloads with two meanings.
- `esc` dismisses the list on the first press (`pathSuggest.clear`, form stays
  open) and only cancels the form — the old, only meaning — on a second press
  once the list is already gone. This is the same "closest thing on screen
  goes first" shape the rest of the app uses for esc.

The list's own key handling stays independent of its rendering (still a plain
line list under the field, one `▸` marker) — the follow-up issue restyling it
as a floating overlay changes `pathSuggest.lines`/`formModal.view` only, none
of `formModal.update`.

The footer — a modal's options bar — swaps to
`tab complete path · ↑↓ select · enter accept · ctrl+enter save · esc dismiss`
exactly when the list owns these keys, so the bar never advertises a binding
that is currently taken. `keyMap.formPathComplete()` documents the same keys
in `?` on the Connections panel: `PathCandidateNav`/`PathCandidateAccept`/
`PathCandidateDismiss` are dedicated bindings (like the query editor's
`CompleteNext`/`CompletePrev`) rather than reusing `NextField`/`PrevField`/
`FormSave`/`Back`, because their help text ("select candidate", not "next
field") only applies while the list is up.

## Fitting the modal

Modals are centered on the full terminal, so suggestion rows cannot simply be
appended: a tall list would push the box past the screen and defeat the
tiny-terminal guard. `formModal.view` therefore budgets the rows it has left
(`maxH` minus title, body, fields, error and footer, minus box chrome) and
passes that to `pathSuggest.lines`, which shrinks the list and collapses the
remainder into the `… +N more` tail. On a very short terminal the budget
reaches zero and no rows render at all — `tab` still completes.

Candidates are cleared whenever the cursor leaves the field, the engine select
changes (which can hide the field outright), or the form closes or submits, so
a stale list can never outlive the input it describes.
