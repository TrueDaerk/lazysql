---
type: Design Decision
title: Staged cell edits with an explicit commit transaction
description: Why edits accumulate in an engine-agnostic changeset instead of executing, how a row is identified only by its declared primary key, and how the commit runs everything in one transaction.
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
editor unstages the cell. Row insert/delete (a separate issue) will
extend the same structure.

### Rows are identified by the primary key, or not at all

The key columns come from the cached metadata fetch (the same one the
Structure tab uses; `e` triggers it lazily like `y` does for the DDL,
via `metaView.editAfterLoad`). A table without a declared primary key
gets an explanatory modal instead of an editor: identifying a row by
"all columns" or a rowid heuristic can silently hit more rows than the
one on screen, so lazysql refuses to guess. For rendering, the grid
does *not* need metadata: `Changeset.PKColsFor` recovers the key
columns from any staged change of the table, which is how staged cells
stay highlighted (yellow, showing the staged value) after the metadata
cache was reset.

### Commit is one transaction, and failure keeps the changeset

`ExecTx` wraps all statements in `BeginTx … Commit`; the first error
rolls everything back and the reply keeps the changeset intact, so the
user fixes the offending edit and retries. On success the exact
parameterized statements are appended to the command log between
`BEGIN;` and `COMMIT;` lines, recorded in the history panel, the
changeset is cleared and the page reloads. `u` unstages the cursor
cell, `U` confirms and discards everything; `resetBrowse` (connection
switch/removal) also discards, because staged changes reference tables
of the connection that owned them.

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
