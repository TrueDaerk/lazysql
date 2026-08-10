---
type: Design Decision
title: Per-connection read-only mode
description: One guard in the driver session enforces read-only, the UI only decorates it, and classification runs on the tokenizer.
tags: [db, ui, config, read-only, safety]
generated:
  by: claude-code/opus-5
  at: 2026-08-10T00:00:00Z
---

# Per-connection read-only mode

`read_only = true` on a `[[connections]]` profile makes a connection
refuse every write. An absent key means read-write, so configs written
before the flag existed load unchanged.

## One choke point, not scattered checks

The three doors every write in lazysql goes through are `conn.Exec`
(query-editor DML/DDL, row operations), `conn.ExecTx` (the changeset
commit) and the `Query*` family (a data-modifying CTE returns rows, so it
arrives as a query). All three are guarded in `internal/db/readonly.go`
and `conn.go`; nothing above them is trusted to check first.

- `Exec` and `ExecTx` are refused outright — nothing that only reads is
  routed through them.
- `Query`, `QueryLimit` and `QueryStream` are refused when
  `db.ContainsWrite` finds a write among the statements of what they were
  handed. It splits first rather than asking `IsWrite` about the whole
  string: text that ends up inside a query — a free-text row filter, say
  — could carry a second statement after a `;` that the leading keyword
  says nothing about.
- The refusal is `db.ErrReadOnly`, whose message — `connection is
  read-only` — is what the UI shows verbatim, so the wording is the same
  whether the UI or the session stopped the action.
- Introspection does not go through those doors at all: dialect code uses
  the `querier` adapter, so listing tables and reading DDL is unaffected.

Every rejection is written to the command log through the same `Logger`
that records executed statements, prefixed `-- REJECTED (read-only)` and
carrying `ErrReadOnly` as its outcome. A blocked attempt is part of the
audit trail, not a silent no-op.

`Driver.ReadOnly()` reports the mode. The flag is set once at
`db.OpenOpts` and never changes: a live session cannot be talked out of
read-only mode.

## The UI decorates, it does not enforce

`Model.readOnly()` asks the *driver*, not the profile — a profile edited
after connecting must not make the UI believe writes are back. With it
set:

- `startEdit`, `startDelete`, `startInsert` and `openCommitModal` return
  a status line instead of staging (`readOnlyBlocked`, `internal/ui/edit.go`);
  `openCommitModal` is guarded too, so a changeset staged before the
  connection changed still cannot get out.
- `submitQuery` rejects a whole script that contains any write before the
  run starts (`rejectReadOnlyRun`), so a multi-statement script never
  half-runs. The reason lands in the command log and in the Data tab.
- The options bar drops the write keys (`keyMap.writeBindings`), while
  `?` keeps listing them — the "every binding appears in `?`" rule is
  intact, and the keys stay dispatchable so pressing one still explains
  itself instead of doing nothing.
- A 🔒 (`lockMark`) precedes the profile's name in panel `[1]`, marks the
  main view's title bar and appears as the `access` line of the
  connection detail. The panel's marker lives in `sidePanel.decor`, keyed
  by row text, so the filter, the cursor and `selectByName` keep working
  on the undecorated name.

## Classification is tokenizer-based

`ClassifyStatementFor` (and `IsWrite` over it) runs on `internal/sqlhl`'s
tokenizer, never on substring search. That is what makes `WITH x AS
(DELETE …) SELECT *` a write while `SELECT 'delete me'`, `-- DELETE FROM
t` and `SELECT "delete" FROM t` stay reads. Splitting a script is
dialect-aware for the same reason, so backticks, `#` comments and
dollar-quoted bodies are read the way their engine reads them.

Two deliberate asymmetries:

- An unrecognized leading keyword — including a statement that is only
  comments — classifies as a **write**. Erring that way costs a false
  refusal; erring the other way costs a write on a production database.
- `EXPLAIN ANALYZE` classifies as a read for the query editor (it returns
  a plan to render) but as a write for `IsWrite`, because on PostgreSQL
  and MySQL it really does execute the statement it explains. Plain
  `EXPLAIN` never does and is allowed.

## Engine-level read-only is defence in depth

`ConnParams.ReadOnly` adds the engine's own read-only parameter to the
DSN — `mode=ro`, `access_mode=read_only`,
`default_transaction_read_only=on`, `transaction_read_only=1`. The
read-only parameters win over a profile's own options, so an option
cannot quietly re-open the connection for writing, and the profile's
option map is copied rather than mutated.

None of them is the guarantee: MySQL's variable does not stop DDL,
PostgreSQL's is a default a `SET` could undo, and DuckDB cannot apply it
to an in-memory database at all. See
[reference/read-only-per-engine](../reference/read-only-per-engine.md)
for the per-engine detail and version floors.
