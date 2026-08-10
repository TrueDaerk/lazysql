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
├─[2] Objects──────┤│ 2  │ bob     │ bob@example.com            │
│ ▾ shop           ││ …                                         │
│     ▾ Tables     ││                                           │
│         users    ││                                           │
│         orders   │├─Command log───────────────────────────────┤
│     ▸ Views      ││ SELECT * FROM users LIMIT 100;            │
│     ▸ Triggers   ││                                           │
├─[3] Query────────┤│                                           │
│ SELECT * FROM u… ││                                           │
└──────────────────┘└───────────────────────────────────────────┘
 1-3 jump  tab cycle  enter open  l/h expand  ? help  q quit
```

The `[2] Objects` panel is one tree: databases (or schemas) expand into
object categories — **Tables**, **Views**, **Triggers** — and each
category expands into its objects. A category's contents are read from
the server the first time it is expanded and cached until `R` reloads
them. `enter` toggles a branch and opens a leaf; `l`/`h` expand and
collapse lazygit-style. Single-namespace connections (SQLite, DuckDB with
nothing attached) skip the database level entirely.

## Key bindings (core)

| Key | Action |
|-----|--------|
| `1`–`3` | Jump to panel |
| `tab` / `shift+tab` | Cycle panels |
| `j`/`k`, `↑`/`↓` | Move within panel |
| `enter` | Drill in / open |
| `esc` | Back / cancel |
| `e` | Edit cell |
| `d` | Delete row (confirm) |
| `n` | New (connection/row, context-sensitive) |
| `y` | Copy (cell/row/table submenu) |
| `l`/`h`, `→`/`←` | Expand / collapse a node in `[2] Objects` |
| `R` | Reload the focused panel's level from the server |
| `:` | Focus the SQL query editor, panel `[3]` |
| `c` | Commit staged changes |
| `B` | Dump / restore the database (panels `[1]` and `[2]`) |
| `E` | Export the selected database's DDL (panel `[2]`) |
| `@` (or `L`) | Expand the command log |
| `?` | Help / all bindings |
| `q` | Quit |

The second spelling of a binding is there for non-US keyboards: `@`,
`[` and `]` are AltGr chords on German QWERTZ and French AZERTY, which
terminals may not deliver at all, so `L`, `,` and `.` reach the same
actions. Both spellings stay bound, and either can be replaced through
the `[keys]` config section.

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
| `[` / `]` (or `,` / `.`) | Previous / next main-view tab (Data, Structure, Indexes, DDL, Relations) |

### Date and time columns

`e` on a `DATE`, `DATETIME`, `TIMESTAMP`, `TIMESTAMPTZ` or `TIME` column —
in every dialect's spelling, precision and time-zone qualifier included —
opens a calendar instead of a bare text field. `ctrl+t` opens the same
picker for the field under the cursor in the insert/duplicate form:

| Key | Action |
|-----|--------|
| `h`/`l`, `←`/`→` | Previous / next day — or previous / next time field on the clock |
| `k`/`j`, `↑`/`↓` | Previous / next week — or adjust the time field under the cursor |
| `[` / `]` (or `H`/`L`, `,`/`.`) | Previous / next month |
| `tab` | Switch between the date and time halves |
| `t` | Jump to now |
| `e` | Raw text entry — for `NULL`, `now()`, `CURRENT_TIMESTAMP` and anything else a calendar cannot spell (`ctrl+t` comes back) |
| `enter` | Stage the value, ISO-formatted |
| `esc` | Cancel, staging nothing |

The picker only produces a value. It is staged in the changeset like any
other edit and executes nothing until `c` commits.

### Mouse

The mouse is a shortcut for keys that already exist; nothing is
mouse-only. Clicking a panel focuses it (like its number), clicking a row
moves the cursor onto it, and clicking the row the cursor is already on
is `enter` — so a second click drills in, which on a tree branch means
expand or collapse. Clicking a
`Data`/`Structure`/`Indexes`/`DDL`/`Relations` header in the main view
switches that tab.

The wheel scrolls whatever is *under the pointer*, focused or not: a side
panel's list, the data grid's rows, the query editor, and an open popup
(help, cell detail, command log, history). In the grid it stops at the
page boundary — `ctrl+f`/`ctrl+b` turn pages, because that is a query.

Because lazysql now asks the terminal for mouse events, the terminal no
longer maps the wheel to arrow keys on its own, and dragging to select
text needs the terminal's override modifier (`shift` in most terminals,
`option`/`alt` in iTerm2 and Terminal.app). `y` copies from inside the
app and never needs it.

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

The query editor is panel `[3]`, not a popup: it stays in the layout, and
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

### Color tags

A connection can carry an environment color tag — set it in the
connection form's "Color tag" field: pick one of the six named colors, or
"custom…" for any ANSI name, 256-color index, or hex value the `[theme]`
section also accepts. It is entirely optional; an untagged connection
looks exactly as before.

While a tagged connection is active, a `●` in its tag color precedes its
name in panel `[1]` and in the main view, and the main view's top border
is tinted the same color. Only the top border changes — the
sides/bottom (and the panel focus color generally) keep signalling focus
the way they always have, so a tag can never be mistaken for "this panel
is focused". Confirm modals for a changeset commit and for an unguarded
DELETE/UPDATE (see above) render the connection's name in its tag color
too, for extra salience right before something destructive runs.

An invalid color value (a typo, an unrecognized name) never blocks
startup: the connection loads with no tag, and lazysql logs a warning
naming the offending connection.

### Dump and restore

`B` on panel `[1]` or `[2]` opens a two-entry menu — dump the database to
a file, or restore such a file back into it. Both open a form that shows
the exact command about to run, with an editable arguments line and a
path field.

lazysql drives the engine's own tool rather than serializing the database
itself, so what comes out is actually restorable:

| Engine | Dump | Restore |
|--------|------|---------|
| PostgreSQL | `pg_dump` | `psql` |
| MySQL | `mysqldump` | `mysql` |
| MariaDB | `mariadb-dump` (or `mysqldump`) | `mariadb` (or `mysql`) |
| SQLite | `VACUUM INTO` | copy the file back |
| DuckDB | `EXPORT DATABASE` (a directory) | `IMPORT DATABASE` |

Host, port, user and database are prefilled from the connection; the
password comes from the OS keyring like everywhere else. **It never
reaches the tool's argv** — where `ps` would show it to every user on the
machine — nor the environment. It travels in a temporary `0600` file (a
`.pgpass` named by `PGPASSFILE`, or a MySQL option file named by
`--defaults-extra-file`) that is deleted the moment the tool exits, and it
appears in neither the previewed command nor the command log.

The tool's stderr is streamed into the command log as it runs; `X` cancels
a running job and kills the tool's whole process group, and a cancelled or
failed dump has its partial file removed. A missing binary is reported by
name before any prompt appears. When the connection runs through an SSH
tunnel, lazysql opens a loopback-only local forward for the run and points
the tool at that.

A restore overwrites data and cannot be undone, so it does not run until
the database name has been typed into a confirmation field, and it is
refused outright on a read-only connection.

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

### `restore_session`

On quit, lazysql remembers the connection, database, table, tab and grid
cursor you were on in `${XDG_STATE_HOME:-~/.local/state}/lazysql/session.json`
(a connection *name* only — never a password) and reconnects to it on the
next start. Set `restore_session = false` to turn this off permanently, or
pass `--no-restore` to skip it for one run:

```toml
restore_session = false
```

Either way, `esc` cancels an auto-connect in progress and drops back to
the plain connections panel; a deleted connection, an unreachable host or
a dropped table degrade the same way, with a note in the command log.

### Clipboard

`y` copies through the native clipboard (`pbcopy`, `xclip`, `xsel`) when
there is one. When there is not — an SSH session, a container — the text
goes out as an OSC 52 escape sequence instead, so it lands on the
clipboard of the terminal you are actually sitting in front of. Only if
that is unavailable too (no terminal, `TERM=dumb`, or a copy larger than
128 KiB, which terminals silently drop) does the copy fall back to a temp
file, whose path the command log names. The log always says which of the
three happened.

Some terminals ship with OSC 52 off (iTerm2 has a "may access clipboard"
setting; macOS Terminal.app does not support it), and tmux needs
`set-clipboard` left at `external` or set to `on`. Set `LAZYSQL_NO_OSC52=1`
to skip the escape sequence entirely and go straight to the temp file.

Pasting is the terminal's own paste (⌘V, ctrl+shift+V, middle click):
lazysql takes bracketed paste everywhere text is typed, including the
query editor in normal mode, where the pasted characters are inserted as
text rather than run as vim commands. Vim's `p` is unrelated — it pastes
the editor's own `x`/`dd`/`yy` register.

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
