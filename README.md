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
- **Edit** — change single cells inline; insert, duplicate, and delete rows. All mutations are *staged* first (lazygit-style) and only applied on explicit commit — with rollback.
- **Query editor** — free-form SQL with history, result tabs, and cancellation.
- **Copy & export** — cell, row, or whole table as CSV, JSON, or `INSERT` statements to clipboard or file.
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
├─[4] Query history┤│ SELECT * FROM users LIMIT 100;            │
├─[5] Query────────┤│                                           │
│ SELECT * FROM u… ││                                           │
└──────────────────┘└───────────────────────────────────────────┘
 1-5 jump  tab cycle  enter open  e edit  d delete  ? help  q quit
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
| `:` | Focus the SQL query editor, panel `[5]` |
| `c` | Commit staged changes |
| `?` | Help / all bindings |
| `q` | Quit |

In the data grid (`enter` on a table, `esc` back):

| Key | Action |
|-----|--------|
| `h`/`l`, `←`/`→` | Move the cell cursor between columns |
| `ctrl+f` / `ctrl+b`, `pgdn`/`pgup` | Next / previous page |
| `s` | Sort the cursor column (ASC → DESC → off) |
| `f` | Quick `WHERE` filter |
| `v` | Show the full cell value (JSON pretty-printed) |

The query editor is panel `[5]`, not a popup: it stays in the layout, and
running a script never closes it or clears the buffer. It has two modes.
In normal mode the panel's own keys apply:

| Key | Action |
|-----|--------|
| `i` / `enter` | Start editing (insert mode) |
| `ctrl+r` | Run the buffer |
| `D` | Clear the buffer (confirms first) |
| `j`/`k` | Move the cursor between lines |
| `esc` | Back to the previous panel, buffer kept |

In insert mode every key types into the buffer except:

| Key | Action |
|-----|--------|
| `ctrl+r` | Run the buffer (returns to normal mode) |
| `esc` | Back to normal mode, buffer kept |
| `ctrl+c` | Cancel the running query |

With a query result on screen the grid's own keys (paging, `v`, the tabs)
work straight from the editor panel in normal mode, so iterating on a
statement never costs the editor its focus.

Several statements separated by `;` run in order; the result of the last
`SELECT` is what the Data tab shows, and anything that changes data asks
first. Every executed statement is appended to `[4] Query history`, which
persists in `${XDG_STATE_HOME:-~/.local/state}/lazysql/history` with the
engine it ran on and when. There, `enter` loads an entry back into the
editor, `x` runs it, `d` deletes it, `D` clears the history and `/` filters.

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
`deleted`, `error`, `selection-bg`, `row-cursor-bg`, `cell-cursor-bg`.

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
