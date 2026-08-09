---
type: Design Decision
title: SQL syntax highlighting, and why the editor draws itself
description: Why lazysql hand-rolled a SQL tokenizer instead of pulling in chroma, why the tokenizer never fails and emits gap-free tokens, and why the query editor renders its own gutter, wrapping and cursor instead of using the Bubbles textarea's View.
tags: [tui, query, editor, highlighting, tokenizer, textarea, theme]
generated:
  by: claude-code/opus-5
  at: 2026-08-09T00:00:00Z
sources:
  - resource: https://github.com/TrueDaerk/lazysql/issues/29
    title: "Issue #29 — SQL syntax highlighting in the query editor"
  - resource: https://pkg.go.dev/charm.land/bubbles/v2/textarea
    title: "Bubbles v2 textarea — the component lazysql keeps as its input model"
---

# SQL syntax highlighting

## Decision

`internal/sqlhl` is a hand-written SQL scanner. `internal/ui` renders the
query editor itself — gutter, wrapping, colours and cursor — and keeps
the Bubbles `textarea` purely as the input model. Every colour is a named
`[theme]` slot.

## 1. A tokenizer, not a dependency

Chroma is the obvious library, and it is the wrong trade here: it drags
in a lexer registry and a style system for one language and five token
kinds, in a binary that already carries DuckDB. The scanner that replaces
it is ~350 lines and does exactly what a highlighter needs.

It is a scanner, not a parser, and the difference is the point.
Highlighting runs on every keystroke over a buffer that is usually
half-written, so the scanner has two hard rules:

- **It never fails.** There is no error return. An unterminated quote or
  block comment runs to the end of the input, which is also what the eye
  expects while typing: after `SELECT '` everything *is* inside the
  literal.
- **It never loses a byte.** Tokens are contiguous — token *n* starts
  where token *n-1* ended, and the last one ends at `len(src)`. A test
  enforces this over deliberately broken input for every dialect.

That second rule is what makes `Kinds(d, src) []Kind` — one kind per rune
— a two-line function, and it is the only shape the renderer can use:
wrapping, slicing a display row and overlaying a cursor are all rune
operations, and none of them can be expressed in byte offsets.

## 2. What differs per dialect

A shared core keyword list plus per-dialect extras, and four lexical
rules that genuinely differ:

| | MySQL/MariaDB | PostgreSQL | SQLite | DuckDB |
|---|---|---|---|---|
| `"x"` | string literal | quoted identifier | quoted identifier | quoted identifier |
| `\` in `'…'` | escapes | literal backslash | literal backslash | literal backslash |
| `#` | line comment | operator | operator | operator |
| `/* /* */ */` | closes at the first `*/` | nests | closes at the first `*/` | nests |

Plus `$tag$…$tag$` and `$1` on PostgreSQL/DuckDB, `` `x` `` and `@var` on
MySQL, and `?` / `:name` / `:1` everywhere. The double-quote row follows
MySQL's default `sql_mode`, and is the same family of trap as
[reference/sqlite-double-quoted-strings](../reference/sqlite-double-quoted-strings.md);
the backslash row is
[reference/sql-literal-escaping](../reference/sql-literal-escaping.md)
seen from the lexer's side.

Keyword membership is a *highlighting* list, not a grammar: type names
are in it because `CREATE TABLE` reads better with them picked out, and
nothing downstream treats a word's presence as "reserved". An unknown
engine name falls back to the shared core rather than erroring —
highlighting is decoration, and an unrecognized connection should still
be readable.

## 3. The editor draws itself

The Bubbles `textarea` takes a plain string and styles it whole. There is
no hook for styling part of a line, so there were two ways in:
post-process its ANSI output, or keep it as the input model and draw the
buffer in lazysql. The second won: parsing back a component's escape
sequences to find where the gutter ends and which logical line a display
row came from is guesswork that breaks on the next release.

So the split is:

- **The textarea owns** the text, the cursor and every editing key.
  `Value()`, `Line()` and `Column()` are read back from it.
- **`internal/ui/highlight.go` owns** the line-number gutter, the
  wrapping, the colours and the cursor cell.

The one rule that keeps the two honest: **only one of them may wrap**.
If the textarea soft-wrapped at the panel width, its idea of "the row
above" would disagree with what is on screen, and `k` would jump
somewhere the user did not look at. `newQueryEditor` therefore clears its
`Prompt`, turns off `ShowLineNumbers` and pins its width at
`editorWrapWidth` (500 cells, the component's own maximum), so inside the
model every realistic SQL line is one unwrapped row and the cursor moves
one *logical* line at a time. Display wrapping is then entirely ours.

Three consequences worth naming:

- **Hard wrap, not word wrap.** Breaking on words moves the wrap point
  every time a word is typed. In an editor that is flicker; in code it is
  also wrong, since a query has no words to keep together.
- **The scroll offset is derived, never stored.** The window scrolls only
  as far as it must to keep the caret visible, computed from the buffer
  and the cursor. There is no scroll position that can drift out of sync
  with an edit made from somewhere else (`x` on the history panel, say).
- **The block is exactly the height it was asked for.** `editorBlock(w, h)`
  pads to `h` rows, so the main view stacks the result under it without
  measuring.

## 4. Colours are theme slots

`sql-keyword`, `sql-string`, `sql-number`, `sql-comment` and
`sql-placeholder` are `[theme]` names like every other colour (see
[design/configurable-keys-and-theme](configurable-keys-and-theme.md)), so
the highlighting follows a preset and can be overridden one slot at a
time. Delimited identifiers borrow `accent` rather than claiming a sixth
slot.

Identifiers and operators have **no** slot: they keep the terminal's own
foreground. That is not an omission — it is what makes the coloured
tokens stand out, and it keeps a `SELECT` over twenty columns from
turning into a wall of colour.

The cursor is drawn with reverse video in insert mode and with
`cell-cursor-bg` in normal mode. Reverse is used deliberately: it stays
legible over any token colour a theme can produce, which a fixed
background cannot promise.

## 5. Reuse

`highlightSQL(styles, dialect, line)` is the entry point for read-only
callers — the panel `[5]` preview uses it today, and the history pane is
the next one. It truncates first and highlights the result: styling and
*then* cutting would slice an escape sequence in half. The cost is that a
token cut by the truncation is re-read on its own, which at worst loses
the last word on a preview line its colour.

See also
[design/query-editor-panel](query-editor-panel.md) for the panel the
editor lives in, and
[design/query-editor-and-history](query-editor-and-history.md) for what
running the buffer does.
