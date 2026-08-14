# The command log

Every SQL statement lazysql executes is written to the log panel under the
main view — the page queries behind the grid, the introspection behind the
Structure tab, the `EXPLAIN` behind `ctrl+e`, the statements a commit runs, and
everything you type into the query editor.

```text
├─Command log───────────────────────────────────────┤
│ SELECT * FROM "users" LIMIT 100 OFFSET 0;         │
│ SELECT COUNT(*) FROM "users";                     │
│ -- copy users as CSV (streaming, max 5000 rows)…  │
│ UPDATE "users" SET "email" = $1 WHERE "id" = $2;  │
```

`@` — or `L`, for terminals where `@` is an AltGr chord — expands it into a
full-height scrollable view. ++esc++ closes it again.

## Why it exists

Two reasons, and both matter more than a debug feature would:

- **Nothing is hidden.** A TUI that edits a database has to be answerable for
  what it sent. The log is where "what did that key actually do" is answered,
  and it is the same feed whether the statement came from a keystroke or from
  your own SQL.
- **It is a source of statements.** The page query behind the grid, with your
  filter and sort applied, is a real statement you can read and reuse.

## What lands in it

| Kind | Example |
|---|---|
| Statements | `SELECT`, `UPDATE`, `INSERT`, `DELETE`, `EXPLAIN`, DDL |
| Status notes | `-- copy users as CSV (streaming, max 5000 rows)…` |
| Refusals | `-- REJECTED (read-only)`, `-- edit skipped: …` |
| Warnings | an unparsed filter clause, an invalid connection color |
| External tools | the stderr of `pg_dump` / `mysqldump` / `psql` while a dump or restore runs |

Values that travel as bound query parameters are shown as placeholders, not
interpolated — the log reflects what was actually sent.

!!! note "Passwords never appear"
    Not in a connection string, not in a dump command line, not in a note.
    A dump tool's password travels in a temporary `0600` file that is deleted
    the moment the tool exits, and neither the previewed command nor the log
    contains it.

## The log is not the query history

They are different things:

- the **command log** is this session's feed of everything lazysql executed,
  in one panel, gone when you quit;
- the **query history** records the statements *you* ran in the editor, with
  the engine and timestamp, and persists in
  `${XDG_STATE_HOME:-~/.local/state}/lazysql/history`. `H` in the editor opens
  it — see [The query editor](../guides/query-editor.md#history-and-snippets).
