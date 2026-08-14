# Your first connection

Start lazysql. Panel `[1] Connections` has the focus and is empty. Press `n`.

## Step one: pick the engine

The engine is the one answer that decides which fields the form has at all, so
it is asked alone and first. Every choice carries a digit, so the common case
is a single keystroke:

```text
┌─ New connection ───────────────────────┐
│  1  PostgreSQL   server · default port 5432
│  2  MySQL        server · default port 3306
│  3  MariaDB      server · default port 3306
│  4  SQLite       single file on disk
│  5  DuckDB       local file — or in-memory
└────────────────────────────────────────┘
 1-9 pick engine  ↑/k ↓/j  enter choose  esc back
```

`j` / `k` and ++enter++ work like every other list in the app, and ++esc++
cancels.

## Step two: fill in the form

Picking an engine builds the form that engine needs, grouped under section
headers. A server engine gets:

| Section | Fields |
|---|---|
| **Profile** | Name, Color tag (and Custom color, when the tag is `custom…`) |
| **Server** | Host, Port, Database |
| **Credentials** | User, Password, Ask on connect |
| **SSH tunnel** | Enabled — and, once enabled, SSH host, port, user, auth method, key file, secret |
| **Advanced** | Options, Read-only |

A file engine (SQLite, DuckDB) gets **Profile**, a **Storage** section with a
single `File` field, and **Advanced**. There is no host, no port and no
password, because there is nothing to authenticate against.

Defaults that save typing:

- **Host** is prefilled with `localhost` when creating — the common case is
  the database on this machine. Overtype it for anything else.
- **Port** may be left empty; the engine's default is used (`5432` for
  PostgreSQL, `3306` for MySQL and MariaDB).
- **Database** is optional. Leave it empty to land on the server's default and
  pick the database from the object tree afterwards.
- **Name** may be left empty for a file engine — the profile is named after
  the file.
- **File** may be left empty for DuckDB, which then opens an **in-memory**
  database. SQLite requires a path.

### Keys in the form

| Key | Action |
|---|---|
| `↓` / ++tab++ | Next field |
| `↑` / ++shift+tab++ | Previous field |
| `←` / `→` | Change the value of a select or a checkbox |
| ++tab++ | On the `File` field: complete the path (again to cycle candidates) |
| ++ctrl+t++ | Test the connection as shown, without saving |
| ++ctrl+e++ | Go back to the engine picker, keeping what you typed |
| ++enter++ | Save |
| ++esc++ | Back one step — to the picker while creating, closing the form otherwise |

Nothing you typed is thrown away by a step backwards: the draft travels with
you through the picker and back.

## Where the password goes

The password field writes to the **OS keyring**, keyed by the connection name.
It is never written to `config.toml`, never printed to the command log, and
never passed on a command line.

If you would rather not store it at all, tick **Ask on connect**: lazysql
prompts for the password on every connect and forgets it when the connection
closes.

## Connect

Back on panel `[1]`, ++enter++ (or ++space++) on the profile connects. `t`
tests it without opening it — useful right after an edit.

Once connected, panel `[2] Objects` fills with the tree: databases (or
schemas) → object categories (**Tables**, **Views**, **Triggers**) → objects.
`l` expands, `h` collapses, ++enter++ toggles a branch and opens a leaf.

```text
├─[2] Objects──────┤
│ ▾ shop           │
│     ▾ Tables     │
│         users    │
│         orders   │
│     ▸ Views      │
│     ▸ Triggers   │
```

++enter++ on a table opens it in the main view, on the **Data** tab. You are
now in [Browsing objects](../guides/browsing-objects.md) and
[The data view](../guides/data-view.md).

!!! tip "Single-namespace connections skip a level"
    SQLite, and DuckDB with nothing attached, have exactly one namespace, so
    the tree starts at the category level instead of making you expand a
    database that could never have a sibling.

## Editing a connection later

| Key | On panel `[1]` |
|---|---|
| `e` | Edit the profile — the same form, ++ctrl+e++ still switches engine |
| `d` | Remove it, after a confirmation (its keyring entries go with it) |
| `t` | Test it |
| `D` | [Schema diff](../guides/schema-diff.md) against another connection |
| `B` | [Dump or restore](../guides/dump-and-restore.md) the database |

## Optional: come back where you left off

lazysql can remember the connection, database, table, tab and cursor you quit
on. It is **opt-in** — by default startup is a plain connections panel that
dials nothing:

```toml
restore_session = true
```

With it enabled, `--no-restore` skips the reconnect for one run, and ++esc++
cancels an auto-connect in progress. A deleted connection or an unreachable
host degrades to the plain panel with a note in the command log.
