---
type: Design Decision
title: Autocomplete on the grid's inline WHERE line
description: How the editor's completion popup was generalized to a second line without being duplicated — a completionSite derived from the focus, a completionScope pairing the word under the caret with the whole statement it sits in (clause plus the line's uneditable SELECT prefix), the anchor read off the bottom-pinned line's own render, the budget that forces the box above it, and the key precedence that gives the popup esc and the arrows while leaving enter the line's own verb unless a row was picked.
tags: [tui, db, main-view, filtering, completion, autocomplete, keybindings, layout]
generated:
  by: claude-code/opus-5
  at: 2026-08-20T00:00:00Z
sources:
  - resource: https://github.com/TrueDaerk/lazysql/issues/183
    title: "Issue #183 — Autocomplete in the inline quick filter"
---

# Autocomplete on the filter line

## Decision

The completion popup is no longer the query editor's. It belongs to
**whichever line is taking text** — the editor's buffer, or the data
grid's inline `WHERE` line ([inline-where-filter](inline-where-filter.md))
— and there is still exactly one of it, one list-building path, one
ranking, one renderer.

Typing on the filter line offers the open relation's columns first, then
the keywords and functions a `WHERE` clause is written from; `ctrl+space`
opens the list on an empty clause; `tab` accepts and quotes per dialect.
Nothing about the editor's behaviour changed.

## 1. The site is derived, not stored by the opener

`completionSite` (`siteNone`, `siteEditor`, `siteFilter`) answers "which
line owns the keyboard", and `Model.completionSite()` answers it from the
focus — the same conditions `Update`'s routing uses, in the same order:

```go
if m.filterInputOpen() { return siteFilter }        // grid's `/` line
if m.focus == panelQuery && m.editor.editing { ... } // editor, insert mode
return siteNone
```

The two can never be true at once: the filter line is only live on the
focused grid, insert mode only on the focused editor. So the popup does
not need to remember which line opened it — every function that reads a
line re-derives it, and a stale site can never type into a line that is
not on screen.

`completion.site` is recorded anyway, for the two places that are *not*
reading a line: `completionLayer`, which has to know which frame to
anchor the box on, and `closeFilterInput`, which must drop a popup
floating over the line it is closing without touching the editor's — it
runs on paths the editor takes too (`openTable`, `resetBrowse`,
`setFocus`).

## 2. The scope: the word, and the statement around it

The old `completionSources(ctx)` read `m.script()` for its relation scan.
That is the one editor-bound assumption in the list building, and it is
the interesting one, because the filter line's answer is not its value:

```
SELECT * FROM "orders" WHERE  status = 'op
└──── prefix: not the value, but part of the statement ────┘
```

Reading the clause alone would leave the popup with **no relation to take
columns from** — the clause never names one — and with a word that looks
like it sits at the start of a statement rather than after a `WHERE`.

So the pair travels together as a `completionScope`: the
`completionContext` under the caret, and `stmt`, the whole statement the
word sits in. The editor's is `m.script()`; the line's is
`fi.prefix + clause`. `referencedRelations` then finds `orders` in the
prefix exactly the way it finds a table in a `FROM`, `ensureSchemaColumns`
fetches its columns through the same cache, and the qualified form
(`orders.st`) works on the line for free.

This is why no "the open relation is `m.data.table`" shortcut was taken.
It would have been three lines, and it would have been a second answer to
a question the token scan already answers — with its own edge cases the
first one has tests for.

## 3. Anchoring a popup on the bottom row

The editor's anchor comes from `renderEditor`, which returns the caret's
cell inside the block it just laid out. The filter line needs the same
trick for the same reason — the prefix can shorten (`WHERE `) or vanish
in a narrow box, and the clause scrolls horizontally under its own window
— so `filterInput.view` records the caret's cell in `fi.caret` while
rendering, and `filterAnchor` turns it into a screen cell. Its **row** is
not read from anything: the line is pinned to the last content row of the
main box, so the row is that box's height, the same arithmetic
`renderMainColumn` and `dataBody` use.

