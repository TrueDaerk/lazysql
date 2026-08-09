---
type: Design Decision
title: DDL export to file — single table and whole database
description: Why the DDL tab's `E` skips the streaming export worker, how a whole-database DDL export orders tables by foreign-key dependency, and why a cycle falls back to alphabetical instead of failing.
tags: [tui, export, ddl, sql, foreign-keys]
generated:
  by: claude-code/sonnet-5
  at: 2026-08-09T00:00:00Z
sources:
  - resource: "GitHub issue TrueDaerk/lazysql#44"
---

# DDL export to file

## Decision

`E` already exports a relation's data — see
[design/copy-and-export](copy-and-export.md). Two more shapes of "get the
DDL out" build on the same key rather than a new one:

- On the **DDL tab**, `E` writes the cached `TableDDL` output verbatim to
  a `.sql` file instead of streaming rows. It shares nothing with
  `exportState`: a `CREATE TABLE` statement is a few kilobytes, so the
  write happens synchronously in the `tea.Cmd` closure with no worker
  goroutine, no channel and no progress messages — the streaming
  machinery in `internal/ui/export.go` exists for tables that can be
  megabytes, and a DDL statement never is.
- On the **Tables panel** (`[3]`), `E` exports every relation of the
  browsed database into one `.sql` file: a header comment, then
  `-- table: name` followed by that table's DDL, in dependency order.

Both stay inside `internal/ui`; neither adds a `Driver` method; both
read through `TableForeignKeys` and `TableDDL`, which
[design/main-view-tabs](main-view-tabs.md) already introspects for the
Structure/Indexes/DDL tabs.

### Dependency ordering is a pure function over names

`db.DDLOrder(tables []string, deps map[string][]string) (order []string, acyclic bool)`
lives in `internal/db/ddlorder.go`, next to the other engine-agnostic
helpers ([reference/sql-literal-escaping](../reference/sql-literal-escaping.md)
and friends). It is Kahn's algorithm with an alphabetical tie-break at
every step — not just at the end — so the order is deterministic
regardless of map iteration or the order tables were listed in.

`deps` comes from each table's foreign keys, filtered two ways before it
reaches `DDLOrder`:

- A self-reference (a `categories` table pointing at its own `id`) is
  dropped. It is not a real ordering constraint — a table always creates
  itself regardless of what its own constraint points at — and if
  `DDLOrder` treated it as a cycle, every tree/hierarchy table would
  trigger the alphabetical fallback for no reason.
- A foreign key naming a table outside the export set (a different
  schema, or a relation the export was not asked for) is dropped too. An
  export only ever covers what it was asked to export; a dependency it
  can never satisfy should not poison the ordering of the ones it can.

### A cycle falls back to alphabetical, not to failure

Two tables can reference each other (order confirmation ↔ payment, or
two tables built with `ALTER TABLE ADD CONSTRAINT` after the fact).
There is no total order that satisfies both edges, so `DDLOrder` returns
every table alphabetically and `acyclic = false`. The exported file says
so in its header (`-- ordering: alphabetical — the foreign keys form a
cycle`) and the command log repeats it — a silently wrong order would be
worse than an honestly absent one, and the file is still complete: every
`CREATE TABLE` a real engine can run standalone works regardless of
where in the file it sits, since the constraint text travels with the
statement that declares it.

### A relation without DDL does not abort the run

`TableDDL` can fail for one relation (a view the engine cannot reverse,
a permission gap) without the introspection call for any other relation
failing — the same distinction
[design/main-view-tabs](main-view-tabs.md) draws with `ddlErr` on the
single-relation fetch. The whole-database export keeps that property: a
failure is written inline as `-- DDL unavailable: <err>` and counted,
but the loop moves on to the next table. The final command-log line
names how many relations had no DDL, if any — a partial export that
says what it is missing beats one that silently produces fewer
`CREATE TABLE` statements than the database has tables.

### Guarded like the file export, without its cancel key

`Model.dbDDLExport` (`dbDDLExportState`) mirrors `exportState`: a
`running` flag so only one scan runs at a time, an `id` so a reply that
lands after the run it belonged to was superseded is ignored rather than
clobbering whatever started since, and a `cancel` `resetBrowse` calls
when the connection the scan reads through is about to close — the same
reason it already cancels the streaming table export and a running
script. What it does not have is an `X` binding: a whole-database export
is one `TableForeignKeys` and one `TableDDL` round trip per relation —
dozens of round trips, not the unbounded row count a table export
streams — so it is expected to finish before a user would reach for a
manual cancel key. The scan checks `ctx.Err()` before each relation's
round trip, so a `resetBrowse` cancellation (or the two-minute timeout)
stops it promptly instead of running every remaining relation through a
driver that may already be closed. The file itself is written once, at
the very end, so a cancelled run leaves nothing on disk — there is no
partial file to clean up the way the streaming export removes one.

## Consequences

- `E`'s meaning is now three-way context-sensitive: the Data (and
  Structure/Indexes/Relations) tabs still stream the table's rows, the
  DDL tab writes that one `CREATE TABLE` statement, and the Tables panel
  writes every relation's. All three share the key by panel/tab context,
  the same pattern [design/foreign-key-navigation](foreign-key-navigation.md)
  uses for `g`/`G` meaning different things by cursor position.
- The single-relation DDL file contains *exactly* `TableDDL`'s output —
  no header, no trailing semicolon added — so it is a drop-in
  `CREATE TABLE` fragment. The whole-database file is not: it needs the
  `-- table:` separators and the ordering note to be useful as one
  document, so it is not simply N single-relation files concatenated.
