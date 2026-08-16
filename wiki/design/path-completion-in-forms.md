---
type: Design Decision
title: Filesystem path completion in the connection form
description: A pure pathcomplete engine plus a pathSuggest holder on formModal; tab completes the File field while candidates exist and cycles an ambiguous match, ↓/↑ walk the open list instead of changing fields, enter/tab accept the highlighted candidate and advance to the next field (a directory candidate stays and descends instead), esc dismisses the list before it cancels the form, and the candidates render as a bordered layer floating over the modal rather than as rows inside it.
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
the filesystem: candidates refresh on every keystroke and float in a bordered
box under the field, and `tab` extends the input to the longest unambiguous
prefix.

The logic is split in two, and the split is the point:

- `internal/pathcomplete` — a pure, dependency-free engine. Given a partially
  typed path it returns `Result{Candidates, Completed}`. It imports nothing
  from the UI, so it is testable against tempdir fixtures with no model, no
  terminal and no server.
- `internal/ui/pathsuggest.go` — the presentation glue: a `pathSuggest` value
  embedded in `formModal`, holding the live candidate list for whichever field
  is under the cursor, plus the overlay rendering (final path component only,
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
  extension), same as before, staying on the field either way since the path
  is still incomplete.
- Once that prefix is already the whole input — an ambiguous match with
  nothing left to extend, e.g. `sales.` against `sales.duckdb` and
  `sales.sqlite` — the next `tab` press highlights the first candidate
  instead (`pathSuggest.cycling`), so the ambiguity can be resolved from the
  keyboard, but still does not accept it — a highlight has only just
  appeared. The candidate currently applied to the field is the highlighted
  row of the floating box.
- Once `pathSuggest.cycling` is already true when `tab` is pressed — a
  candidate was explicitly selected, either by this cycling or by `↓`/`↑` —
  `tab` accepts it and advances to the next field, the same as `enter` (see
  below): `formModal.acceptSuggestion` is the shared path both keys call.
  This means repeated `tab` alone can no longer step through every
  candidate; `↓`/`↑` is the way to preview more than one before accepting
  with `tab` or `enter`.
- `shift+tab` reverses an in-progress cycle without accepting. Outside a
  cycle it keeps its old meaning of walking to the previous field.
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

### Accept-and-advance, and the directory exception (#170)

Accepting a candidate used to leave the cursor on the same field — the list
was gone, but the user still had to press `tab`/`↓` themselves to reach the
next field. `formModal.acceptSuggestion` (called by both `enter` and `tab`
once a candidate is highlighted, see above) now does both in one step:

- It applies `pathSuggest.accept()` to the field, same as before.
- A **file** candidate (or a directory candidate in the `dirs`-only flavor,
  see below) is a complete, usable value, so it dismisses the list
  (`formModal.move` clears `pathSuggest` as a side effect) and moves the
  cursor to the next field.
