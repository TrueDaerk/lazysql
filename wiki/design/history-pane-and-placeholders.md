---
type: Design Decision
title: The floating history pane and placeholder execution
description: Why panel [4] Query history became a floating pane opened from the editor's normal mode, how ?/:name placeholders are detected with the sqlhl tokenizer instead of a regex, why the prompt-and-bind step lives in submitQuery rather than in the pane, and how the entered values reach the driver only as bound parameters rewritten to the dialect's marker.
tags: [tui, history, placeholders, prepared-statements, sql-injection, modal]
generated:
  by: claude-code/fable-5
  at: 2026-08-09T00:00:00Z
sources:
  - resource: https://github.com/TrueDaerk/lazysql/issues/34
    title: "Issue #34 — Replace history panel with floating pane and placeholder execution"
---

# The floating history pane and placeholder execution

> **Keymap superseded** by
> [query-editor-ux-rework](query-editor-ux-rework.md) (issue #88): the
> pane opens with `H` (`backspace` remains an alias), `enter` now
> *loads* the selection into the editor and `r` runs it, and the pane's
> keys are keyMap bindings (`hist-*` override names) rendered into its
> footer and `?`. The placeholder detection and prompt-and-bind flow
> below are unchanged.

## Decision

Panel `[4] Query history` is gone. The history is a **floating pane**
(`historyModal` in `internal/ui/historypane.go`), opened with
`backspace` from the query editor's normal mode: an always-visible
panel was overkill for a recall list, and its removal renumbers the
editor to `[4]` (`jump` is now `1`–`4`). The storage
(`internal/history`, JSON Lines, newest first) is untouched; every
executed statement is still appended through `historyEntryMsg`.

In the pane, `enter` **executes** the selected entry, `e` loads it into
the editor, `d` deletes it (on disk too), `esc` closes. Rows are
`time  flattened SQL`, syntax-highlighted; the selected entry's full
statement, engine and timestamp render in a detail area below the list.

## The pane is a modal, not a layer of its own

It reuses the `modal` interface: the root already routes every key to
an open modal and composites it centered, and the pane needs exactly
that — plus the replacement-modal rule (`shouldClose && m.modal == cur`),
which is what lets `enter` swap the pane for the parameter prompt or
the unguarded-write confirm in one keystroke. Like every modal it
renders from its own snapshot of the entries; `d` mutates both the
snapshot and `m.history`, matching the entry **by value** (SQL, engine,
timestamp) because the model may have recorded new statements since the
snapshot shifted the indexes.

## Placeholder detection is tokenizer-based

`db.ExtractPlaceholders` walks `sqlhl.Tokenize` and keeps only
`Placeholder` tokens spelled `?` or `:name`. That inherits every
lexical rule the highlighter already got right: `?`/`:name` inside
string literals, comments and quoted identifiers are text, `::type` is
an operator (the PostgreSQL cast), `$1` is already-parameterized SQL
and MySQL's `@var` is server state — none of them prompt. A regex would
have to re-learn dollar quoting, nested block comments and `''`
escapes; the tokenizer never fails and never loses a byte, which is
exactly the contract extraction needs.

Named placeholders deduplicate in order of first appearance: `:a, :b,
:a` prompts twice and binds `:a`'s one value at both positions.

## Prompt-and-bind lives in `submitQuery`, not in the pane

`submitQuery` checks a **single-statement** script for placeholders and
opens `paramsModal` (one input per placeholder) before vetting and
running. Because every run path funnels through `submitQuery`, the
prompt works identically from the history pane, `ctrl+r` in the editor
and `R`/`enter` re-run on a result grid — and a re-run of a placeholder
statement re-prompts instead of failing on a bare `?`. Multi-statement
scripts skip the prompt: one value set cannot span statements, so they
run as written, exactly as before.

On confirm, `db.BindPlaceholders` rewrites each placeholder token to
`Dialect.Placeholder(n)` — `?` for MySQL/SQLite/DuckDB, `$n` for
PostgreSQL — and returns the values as an argument list. **The entered
values never enter the statement text**; they travel to
`Driver.QueryLimit`/`Exec` as bound parameters, so a value like
`'; DROP TABLE t;--` is data (covered by tests in
`internal/db/placeholders_test.go`). A repeated `:name` appends its
value once per position rather than reusing `$n`, which keeps the
rewrite correct for the `?`-style dialects that cannot repeat a marker.

The worker's `queryStmtMsg.sql` reports the statement **as typed**
(placeholders and all): that is what the history re-appends and the
Data tab shows, while the command log gets the rewritten SQL with its
args from the driver's `Logger` — the audit trail shows what actually
ran.

## Consequences

- Digits and `tab` cycle four panels with no gap; the editor's options
  bar and `?` gained `backspace  query history`, and the `[keys]`
  action names `load-query`, `run-query`, `delete-history` and
  `clear-history` are gone (a config overriding them now fails
  startup, the same as any unknown action); `history` is the new
  overridable opener.
- `enter` changed meaning relative to the old panel (it loaded, `x`
  ran). In a deliberate pane reached from the editor, executing is the
  point; loading moved to `e`.
- The old panel's `/` fuzzy filter and `D` clear-all did not move into
  the pane; `d` deletes one entry at a time.

## See also

- [query-editor-and-history](query-editor-and-history.md) — what a run
  does once it starts; the history file format.
- [query-editor-panel](query-editor-panel.md) — the editor panel the
  pane opens from, now `[4]`.
- [sql-syntax-highlighting](sql-syntax-highlighting.md) — the `sqlhl`
  scanner the detection reuses.
