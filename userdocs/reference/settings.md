# Settings

Everything lazysql reads from disk or the environment.

## `config.toml`

```text
${XDG_CONFIG_HOME:-~/.config}/lazysql/config.toml
```

Written atomically, owner-only (`0600`). A missing file is not an error — it is
what a first run sees.

### Top level

| Key | Type | Default | Meaning |
|---|---|---|---|
| `restore_session` | bool | `false` | Reconnect to the last connection/database/table/tab/cursor on start |

### `[[connections]]`

One table per profile. Managed from panel `[1]`; safe to read, and editable by
hand if you keep the field names.

| Key | Type | Meaning |
|---|---|---|
| `name` | string | Profile name — also the keyring key. Required |
| `engine` | string | `mysql`, `mariadb`, `postgres`, `sqlite` or `duckdb`. Required |
| `host` | string | Server host, or an absolute path for a unix socket. Required for server engines |
| `port` | int | Omitted = the engine's default (5432 / 3306) |
| `user` | string | Login |
| `database` | string | Default database; omitted = the server's own default |
| `file` | string | Database file for SQLite and DuckDB. Required for SQLite; empty DuckDB = in-memory |
| `ask_password` | bool | Prompt on every connect instead of reading the keyring |
| `read_only` | bool | Refuse every write on this connection |
| `color` | string | Environment tag: an ANSI name, a 256-color index, or hex |
| `options` | table | Extra DSN parameters, e.g. `sslmode = "disable"` |
| `[connections.ssh]` | table | The jump host — see below |

### `[connections.ssh]`

| Key | Type | Meaning |
|---|---|---|
| `enabled` | bool | Dial through the jump host |
| `host` | string | Bastion host, or a `~/.ssh/config` alias |
| `port` | int | Default `22` |
| `user` | string | Empty = `~/.ssh/config`, then the OS user |
| `auth` | string | `agent`, `key` or `password` |
| `key_file` | string | For `key` auth; empty = the alias's `IdentityFile` |

!!! warning "No secrets in this file"
    There is no password key, by design. Database passwords and SSH
    secrets/passphrases live in the **OS keyring**, keyed by the connection
    name. Removing a connection removes both of its keyring entries.

### Example

```toml
restore_session = true

[[connections]]
name = "local-pg"
engine = "postgres"
host = "localhost"
port = 5432
user = "postgres"
database = "shop"
color = "green"

[[connections]]
name = "prod-pg"
engine = "postgres"
host = "10.0.0.4"
user = "readonly"
database = "shop"
read_only = true
color = "red"

  [connections.options]
  sslmode = "require"

  [connections.ssh]
  enabled = true
  host = "bastion"
  auth = "agent"

[[connections]]
name = "notes"
engine = "sqlite"
file = "~/notes.db"
```

## `[keys]`

Each entry rebinds one action. The value is one key, or several separated by
commas — all of them stay live.

```toml
[keys]
quit = "ctrl+q"
down = "j, down, ctrl+n"
where-filter = "/"
```

An unknown action name, or an empty value, **fails startup** with a message on
stderr listing every valid name.

Key names are the terminal's own spellings: `up`, `down`, `left`, `right`,
`enter`, `esc`, `tab`, `shift+tab`, `space`, `backspace`, `pgup`, `pgdown`,
`ctrl+f`, `ctrl+space`, `ctrl+enter`, and a bare character for a character key.
`lazysql --debug-keys` prints the spelling your terminal produces for any key.

### Every action name

What each one does, and its default keys, is in
[Keybindings](keybindings.md).

