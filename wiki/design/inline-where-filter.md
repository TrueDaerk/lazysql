---
type: Design Decision
title: Inline WHERE input with per-table filter history
description: Why `/` on the data grid types a WHERE clause into a line inside the grid instead of opening a popup, how the immutable `SELECT * FROM <relation> WHERE ` prefix is built and rendered, and how applied clauses are recalled per connection + relation out of the JSON Lines history store.
tags: [tui, db, main-view, filtering, history, keybindings, security]
generated:
  by: claude-code/opus-5
  at: 2026-08-12T00:00:00Z
sources:
  - resource: https://github.com/TrueDaerk/lazysql/issues/130
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

## The prefix is the textinput's prompt

`filterInput` (in `internal/ui/filterinput.go`) is a `textinput.Model`
whose `Prompt` is the statement prefix. That is what makes the label
immutable for free: a prompt is not part of the value, so it cannot be
selected, backspaced into or submitted, and the horizontal scrolling of a
long clause happens inside the value alone.

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

The caret does not blink: `Styles.Cursor.Blink` is off, so the line costs
no timer per keystroke and matches the editor's own static cursor.

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

## History: the same store, keyed

Applied clauses are recorded in
`${XDG_STATE_HOME:-~/.local/state}/lazysql/filters` —
`internal/history/filters.go`. It is deliberately **not** a new format:
same `history.Entry`, same JSON Lines file, same append-only write, same
owner-only mode, same compaction on load as the statement history next to
it. The scope rides in a new `Entry.Key`:

```go
history.Scope(conn, database, table) // "prod\x1fshop\x1forders"
```

The separator is the ASCII unit separator, so no connection, database or
relation name can spell another scope's key — a `prod/shop` connection
cannot impersonate the `shop` database of `prod`. An empty table names
the connection as a whole, which is the shape the query editor's history
will use when it becomes per-connection (the related issue): that history
only has to start writing a coarser `Scope` into the same field, rather
than growing a second store.

`Model.recordFilter` prepends the clause to `Model.filters` and persists
it. A clause already in the same scope **moves to the front** instead of
being stored twice — re-applying a filter would otherwise push every
other one down a slot per press — and that is why the write is sometimes
a full `SaveFilters` rewrite rather than an append: an append can only
add a line, never drop the older copy or the entry a scope has grown past
`MaxScopeEntries` (50 per scope, inside the file-wide `MaxEntries` cap of
1000).

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
  history this store mirrors, and the pane that browses it.
- [catalog-browsing](catalog-browsing.md) — the side panels' inline `/`
  fuzzy filter, the interaction this one now matches.
- [keybindings-single-source](keybindings-single-source.md) — why the
  four keys are bindings rather than literals in the update function.
