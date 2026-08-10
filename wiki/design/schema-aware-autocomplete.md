---
type: Design Decision
title: Schema-aware autocomplete in the query editor
description: Why the completion popup is a layer and not a modal, how it derives its list from a token scan instead of a parser, the ranking rule and what it optimizes for, the self-invalidating per-connection+database column cache that keeps Update non-blocking, and when a completed identifier is quoted.
tags: [tui, query, editor, completion, autocomplete, schema, cache, quoting, keybindings]
generated:
  by: claude-code/opus-5
  at: 2026-08-09T00:00:00Z
sources:
  - resource: https://github.com/TrueDaerk/lazysql/issues/30
    title: "Issue #30 — Schema-aware autocomplete in the query editor"
  - resource: https://pkg.go.dev/charm.land/lipgloss/v2#NewCompositor
    title: "lipgloss v2 layer compositor — the same mechanism the modals use"
---

# Schema-aware autocomplete

## Decision

The query editor completes SQL keywords, table and view names, and the
columns of the tables the buffer mentions. The popup is a **layer, not a
modal**; its suggestion list is **derived on every keystroke** from a
token scan of the buffer; the column metadata behind it lives in a cache
keyed by connection + database that **invalidates itself**; and every
fetch is a `tea.Cmd`, so `Update` never waits for a server.

## 1. A layer, not a modal

Every other popup in lazysql is a `modal`, and the routing order gives a
modal every key ([tui-shell-architecture](tui-shell-architecture.md)).
That is exactly wrong here: a completion popup exists *while the user is
typing*, and the typing is what narrows it. A modal would have to
re-implement the textarea to stay open.

So the popup is state on the model (`Model.completion`) rendered as a
second `lipgloss.NewLayer` in `View`, the same compositor call the modals
use, and it claims exactly four keys in `updateEditor` — before insert
mode's own keys, after nothing. Everything else still types.

The two can never collide: a modal takes keys before the editor sees one,
so `completionLayer` returns nothing while `m.modal != nil`, and there is
never more than one floating box on screen.

### Anchoring

The compositor places by absolute screen cell, and nothing in the nesting
`View` builds — side column, main column box, its border, the query
view's header line, the editor block, the wrapped display row the caret
sits on — knows where it ended up. `completionLayer` therefore walks the
same layout math `View` does, which is why `sideWidth`, `mainColumnRect`
and `commandLogHeight` were extracted out of `View` and
`renderMainColumn`: there is one copy of those numbers, used twice.

`renderEditor` returns the caret's row and column inside the block it
renders, so the anchor comes from the code that laid the rows out rather
than from a second guess at the wrapping. The caret's *cell* is not its
rune index — a wide rune before it takes two columns — which is why the
offset is measured with `lipgloss.Width` and not `len`.

`placePopup` then fits the box: one row below the anchor, slid left at
the right edge, flipped above when the bottom would cut it off. It is a
pure function and unit-tested as one, because with the editor capped at
half the main view the flip case is not reachable by resizing.

## 2. A token scan, not a parser

Which columns are worth offering depends on which tables the statement
touches, and finding that out does not require knowing SQL.
`sqlhl.Tokenize` already reads half-written SQL without failing (see
[sql-syntax-highlighting](sql-syntax-highlighting.md)), so
`referencedRelations` walks its `Ident`/`QuotedIdent` tokens and looks
each one up in the relation list. `FROM users u JOIN orders` finds both
without knowing what `FROM` or `JOIN` mean, and a *string literal*
spelling a table name does not count, because the tokenizer already told
us it is a string.

The costs are honest and small: an alias is not resolved, and a column
that happens to share a table's name buys one extra column list. Both are
cheaper than a parser that has to have an opinion about every dialect's
grammar.

The word under the caret is read the same way — backwards over identifier
characters, with two rules that come from SQL rather than from text
editing:

- A **dot** before the word makes it a member access. `customers.na`
  offers the columns of `customers` and nothing else — no keywords, no
  other tables. A delimited qualifier (`"my table".`) is read back to its
  matching opening quote.
- A run that **starts with a digit** is part of a number, not an
  identifier: `1.5` offers nothing and qualifies nothing.

## 3. Ranking

Filtering is case-insensitive substring; ordering is, in priority:

1. **prefix before substring** — typing `us` wants `users` above
   `campus_id`;
2. **schema before keywords**, by kind: column, table, view, keyword;
3. shorter first, then alphabetical, so a repeated keystroke never
   reshuffles the list.

Rule 2 is the opinionated one. A user knows `SELECT`; what they cannot
remember is whether the column is `customer_name` or `customerName`. The
keyword list is there for the times the popup is the only thing open, not
because keywords are what needs completing.

Columns carry the relation they came from as their detail text, and a
name two joined tables share collapses to one row — the first, i.e. the
highest-ranked source.

## 4. The cache, and why it invalidates itself

The `[2]` tree already caches the relation list of the browsed namespace, so
completion reads `Model.relations` directly: a second copy would be a
second thing to invalidate. Column names are the new state, and they are
keyed by **connection + database** — nothing less identifies a column
list, since two connections can hold a table of the same name and so can
two databases of one connection.

