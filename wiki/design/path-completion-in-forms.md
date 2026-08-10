---
type: Design Decision
title: Filesystem path completion in the connection form
description: A pure pathcomplete engine plus a pathSuggest holder on formModal; tab completes the File field while candidates exist and only then stops moving between fields.
tags: [tui, modal, forms, connections, completion]
generated:
  by: claude-code/opus-5
  at: 2026-08-10T00:00:00Z
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
  leading `~` gets candidates that keep the `~`. `Expand` is the single
  tilde-expansion helper — callers must not keep local copies.
- Hidden entries are offered only when the typed base name explicitly starts
  with a dot, so `~/` does not drown in dotfiles.
- Matching is case-sensitive first and falls back to case-insensitive, so
  `~/dev` still finds `~/Development`.
- Directories carry a trailing separator, which is what makes "accept a
  directory, press tab again" descend rather than stall.
- `MaxCandidates = 50` caps the list; beyond that the shared-prefix extension
  is the only useful help anyway.

## Why tab completes instead of moving fields

`tab` already meant "next field" in every form. The routing rule
([design/tui-shell-architecture](tui-shell-architecture.md)) says an open modal
swallows every key, so the conflict is entirely `formModal.update`'s to
resolve — there is no root-level fallthrough to lean on.

The resolution is state-dependent rather than a new key:

- While the cursor is on a `withSuggest` field **and** candidates exist, `tab`
  completes.
- Otherwise `tab` keeps its old meaning and moves to the next field.
- `↓`/`↑` (and `shift+tab`) always walk fields, so field navigation is never
  unreachable — they were already bound, so nothing new had to be learned.

A dedicated completion key (`ctrl+space`, as the query editor uses) was
rejected here: a path input is the one place where every shell has trained the
user that `tab` completes, and the fallback keeps the old habit working the
moment there is nothing to complete.

The footer — a modal's options bar — swaps to
`tab complete path · ↑↓ field · …` exactly when completion owns the key, so
the bar never advertises a binding that is currently taken. `keyMap.
formPathComplete()` documents the same three keys in `?` on the Connections
panel, following the precedent of `editorCompletion()`.

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
