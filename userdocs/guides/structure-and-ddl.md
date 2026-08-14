# Structure, indexes and DDL

Four of the main view's five tabs describe the open relation rather than its
rows. `<` / `>` cycle them; all four are read-only.

The three introspection tabs share **one** metadata fetch, so switching
between them after the first is instant. `j` / `k` scroll a tab that does not
fit.

## Structure

The relation's columns:

| Column | What |
|---|---|
| `#` | Ordinal position |
| `name` | Column name |
| `type` | Declared type, as the engine spells it |
| `null` | `yes` / `no` |
| `default` | Column default, if any |
| `key` | `PK` for the primary key, `UNI` / `IDX` for the indexes covering it, `FK` when a foreign key constrains it |
| `extra` | Whatever the engine reports on top — `auto_increment`, a generated-column expression, and so on |

## Indexes

Two tables on one tab.

**Indexes** — `index`, `type`, `unique`, `columns`.

**Foreign keys** — `constraint`, `columns`, `references` (as
`table (columns)`), `on update`, `on delete`.

Either section reads `none` when the relation has no such objects.

## DDL

The `CREATE` statement, as the engine reports it — or, where the engine keeps
no statement to report, as lazysql synthesizes it from the introspection it
already has. A synthesized statement includes secondary `CREATE INDEX`
statements and foreign-key constraints, so it says as much as a stored one.

| Engine | Where the DDL comes from |
|---|---|
| MySQL / MariaDB | `SHOW CREATE TABLE` |
| SQLite | The stored statement in `sqlite_master` |
| PostgreSQL | Synthesized from `information_schema` and `pg_*` |
| DuckDB | Synthesized from the `duckdb_*` functions |

`y` opens the copy menu (the DDL statement is one of its entries) and `E`
writes it to a `.sql` file. See [Copy and export](copy-and-export.md).

!!! note "SQLite hides `AUTOINCREMENT`"
    SQLite's stored DDL text is the statement you originally wrote, so what it
    reports is exactly that — including the parts other engines normalize away.

## Relations

The same foreign-key information as the Indexes tab, but arranged as a hub for
walking the schema:

```text
Outgoing — orders references
  user_id → users.id
  product_id → products.id

        ┌─ orders ─┐

Incoming — referenced by
  order_items.order_id → id
```

The constraints the open relation declares are listed above it, the tables that
reference it below, both written `column → table.column` and read outwards
from the open relation.

| Key | Action |
|---|---|
| `j` / `k` | Pick a related table |
| ++enter++ | Open it, with the Relations tab still selected |
| ++esc++ | Unwind the walk |

Because ++enter++ opens the related table **unfiltered** and keeps the tab,
repeated ++enter++ walks the schema, and ++esc++ walks back.

The incoming half needs the foreign keys of every table in the namespace, so
it is scanned in the background — the tab says `scanning…` meanwhile — and
cached per database, shared with the grid's `G`.

!!! info "It is a hub, not an ERD"
    One table at a time, with its immediate neighbours. There is no whole-schema
    diagram; for comparing two schemas wholesale, see
    [Schema diff](schema-diff.md).
