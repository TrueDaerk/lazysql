---
type: Design Decision
title: Staged DDL for tables and indexes
description: Issue #224 — create/rename/truncate/drop relations and add/alter/drop columns and indexes stage into the same changeset as data edits, as SQL-free values the dialect renders; the driver refuses what the engine cannot do and every write on a read-only session, and a commit that changed the schema re-reads the tree and the metadata.
tags: [db, ui, ddl, changeset, dialects, read-only, security]
generated:
  by: claude-code/opus-5.5
  at: 2026-09-25T00:00:00Z
---

# Staged DDL

## Decision

Schema changes ride the [staged changeset](staged-changeset.md) instead of
becoming a second, unstaged way to change the database. `S` opens a schema
menu — relation-level operations in `[2] Objects`, column- and index-level
ones in the main view — every entry ends in a centered modal, and every
modal ends in `Changeset.StageSchema`. Nothing executes before `c`, and the
commit is the same `ExecTx` call data edits use.

## The changes carry values, never SQL

`internal/db/ddl.go` adds nine `db.Change` implementations — `CreateTable`,
`AddColumn`, `AlterColumn`, `DropColumn`, `RenameRelation`, `DropRelation`,
`TruncateTable`, `CreateIndex`, `DropIndex` — grouped by the `SchemaChange`
interface. They hold names, a type name and a `ColumnDefault` (kind + value),
not statement text. `Change`'s methods stay unexported, so no UI package can
put SQL of its own into a commit.

`Change.Statement(d) Statement` became `statements(d) ([]Statement, error)`:
an alter may need several statements (a PostgreSQL rename cannot share an
`ALTER TABLE` with other actions; DuckDB takes one action per statement), and
an engine that cannot do an operation must be able to say so.
`Changeset.Statements` returns an error for the same reason — a commit that
silently skipped an unrenderable change would not be the commit the user
confirmed. It only fails if a changeset outlives the connection it was staged
for; `resetBrowse` discards it on every switch anyway.

## Dialect differences live behind the Driver

Each `Dialect` answers two new questions (`internal/db/ddl_dialects.go`):

- `schemaSupport(op SchemaOp) error` — nil, or an `ErrUnsupported` carrying
  the engine's reason (`unsupportedError` unwraps to `ErrUnsupported`, its
  message is the reason alone, which the UI shows verbatim);
- `schemaSQL(c) ([]Statement, error)` — the spelling, with the shared forms in
  `schemaSQLCommon` and each dialect overriding only where it differs.

`SchemaOp` is finer than the user-visible operations where engines split
them: `OpRenameColumn` vs `OpAlterColumn` (SQLite renames but cannot redefine),
`OpRenameTable` vs `OpRenameView` (SQLite cannot rename a view). A change
reports the ops it needs (`AlterColumn` needs only the ones for what the user
actually changed), and rendering asks about every one before spelling
anything. The per-engine detail is in
[reference/ddl-per-dialect](../reference/ddl-per-dialect.md).

`Driver` gains the session-aware entry points the UI uses:

- `SchemaSupport(op)` — what the menus ask before offering an entry;
- `SchemaSQL(c)` — renders a change to the exact statements the commit will
  run, without running anything. The UI stages only what it accepts, and
  logs those statements (`-- stage: …`) at staging time.

## What the UI shows for an unsupported operation

The menu entry stays, labelled `— not supported by SQLite`, and choosing it
opens a modal with the engine's reason instead of a form. Hiding it would
leave the user wondering where "truncate" went; offering it as if it worked
would produce SQL that fails on commit. The SQLite alter form shrinks to the
name field with the reason in its body, since a rename is the one alteration
SQLite has.

## Validation: identifiers quoted, values escaped, fragments held to a grammar

- Names go through `QuoteIdent`, so they only need to be non-empty and
  NUL-free (`needName`).
