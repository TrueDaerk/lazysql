# lazysql

lazysql is a **lazygit-style terminal UI for SQL databases**. It browses
connections, walks schemas, pages through data, edits rows and runs queries —
all from numbered side panels, a large main view and single-key,
context-sensitive bindings.

Supported engines: **MySQL**, **MariaDB**, **PostgreSQL**, **SQLite** and
**DuckDB**.

```text
┌─[1] Connections──┐┌─Data | Structure | Indexes | DDL | Relations ──────┐
│ ▸ local-mariadb  ││ id │ name    │ email                               │
│   prod-pg        ││ 1  │ alice   │ alice@example.com                   │
├─[2] Objects──────┤│ 2  │ bob     │ bob@example.com                     │
│ ▾ shop           ││ …                                                  │
│     ▾ Tables     ││                                                    │
│         users    ││                                                    │
│         orders   │├─Command log────────────────────────────────────────┤
│     ▸ Views      ││ SELECT * FROM "users" LIMIT 100;                   │
│     ▸ Triggers   ││                                                    │
├─[3] Query────────┤│                                                    │
│ SELECT * FROM u… ││                                                    │
└──────────────────┘└────────────────────────────────────────────────────┘
 1-3 jump  tab cycle  enter open  l/h expand  ? help  q quit
```

## Why it exists

GUI database clients are heavy, and they are a second window to find. The raw
CLI clients (`mysql`, `psql`) are fast, but browsing a schema through them is
`SHOW TABLES`, then `DESCRIBE`, then a `SELECT` you type out in full, with
nothing on screen to point at.

lazysql takes the shape lazygit found for Git and applies it to a database:
the objects on the left, the thing you selected on the right, one key per
action, and a modal popup for anything that needs typing. Nothing that changes
data happens implicitly — edits, inserts and deletes collect in a
[staged changeset](concepts/staged-mutations.md) and only run when you commit
them.

!!! note "What this project is"
    lazysql is a personal project: built by one person, to that person's
    taste, with heavy AI assistance. The defaults follow a specific set of
    lazygit and vim habits, on a German keyboard, in a specific terminal,
    because that is the setup it was built for. There is no support promise
    and no roadmap commitment.

    None of that is a warning label — it is just useful to know before you
    decide whether it fits how *you* work. It is public on purpose: use it if
    it suits you, and pull requests that improve it are genuinely welcome.
    More in [About &amp; contributing](contributing.md).

## Install

lazysql is a single Go binary. Grab a prebuilt one from the
[releases page](https://github.com/TrueDaerk/lazysql/releases), or build it:

```sh
git clone https://github.com/TrueDaerk/lazysql.git
cd lazysql
make install            # installs to ~/.local/bin/lazysql
```

Then run `lazysql` and press `n` to create your first connection.
[Installation](getting-started/installation.md) covers the build requirements
(DuckDB needs CGO); [Your first connection](getting-started/first-connection.md)
walks the two-step connection form.

## Find your way around

<div class="grid cards" markdown>

- :material-rocket-launch: **[Getting started](getting-started/index.md)**

    Install, check your terminal, create a connection, and learn the handful
    of keys that get you moving.

- :material-shape: **[Concepts](concepts/index.md)**

    The panel model, how focus works, why every mutation is staged before it
    runs, and what the command log is for.

- :material-book-open-variant: **[Guides](guides/index.md)**

    One page per feature area — browsing, the data grid, editing, the query
    editor, copy and export, schema diff, dump/restore, SSH tunnels and
    configuration.

- :material-table: **[Reference](reference/index.md)**

    The complete keybinding table, every config file key, and what each engine
    does differently.

</div>

Stuck? [Troubleshooting](troubleshooting.md) collects the failure modes worth
knowing about — a good share of them are the terminal rather than the app.

## The short version of the keyboard

| Keys | What it does |
|---|---|
| `1` `2` `3` | Jump to a side panel |
| ++tab++ / ++shift+tab++ | Cycle panels (the main view is in the cycle) |
| `j` / `k` | Move within the focused panel |
| ++enter++ | Drill in — connect, open a table, toggle a tree branch |
| `l` / `h` | Expand / collapse a branch of the object tree |
| ++esc++ | Back out one step |
| `:` | Focus the query editor, panel `[3]` |
| `c` | Commit the staged changes |
| `?` | Every binding for the focused panel |
| `q` | Quit |

Keys are **case-sensitive**: `d` stages a row delete, `D` duplicates the row.
The full table is in [Keybindings](reference/keybindings.md), and `?` inside
the app always shows the bindings as *you* have configured them.

## Configuration in one paragraph

lazysql reads `${XDG_CONFIG_HOME:-~/.config}/lazysql/config.toml`. Connections
are managed through the UI and written there — passwords never are; they go to
the OS keyring. Two hand-edited sections sit next to them: `[keys]` rebinds any
action, `[theme]` recolors the interface.

```toml
restore_session = true

[theme]
theme = "light"
border-focused = "#1a7f37"

[keys]
quit = "ctrl+q"
```

See [Configuration](guides/configuration.md) for the tour and
[Settings](reference/settings.md) for every key.

## Looking for the internals?

This site documents *using* lazysql. The architecture documentation — one
concept document per subsystem, aimed at contributors — lives in
[`wiki/`](https://github.com/TrueDaerk/lazysql/blob/main/wiki/index.md) in the
repository, and planning happens in
[GitHub issues](https://github.com/TrueDaerk/lazysql/issues).