The placement then differs from the editor's in one way. Below a line on
the last row lie the box's own bottom border and the command log — not
somewhere a list anchored on the grid may be drawn. `completionLayer`
therefore gives the filter popup a **shorter column to live in**: the
budget ends at the line itself, which both sizes the box to the room
above it (`completionPopup(mw, limit)`) and makes `placePopup` flip it up
there (`placePopup(..., limit)`). The editor keeps the full column, so
its behaviour is bit-for-bit unchanged.

At `minHeight` (18 rows) the grid has ten content rows and the flipped
box takes eight of them plus its border. If a terminal ever has room for
neither side, `placePopup`'s existing rule applies — the box stays below
and is clipped there, which is the half being read.

## 4. Key precedence on a line that already used those keys

The filter line's four keys and the popup's four overlap almost entirely.
The popup goes first, as it does in `updateEditor`, with one exception:

| Key | Popup open | Popup closed |
|---|---|---|
| `esc` | close the popup, keep the clause | close the line |
| `↑`/`↓`, `ctrl+p`/`ctrl+n` | move the selection | walk the filter history |
| `tab` | accept | complete (or nothing) |
| `enter` | **apply the clause** — unless a row was picked | apply the clause |

`esc` and the arrows are the issue's requirement and the editor's
behaviour; the history is reachable again the moment the popup closes,
which is one `esc`.

`enter` is the exception because on this line `enter` is *the* verb: it
is how a filter runs. A popup is open over most of a clause's last word,
so an `enter` that always accepted would make running a filter a two-key
gesture — and would have broken `applyWhereFilter`, i.e. every filter
test in the suite, which is a fair proxy for every user.

The rule that separates the two intents is **whether the user moved in
the list**: `completion.picked` is set by `moveCompletion` and by nothing
else. An untouched popup's selection is just the top row, which the popup
has from the moment it opens; a moved one is a choice. `tab` accepts
either way, so accepting without moving costs no extra key.

`picked` earns a second keep: `restackCompletion` now carries the
selection across a landed column fetch **only when it was picked**. Before,
it pinned whatever happened to be on top — and on the filter line that is
routinely a keyword, because the popup opens before the columns arrive:
typing `or` on a cold cache selected `OR`, and the `"order date"` that
landed a moment later ranked below a selection nobody had chosen.

## 5. Documentation, and the one thing that is not documented twice

`keyMap.editorCompletion()` is now `keyMap.completionKeys()`: the same
slice, dispatched by two update functions instead of one. The options bar
swaps to it whenever a popup is open, on either line.

`Complete` was added to `keyMap.filterInput()` — last, not first. The bar
truncates to its width, and the two keys that decide a clause's fate
(apply, cancel) outrank the one that helps write it.

The popup's own four keys are **not** added as a fifth `?` group for
`panelMain`. The help modal packs groups into rows and scrolls, and a
fifth group pushed the date picker's keys below the first screenful
(`TestDatePickerKeysAreDocumented` caught it). They stay documented under
`[3] Query`, where the same popup is described, and the options bar shows
them in context on the grid — which is the affordance that matters while
one is open.

## Consequences

- **`esc` on the filter line can take one more press**, exactly as it did
  in the editor when completion landed there.
- **Every keystroke on the line may start a column fetch.** It is the
  same self-invalidating cache with the same `maxSchemaFetch` cap; the
  practical difference is that a filter line touches one relation, so it
  is one request per line, ever.
- The line's popup is offered **tables and views as well as columns**.
  They are ranked below the columns and are occasionally right — a
  subselect in a verbatim clause — so nothing filters them out.
- `filterInput.caret` is written during rendering, like `formModal`'s
  path-completion anchor. `completionLayer` runs after the body is
  rendered in `View`, so it reads a value from this frame; a caller that
  asked for the layer without rendering would get the previous frame's
  column.

## See also

- [schema-aware-autocomplete](schema-aware-autocomplete.md) — the popup
  itself: the layer-not-a-modal rule, the token scan, the ranking, the
  column cache and the quoting.
- [inline-where-filter](inline-where-filter.md) — the line this now
  completes on, its prefix, its renderer and its history.
- [tui-shell-architecture](tui-shell-architecture.md) — the routing order
  the site derivation mirrors.
- [keybindings-single-source](keybindings-single-source.md) — the one
  table behind the bar, `?` and dispatch.
