---
type: Design Decision
title: EXPLAIN view for the editor's statement
description: Why the query plan is a Driver method returning one dialect-agnostic Plan (tree, grid or preformatted text) instead of dialect-aware UI code, why it takes over the main view instead of opening a modal, how the statement under the caret is picked in a multi-statement buffer, and why ANALYZE is never sent.
tags: [tui, query-editor, explain, query-plan, db, keybindings]
generated:
  by: claude-code/opus-5
  at: 2026-08-09T23:30:00Z
sources:
  - resource: https://github.com/TrueDaerk/lazysql/issues/46
    title: "Issue #46 — EXPLAIN view for the current query (dialect-aware)"
---

# EXPLAIN view for the editor's statement

## Decision

`ctrl+e` in the query editor (either mode) asks the server how it would
run **one** statement and shows the plan where the editor was.

The dialect work sits entirely behind one new Driver method:

```go
Explain(ctx context.Context, sql string) (*Plan, error)
```

which delegates to an unexported `Dialect.explain`, exactly like the
introspection calls do. UI code never learns which prefix its engine
wants — see [reference/explain-per-dialect](../reference/explain-per-dialect.md)
for what each one actually needs.

## Why one Plan type with three renderings

The four engines produce three genuinely different kinds of answer: a
node tree (PostgreSQL's and MySQL's JSON, SQLite's id/parent rows), a
table (MySQL's classic `EXPLAIN`), and a finished ASCII diagram
(DuckDB's). Forcing all three into a tree would mean re-parsing DuckDB's
box art, which can only lose information; forcing all three into text
would mean the UI cannot indent, colour or scroll by node later.

So `db.Plan` carries a `Format` plus exactly one filled field —
`Nodes`, `Grid` or `Raw` — and answers `Lines()`. The UI asks for lines
and scrolls them. Adding a fifth engine adds a dialect method, not a UI
branch.

## Why ANALYZE is never sent

`EXPLAIN ANALYZE` executes the statement. A plan is something the user
asked to *look at*; a look that deletes rows is not a look. Only the
planning form is ever sent, which is what makes explaining a `DELETE` as
safe as explaining a `SELECT` — and why `ctrl+e`, unlike `ctrl+r`, needs
no unguarded-write confirm modal. An opt-in `EXPLAIN ANALYZE` would need
its own key *and* its own confirmation; it is deliberately not here.

## Why the main view, not a modal

The plan is long, wide and read side by side with the statement that
produced it. A modal would have to be nearly full screen to be useful,
and would then own every key — including the ones that get back to
editing. Instead the plan replaces the editor inside the main view while
panel `[5]` keeps the focus, the same shape the schema diff uses over
panel `[1]`.

That makes the return trip free: `esc` clears `Model.plan` and the
editor is back with its text, cursor and mode untouched — the buffer was
never written to in the first place. `i` and `ctrl+r` also fall through
and put the editor back, because both mean "I am done reading".

`updatePlanKeys` claims only the report keys (`j`/`k`, paging, `g`/`G`,
`y`, `esc`) and hands everything else to the editor's normal mode, so the
vim layer never sees a `j` that was meant as a scroll.

## Which statement gets explained

No engine explains a batch, and a script has as many plans as
statements. The buffer's statement **under the caret** is the one that
runs.

`db.SplitStatementSpans` is the existing dialect-aware splitter with rune
offsets kept, and `db.StatementAt` maps a caret offset onto a span: a
caret inside a statement picks it, and a caret in the whitespace or on
the `;` after one picks the statement it just left — which is where the
caret sits after typing a statement out. The rule is total: with at least
one statement in the buffer there is always an answer, so "multi-statement
buffer" never becomes an error case on its own. The empty buffer is the
only refusal.

## What is refused locally

Three things never reach the server, each with its reason in the command
log rather than a modal:

- no connection,
- an empty buffer,
- a statement with `?`/`:name` placeholders — there are no values to plan
  with, and opening the value prompt for a statement that is not going to
  run would misrepresent what the key does. The message names `ctrl+r`,
  which does bind them.

## Logging

Nothing here formats SQL for the log. The `EXPLAIN` runs through the
ordinary `querier`, so `Driver.Logger()` catches it exactly like a browsed
page or a committed changeset — see
[design/command-log-panel](command-log-panel.md). The view adds only its
own status notes ("explain SELECT on …", "plan for SELECT: 12 lines").

The plan is **not** written to the query history: the history is what the
user asked to run, and an explain is a question about a statement rather
than the statement itself.
