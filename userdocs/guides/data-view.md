# The data view

++enter++ on a table opens the **Data** tab: one page of rows, with a cell
cursor you can move in both directions.

Focus matters here — the grid is the main view, and its keys only act while
the main view has the focus. ++tab++ reaches it; ++esc++ leaves.

## Paging

A page is **100 rows**. The grid holds exactly one at a time, which is what
makes a 100 000-row table as cheap to open as a 100-row one.

| Key | Action |
|---|---|
| `ctrl+f` / `pgdn` | Next page |
| `ctrl+b` / `pgup` | Previous page |

The page query and its `COUNT(*)` run as two independent statements, so a slow
count never holds up the rows. Both appear in the
[command log](../concepts/command-log.md).

The wheel scrolls rows but stops at the page boundary: turning a page is a
query, and that stays an explicit key.

## Moving the cursor

| Key | Action |
|---|---|
| `j` / `k`, `↑` / `↓` | Move the row cursor |
| `h` / `l`, `←` / `→` | Move the cell cursor across columns |

## Sorting

`s` sorts by the cursor column, cycling **ASC → DESC → off**. The sort is part
of the page query, so it orders the whole table, not the visible page.

## Filtering rows

`/` (or `f`) opens an input line at the bottom of the grid — not a popup. The
line is labelled with the statement your clause goes into:

```text
SELECT * FROM "orders" WHERE ▏
```

That label is not editable: the relation is quoted per dialect, and what you
type is the `WHERE` clause and nothing else.

| Key | In the filter line |
|---|---|
| ++enter++ | Apply |
| ++esc++ | Cancel — the grid is left exactly as it was |
| `↑` / `ctrl+p` | Previous filter for this table |
| `↓` / `ctrl+n` | Next filter |
| `F` | (in the grid) Clear the filter |

An empty clause clears the filter, as does `F`.

### Your clause is SQL, but its values are parameters

lazysql takes the clause apart into comparisons whose values travel as **bound
query parameters** — so a quote or a `%` inside a literal is data, never SQL.
Anything it cannot take apart (`IN`, `OR`, parentheses, a subselect) runs
verbatim and is flagged `where (verbatim)` in the status line, with a warning
in the command log.

The active filter is shown in the status line too, and paging, the row count
and every export respect it.

### Filter history

Applied filters are remembered **per connection and table** in
`${XDG_STATE_HOME:-~/.local/state}/lazysql/filters`, so `↑` / `ctrl+p` walk
what you filtered this table by before — including in the next session.
Walking forward past the newest entry gives back the clause you were half-way
through typing.

## Looking at one value

`v` opens the **cell detail popup**: the full value, JSON as a collapsible
tree, a BLOB as a hex dump. The title names the column's type and the value's
byte length.

| Key | In the popup |
|---|---|
| `j` / `k` | Scroll |
| `ctrl+d` / `ctrl+u` | Scroll half a screen |
| `y` | Copy the **raw** value — never the rendering |
| ++esc++ | Close |

A JSON or JSONB value is not a wall of indented text: it opens as a tree you
fold and unfold, so a large document reads as an overview you drill into.

```
▾ {
    "id": 4711
  ▾ "customer": {
      "name": "ACME GmbH"
    ▸ "address": {…} 5 keys
  ▸ "items": […] 40 items
```

The top two levels open, everything deeper starts folded, and a folded node
says what it hides (`{…} 5 keys`, `[…] 40 items`). Navigation is the `[2]
Objects` tree's: `j` / `k` between visible nodes, `enter` or `l` to expand
(again to step into the node), `h` to collapse or step back out to the
parent. `y` still copies the **raw** JSON, not the tree.

## Looking at one row

`x` opens the **row detail** view: the cursor row as a scrollable
name / type / value list instead of the grid's horizontal slice — psql's `\x`,
for a table too wide to read a row of at a glance. Staged edits, staged
inserts and `NULL` are marked as they are in the grid, and `v` opens the cell
popup from there.

## Following foreign keys

Columns that take part in a foreign key are marked `⇒` in the grid header.

| Key | Action |
|---|---|
| `g` | Follow the cursor column's foreign key to the referenced row |
| `G` | List the rows referencing this one, and jump to them |
| `ctrl+o` | Back to the previous table, filter and cursor |

`g` opens the referenced table filtered to the referenced row. A composite key
contributes one condition per column pair, and every value is bound as a query
parameter. A `NULL` cell has no target, so the key does nothing and says why in
the command log.

`G` goes the other way: it scans the namespace's foreign keys once — cached
afterwards — and lists the tables that reference the row under the cursor.

Both directions push the page they came from onto a **jump history**.
`ctrl+o` walks it back, restoring the table, its filter, its sort and the cell
cursor. ++esc++ does the same while there is history left, before it gives up
the grid's focus.

## Selecting rows

| Key | Action |
|---|---|
| `ctrl+v` (or `V`) | Start a selection anchored at the cursor row |
| `j` / `k` | Extend it |
| `shift+↑` / `shift+↓` (or `K` / `J`) | Anchor and extend in one key |
| `shift+←` / `shift+→` (or `C`, then `h` / `l`) | Narrow it to a block of columns |
| `ctrl+c` | Copy the selection — the copy menu, scoped to it |
| `e` | Edit the cursor column in **every** selected row |
| ++esc++ | Clear it |

With a column block up, the status line reads `N rows × M columns selected`,
only the block is tinted, and the copy scopes carry only those columns — the
CSV header, the JSON keys and the `INSERT` column list all shrink with it.

The selection is dropped by anything that replaces the rows under it — a
reload, a sort, a filter, a page turn — so a staged edit can never land on a
row that was never picked.

!!! tip "If `shift`+arrows do nothing"
    That is the terminal, not the app — macOS Terminal.app cannot report them.
    `V`, `K`, `J` and `C` are the equivalents that work anywhere. See
    [Terminal setup](../getting-started/terminal-setup.md#shiftarrows).

## The main-view tabs

`<` / `>` cycle **Data → Structure → Indexes → DDL → Relations** and back.
`[` / `]` and `,` / `.` are bound to the same actions for other keyboard
layouts. Selecting another relation keeps the tab you were on.
