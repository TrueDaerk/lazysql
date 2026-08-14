# Panels and layout

The screen is a left column of three numbered side panels, a large main view
on the right with the command log under it, and one options bar across the
bottom.

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

## `[1] Connections`

Your saved connection profiles. ++enter++ connects, `n` creates, `e` edits,
`d` removes, `t` tests. `D` diffs this connection's schema against another's;
`B` opens the dump/restore menu.

A connection can carry an **environment color tag**. While a tagged connection
is active, a `●` in that color precedes its name here and in the main view,
and the main view's *top* border is tinted to match. Only the top border — the
sides keep signalling focus, so a tag can never be mistaken for "this panel is
focused". A read-only connection is marked 🔒 instead.

## `[2] Objects`

One tree, three levels:

```text
database (or schema)
    └─ category — Tables · Views · Triggers
            └─ object
```

`l` / `→` expands, `h` / `←` collapses, ++enter++ toggles a branch and opens a
leaf. A category's contents are read from the server the first time it is
expanded and cached until `R` reloads them, so walking back over a branch you
have already seen costs nothing.

Connections with exactly one namespace — SQLite, and DuckDB with nothing
attached — skip the database level entirely.

`/` filters the expanded level by case-insensitive subsequence, keeping the
ancestors of matching branches visible.

## `[3] Query`

The SQL editor. It is a **panel, not a popup**: running a script never closes
it or clears the buffer, and the result appears in the main view next to it.
It has two vim-style modes — the panel gains focus in normal mode, `i` enters
insert mode, ++esc++ returns.

`:` focuses it from anywhere.

## The main view

Whatever the focused panel points at. For a table that is five tabs:

| Tab | Contents |
|---|---|
| **Data** | One page of rows, with the filter and sort applied |
| **Structure** | Columns: `#`, name, type, null, default, key (`PK`/`UNI`/`IDX`/`FK`), extra |
| **Indexes** | Indexes and their columns |
| **DDL** | The `CREATE` statement, from the server or synthesized |
| **Relations** | Outgoing and incoming foreign keys, walkable with ++enter++ |

`<` / `>` cycle them (`[` / `]` and `,` / `.` do the same). The main view is
also a **focus target**: it has no number, but ++tab++ reaches it once
something is open, and that is where the data grid's own keys live.

Views, triggers, query results and the schema-diff report take over the same
space with their own rendering.

## The command log

The slim panel under the main view carries every statement lazysql executes,
plus its own status notes. `@` (or `L`) expands it into a scrollable view.
See [The command log](command-log.md).

## The options bar

The last line is the bindings that apply *right now*: the focused panel's own
actions first, then the universal keys. It is generated from the same
`key.Binding` table that dispatches the keys and fills `?`, so it can never
advertise a key that does not work — and on a read-only connection, the keys
that would only answer "connection is read-only" drop out of it.

## Screen modes

`+` and `_` cycle how much room the focused **side panel** gets relative to the
rest of the layout — **normal**, **half**, **full** — the same three modes
lazygit has. The choice is remembered between runs in `state.toml`.
