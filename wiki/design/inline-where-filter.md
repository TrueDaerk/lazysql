---
type: Design Decision
title: Inline WHERE input with per-table filter history
description: Why `/` on the data grid types a WHERE clause into a line inside the grid instead of opening a popup, how the immutable `SELECT * FROM <relation> WHERE ` prefix is built and rendered, why the line draws its own dialect-highlighted clause and caret instead of calling textinput.View(), the three cues that say the keyboard has moved to it, and how applied clauses are recalled per connection + relation out of the JSON Lines history store using the same Entry scope fields the query history is filtered by.
tags: [tui, db, main-view, filtering, history, keybindings, highlighting, theme, security]
generated:
  by: claude-code/opus-5
  at: 2026-08-20T00:00:00Z
sources:
  - resource: https://github.com/TrueDaerk/lazysql/issues/130
  - resource: https://github.com/TrueDaerk/lazysql/issues/180
---

# Inline WHERE filter

## Decision

`/` on the data grid opens **a line, not a popup**. It sits at the bottom
of the grid, in the row the status line normally occupies, pre-labelled
with the statement the clause goes into:

```
SELECT * FROM "orders" WHERE  status = 'open' AND total > 100
└────────── prompt, not editable ──────────┘ └─ what the user types ─┘
```

`enter` applies it, `esc` cancels and leaves the grid exactly as it was,
an empty clause clears the filter, and `↑`/`ctrl+p` and `↓`/`ctrl+n` walk
the filters previously applied **to this relation of this connection**.

This replaces `filterModal`, the two-mode popup (column/operator/value,
`ctrl+t` for a free-text fragment) described in
[data-grid](data-grid.md). Filtering is the most repeated action in the
grid, and a popup made it a three-step one: open, land on the right
field, type. Typing straight into the grid is the interaction the rest of
the shell already uses for `/` — the side panels' inline fuzzy filter —
so the key now means the same thing everywhere: *type what narrows this
list*.

## The textinput is the model, this file is the renderer

`filterInput` (in `internal/ui/filterinput.go`) holds a `textinput.Model`
but never calls its `View()`. The clause is SQL and has to be coloured
token by token; a `textinput` styles its value whole, exactly the way a
`textarea` does — which is why the query editor already draws its own
buffer ([query-editor-and-history](query-editor-and-history.md)). The
line makes the same split, for the same reason:

- The component owns the **value, the cursor and every editing key**.
  `Value()` and `Position()` are read back from it.
- `filterInput.view` owns the **prefix, the colours, the caret cell and
  the horizontal scroll**.

The prefix is therefore drawn text rather than the component's `Prompt`,
and it stays immutable for the same reason it was before: it is not part
of the value, so nothing can select, backspace into or submit it. The
component's own `Prompt` and `Width` stay unset — anything it computed
from them would be a second, disagreeing answer to a question the
renderer already answers.

**Highlighting.** `sqlhl.Kinds(dialect, clause)` tokenizes the typed
clause and `renderTokens` writes it with `styles.sqlStyle`, the same
palette the editor uses — keywords, strings, numbers, placeholders and
quoted identifiers, all themeable through `[theme]`
([sql-syntax-highlighting](sql-syntax-highlighting.md),
[configurable-keys-and-theme](configurable-keys-and-theme.md)). The
dialect is `Model.sqlDialect()`, captured
when the line opens: `"x"` is a quoted identifier on SQLite and a string
literal on MySQL, and the line reads the clause the way the connection
behind it will. The prefix is *not* run through the tokenizer — it is
chrome, and a muted `SELECT … WHERE` is what says "this part is not
yours".

**Scrolling.** `filterInput.window` picks the visible slice of the clause
in cells, not runes ([runes-cells-and-ansi-in-rendering](../reference/runes-cells-and-ansi-in-rendering.md)):
it scrolls only as far as it must to keep the caret — including the empty
cell past the end of the clause — inside the box, never further right
than the point where the tail fills it, and never inside a grapheme
cluster. That last rule is why a combining accent cannot be scrolled away
from the letter carrying it.

The prefix comes from `db.FilterPrefixSQL(dialect, database, table)`, not
from string-building in the UI. The relation is therefore quoted by the
same `Dialect.QuoteIdent` and qualified by the same `qualifiedTable` that
`db.PageSQL` will use, so the identifier shown above the grid is
character-for-character the one the statement runs against — a
`"my table"` or a backticked MySQL name included.

In a box too narrow to hold both the prefix and a usable clause
(`minFilterClauseWidth`, 16 cells) the prefix degrades to `WHERE `.
Truncating the line instead would cut off the end the caret is on, which
is the only part that has to stay visible.

The caret does not blink: it is a reversed cell (`styles.editorCursor`)
drawn by the renderer, so the line costs no timer per keystroke and
matches the editor's own static cursor.

## Saying where the keyboard is

A line at the bottom edge of a big grid is easy to miss, and issue #180
is the report of people typing at a grid that had already handed the keys
over. Three cues answer it, all of them theme colours rather than
hard-coded ones, and all of them gone the moment the line closes:

- **A focus bar.** The line starts with `▌` in `styles.filterFocus` —
  the same green a focused panel's border wears
  ([tui-shell-architecture](tui-shell-architecture.md)). The green means
  one thing throughout the app: *the keyboard is here*. A box under four
  cells wide drops the bar rather than a cell of clause.
- **A caret in coloured text.** The reversed cell sits inside the
  highlighted clause, which no other bottom-line state has.
