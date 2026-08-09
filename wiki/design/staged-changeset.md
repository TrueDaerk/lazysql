---
type: Design Decision
title: Staged cell edits and row operations with an explicit commit transaction
description: Why edits, row deletes and row inserts accumulate in an engine-agnostic changeset instead of executing, how a row is identified only by its declared primary key, and how the commit runs everything in one transaction.
tags: [tui, db, editing, changeset, transactions, security]
generated:
  by: claude-code/fable-5
  at: 2026-08-09T00:00:00Z
---

# Staged changeset

## Decision

`e` on a Data-tab cell never executes SQL. It records a
`db.CellChange` in a `db.Changeset` (`internal/db/changeset.go`), and
the UPDATEs only run when the user confirms the `c` commit modal —
all of them inside one transaction via `Driver.ExecTx`. This is the
lazygit staging model applied to mutations, and it is what the
"never auto-execute destructive SQL" rule looks like in code.

### The changeset is engine-agnostic

A `CellChange` holds `(Database, Table, PKCols, PKVals, Column,
OldValue, NewValue)` — plain normalized values, no SQL. SQL is only
rendered by `UpdateSQL(dialect, change)`, which quotes identifiers and
numbers placeholders per dialect; the new value and every key value
travel as parameters. The changeset is keyed by
`(database, table, column, typed pk values)`, so editing a cell twice
replaces the pending change (keeping the original `OldValue`) instead
of stacking two UPDATEs, and restoring the original value in the
editor unstages the cell.

### Row operations are the same changeset, not a second one

`d`, `n` and `D` stage `db.RowDelete` and `db.RowInsert` values into the
*same* `Changeset` as cell edits, so `c` still commits everything in one
transaction and `U` still discards everything. The three types satisfy
one `db.Change` interface — `key()`, `target()` and
`Statement(dialect)` — and the changeset keeps a single ordered `[]Change`
plus a key index, which is what makes commit order equal staging order
across kinds. The interface's methods are unexported, so no UI package
can add a fourth kind carrying its own SQL text.

Keying differs by kind, because identity does:

- a delete keys on `(database, table, typed pk values)`, so pressing `d`
  twice on a row is a no-op instead of two DELETEs;
- an insert has *no* natural identity — two identical new rows are two
  rows — so `StageInsert` assigns a monotonic `ID` and `u` unstages by
  it.

Staging a delete drops that row's pending cell edits (`StageDelete`):
UPDATEing a row the same transaction removes is dead work, and hiding it
under a strikethrough row would be worse. For the same reason `e` on a
row already staged for deletion is refused rather than silently staged.

### The grid renders what is staged, including rows that do not exist

`buildGrid` returns a `rowKind` per rendered row next to the columns:
plain, deleted (red, struck through) or inserted. Staged inserts are
appended after the page as *phantom* rows — green, with `DEFAULT` in
every column the INSERT leaves out — so the pending row is visible where
it will land. The cursor has to be able to reach them, so `dataView`
carries an `extraRows` count that `Model.clampCursor` re-derives from the
changeset before every clamp; `dataView` itself never learns about the
changeset.

### Inserts name only the columns the form filled in

The insert form (`n`, or `D` prefilled from the row under the cursor)
gives every column three states: a typed value, an explicit `NULL`
(`ctrl+n`) or the column's default (`ctrl+d`). A column left on DEFAULT
is simply absent from the generated statement, which is how
auto-increment and `DEFAULT`-clause columns get their engine-assigned
value — and why `D` clears the key of the row it duplicates. The form
refuses to stage a NOT NULL column that nothing will fill in; everything
else is left to the engine, with the commit's rollback as the safety
net. Deleting needs the primary key and is disabled without one;
inserting does not, and is allowed on a PK-less table.

### Rows are identified by the primary key, or not at all

The key columns come from the cached metadata fetch (the same one the
Structure tab uses). `e`, `d`, `n` and `D` all need it before they can
open anything, so they park themselves in `metaView.afterLoad` — one
`actionID`, replayed through `runAction` when the reply lands — and the
same key works identically warm or cold. A table without a declared
primary key
gets an explanatory modal instead of an editor: identifying a row by
"all columns" or a rowid heuristic can silently hit more rows than the
one on screen, so lazysql refuses to guess. For rendering, the grid
does *not* need metadata: `Changeset.PKColsFor` recovers the key
columns from any staged change of the table, which is how staged cells
stay highlighted (yellow, showing the staged value) after the metadata
cache was reset.

### Commit is one transaction, and failure keeps the changeset

`ExecTx` wraps all statements — UPDATEs, DELETEs and INSERTs alike — in
`BeginTx … Commit`; the first error
rolls everything back and the reply keeps the changeset intact, so the
user fixes the offending edit and retries. On success the exact
parameterized statements are appended to the command log between
`BEGIN;` and `COMMIT;` lines, recorded in the history panel, the
changeset is cleared and the page reloads. `u` unstages whatever is
under the cursor — a phantom row's whole INSERT, a struck-through row's
DELETE, otherwise the cursor cell — and `U` confirms and discards
everything; `resetBrowse` (connection switch/removal) also discards,
because staged changes reference tables of the connection that owned
them.

## Consequences

- Typed input is converted back toward the cell's previous type
  (`convertInput`): text that parses as the old `int64`/`float64`/
  `bool`/`time.Time` is bound as that type, anything else is bound as
  a string and the engine gets the final say on commit.
- The NULL toggle (`ctrl+n`) is the only way to stage SQL NULL — an
  emptied input stages an empty string. Toggling NULL on a NOT NULL
  column warns in the modal but is allowed; the commit's rollback is
  the safety net.
- The status line badge ("3 staged changes") counts the whole
  changeset, not just the open table, because `c` commits all of it.
- Insert form text is bound with the type the *column* declares
  (`convertForColumn`), not the type a neighbouring value happened to
  have. `NUMERIC`/`DECIMAL` are deliberately bound as strings: a
  `float64` would drop digits the user typed.
- `D` duplicates the values the grid *shows*, staged cell edits
  included. Copying values only the server still holds would contradict
  what the user is looking at.
- An insert with every column on DEFAULT is still a valid statement, and
  the engines disagree on how to spell it — see
  [reference/insert-default-values](../reference/insert-default-values.md).