`syncSchema` compares that key against the model on every use and drops
the cache when it differs, instead of relying on `openDatabase`,
`resetBrowse` and every future third way of changing the namespace to
remember. It also bumps a generation counter, which is what makes a
reply for the namespace the user just left droppable rather than
repopulating the new one.

Three rules keep it from becoming a second source of stalls:

- **Nothing waits.** A miss starts a `tea.Cmd` and the popup opens on
  what is already cached, saying `loading columns…` while the rest is in
  flight. When the reply lands, `restackCompletion` rebuilds the list —
  carrying the selection across by name, because the user did not move.
- **A keystroke opens at most `maxSchemaFetch` (4) requests.** A
  twelve-way join would otherwise open twelve round trips on the first
  character typed; the rest are picked up by the next keystroke, which is
  soon enough for a popup.
- **A failed introspection is not retried.** It costs that relation's
  columns and a line in the command log; retrying it on every keystroke
  would cost the connection.

`explicit` is stored on the popup state rather than passed per call: a
`ctrl+space` on a one-character prefix would otherwise close itself the
moment the metadata it was waiting for arrived.

## 5. Quoting only where it is required

`quoteCompletion` inserts a bare identifier whenever a bare identifier
resolves back to itself, and quotes otherwise. Over-quoting is not free:
`"users"` in a buffer the user then edits by hand is noise, and a
backtick where none was needed is a portability trap.

It quotes when the name is not a plain `[A-Za-z_][A-Za-z0-9_]*` word,
when it collides with a keyword, and — **on PostgreSQL only** — when it
is not already lower-case, because Postgres folds an unquoted identifier
and a table created as `"Users"` would otherwise resolve to `users`.
MySQL, SQLite and DuckDB preserve what they were given, so mixed case
goes in bare there. The quoting itself is `Dialect.QuoteIdent`, so the
delimiter is the engine's own (see
[db-driver-abstraction](db-driver-abstraction.md)).

The keyword test uses the highlighter's set, which is deliberately
generous — it holds type names too, so `date` comes back quoted. That
asymmetry is on purpose: quoting an identifier that did not need it is
still valid SQL, and the other mistake is a syntax error.

## 6. One keyword list, two consumers

`sqlhl.Keywords(dialect)` and `sqlhl.IsKeyword(dialect, word)` export the
set the highlighter already matches against. A word worth colouring is a
word worth offering, and keeping one list means adding a dialect's
keyword gains both at once — a test asserts every offered word is one the
highlighter colours.

Which is also what makes the per-dialect difference fall out for free:
`QUALIFY` is offered on DuckDB and not on MySQL, `AUTO_INCREMENT` the
other way round.

## Keybindings

| Action name | Default | Meaning |
|---|---|---|
| `complete` | `ctrl+space`, `tab` | open the popup |
| `complete-next` | `↓`, `ctrl+n` | next suggestion |
| `complete-prev` | `↑`, `ctrl+p` | previous suggestion |
| `accept-completion` | `enter`, `tab` | insert the selection |
| `close-completion` | `esc` | close the popup only |

`esc` is the one that matters. It closes the popup and touches nothing
else — not the buffer, not insert mode, not the focus — which is why
`close-completion` is a binding of its own rather than a third meaning of
`back`, exactly as `leave-insert` is
([query-editor-panel](query-editor-panel.md)).

`tab` is bound to both open and accept. Open wins when the popup is
closed and there is a word before the caret; accept wins when it is open;
and with nothing but whitespace before the caret `tab` stays an ordinary
tab, so indentation still works. `ctrl+@` is bound alongside
`ctrl+space` because that is what some terminals send for it.

The four popup keys live in `keyMap.editorCompletion()`, the sibling of
`editorInsert()`: both are dispatched by `updateEditor` rather than
through `panelActions`, because they only mean anything inside the
buffer. The options bar shows whichever group is live, `?` lists both as
extra groups for `panelQuery`, and `TestEveryDocumentedKeyIsBound` was
taught that these two groups are bound without being actions.

## Consequences

- **`esc` in the editor now takes one more press** when the popup is
  open. That is the issue's requirement and the behaviour of every
  editor with completion, but it does change a documented flow, so
  `TestEscLeavesInsertModeAndKeepsTheBuffer` asserts the new sequence.
- **The auto-trigger is two characters.** One matches most of a schema
  and would make the popup a permanent fixture; `ctrl+space` still opens
  it on nothing at all.
- **The popup shows at most 8 rows** of at most 200 ranked items. The
  scroll window is derived from the cursor, not stored — the same rule
  the editor's own scroll follows.
- Aliases are not resolved: `FROM customers c` then `c.` offers nothing,
  because `c` is not a relation. Resolving aliases needs the `FROM`
  clause, i.e. a parser, and is a separate change.

## See also

- [query-editor-panel](query-editor-panel.md) — the panel and the
  normal/insert split the popup's keys sit inside.
- [sql-syntax-highlighting](sql-syntax-highlighting.md) — the tokenizer
  the buffer scan and the keyword lists come from.
- [catalog-browsing](catalog-browsing.md) — the relation list completion
  reuses instead of caching twice.
- [keybindings-single-source](keybindings-single-source.md) — the table
  the five new actions were added to.
- [reference/lipgloss-v2-sizing](../reference/lipgloss-v2-sizing.md) —
  why the layer coordinates are computed from total block sizes.