- A text default is an escaped literal (`QuoteLiteral` — the same per-dialect
  escaping as [reference/sql-literal-escaping](../reference/sql-literal-escaping.md));
  a numeric default must match a number literal. No engine accepts a bound
  parameter in DDL, so "parameterized" is not available here — every DDL
  statement is rendered with no args, and the commit preview shows `args []`.
- A type and a default expression are SQL fragments by nature.
  `ValidateTypeName` allows `[A-Za-z0-9_ (),.\[\]]` with balanced parentheses
  and commas only inside them (a top-level comma would start a second column
  definition the preview never named). `ValidateExpression` tokenizes with
  `sqlhl` and refuses comments, a `;` outside a literal, parentheses that
  close more than they opened or stay open, a top-level comma, and a literal
  or quoted identifier left open — detected by appending a ` )` sentinel and
  checking it is still a token of its own.

## Read-only: refused at the driver

`conn.SchemaSQL` and `conn.SchemaSupport` return `ErrReadOnly` on a read-only
session, and `SchemaSQL` writes the refused statement to the command log as
`-- REJECTED (read-only)` like every other refusal. `ExecTx` would refuse the
commit anyway; the stage-time refusal means nothing is ever staged. The UI's
`S` answers `readOnlyBlocked` first and `S` joins `writeBindings`, so it
leaves the options bar — see [read-only-connections](read-only-connections.md).

## Destructive operations confirm twice

Drop table/view, truncate, drop column and drop index open a danger confirm
that names the rendered statement and says confirming only stages it. The
commit modal is the second confirm, like for every staged change.

## Visible before commit

- the stage-time log line and the commit preview (exact SQL, args);
- the Structure and Indexes tabs end with a "Staged schema changes" block for
  the open table (`stagedSchemaLines`), the table above it giving up the rows;
- `[2]` notes (`staged: drop`, `staged: rename → x`, `staged: 2 changes`, and
  `staged: + t` on a Tables category for a `CREATE TABLE`), recomputed by
  `markStagedSchema` on every `refreshTree`, which staging, unstaging,
  discarding and committing all end in;
- `S` → *staged schema changes* lists them, `enter` unstages one.

## After the commit

`changesCommittedMsg` carries the schema changes it committed, and
`afterSchemaCommit` then: re-reads the relation listing of every namespace a
change targeted (only where the tree holds one — an unexpanded namespace has
nothing stale), drops the foreign-key caches and the completion column cache,
and either reloads the open relation's metadata and page, closes it (dropped,
or renamed while browsed from another namespace) or reopens it under its new
name. On MySQL/MariaDB a *failed* commit re-reads too, because DDL before the
failure has already been committed by the engine.

## Create table is a draft, not a form

A table has a variable number of columns, which the fixed-field `formModal`
cannot express. The first form takes the table name and the first column
(prefilled `id integer NOT NULL PRIMARY KEY`); after it, a `tableDraft` held by
the closures of a `menuModal` lists the columns (`enter` edits one, with a
remove toggle), `a` adds one, `r` renames the table and `s` stages the whole
`CREATE TABLE`. Each column is dry-rendered through the dialect when its form
is submitted, so a bad type or default is caught on the form that typed it.

## Rejected alternatives

- **Executing DDL directly behind a confirm** — a second, unstaged write path,
  exactly what CLAUDE.md's staging rule forbids.
- **Storing rendered SQL in the change** — would make the changeset
  dialect-bound and let a UI-built string reach the commit.
- **Direct keys per operation** (`n` add column on the Structure tab, …) — the
  Data tab already binds `n`/`d`/`e` to row operations on the same panel's
  action list; one `S` menu per context keeps every operation discoverable
  in one place and leaves room for the "not supported" entries.
- **Falling back to `DELETE FROM` for SQLite's missing `TRUNCATE`** — a
  different statement with different semantics (triggers fire, no reset of
  `AUTOINCREMENT`); the issue asks for `ErrUnsupported`, and row deletes are
  already stageable.
