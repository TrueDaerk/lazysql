---
type: Design Decision
title: Paginated data grid with sorting and row filtering
description: How the main view browses a relation one page at a time, how the cell cursor and the column window work, and how the row filter binds every value as a parameter in both its structured and its free-text mode.
tags: [tui, db, main-view, pagination, sorting, filtering, security]
generated:
  by: claude-code/opus-5
  at: 2026-08-09T00:00:00Z
updated:
  - by: claude-code/sonnet-5
    at: 2026-08-12T00:00:00Z
    note: column window now takes a remembered offset so leftward cursor moves scroll minimally (issue #126)
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

### The row window is derived, not stored — the column window can't be

The visible row range is computed from the cell cursor at render time
(`rowWindow`) rather than kept in the model: anchored at the top until the
cursor passes the last visible row, then following it. That rule works
because it is a pure function of the cursor alone — for a given cursor row
there is only one right answer for where the page-window starts.

The column window cannot be purely derived the same way. Minimal-scroll
means: while the cursor is inside the current window, the window does not
move — including leftward moves, which is the part issue #126 regressed.
But "is the cursor still inside the current window" needs the
current window's left edge as an input, and for a given cursor column more
than one window can legally contain it (e.g. cursor 2 fits both `[0,3)` and
`[1,4)`); which one is right depends on where the window was a moment ago,
not on the cursor value in isolation. So `columnWindow(cols, cursor, off,
w)` takes the previous left edge `off` (`dataView.colOff`) as an explicit
argument: it is reused unchanged while `cursor` is still inside `[off,
end)`, pulled left to `cursor` when the cursor passed the left edge, and
pushed right only as far as needed when the cursor passed the right edge.

`View` has a value receiver, so it cannot persist `colOff` back into the
model itself. `Model.clampCursor` — the single choke point every cursor-
or page-changing action already runs through — calls `syncColOff` after
clamping, which re-derives the grid's column widths and content width and
recomputes `colOff` the same way the next render will. That keeps the
stored offset in step with what's on screen without View ever mutating
state. `columnWindow` still re-clamps `off` against the current `cols`/`w`
on every call (`0 <= off <= cursor`, then re-packed to fit `w`), so a
stale offset — left over from a wider terminal, or a column list that
shrank — can never leave the cursor, or the window itself, out of bounds.

Column widths come from the *whole page*, not the visible rows, so
scrolling vertically never makes the grid jitter sideways. Widths are
clamped to `[4, 32]` cells and over-long cells are truncated with an
ellipsis; `v` opens the untruncated value in a scrollable modal — see
[cell-detail-popup](cell-detail-popup.md) for how it picks between
pretty-printed JSON, a hex dump and plain text.

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
covering the grid too. `enter` on a table in `[2] Objects` opens the relation and
moves focus there; `esc` pops the focus stack straight back.

Any code indexing `Model.panels` or `panelHeights` by focus has to guard
`m.focus < panelCount` — half screen mode, for instance, expands the
focused *side* panel and keeps the even split when the grid is focused.

## Row filter: a modal with two modes

`/` (`f` still works) opens `filterModal`, and `F` clears whatever it
applied. The modal has two modes because filtering has two audiences,
and one popup covers both:

**Structured** (the default) is three fields — column, operator, value —
over `formModal`, the same multi-field popup the connection editor uses.
The column select starts on the column the cell cursor sits on, which is
almost always the one meant, so a wide table costs no cycling. The
operators are `=`, `!=`, `<`, `>`, `<=`, `>=`, `LIKE`, `IS NULL` and
`IS NOT NULL`; the value field hides itself for the two NULL tests,
since they bind nothing. With a structured filter already active a
fourth field offers to `AND` the new condition onto it (`dataView.conds`
keeps the conditions for exactly that); the conditions of a fragment
typed as free-form SQL cannot be taken apart, so the toggle is not
offered on top of one.

`db.BuildFilter` turns the conditions into the `Filter`: identifiers
through `Dialect.QuoteIdent`, every value into a `Dialect.Placeholder`
bound as a query parameter. A value can therefore never be SQL — a
quote does not end a literal and `%` is a wildcard only under `LIKE`.

What a value binds *as* is decided by the column's declared type
(`db.typeClass`), not by sniffing the text: `intcol = $1` has to reach
PostgreSQL with an integer parameter, and `textcol = $1` with a string,
or the engine rejects the comparison. `LIKE` is the exception — its
pattern is text even against a numeric column. Type names are matched
whole after the length and any trailing modifier are cut off, because a
substring test would read PostgreSQL's `point` and `interval` as
integers. An unknown type falls back to sniffing a number; a value the
column's type cannot hold is reported on the modal's error line, which
keeps the form open rather than sending a statement the engine would
refuse with a worse message.

**Advanced** (`ctrl+t`) replaces the three fields with one free-text
`WHERE` fragment — the mode the filter used to be, unchanged, described
below. Applying it drops the structured conditions, because they are no
longer what the grid shows.

## The advanced fragment: parse first, interpolate last

Interpolating a typed fragment into the statement
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
- The structured mode offers only `AND`. `OR`, `IN`, `BETWEEN` and
  parentheses stay the advanced mode's business, where they run verbatim
  and are flagged as such — a condition builder that grew a boolean tree
  would be a query editor, and there already is one.
- The column list comes from the introspected metadata when the
  Structure tab has loaded it and from the page's own result columns
  otherwise. The fallback carries whatever type names the driver reports
  for the result set, which can be less precise than the declared ones —
  an unknown type only costs the value the type-directed binding and
  falls back to sniffing.

## See also

- [cell-detail-popup](cell-detail-popup.md) — what `v` renders and how
  it decides.
- [row-detail-view](row-detail-view.md) — `x`'s expanded name/type/value
  list for the whole cursor row.
- [foreign-key-navigation](foreign-key-navigation.md) — `g`/`G`/`ctrl+o`,
  the `⇒` header mark and the jump history layered on this grid.
- [catalog-browsing](catalog-browsing.md) — how the relation reaches the
  grid in the first place.
- [db-driver-abstraction](db-driver-abstraction.md) — the `Driver` and
  `Dialect` interfaces this builds on.
- [../reference/sqlite-double-quoted-strings](../reference/sqlite-double-quoted-strings.md)
  — why a filter on a misspelled column silently matches nothing there.
