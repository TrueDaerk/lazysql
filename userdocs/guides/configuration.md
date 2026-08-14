# Configuration

lazysql reads one file:

```text
${XDG_CONFIG_HOME:-~/.config}/lazysql/config.toml
```

It holds your connections — written by the UI, not by hand — plus three
optional things you *do* edit by hand: `[keys]`, `[theme]` and
`restore_session`.

The complete list of names lives in [Settings](../reference/settings.md); this
page is the tour.

!!! danger "A bad `[keys]` or `[theme]` name fails startup"
    Both sections are validated before the app draws anything, and an unknown
    name is a hard error on stderr **listing every valid name**. That is
    deliberate: a half-applied override that silently drops the one binding
    you were trying to change is worse than a message.

## Connections

Managed from panel `[1]` — `n` creates, `e` edits, `d` removes. Each profile
is a `[[connections]]` entry:

```toml
[[connections]]
name = "prod-pg"
engine = "postgres"
host = "db.internal"
port = 5432
user = "app"
database = "shop"
read_only = true
color = "red"
```

**Passwords are never in this file.** They live in the OS keyring, keyed by the
connection name, or are prompted for on each connect when
`ask_password = true`.

## Read-only connections

Tick **Read-only** in the connection form (or set `read_only = true`) and
everything that would change data or schema is refused:

- the query editor rejects DML and DDL — including a data-modifying CTE such
  as `WITH … DELETE` — before the run starts, while `SELECT` and `EXPLAIN`
  work as usual;
- cell edits, row inserts, row deletes and the changeset commit answer
  `connection is read-only` instead of staging, and their keys drop out of the
  options bar;
- a restore is refused outright;
- every blocked attempt is appended to the command log, marked
  `-- REJECTED (read-only)`.

The refusal lives in the driver session, so nothing in the UI can route around
it. On top of that, lazysql asks the **engine** for a read-only session where
one exists — see [Engines](../reference/engines.md#read-only-sessions).

A read-only profile is marked 🔒 next to its name and in the main view's title.

## Color tags

A connection can carry an environment color tag — set it in the connection
form's **Color tag** field. Pick one of six named colors (`red`, `yellow`,
`green`, `blue`, `magenta`, `cyan`), or `custom…` for any ANSI name,
256-color index or hex value the `[theme]` section also accepts.

While a tagged connection is active:

- a `●` in the tag color precedes its name in panel `[1]` and in the main view;
- the main view's **top** border is tinted to match — only the top, so the
  focus color keeps meaning focus;
- the confirm modals for a changeset commit and for an unguarded
  `DELETE`/`UPDATE` render the connection's name in its tag color, right
  before something destructive runs.

An invalid color never blocks startup: the connection loads untagged and
lazysql logs a warning naming it.

## `[keys]` — rebinding

Override any action by name. The value is one key, or several separated by
commas:

```toml
[keys]
quit = "ctrl+q"
edit-cell = "e"
down = "j, down, ctrl+n"
```

Press `?` in the app for the current list of action names — every one of them
is overridable, and an override changes the key's behavior, its entry in the
options bar and its entry in `?` together, because all three read the same
table. The full list is in
[Settings → keys](../reference/settings.md#keys).

## `[theme]` — colors

`theme` selects a built-in preset; any other key overrides one named color on
top of it.

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

Values accept an ANSI name (`black`…`white`, `bright-black`…`bright-white`,
`gray` / `grey`), a 256-color index (`0`–`255`), or hex (`#rgb` / `#rrggbb`).
The color names are listed in
[Settings → theme](../reference/settings.md#theme).

## `restore_session`

lazysql remembers, on quit, the connection, database, table, tab and grid
cursor you were on — a connection **name** only, never a password — in
`${XDG_STATE_HOME:-~/.local/state}/lazysql/session.json`.

Reconnecting to it on the next start is **opt-in**:

```toml
restore_session = true
```

By default lazysql starts on the plain connections panel without dialing
anything.

With it enabled, `--no-restore` skips the reconnect for one run without
touching the config. Either way ++esc++ cancels an auto-connect in progress,
and a deleted connection, an unreachable host or a dropped table all degrade
to the plain panel with a note in the command log.

## Files lazysql writes on its own

| Path | Contents | Safe to delete |
|---|---|---|
| `…/lazysql/state.toml` | The screen mode | yes |
| `…/lazysql/history` | Query history | yes |
| `…/lazysql/snippets` | Named snippets | you would lose them |
| `…/lazysql/filters` | Per-table filter history | yes |
| `…/lazysql/session.json` | Last browsing position | yes |

`state.toml` sits next to `config.toml`; the rest live under
`${XDG_STATE_HOME:-~/.local/state}/lazysql/`. None of them is hand-edited, and
a corrupt one degrades to defaults rather than blocking startup.

## Environment variables

| Variable | Effect |
|---|---|
| `XDG_CONFIG_HOME` | Where `config.toml` and `state.toml` live |
| `XDG_STATE_HOME` | Where history, snippets, filters and the session live |
| `LAZYSQL_NO_OSC52` | Set to `1` to never emit an OSC 52 clipboard escape |

## Command-line flags

| Flag | Effect |
|---|---|
| `--version` | Print the version and exit |
| `--no-restore` | Start without restoring the last session |
| `--debug-keys` | Print what the terminal reports for each key (`ctrl+q` quits) |