- A **directory** candidate is not a complete value in the general flavor —
  advancing past it would leave an unusable path — so instead it stays on
  the field and calls `pathSuggest.refresh` with the accepted directory,
  which re-runs completion inside it (the same "accept a directory, see its
  contents" step that used to require a manual second `tab`).
- The directory check is `strings.HasSuffix(v, separator)`, not a stat call:
  `pathcomplete.rank` already appends the separator to every directory
  candidate, so the accepted string carries the answer.
- `pathSuggest.dirs` (the directories-only flavor, `pathcomplete.Dirs` — used
  by inputs that only ever want a folder, e.g. a future project picker) flips
  the exception: there a directory *is* the field's whole answer, so it
  advances like a file would in the general flavor. No form field wires this
  flavor up yet, but the exception is what `formModal.acceptSuggestion`
  checks (`!f.sugg.dirs`), not the presence of a trailing separator alone.

The footer reflects the two states while candidates are up: `tab complete
path · ↑↓ select · enter accept & next · ctrl+enter save · esc dismiss` before
anything is highlighted (`enter` still accepts-and-advances even without an
explicit `↓`/`↑` — the first candidate counts as highlighted), and `tab/enter
accept & next · shift+tab back · ↑↓ select · ctrl+enter save · esc dismiss`
once `pathSuggest.cycling` is true.

The list's own key handling is independent of its rendering: restyling it as a
floating overlay (#155, below) touched `pathSuggest`'s row rendering,
`formModal.view` and `Model.View` only — not one line of `formModal.update`.

The footer — a modal's options bar — swaps to
`tab complete path · ↑↓ select · enter accept · ctrl+enter save · esc dismiss`
exactly when the list owns these keys, so the bar never advertises a binding
that is currently taken. `keyMap.formPathComplete()` documents the same keys
in `?` on the Connections panel: `PathCandidateNav`/`PathCandidateAccept`/
`PathCandidateDismiss` are dedicated bindings (like the query editor's
`CompleteNext`/`CompletePrev`) rather than reusing `NextField`/`PrevField`/
`FormSave`/`Back`, because their help text ("select candidate", not "next
field") only applies while the list is up.

## A floating overlay, not form rows (#155)

Candidates were originally extra lines spliced into the field block. That made
the modal grow and shrink while typing — the form's fields walked down the
screen under a lengthening list — and it looked nothing like the query editor's
completion, which is a bordered popup composited by the lipgloss layer
compositor ([design/schema-aware-autocomplete](schema-aware-autocomplete.md)).

The list now uses the same mechanism, which means the form's layout reserves
nothing for it and its height is identical whether or not the list is up:

- `pathSuggest.rows` returns structured rows (`suggestRow{text, selected,
  tail}`) instead of pre-marked strings, and `pathSuggest.popup` renders them
  into `styles.popup`'s bordered box. The highlighted row is a full-width
  `styles.popupSelected` bar, like the editor popup's selection, rather than a
  `▸` marker — the box's own border already separates the list from the form,
  so the marker had nothing left to do.
- The box shrinks to its longest row (capped at the field's input width) so a
  list of short file names does not draw an empty gutter.

### Anchoring inside a centered modal

The compositor places by absolute screen cell, and a modal box does not know
where it was centered. The offset is therefore recorded where it is still
known and resolved where the origin is:

- `formModal.view` computes the field block anyway, so it stores the value
  cell of the completing field as `anchorX/anchorY` — relative to the box's own
  top-left, i.e. the modal frame (border + padding, read from
  `styles.modal`), the title and its blank line, an optional body block, then
  the field's row *inside the scroll window*. A field the window clipped away
  sets `anchorOK = false`, so the overlay is dropped rather than pointing at an
  unrelated row.
- `Model.pathSuggestLayer` (internal/ui/view.go) adds the modal's placement to
  that offset and hands the result to `placePopup` — the same helper the editor
  popup uses, so the clamping rules are shared, not duplicated: one row below
  the field, slid left at the right edge, flipped above when the rows below run
  out.
- `Model.View` composites it as a third layer at `Z(2)`, over the modal at
  `Z(1)`.

### Fitting a small terminal

The vertical budget is the roomier side of the field (`m.height - ay - 3`
below, `ay - 2` above; the border costs two rows either way), capped at
`maxSuggestLines`. `pathSuggest.rows` shrinks to that and collapses the
remainder into the `… +N more` tail. A one-row budget spends it on a candidate
rather than on a lone `+N more`, which would cost a box and say nothing. When
neither side has room for a bordered row at all, the overlay is skipped
entirely — `tab` still completes. Verified with `scripts/ptycheck.py` at 60×18
(the tiny-terminal minimum), where the box flips above the field and shrinks to
six rows plus the tail.

Candidates are cleared whenever the cursor leaves the field, the engine select
changes (which can hide the field outright), or the form closes or submits, so
a stale list can never outlive the input it describes.

One size wobble remains, and it is not the list: the footer swaps to the longer
completion hint, which can widen the box by a few cells. That belongs to the
stable-popup-size work, not here.
