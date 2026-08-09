---
type: Design Decision
title: Schema diff — normalized snapshot model and dual-connection lifecycle
description: Why the diff compares db.Schema snapshots built from the existing introspection calls, how type synonyms are normalized only within one engine family, why both sides are dialed fresh, and why the report generates no migration SQL.
tags: [schema-diff, introspection, connections, diff, dialects]
generated:
  by: claude-code/fable-5
  at: 2026-08-09T00:00:00Z
---

# Schema diff

`D` on a connection in panel [1] compares its schema with another
connection's (issue #43). The design splits into a pure comparison model
in `internal/db/schemadiff.go` and a worker-driven UI flow in
`internal/ui/schemadiff.go`.

## The normalized schema model

A snapshot is `db.Schema`: the engine, a display label, and one
`SchemaTable` per table — `Columns []Column`, `Indexes []Index`,
`ForeignKeys []ForeignKey`, exactly the types the Driver interface
already returns. There is deliberately **no new introspection surface**:
`IntrospectSchema` is a loop over `ListRelations` + `TableColumns` +
`TableIndexes` + `TableForeignKeys`. Whatever a dialect reports for the
Structure tab is what the diff compares, so a dialect fix improves both
for free.

What the model leaves out is as deliberate as what it keeps:

- **Views are excluded.** Their shape is their query; comparing
  column lists of two views says nothing useful about whether they are
  "the same".
- **`Column.Extra` is not compared.** The field is documented as
  display-only in `db.go` and its vocabulary is pure engine noise
  ("auto_increment" vs "identity always").
- **Primary-key indexes are skipped in the index diff.** Engines name
  them on their own schedule ("PRIMARY", `<table>_pkey`, nothing at all
  in SQLite); the key itself is still compared through the columns'
  `PrimaryKey` flags.
- **Indexes and foreign keys match by name.** SQLite invents FK names
  (`fk_0`) because `PRAGMA foreign_key_list` has none; two SQLite files
  with identical constraint order still match. Cross-engine, name-based
  matching over-reports — accepted, see below.

## Type-synonym normalization is family-scoped

`NormalizeType` always folds case and whitespace, and maps an engine
family's type synonyms (`typeSynonyms`) **only when both sides belong to
the same family**. MariaDB folds onto MySQL as one family. So SQLite
`INT` vs `INTEGER` is not a difference, but MySQL `INT` vs PostgreSQL
`INTEGER` is reported — mapping across engines would need a lossy
"universal type" model that hides real differences (MySQL `TINYINT(1)`
vs Postgres `BOOLEAN` are *not* interchangeable in DDL). A cross-engine
diff instead states in its report header that types are compared
verbatim. The synonym maps are conservative on purpose: a missing
synonym costs a false "changed" line, a wrong one hides a real change.

`""` and `NO ACTION` are folded together for FK actions via the same
`referentialAction` helper the DDL synthesizer uses.

## Dual-connection lifecycle

The worker dials **both sides fresh** via the same `dial()` a normal
connect uses (keyring passwords, SSH tunnels and all) and closes them
when done — it never borrows the active session's driver. That costs
one extra dial when diffing the active connection, and buys: no shared
state with the browsing session, a diff that keeps working while the
user disconnects or switches connections, and one code path instead of
two. "Ask on connect" profiles chain password prompts (A's, then B's)
before the run starts, through `diffSecretMsg`.

Progress streams over an unbuffered channel exactly like the file
export (`startSchemaDiffCmd`/`waitDiffCmd`). The UI reads until the
done message lands — `esc` during a run only logs, it does not abandon
the channel, because an abandoned unbuffered channel would strand the
worker goroutine mid-send.

## Report, not migration

The output is a colored, scrollable report in the main view (added
green / removed red / changed yellow, the staged-changes color
language), copyable (`y`) and exportable (`E`) as plain text via
`SchemaDiff.RenderText`. Generating migration SQL is explicitly out of
scope: half a diff (index options, column order, engine-specific
defaults) does not round-trip into portable DDL, and a wrong ALTER is
worse than none. A follow-up could generate per-dialect suggestions for
the unambiguous subset (missing tables/columns/indexes).

Related: [db-driver-abstraction](db-driver-abstraction.md),
[main-view-tabs](main-view-tabs.md),
[reference/dialect-introspection-quirks](../reference/dialect-introspection-quirks.md).
