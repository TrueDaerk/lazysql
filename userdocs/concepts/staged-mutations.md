# Staged mutations

lazysql never executes destructive SQL as a side effect of editing. Changing a
cell, deleting a row and inserting one all *record* what you asked for. The SQL
runs when you explicitly commit — and then all of it runs in **one
transaction**, so a failure applies nothing.

This is the lazygit staging area, applied to rows.

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

## Multi-row edits

With a [row selection](../guides/editing-data.md#editing-a-column-across-rows)
up, `e` opens the usual edit modal for the *cursor column* and stages the
confirmed value in that column of **every** selected row — one pending change
each, all visible, none executed before `c`. Rows that cannot be identified
safely (already staged for deletion, or with a primary key the result set does
not carry) drop out and are named in the command log.

## Read-only connections

A connection marked read-only refuses to stage anything at all: cell edits, row
inserts, row deletes and the commit answer `connection is read-only`, their
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
