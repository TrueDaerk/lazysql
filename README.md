# lazysql

A lazygit-style terminal UI for managing SQL databases. Browse connections, inspect tables, edit data, and run queries — all with single-key, context-sensitive bindings.

Supported databases: **MySQL**, **MariaDB**, **PostgreSQL**, **SQLite**, **DuckDB**.

## Why

GUI database clients are heavy; raw CLI clients (`mysql`, `psql`) are fast but clumsy for browsing and editing. lazysql brings the lazygit workflow to databases: numbered side panels, a large detail view, mnemonic keys, and modal popups for anything interactive.

## Features (planned)

- **Connection manager** — add/edit/delete/test connections; passwords stored in the OS keyring, never in plain text.
- **Browse** — databases/schemas, tables and views, with fuzzy filtering.
- **Data view** — paginated rows, column sorting, quick `WHERE` filters.
- **Structure view** — columns, indexes, foreign keys, and generated DDL.
- **Relations view** — a per-table map of incoming and outgoing foreign keys, walkable with `enter`.
- **Schema diff** — compare the schemas of two connections (`D` on a connection): tables, columns, indexes and foreign keys, with type synonyms normalized within one engine family.
- **Edit** — change single cells inline; insert, duplicate, and delete rows. All mutations are *staged* first (lazygit-style) and only applied on explicit commit — with rollback.
- **Query editor** — free-form SQL with dialect-aware syntax highlighting, schema-aware autocomplete, history, result tabs, and cancellation.
- **Copy & export** — cell, row, or whole table as CSV, JSON, or `INSERT` statements to clipboard or file; a table's DDL, or a whole database's DDL in foreign-key dependency order, to a `.sql` file.
- **Command log** — every SQL statement the app executes is visible.
- **SSH tunnels** — connect to remote databases through a jump host.
- **Configurable** — keybindings and theme via config file.

## UI layout

```
┌─[1] Connections──┐┌─Main: Data | Structure | DDL ─────────────┐
│ ▸ local-mariadb  ││ id │ name    │ email                      │
│   prod-pg        ││ 1  │ alice   │ alice@example.com          │
├─[2] Databases────┤│ 2  │ bob     │ bob@example.com            │
│ ▸ shop           ││ …                                         │
├─[3] Tables───────┤│                                           │
│ ▸ users          ││                                           │
│   orders         │├─Command log───────────────────────────────┤
├─[4] Query────────┤│ SELECT * FROM users LIMIT 100;            │
│ SELECT * FROM u… ││                                           │
│                  ││                                           │
└──────────────────┘└───────────────────────────────────────────┘
 1-4 jump  tab cycle  enter open  e edit  d delete  ? help  q quit
```

## Key bindings (core)

| Key | Action |
|-----|--------|
| `1`–`5` | Jump to panel |
| `tab` / `shift+tab` | Cycle panels |
| `j`/`k`, `↑`/`↓` | Move within panel |
| `enter` | Drill in / open |
| `esc` | Back / cancel |
| `e` | Edit cell |
| `d` | Delete row (confirm) |
| `n` | New (connection/row, context-sensitive) |
| `y` | Copy (cell/row/table submenu) |
| `:` | Focus the SQL query editor, panel `[4]` |
| `c` | Commit staged changes |
| `?` | Help / all bindings |
| `q` | Quit |

In the data grid (`enter` on a table, `esc` back):

| Key | Action |
|-----|--------|
| `h`/`l`, `←`/`→` | Move the cell cursor between columns |
| `ctrl+f` / `ctrl+b`, `pgdn`/`pgup` | Next / previous page |
| `s` | Sort the cursor column (ASC → DESC → off) |
| `/` (or `f`) | Filter rows (modal) |
| `F` | Clear the filter |
| `v` | Cell detail popup: full value, JSON pretty-printed, BLOBs as a hex dump (`j`/`k`, `ctrl+d`/`ctrl+u` to scroll, `y` to copy the raw value, `esc` to close) |
| `g` | Follow the foreign key of the cursor column to the referenced row |
| `G` | List the rows referencing this one and jump to them |
| `ctrl+o` (or `esc`) | Back to the previous table, filter and cursor |
| `[` / `]` | Previous / next main-view tab (Data, Structure, Indexes, DDL, Relations) |

`/` opens the filter modal on the column under the cursor: pick a column,
an operator (`=`, `!=`, `<`, `>`, `<=`, `>=`, `LIKE`, `IS NULL`,
`IS NOT NULL`) and a value. The value is always bound as a query
parameter and the column name is quoted per dialect, so quotes and `%` in
it are data, never SQL. With a filter already active the modal offers to
`AND` the new condition onto it instead of replacing it. `ctrl+t` inside
the modal switches to advanced mode: a free-form `WHERE` fragment, which
is parameterized where it can be and flagged `where (verbatim)` in the
status line where it cannot. The active filter is shown in the grid's
status line, paging and the row count both respect it, and `F` clears it.

