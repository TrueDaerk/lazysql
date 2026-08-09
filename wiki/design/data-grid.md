---
type: Design Decision
title: Paginated data grid with sorting and quick filter
description: How the main view browses a relation one page at a time, how the cell cursor and the column window work, and why the quick filter is parsed before it reaches SQL.
tags: [tui, db, main-view, pagination, sorting, filtering, security]
generated:
  by: claude-code/opus-5
  at: 2026-08-09T00:00:00Z
---

# Data grid

## Decision

The main view's **Data** tab renders exactly one page of one relation.
`dataView` (in `internal/ui/data.go`) holds that page plus everything
that shaped the query behind it: `page`, `filter`, `sort`, the cell
cursor `(row, col)`, and a `req` sequence number.

### Only the page is fetched

Opening a relation, turning a page, sorting and filtering all funnel
through `Model.reloadPage`, which issues **two** independent commands:

- `loadPageCmd` → `pageLoadedMsg` — `SELECT * … LIMIT 100 OFFSET n`
- `countRowsCmd` → `rowCountMsg` — `SELECT COUNT(*) …` behind the same
  filter

The count is a separate round trip on purpose: the grid renders as soon
as the page lands, and the status line grows its `of ~N` part later. A
slow or failing count costs the total and nothing else — browsing keeps
working. `dataPageSize` is 100, so a 100k-row table costs the same as a
100-row one: the model never holds more than one page.

Both replies carry `req` plus the connection and table they were started
for. `Model.fresh` drops anything that no longer matches, so a page the
user has already sorted or paged past cannot overwrite the current one.

### Sort, filter and paging compose

`filter` and `sort` live next to `page` in the same struct, and
`reloadPage` always rebuilds the statement from all three. So a filter
survives paging and sorting; a sort survives paging. Changing the filter
or the sort resets `page` to 0, because an offset into a differently
ordered or differently sized result means nothing.

`s` cycles the column under the cursor ASC → DESC → unsorted, marked in
the header with `▲`/`▼`.

### Scroll windows are derived, not stored

Both the visible row range and the visible column range are computed
from the cell cursor at render time (`rowWindow`, `columnWindow`) rather
than kept in the model. `View` has a value receiver, so a window stored
during rendering would be thrown away anyway — and deriving it means a
resize can never leave a stale offset behind. The rule is the usual one:
anchored at the start until the cursor passes the last visible slot,
then following the cursor.

Column widths come from the *whole page*, not the visible rows, so
scrolling vertically never makes the grid jitter sideways. Widths are
clamped to `[4, 32]` cells and over-long cells are truncated with an
ellipsis; `v` opens the untruncated value in a scrollable modal, which
pretty-prints it when it is a JSON object or array.

`NULL` renders as a dim `NULL`, which is what distinguishes it from the
string `"NULL"`.

### Borders are drawn per row, not a lipgloss `Table`

Columns are separated by a `│` and the header is set off from the data by
a `─`/`┼` rule (`internal/ui/datagrid.go`, `gridHeader`/`gridRow`). Both
are rendered by hand, one row at a time, rather than adopting lipgloss's
`Table` component: the grid already has its own paging (`rowWindow`,
`columnWindow`) and per-cell state (cursor, NULL, staged edit, pending
delete/insert) that `Table` does not model, and `Table` renders its whole
body from a data matrix up front rather than a window of already-styled
strings.

The separator occupies exactly the `colGap` cell that used to be a plain
space, so column width math (`minColWidth`/`maxColWidth`, `columnWindow`)
needed no change. `gridHeader` grew from two lines (name, type) to three
(name, type, rule); `dataContent`'s `bodyRows` budget shrank by one line
to match.

Both the separator and the rule are styled with `gridSeparator`, which
resolves to `colorMuted` — the same dim tone as a blurred panel border —
so they recede behind the data. Reusing `colorMuted` rather than adding a
dedicated palette slot means the separator already tracks the `[theme]`
config's `border-blurred` override and looks right in both the `default`
and `light` presets with no extra configuration. The cursor row's
separator is wrapped in `rowCursor` instead, the same way the old space
gap was, so the row's background tint stays contiguous under the bar.

### The main view is a focus target

Focus is still a single `panelID`, but the enum gained
`panelMain = panelCount`: a value that takes focus like a side panel yet
has no number, no `Model.panels` entry and no slot in the side column.
That keeps one keybinding table, one options bar and one `?` listing
covering the grid too. `enter` on `[3] Tables` opens the relation and
moves focus there; `esc` pops the focus stack straight back.

Any code indexing `Model.panels` or `panelHeights` by focus has to guard
`m.focus < panelCount` — half screen mode, for instance, expands the
focused *side* panel and keeps the even split when the grid is focused.

## Quick filter: parse first, interpolate last

`f` prompts for a WHERE fragment. Interpolating that into the statement
is the injection shape lazysql must not have, so `db.ParseFilter` first
tries to take the fragment apart into a chain of
`column <op> <literal>` terms joined by `AND`. What it recognises it
rewrites: identifiers through `Dialect.QuoteIdent`, values into
`Dialect.Placeholder(n)` with the value bound as a query parameter.

Recognised: `= <> != > >= < <= LIKE NOT LIKE ILIKE` against a quoted
string, integer, float or boolean, plus `IS [NOT] NULL`. Bare, double-
quoted and backtick-quoted identifiers all work.

Anything else — parentheses, `OR`, function calls, `IN`, `BETWEEN`,
column-to-column comparisons, `= NULL`, an unterminated literal — is
kept **verbatim**: it still runs, because a quick filter that refuses
half of SQL is useless, but `Filter.Verbatim` is set and both the
command log (`-- WARNING: filter … could not be parameterized`) and the
grid's status line (`where (verbatim) …`) say so.

`col = NULL` is deliberately *not* rewritten: it is never what the user
means, and silently binding it would hide the mistake.

## Command log

`reloadPage` logs both statements it executes, with bound arguments
spelled out after them (`… -- args [100]`), and records the page
`SELECT` in `[4] Query history`. The verbatim warning is logged *before*
the statement it is about, so it cannot scroll out of sight above it.

## Consequences

- Inline editing (a later issue) reuses `dataView.row/col` unchanged;
  the cursor is model state precisely so it can.
- `COUNT(*)` on a very large unindexed table can be slower than the page
  query. It is asynchronous and its failure is non-fatal, but the status
  line can lag behind the grid by a moment on such tables.
- `Driver.QueryPage` takes a `*db.Filter` rather than a string, so the
  parameterization decision is made once, in one place, and cannot be
  bypassed by a caller assembling its own WHERE text.

## See also

- [catalog-browsing](catalog-browsing.md) — how the relation reaches the
  grid in the first place.
- [db-driver-abstraction](db-driver-abstraction.md) — the `Driver` and
  `Dialect` interfaces this builds on.
- [../reference/sqlite-double-quoted-strings](../reference/sqlite-double-quoted-strings.md)
  — why a filter on a misspelled column silently matches nothing there.
