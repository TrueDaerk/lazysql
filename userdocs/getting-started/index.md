# Getting started

Three pages, in the order you need them:

- **[Installation](installation.md)** — a prebuilt binary or a build from
  source, and what the DuckDB driver needs from your toolchain.
- **[Terminal setup](terminal-setup.md)** — lazysql runs anywhere, but a few
  bindings depend on what your terminal reports. This is how to find out, and
  what the fallbacks are.
- **[Your first connection](first-connection.md)** — the engine-first
  connection form, where the password goes, and the first table.

## The five minutes after that

Once a table is open, these are the keys worth trying in order:

| Key | What happens |
|---|---|
| `j` / `k` | Move the row cursor |
| `h` / `l` | Move the cell cursor across columns |
| `v` | Cell detail popup — the full value, JSON pretty-printed, BLOBs as hex |
| `s` | Sort the cursor column (ASC → DESC → off) |
| `/` | Type a `WHERE` clause into a line at the bottom of the grid |
| `<` / `>` | Previous / next main-view tab (Data, Structure, Indexes, DDL, Relations) |
| `e` | Edit the cell — the change is *staged*, not executed |
| `c` | Commit everything staged, in one transaction |
| `?` | Every binding that applies right now |

Nothing in that list except `c` sends a statement that changes data. See
[Staged mutations](../concepts/staged-mutations.md) for why.