Columns that take part in a foreign key are marked `⇒` in the grid
header. `g` on one of them opens the referenced table filtered to the
referenced row; a composite key contributes one condition per column
pair, and every value is bound as a query parameter. A NULL cell has no
target, so the key does nothing and says why in the command log. `G`
goes the other way: it scans the namespace's foreign keys once and lists
the tables that reference the row under the cursor. Both directions push
the page they came from onto a jump history that `ctrl+o` — or `esc`,
before it leaves the grid — walks back, restoring the table, its filter,
its sort and the cell cursor.

The `Relations` tab (`]` from the grid, four times) is the same
information one table at a time: the constraints the open relation
declares above it, the tables that reference it below it, both written
`column → table.column`. `j`/`k` pick a related table and `enter` opens
it with the tab still selected — no row filter, so repeated `enter`
walks the schema; `esc` unwinds the walk. The incoming half needs the
foreign keys of every table in the namespace, so it is scanned in the
background (the tab says `scanning…` meanwhile) and cached per database,
shared with `G`. It is a per-table hub, not a full ERD.

`D` on a connection in panel `[1]` diffs its schema against another
connection's: a popup picks the other side and the namespace on each
(empty = the connection's default). Both sides are dialed fresh —
keyring passwords resolve as on connect, "ask on connect" profiles
prompt first — introspected through the same driver interface as the
Structure tab, and closed again. The report takes over the main view:
tables only in A (red), only in B (green), and per-table column, index
and foreign-key differences as `A: … / B: …` lines (yellow). `j`/`k`
scroll it, `y` copies it, `E` exports it to a file, `esc` dismisses it.
Within one engine family type synonyms are normalized (SQLite `INT` =
`INTEGER`); a cross-engine diff compares types verbatim and says so in
the report header. The report is read-only — it generates no migration
SQL.

The query editor is panel `[4]`, not a popup: it stays in the layout, and
running a script never closes it or clears the buffer. It has two
vim-style modes; the panel gains focus in normal mode:

| Key | Action |
|-----|--------|
| `i` / `enter` | Insert mode at the cursor |
| `a` | Insert mode after the cursor |
| `o` / `O` | Open a line below / above and insert |
| `h`/`j`/`k`/`l`, arrows | Move the cursor |
| `w` / `b` | Next / previous word |
| `0` / `$` | Line start / end |
| `gg` / `G` | Buffer start / end |
| `x` | Delete the character under the cursor |
| `dd` / `yy` | Delete / yank the line |
| `p` | Paste (a yanked line below, characters after the cursor) |
| `ctrl+r` | Run the buffer |
| `ctrl+e` | Explain the statement under the cursor |
| `ctrl+s` | Save the buffer as a named snippet |
| `D` | Clear the buffer (confirms first) |
| `esc` | Back to the previous panel, buffer kept |

In insert mode every key types into the buffer except:

| Key | Action |
|-----|--------|
| `ctrl+r` | Run the buffer (returns to normal mode) |
| `ctrl+e` | Explain the statement under the cursor (returns to normal mode) |
| `ctrl+s` | Save the buffer as a named snippet (insert mode is kept) |
| `ctrl+space` / `tab` | Complete the word under the cursor |
| `esc` | Back to normal mode, buffer kept |
| `ctrl+c` | Cancel the running query |

Typing two identifier characters opens the autocomplete popup by itself;
`ctrl+space` opens it on any prefix, including none. It offers the
dialect's SQL keywords, the tables and views of the current database, and
the columns of every table the buffer already mentions — `customers.`
narrows it to that table's columns. Schema metadata is fetched in the
background and cached per connection and database, so the popup never
stalls the editor: it shows what is cached and fills in when the fetch
lands. While it is open:

| Key | Action |
|-----|--------|
| `↑`/`↓`, `ctrl+p`/`ctrl+n` | Move the selection |
| `enter` / `tab` | Accept, inserting at the cursor |
| `esc` | Close the popup only — the buffer and insert mode are untouched |

With a query result on screen the grid's keys the vim layer does not
claim (paging, `v`, the tabs) work straight from the editor panel in
normal mode, so iterating on a statement never costs the editor its
focus.

Several statements separated by `;` run in order; the result of the last
`SELECT` is what the Data tab shows, and anything that changes data asks
first. Every executed statement is appended to the query history, which
persists in `${XDG_STATE_HOME:-~/.local/state}/lazysql/history` with the
engine it ran on and when. `backspace` in the editor's normal mode opens
it as a floating pane: `enter` runs the selected entry — a statement with
`?` or `:name` placeholders first prompts for their values and executes
as a prepared statement, with the values bound as parameters — `e` loads
it into the editor, `d` deletes it and `s` keeps it as a named snippet.

### Query plans

`ctrl+e` shows how the server would run the statement the cursor is in —
the plan replaces the editor in the main view, `j`/`k` and `ctrl+f`/`ctrl+b`
scroll it, `y` copies it, and `esc` puts the editor back with the buffer,
cursor and mode untouched. Each engine's own form is used: PostgreSQL's
`EXPLAIN (FORMAT JSON)` and MySQL/MariaDB's `EXPLAIN FORMAT=JSON` render as
an indented tree with the node's cost and row estimate (MySQL falls back to
the tabular `EXPLAIN` on servers without the JSON format), SQLite's
`EXPLAIN QUERY PLAN` as its id/parent tree, and DuckDB's `EXPLAIN` diagram
as it comes.

