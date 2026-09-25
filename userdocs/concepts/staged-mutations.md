# Staged mutations

lazysql never executes destructive SQL as a side effect of editing. Changing a
cell, deleting a row and inserting one all *record* what you asked for. The SQL
runs when you explicitly commit — and then all of it runs in **one
transaction**, so a failure applies nothing.

This is the lazygit staging area, applied to rows — and to the schema: the
[schema changes](#schema-changes-ddl) `S` stages ride the same changeset.

## What staging looks like

| Key | Stages |
|---|---|
| `e` | An `UPDATE` of the cursor cell |
| `d` | A `DELETE` of the cursor row |
| `n` | An `INSERT` — a form opens for the new row's values |
| `D` | An `INSERT` prefilled from the cursor row, with its key cleared |

Staged changes are visible in the grid rather than hidden in a queue:

- an edited cell shows its **new** value, tinted;
- a row staged for deletion is struck through in red;
- a staged insert appears as a green **phantom row** appended after the page,
  with `DEFAULT` in every column you left out.

## Committing and undoing

| Key | Action |
|---|---|
| `c` | Commit everything staged — a confirm modal first, then one transaction |
| `u` | Unstage what is under the cursor |
| `U` | Discard the whole changeset |

`u` reads the cursor: on a phantom row it drops that whole `INSERT`, on a row
staged for deletion it drops the `DELETE`, and anywhere else it drops the
cursor cell's edit.

Editing a cell back to its original value **unstages** it instead of staging a
no-op `UPDATE`, so undoing by hand works the way you would expect.

Quitting with changes staged asks first — `q` on a dirty changeset opens a
confirm that names how many changes would be lost.

## How a row is identified

A staged change targets a row by its **declared primary key**, never by row
number, offset or a heuristic — and always by the full key, composite keys
included. Two consequences follow:

!!! warning "A table without a primary key is not editable"
    Not a limitation of the editor but of what can be written safely: without a
    key there is no statement that provably touches exactly the row you meant.
    Such a table opens and pages normally; only the staging keys refuse.

The second consequence is that the identity survives everything the page does
underneath it. Re-sorting, filtering, turning a page or reloading does not
disturb what is staged, because nothing staged refers to a screen position.

## Rules that keep the changeset coherent

- **Re-editing replaces.** Two edits to the same cell are one pending change,
  not two — the second replaces the first.
- **A staged delete wins over the cell edits of that row.** Staging the delete
  drops them, and `e` on a row already staged for deletion is refused with a
  note in the command log.
- **A staged insert is not a row yet.** `e` and `d` on a phantom row refuse and
  point at `u`, which removes the whole insert.
- **Commit order is staging order**, across kinds — the statements run in the
  sequence you created them.
- **A failed commit rolls back and keeps the changeset**, so you can fix the
  cause and commit again rather than reconstruct what you had.
- **The changeset never mixes with an editor transaction.** While a
  [transaction](../guides/query-editor.md#transactions) is open in the query
  editor, `c` is refused with an explanation: the changeset commits on another
  connection and could block on rows the transaction holds. Commit or roll
  back the transaction first.

## Multi-row edits

With a [row selection](../guides/editing-data.md#editing-a-column-across-rows)
up, `e` opens the usual edit modal for the *cursor column* and stages the
confirmed value in that column of **every** selected row — one pending change
each, all visible, none executed before `c`. Rows that cannot be identified
safely (already staged for deletion, or with a primary key the result set does
not carry) drop out and are named in the command log.

## Schema changes (DDL)

`S` opens the schema menu. What it offers depends on where you press it:

| Where | Operations |
|---|---|
| `[2] Objects`, on a table or view | create table, rename, truncate (tables), drop |
| `[2] Objects`, anywhere else | create table in that database |
| Main view (any tab of an open table) | add column, create index, drop index |
| Main view, Structure or Data tab | alter or drop the column under the cursor, too |

Every entry opens a centered modal — a form, a prompt or a confirm — and `esc`
cancels at every step. Confirming **stages** the change; nothing runs before
`c`. What is staged shows up:

- as the exact statement in the command log (`-- stage: …`) and, with every
  other staged change, in the commit preview;
- at the bottom of the Structure and Indexes tabs of the table it applies to;
- as a note on the relation in `[2]` (`staged: drop`, `staged: rename → x`),
  and on its database's Tables category for a `CREATE TABLE`.

`S` → *staged schema changes* lists them; `enter` on one unstages it. `U`
discards them with everything else.

Destructive operations — drop table or view, truncate, drop column, drop index
— get a confirm of their own, naming the statement, before they are staged.

After a commit that changed the schema, `[2]` re-reads the affected databases
(a dropped table does not linger), the Structure/Indexes/DDL tabs re-read the
open table, and a table the commit dropped is closed — or reopened under its
new name after a rename.

Types and defaults are the parts of a DDL statement no engine takes as a bound
parameter. A type must look like a type (`varchar(40)`, `numeric(10, 2)`,
`timestamp with time zone`) and a default is either a text literal — escaped
for the engine — a number, `NULL`, or a single SQL expression that cannot end
the statement. Names are always quoted for the engine.

!!! warning "Not every engine can do everything"
    An operation the engine cannot perform is marked *not supported* in the
    menu and explains why when chosen: SQLite has no `TRUNCATE`, cannot rename
    a view, and can only **rename** a column — not change its type, NULL-ness or
    default. See [Engines](../reference/engines.md#schema-changes).

!!! warning "MySQL and MariaDB commit DDL on their own"
    Both engines end the transaction at every DDL statement. A commit with
    schema changes that fails part-way keeps what ran before the failure — the
    commit preview says so on those engines.

## Read-only connections

A connection marked read-only refuses to stage anything at all: cell edits, row
inserts, row deletes, schema changes and the commit answer `connection is read-only`, their
keys drop out of the options bar, and every blocked attempt is written to the
command log marked `-- REJECTED (read-only)`. See
[Configuration](../guides/configuration.md#read-only-connections).

## What is *not* staged

Statements you run yourself in the [query editor](../guides/query-editor.md)
are not staged — they are your SQL and they execute when you run them. lazysql
only insists on a confirmation for an **unguarded** `DELETE` or `UPDATE` — one
with neither a `WHERE` nor a `LIMIT` at its own level, which is the shape that
rewrites the whole table. Everything else runs as written, and appears in the
[command log](command-log.md) afterwards.