`accept-changes` · `accept-completion` · `actions` · `apply-filter` · `back` ·
`backup` · `browse-back` · `cancel-backup` · `cancel-export` ·
`cancel-filter` · `cancel-query` · `clear-filter` · `clear-query` ·
`close-completion` · `col-left` · `col-right` · `collapse-node` ·
`command-log` · `commit-changes` · `complete` · `complete-next` ·
`complete-prev` · `connect` · `copy-menu` · `copy-selection` · `delete-row` ·
`discard-changes` · `down` · `drop-connection` · `duplicate-connection` ·
`duplicate-row` ·
`edit-cell` · `edit-connection` · `edit-query` · `enter` · `expand-node` ·
`explain-query` · `export-database-ddl` · `export-table` · `filter` ·
`filter-hist-next` · `filter-hist-prev` · `follow-fk` · `help` ·
`hist-delete` · `hist-load` · `hist-run` · `hist-section` · `hist-snippet` ·
`history` · `incoming-refs` · `insert-row` · `jump` · `leave-insert` ·
`move-conn-down` · `move-conn-up` ·
`new-connection` · `next-main-tab` · `next-page` · `next-panel` ·
`open-editor` · `open-picker` · `pick-down` · `pick-month-next` ·
`pick-month-prev` · `pick-next` · `pick-prev` · `pick-raw` · `pick-section` ·
`pick-today` · `pick-up` · `prev-main-tab` · `prev-page` · `prev-panel` ·
`quit` · `refresh` · `row-detail` · `run-editor` · `run-statement` ·
`save-snippet` · `schema-diff` · `screen-next` · `screen-prev` ·
`select-columns` · `select-rows` · `shift-down` · `shift-left` ·
`shift-right` · `shift-up` · `sort-column` · `test-connection` ·
`unstage-cell` · `up` · `view-cell` · `vim-append` · `vim-append-eol` ·
`vim-bottom` · `vim-delete-char` · `vim-delete-line` · `vim-insert-start` ·
`vim-left` · `vim-line-end` · `vim-line-start` · `vim-open-above` ·
`vim-open-below` · `vim-paste` · `vim-right` · `vim-top` · `vim-word-back` ·
`vim-word-end` · `vim-word-fwd` · `vim-yank-line` · `where-filter`

The form and engine-picker keys are not in that list: a modal claims every key
it is open for and dispatches them itself, so an override could only change
what the options bar claims, never what the form answers to.

## `[theme]`

`theme` picks a built-in preset; every other key overrides one color on top of
it. An unknown name **fails startup** the same way `[keys]` does.

```toml
[theme]
theme = "light"
border-focused = "#1a7f37"
staged = "yellow"
```

| Preset | For |
|---|---|
| `default` | Tracks your terminal's own ANSI colors |
| `light` | Tuned for a white background |

### Color values

- an ANSI name: `black`, `red`, `green`, `yellow`, `blue`, `magenta`, `cyan`,
  `white`, their `bright-` forms, and `gray` / `grey`;
- a 256-color index: `0`–`255`;
- hex: `#rgb` or `#rrggbb`.

### Color slots

| Name | Colors |
|---|---|
| `border-focused` | The focused panel's border and title |
| `border-blurred` | Every other panel's border |
| `accent` | Headings, selected tabs, emphasis |
| `staged` | A staged edit or insert |
| `deleted` | A row staged for deletion |
| `error` | Errors and refusals |
| `selection-bg` | The multi-row selection background |
| `row-cursor-bg` | The row under the cursor |
| `cell-cursor-bg` | The cell under the cursor |
| `sql-keyword` | SQL keywords in the query editor |
| `sql-string` | String literals |
| `sql-number` | Numeric literals |
| `sql-comment` | Comments |
| `sql-placeholder` | `?` and `:name` placeholders |

Identifiers and operators have no slot: they deliberately keep your terminal's
own foreground.

## `state.toml`

```text
${XDG_CONFIG_HOME:-~/.config}/lazysql/state.toml
```

Disposable UI state, written by the app and never hand-edited.

| Key | Meaning |
|---|---|
| `screen_mode` | The last screen mode: `normal`, `half` or `full` |

A missing, unreadable or corrupt file degrades to defaults rather than
blocking startup.

## State files

Under `${XDG_STATE_HOME:-~/.local/state}/lazysql/`:

| File | Contents |
|---|---|
| `history` | Query history — statement, engine, connection, timestamp |
| `snippets` | Named snippets |
| `filters` | Filter history, per connection and table |
| `session.json` | Last connection name, database, table, tab and cursor |

None of them ever holds a password. All are safe to delete; you would only
miss `snippets`.

## Environment variables

| Variable | Effect |
|---|---|
| `XDG_CONFIG_HOME` | Where `config.toml` and `state.toml` live |
| `XDG_STATE_HOME` | Where history, snippets, filters and the session live |
| `LAZYSQL_NO_OSC52` | Set to `1` to never emit an OSC 52 clipboard escape — the copy goes to a temp file instead |

## Command-line arguments

| Argument | Effect |
|---|---|
| `<file>` | Open a SQLite, DuckDB or Parquet file for this session only; nothing is saved to `config.toml` (see [Your first connection](../getting-started/first-connection.md)) |

## Command-line flags

| Flag | Effect |
|---|---|
| `--version` | Print the version and exit |
| `--no-restore` | Start without restoring the last session |
| `--debug-keys` | Print what the terminal reports for each key pressed; `ctrl+q` quits |