`ANALYZE` is never added, so nothing is executed: explaining a `DELETE` is
as safe as explaining a `SELECT`. A statement with `?`/`:name` placeholders
has no values to plan with and is refused; run it with `ctrl+r` to bind
them. The `EXPLAIN` itself is appended to the command log like any other
statement.

### Snippets

A statement worth reusing gets a name instead of aging out of the
history. `ctrl+s` in the editor (either mode) asks for one and stores the
buffer in `${XDG_STATE_HOME:-~/.local/state}/lazysql/snippets`; an
existing name asks before it is replaced. Only the SQL text is stored —
never a connection or a password.

`tab` in the floating pane switches between its History and Snippets
sections. Snippets are listed by name with their statement previewed and
highlighted; `enter` runs the selected one (through the same placeholder
prompt), `e` loads it into the editor without running it, and `d` deletes
it after a confirmation.

### Read-only connections

A connection can be marked read-only in the connection form (`e` on panel
`[1]`), which writes `read_only = true` on its `[[connections]]` entry.
Everything that would change data or schema is then refused:

- the query editor rejects DML and DDL — including a data-modifying CTE
  such as `WITH … DELETE` — before the run starts, while `SELECT` and
  `EXPLAIN` work as usual;
- cell edits, row inserts, row deletes and the changeset commit answer
  with `connection is read-only` instead of staging, and their keys drop
  out of the options bar;
- every blocked attempt is appended to the command log, marked
  `-- REJECTED (read-only)`.

The refusal lives in the driver session, so nothing can route around it.
On top of that, lazysql asks the engine itself for a read-only session
where one exists: `mode=ro` for SQLite, `access_mode=read_only` for
DuckDB (a file-backed database only), `default_transaction_read_only=on`
for PostgreSQL and `transaction_read_only=1` for MySQL/MariaDB. A
read-only profile is marked with a 🔒 next to its name and in the main
view's title.

## Configuration

lazysql reads `${XDG_CONFIG_HOME:-~/.config}/lazysql/config.toml`. Besides
`[[connections]]` (managed through the UI), it has two optional
user-editable sections. Both fail startup with a message on stderr —
listing every valid name — if they contain an unknown name; they never
start the app with a half-applied override.

### `[keys]`

Override any default binding by action name. The value is one key, or
several separated by commas:

```toml
[keys]
quit = "ctrl+q"
edit-cell = "e"
down = "j, down, ctrl+n"
```

Run `?` in the app for the full current list of action names — every one
of them is overridable, and an override changes the key's behavior, its
entry in the options bar, and its entry in `?` together, since all three
read the same table.

### `[theme]`

`theme` selects a built-in preset (`default`, which tracks your terminal's
own ANSI colors, or `light`, tuned for a white background); any other key
overrides one named color on top of it. Values accept an ANSI name
(`black`…`white`, `bright-black`…`bright-white`, `gray`/`grey`), a
256-color index (`0`-`255`), or hex (`#rgb`/`#rrggbb`):

```toml
[theme]
theme = "light"
border-focused = "#1a7f37"
staged = "yellow"
```

Named colors: `border-focused`, `border-blurred`, `accent`, `staged`,
`deleted`, `error`, `selection-bg`, `row-cursor-bg`, `cell-cursor-bg`,
and the query editor's SQL highlighting — `sql-keyword`, `sql-string`,
`sql-number`, `sql-comment`, `sql-placeholder`. Identifiers and
operators are deliberately left uncolored so they keep your terminal's
own foreground.

## Install

Download a prebuilt binary (darwin/linux, amd64/arm64) from the
[releases page](https://github.com/TrueDaerk/lazysql/releases), or:

```sh
go install github.com/TrueDaerk/lazysql@latest
```

Or build from source:

```sh
git clone git@github.com:TrueDaerk/lazysql.git
cd lazysql
go build .
```

### Build requirements

- **CGO is required** for DuckDB support: `github.com/marcboeker/go-duckdb` bundles the DuckDB C++ engine, so you need a working C/C++ toolchain (`clang` or `gcc`) and `CGO_ENABLED=1` (the default when a toolchain is present).
- SQLite uses the pure-Go `modernc.org/sqlite` — no CGO needed for it.
- All other drivers (MySQL/MariaDB, PostgreSQL) are pure Go.

## Status

Early development. See the [issue tracker](https://github.com/TrueDaerk/lazysql/issues) for the roadmap.

## License

MIT