- **A grid that steps back.** `gridCursor.idle` is set while the line is
  open (`Model.filterInputOpen()`). The cell cursor drops to
  `styles.cellCursorIdle`, the weaker tint, the row tint under it goes
  away and the header column stops wearing the accent. The cursor is
  still *findable* — the page must not lose its place while a filter is
  being typed — but it is no longer the loudest thing on screen.

The grid keeps its green border: the main view does still have the focus,
and the line is inside it. What moved is the keyboard, and that is what
the bar and the idle tint say.

## Routing: the line owns the keyboard

`Model.Update` gained one step, right after the side panels' `/` filter
and before the global keys:

1. open modal
2. a panel's `/` fuzzy filter
3. **the grid's `/` WHERE line**
4. the query editor in insert mode
5. global keys
6. the focused view

So `q` types instead of quitting, `2` types instead of jumping panels and
`tab` types instead of cycling focus — a clause is text, not commands.
Only four keys act, and they are `key.Binding`s of their own
(`ApplyFilter`, `CancelFilter`, `FilterHistPrev`, `FilterHistNext`) rather
than second meanings of `Enter`/`Back`, so `?` and the options bar can
name what they do *here* — the same reason the editor has `LeaveInsert`.
`keyMap.filterInput()` is the single slice all three read.

The keyboard cannot leave the line without answering it (`esc` or
`enter`). A mouse click on another panel can, and `Model.setFocus` closes
the line when the focus lands anywhere but the grid — a line left open
elsewhere would be a caret nothing types into. `openTable`, `resetBrowse`
and `openDatabase` close it too: the prefix names one relation of one
connection and must not outlive either.

A bracketed paste follows the same routing (`updatePaste`) and is
flattened to one line, like the panels' filter.

## Safety is unchanged

The clause stays user-authored SQL by design. `db.ParseFilter` takes it
apart into `column <op> <literal>` terms and binds every literal as a
query parameter; what it cannot parse runs verbatim and is flagged both
in the command log and in the status line (`where (verbatim) …`). See
[data-grid](data-grid.md) for that rule in full. Nothing about the
parameterization changed with this issue — only what opens the clause.

Everything the line applies reaches the command log through
`reloadPage`, exactly like a page turn or a sort, so the executed
statement (with its bound arguments) is visible in `[4]` as before.

## History: the same store, the same keying

Applied clauses are recorded in
`${XDG_STATE_HOME:-~/.local/state}/lazysql/filters` —
`internal/history/filters.go`. It is deliberately **not** a new format,
and deliberately **not** a second way of keying one:

- Same `history.Entry`, same JSON Lines file, same append-only write,
  same owner-only mode, same compaction on load as the statement history
  next to it.
- Same scope fields. Issue #131 scoped the query editor's history by
  `Entry.Connection`; a filter narrows that with `Entry.Database` and
  `Entry.Table`. A statement is scoped by connection, a filter by
  relation, and one set of fields covers both —
  `history.ForConnection(entries, conn)` and
  `history.InRelation(entries, conn, database, table)` are the two
  readings of it.

`InRelation` matches all three fields exactly, without the
empty-`Connection` leniency `ForConnection` grants entries written before
that field existed: an unscoped entry belongs to no relation, and the
filter file is new enough to have none. An empty `Database` matches an
empty `Database` — that is the pseudo-namespace the file engines browse
under, a value rather than a missing one.

The filters live in their own file rather than in `history` because a
`WHERE` clause is not a statement: the `H` pane would offer fragments it
cannot run, and one relation's filters would push statements out of the
shared cap.

`Model.recordFilter` prepends the clause to `Model.filters` and persists
it. A clause already recorded on the same relation **moves to the front**
instead of being stored twice — re-applying a filter would otherwise push
every other one down a slot per press — and that is why the write is
sometimes a full `SaveFilters` rewrite rather than an append: an append
can only add a line, never drop the older copy or the entry a relation
has grown past `MaxRelationEntries` (50 per relation, inside the
file-wide `MaxEntries` cap of 1000).

Recall is non-destructive: the first `ctrl+p` stashes the half-typed
clause as `draft`, and walking forward past the newest entry puts it
back.

A clause is recorded **before** it runs, so a fragment the engine rejects
is recallable and fixable — that is exactly the one worth having. An
empty clause (a clear) records nothing.

## Consequences

- The status line is hidden while the line is open. The two never need to
  be read at once: the status describes the page the clause is about to
  replace.
- `dataView.conds` is gone with the structured form, and so is
  `Model.applyFilterConds`. `db.BuildFilter` and `db.FilterCond` remain
  in `internal/db` with their tests but have no UI caller; a future
  "filter by this cell" action is the obvious use for them.
- The recall list is the *applied* filters, not every keystroke, so it
  stays short enough to walk. There is no pane for it — the one place a
  table's filters are worth reading is the line where the next one is
  typed.
- A broken or unreadable filter file costs `/` its recall list and
  nothing else; the failure is logged and the session keeps recording.
- The filter history is not part of the saved session
  ([session-restore](session-restore.md)): a restored session comes back
  with its filter *applied*, and the recall list is read from disk at
  startup regardless.

## See also

- [data-grid](data-grid.md) — the grid this line sits in, and the
  parse-first rule the clause goes through.
- [query-editor-and-history](query-editor-and-history.md) — the statement
  history this store mirrors, the pane that browses it, and the
  `Entry.Connection` scoping this narrows.
- [catalog-browsing](catalog-browsing.md) — the side panels' inline `/`
  fuzzy filter, the interaction this one now matches.
- [keybindings-single-source](keybindings-single-source.md) — why the
  four keys are bindings rather than literals in the update function.
