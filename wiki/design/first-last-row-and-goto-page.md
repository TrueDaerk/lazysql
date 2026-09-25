---
type: Design Decision
title: Jump to the first/last row and to a given page
description: home/end jump the grid cursor to the first row of the first page and the last row of the last page, and `p` opens a page-number prompt; why home/end/p were picked over the vim gg/G spelling, why the last-row jump trusts the same (possibly estimated) total the status line already reads, and how both reuse reloadPage's cancellation and selection-clearing rather than adding a second path.
tags: [ui, main-view, data-grid, paging, keybindings, cancellation]
generated:
  by: claude-code/sonnet-5
  at: 2026-09-25T00:00:00Z
sources:
  - resource: https://github.com/TrueDaerk/lazysql/issues/221
    note: issue — jump to the first/last row and to a given page in the data grid
---

# Jump to the first/last row and to a given page

## The problem

The grid only ever moved by one page: `ctrl+f`/`pgdown` and
`ctrl+b`/`pgup`. On a 5,000-row table at the default page size (100) that
is 50 presses to reach the last row, and there was no way to land on a
known page at all — a user who knew "the row I want is around page 30"
had no faster way in than holding `ctrl+f` down.

## Decision

Three new grid bindings (`internal/ui/keys.go`, `internal/ui/data.go`):

- `FirstRow` (`home`) jumps to row one of the first page.
- `LastRow` (`end`) jumps to the last row of the last page.
- `GoToPage` (`p`) opens a prompt for a page number.

All three respect whatever filter and sort are running — they reload the
*same query*, just at a different offset — and all three go through
`Model.reloadPage()` for the cases that actually issue a query, so they
get its cancellation of a superseded page/count pair and its
`clearSelection()` for free rather than a second implementation of
either. See [design/page-query-cancellation](page-query-cancellation.md)
for the mechanism itself and
[design/grid-multi-row-selection](grid-multi-row-selection.md) for why a
query-shape change always drops the selection.

### Why not `gg`/`G`

The obvious vim spelling is taken twice over:
[design/foreign-key-navigation](foreign-key-navigation.md) already binds
`g` to follow a foreign key and `G` to list the rows referencing the
current one, and the query editor's own vim layer
([design/vim-mode-query-editor](vim-mode-query-editor.md)) binds `gg`/`G`
to buffer start/end. Reusing either meaning for the grid would only work
by shadowing one of them, in one panel, which is exactly the kind of
context-dependent collision
[design/keybindings-single-source](keybindings-single-source.md) exists
to keep out.

### Why `home`/`end`/`p`

`home` and `end` are unbound anywhere `?` documents today — not the grid,
not the filter input, not the cell detail popup, not the date picker —
and they are exactly the reading every spreadsheet and file manager
already gives those keys: jump to the start, jump to the end. `p` (page)
is free in the same four contexts. None of the three needs the alias
[wiki/reference/keyboard-layout-portability](../reference/keyboard-layout-portability.md)
requires of a *punctuation* binding: `TestNoActionNeedsAltGr` only flags
an action whose every key is drawn from `[ ] { } \ @ | ~ €`, and a named
key or a letter never is.

### The last-row jump trusts the same total the status line already shows

`dataView.pageCount()` — the same call `dataStatus()` uses to render
`(page N/M)` — is the only source of truth for "how many pages are
there". For a browsed table that total is a separate `COUNT(*)` that can
be an *estimate* on some engines (the status line already spells it
`of ~5000` rather than `of 5000` for that reason — see the `isQuery()`
branch in `dataStatus`). `jumpToLastRow` does not treat it as exact
either: it computes the expected row count of the last page from the
same total, but `Model.clampCursor` (already called after every
`pageLoadedMsg`) settles the cursor onto whatever the real page turns out
to hold, the same way it already settles a cursor that outlived a
shorter reply. Nothing added here assumes the count is right beyond what
the status line was already asserting.

A table whose count has not landed yet (`!d.hasTotal`) refuses the jump
outright — `-- last row unknown yet: row count still loading` — rather
than guessing at page one being last. A query result is different: it is
fully materialized (`dataView.all`), so `pageCount()` there is exact and
the jump is a slice, not a round trip.

### Reusing `reloadPage`, not a parallel load path

`jumpToFirstRow`/`jumpToLastRow`/the page prompt's submit handler all set
`m.data.page` (and, for the browsed-table case, a guessed `m.data.row`)
and then call `m.reloadPage()` — the same function `turnPage`, `toggleSort`
and `setDataFilter` call. That is what makes the cancellation and
selection-drop automatic: `reloadPage` bumps `req`, cancels whatever the
previous reload left running through `pageQueries.start`, and clears the
selection before issuing the new pair of queries. A jump that already
sits on its target page (pressing `end` twice) skips the round trip
entirely, matching the existing `next == m.data.page` short-circuit in
`turnPage` — including that neither one bothers clearing the selection in
that no-op case, since nothing about the page actually changed.

### The page prompt is the existing `promptModal`, not a new shape

`GoToPage` opens `newPromptModal("Go to page", …)`
(`internal/ui/modal.go`), the same modal `E` (export path), `ctrl+s`
(save snippet) and the schema-diff/DDL-export flows already use. `esc`
cancels for free — `promptModal.update` already returns `close=true, nil`
on it. Validation happens on submit rather than as the user types: a
page outside `1..pageCount` is refused with a command-log line naming the
valid range (`-- go to page 99 FAILED: out of range (1-3)`), never
clamped to an end — a mistyped page number should say so, not silently
land somewhere the user did not ask for. The same refusal covers text
that is not a whole number at all, and an empty answer is treated as a
cancel rather than a request for page zero.

## Consequences

- The three actions are declared in `keyMap` and wired into
  `panelActions(panelMain)` and `slots()` like every other grid binding,
  so they show up in `?`, the options bar, and `[keys]` overrides.
- No new message types: the jumps ride the existing `pageLoadedMsg` /
  `rowCountMsg` / `pageQueries` plumbing.
- A jump on a query result (`dataView.isQuery()`) never touches the
  network — `setPage` is a slice into `dataView.all`, exactly like
  `turnPage` already treats it.

## See also

- [design/data-grid](data-grid.md) — the one-page-at-a-time main view
  these jumps move within.
- [design/page-query-cancellation](page-query-cancellation.md) — the
  cancellation mechanism reused here.
- [design/foreign-key-navigation](foreign-key-navigation.md) — why `g`/`G`
  were unavailable.
- [reference/keyboard-layout-portability](../reference/keyboard-layout-portability.md)
  — the AltGr rule new bindings are checked against.
