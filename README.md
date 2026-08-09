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
└──────────────────┘└───────────────────────────────────────────┘
 1-4 jump  tab cycle  enter open  e edit  d delete  ? help  q quit
```

## Key bindings (core)

| Key | Action |
|-----|--------|
| `1`–`4` | Jump to panel |
| `tab` / `shift+tab` | Cycle panels |
| `j`/`k`, `↑`/`↓` | Move within panel |
| `enter` | Drill in / open |
| `esc` | Back / cancel |
| `e` | Edit cell |
| `d` | Delete row (confirm) |
| `n` | New (connection/row, context-sensitive) |
| `y` | Copy (cell/row/table submenu) |
| `:` | Open SQL query editor |
| `c` | Commit staged changes |
| `?` | Help / all bindings |
| `q` | Quit |

## Install

```sh
go install github.com/TrueDaerk/lazysql@latest
```

Or build from source:

```sh
git clone git@github.com:TrueDaerk/lazysql.git
cd lazysql
go build .
```

## Status

Early development. See the [issue tracker](https://github.com/TrueDaerk/lazysql/issues) for the roadmap.

## License

MIT
