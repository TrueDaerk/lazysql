# Browsing objects

Panel `[2] Objects` is one tree over everything a connection contains.

```text
├─[2] Objects──────┤
│ ▾ shop           │   ← database (or schema)
│     ▾ Tables     │   ← category
│         users    │   ← object
│         orders   │
│     ▸ Views      │
│     ▸ Triggers   │
│ ▸ analytics      │
```

## Moving through it

| Key | Action |
|---|---|
| `j` / `k` | Move the cursor |
| `l` / `→` | Expand the branch — or step into it, if it is already open |
| `h` / `←` | Collapse it — or step out to the parent, if it is already closed |
| ++enter++ | Toggle a branch; open a leaf in the main view |
| `/` | Filter |
| `R` | Reload from the server |
| `E` | [Export the database's DDL](copy-and-export.md#a-whole-databases-ddl) |
| `B` | [Dump or restore](dump-and-restore.md) |
| `a` | The actions menu for this panel |

## The three levels

**Databases** (or schemas, per engine) come from the server on connect.
Connections with exactly one namespace — SQLite, and DuckDB with nothing
attached — skip this level entirely, because there is never a sibling to
choose between.

**Categories** are fixed: **Tables**, **Views**, **Triggers**. They are always
there, whether or not they have contents, so the shape of the tree does not
change under you as you expand it.

**Objects** are what the category holds.

## Lazy loading and the cache

A category's contents are read from the server **the first time it is
expanded**, not on connect. Collapsing and re-expanding it costs nothing — the
list is cached until `R` reloads it.

That is why the first expand of a big schema takes a moment and the rest do
not, and why a table someone else created since you connected does not show up
until you press `R`.

`R` reloads the focused panel's level: on a category it re-reads that
category, and on a database it re-reads the namespace.

## Filtering

`/` opens an inline filter over the expanded level. It matches by
case-insensitive **subsequence**, so `usr` finds `users` and `user_roles`, and
the ancestors of matching branches stay visible so you can see where a match
sits. ++esc++ clears it.

## What opening a leaf does

| Leaf | Main view shows |
|---|---|
| A table | The **Data** tab, plus Structure, Indexes, DDL and Relations |
| A view | The same tabs — a view pages and introspects like a table |
| A trigger | A read-only definition view |

++enter++ on any of them also moves focus to the main view, which is where the
[grid's keys](data-view.md) live. ++tab++ and ++esc++ move focus back.

!!! note "Triggers are not supported on every engine"
    MySQL, MariaDB, PostgreSQL and SQLite all report them.
    **DuckDB has no triggers**, so the category stays empty there.
