---
type: Design Decision
title: The :name parameter prompt — one form modal, an explicit NULL toggle, session-scoped memory
description: issue #152 — the bespoke paramsModal is replaced by the shared formModal (internal/ui/params.go), every placeholder gets a value field plus a NULL toggle because an empty input cannot say whether the empty string or SQL NULL was meant, db.ParamValue carries that distinction into BindPlaceholders as a nil argument, and the values a statement last ran with are remembered per statement text in an in-memory paramMemory (never on disk); detection stays sqlhl-tokenizer-based, so `::type`, quoted text and comments still never prompt.
tags: [tui, placeholders, prepared-statements, form-modal, null, sqlhl]
generated:
  by: claude-code/opus-5
  at: 2026-08-16T00:00:00Z
sources:
  - resource: https://github.com/TrueDaerk/lazysql/issues/152
    title: "Issue #152 — Support :name query parameters with a prompt modal"
---

# The `:name` parameter prompt

`?`/`:name` placeholders already prompted and bound before this
(see [history-pane-and-placeholders](history-pane-and-placeholders.md),
issue #34). Issue #152 is about the prompt itself: reuse the shared form
modal, make an empty field unambiguous, and stop asking for the same
values twice in one session.

## The prompt is the shared `formModal`, not a modal of its own

`paramsModal` (a hand-rolled stack of `textinput`s in
`historypane.go`) is gone; `newParamsForm` in `internal/ui/params.go`
builds a `formModal` instead. Everything the bespoke modal was
re-implementing — one cursor over labelled fields, `tab`/`↑↓`
navigation, `enter` submits, `esc` cancels, paste into the focused
field, a pinned box size that does not move while typing
([form-modal-stable-size](form-modal-stable-size.md)) — is already
there, and the piece the prompt actually needed, a checkbox, is its
`fieldBool`. The statement under prompt renders through the form's
`withBody` hook, syntax-highlighted, so a prompt opened from the
history pane or a snippet still says *which* query it is about.

One contract changed: the old modal made `enter` walk to the next
field and only run from the last one. The form's `enter` submits from
anywhere. Type-and-enter on a single-parameter prompt is unaffected,
and `tab` is the documented way to move.

Values are read back with `formField.input.Value()`, **not**
`formField.value()`: text fields trim, and a parameter's leading or
trailing spaces are part of the value in a way a hostname's are not.

## An empty field is not NULL — the toggle says which

`= ''` matches the empty-string row, `= NULL` matches nothing, and
`IS NULL` matches the null one. An empty input cannot express that
choice, so every value field is followed by a `↳ NULL` toggle
(`space` flips it; with it on, the text above it is ignored).

The distinction travels as a type: `db.ParamValue{Text, Null}` replaces
the plain `string` that `BindPlaceholders` used to take, and
`ParamValue.arg()` yields a nil `any` for NULL — which every driver
lazysql speaks binds as SQL NULL. The rewrite itself is unchanged: each
placeholder token becomes `Dialect.Placeholder(n)` and the value never
enters the statement text. `db.TextParams` lifts plain strings for
callers (and tests) that have no NULL to express.
`internal/db/placeholders_test.go` runs the empty-vs-NULL pair against
live SQLite **and** DuckDB connections, since "binds as a real
parameter" is a claim about the driver, not about the string we built.

## Last-used values, in memory, keyed by the statement text

`paramMemory` (`internal/ui/params.go`, held as `Model.params`, a
pointer so every copied `Model` shares one store) maps a statement to
the `ParamValue`s it last ran with, capped at 50 statements evicted
oldest-first. `submitQuery` reads it when opening the prompt and writes
it on submit — *before* the run, not after: a statement that failed on
the server is exactly the one whose values are worth getting back.

The key is the statement text exactly as typed. That makes a snippet
and a hand-typed copy of it share one memory for free, and it makes an
edited statement — which may have a different set of placeholders —
start clean instead of pre-filling fields that no longer line up. The
recalled slice is read positionally and index-guarded, so even a
mismatched entry can only under-fill the form, never mis-bind: what is
submitted is whatever the fields hold when `enter` is pressed.

It is deliberately **session-scoped**. The history and the snippets
store persist under `XDG_STATE_HOME`; parameter values are frequently
the one part of a query that is personal data — an email address, a
customer id, a token — and writing them next to the SQL would turn a
recall convenience into a data-retention decision nobody asked for.

## Detection is still the tokenizer's job

Nothing here re-reads the raw text. `db.ExtractPlaceholders` walks
`sqlhl.Tokenize` and keeps only `Placeholder` tokens spelled `?` or
`:name`, which is what keeps a `?` inside a string literal, a `:name`
in a comment or a quoted identifier, and PostgreSQL's `::` cast out of
the prompt.

`::` is the edge case worth naming: `foo::int` must not read as the
placeholder `:int`. The scanner special-cases it ahead of the
placeholder rule (`sqlhl.go`, the `case r == ':'` arm: a following `:`
emits a two-byte `Operator` and nothing else looks at it), so the cast
is *consumed*, not merely skipped — a scanner that only refused to
start a placeholder at the first `:` would still start one at the
second. Covered in `sqlhl_test.go` (the token kind) and in
`placeholders_test.go` (`SELECT a::text FROM t WHERE b = :b` prompts
for `:b` alone, and the cast survives the rewrite verbatim).

## Consequences

- `BindPlaceholders`'s signature changed from `[]string` to
  `[]db.ParamValue`; `TextParams` is the one-line adaptation for
  callers that never mean NULL.
- The prompt walks twice as many fields as there are placeholders. The
  form scrolls, so a statement with many parameters still fits a small
  terminal, and the toggle stays adjacent to the value it qualifies
  rather than living in a separate block.
- The command log requirement needed no work: `Driver.Logger()` already
  records args per statement and `logLine.render` already appends
  `-- args [...]`, so a NULL parameter shows as `<nil>` next to the
  statement that carried it.

## See also

- [history-pane-and-placeholders](history-pane-and-placeholders.md) —
  where detection and the prompt-and-bind step in `submitQuery` came
  from.
- [connection-form-modal](connection-form-modal.md) and
  [form-modal-stable-size](form-modal-stable-size.md) — the modal this
  prompt now reuses.
- [sql-syntax-highlighting](sql-syntax-highlighting.md) — the `sqlhl`
  scanner, including the `::` rule.
