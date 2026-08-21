---
type: Design Decision
title: Ephemeral file connections — open a file without saving a profile
description: issue #188 — `lazysql <file>` and `o` on panel [1] open a SQLite/DuckDB/Parquet file for one run; the profile lives only in Model.ephem, the engine is decided by magic bytes rather than the extension, Parquet is browsed through an in-memory DuckDB view created by a new Options.Setup hook, and disconnecting drops the row back to the saved-connections view.
tags: [connections, cli, sqlite, duckdb, parquet, config]
generated:
  by: claude-code/opus-5
  at: 2026-08-21T00:00:00Z
sources:
  - resource: https://duckdb.org/docs/stable/internals/storage
    note: DuckDB storage header — 8-byte checksum followed by the "DUCK" magic
  - resource: https://www.sqlite.org/fileformat.html
    note: SQLite database header string "SQLite format 3\0" at offset 0
  - resource: https://parquet.apache.org/docs/file-format/
    note: Parquet files begin and end with the 4-byte "PAR1" magic
---

# Ephemeral file connections

Every connection used to have to pass through the wizard and land in
`config.toml` before it could be opened. For a one-off look at a local file
that is pure ceremony — the path *is* the connection — and it leaves a
throwaway profile behind. Issue #188 adds a second, unsaved kind of
connection with two entry points and one behaviour.

## The two entry points

- **CLI**: a positional argument, `lazysql mydb.db`. `main.go` resolves it
  through `Model.OpenFileOnStart` *before* `tea.NewProgram` runs, so a typo
  is a plain command-line error (stderr + exit 1) rather than a modal over
  an empty screen. The dial itself is issued from `Init`, so the connect
  reports through the normal UI like any other.
- **In-TUI**: `o` on panel `[1]` (`actOpenFile`) opens a one-field form
  whose `File` field carries the same path completion the connection form's
  does (see [design/path-completion-in-forms](path-completion-in-forms.md)).
  A bad path reports *in the form*, which stays open.

Both build the same value and go through the same `openEphemeral`, so the
session view they produce is identical by construction, not by parallel
implementation — the acceptance criterion "the TUI variant produces exactly
the same session view as the CLI one" is a test over the two paths' models
(`TestOpenFileModalMatchesCLIVariant`).

## Where an ephemeral connection lives

In `Model.ephem` (`*ephemeralConn`, `internal/ui/ephemeral.go`) and nowhere
else. It never reaches `Config`, so it cannot be written by any of the
flows that call `cfg.Save()`.

The connections panel is rendered from `Model.connNames()` — the ephemeral
name first, then `cfg.Names()` — and every lookup of "the connection called
X" goes through `Model.findConn`, which checks `ephem` before the config.
`m.cfg.Find` survives only where the question really is about the config
file (the wizard's name collisions, the schema diff's *other* side).

Consequences, each one deliberate:

- **Never persisted**: `saveSession` returns early when the active
  connection is the ephemeral one, so quitting an ephemeral session leaves
  the last real session on disk untouched and nothing is restored next
  start.
- **Not editable**: `e`, `d` and `y` log "… is ephemeral" instead of
  opening a form — there is no profile to edit, delete or duplicate.
  `K`/`J` skip it: it is not part of the saved order.
- **Disconnect removes it**: `actDisconnect` calls `dropEphemeral`, so the
  panel returns to the plain saved-connections list with the app still
  running. That is the only way it goes away.
- **One at a time**: opening a second file replaces the first, exactly like
  switching between saved profiles.
- **Name collisions**: the row is labelled after the file's base name, made
  unique against the saved profiles (`local-sqlite (2)`). The name is a
  label only — nothing keys config or keyring entries by it.

`refreshConnections` now also prunes `connState` down to the rows that
exist, so a status cannot outlive the connection it described (an ephemeral
row's, or a removed profile's).

## Engine detection: magic bytes, extension as fallback

`db.SniffFile` (`internal/db/sniff.go`) reads the first 16 bytes and
decides:

| Format | Magic | Offset |
|---|---|---|
| SQLite | `SQLite format 3\0` | 0 |
| Parquet | `PAR1` | 0 (also the last 4 bytes; the leading one is enough) |
| DuckDB | `DUCK` | 8 — the main header is an 8-byte checksum first |

Only when the bytes say nothing does the extension get a vote
(`FormatForExt`): `.sqlite`/`.sqlite3`/`.sq3`/`.s3db`, `.duckdb`/`.ddb`,
`.parquet`/`.parq`/`.pqt`. **`.db` is deliberately absent from that table** —
it is exactly the ambiguous case sniffing exists for, and an empty `.db`
file is better refused than opened with a coin-flipped engine.

The file must exist. Both the SQLite and the DuckDB driver create a
database when opened on a missing path, so a typo would otherwise leave a
stray empty database behind — `SniffFile` stats first and never creates
anything.

## Parquet has no dialect: it is a DuckDB view

`internal/db` knows five engines and Parquet is not one of them. Adding a
sixth would mean a dialect with no catalog, no DDL, no transactions and no
`information_schema` — an interface's worth of `ErrUnsupported`. Instead a
Parquet file opens an **in-memory DuckDB** session (`File` empty) that is
prepared with

```sql
CREATE VIEW "sales" AS SELECT * FROM read_parquet('/abs/path/sales.parquet')
```

built by `db.ParquetViewSQL` — the path quoted as a literal, the view name
(`db.ParquetViewName`: the file's base name, non-identifier characters
folded to `_`) as an identifier. From there the objects tree, the data
grid, the query editor, sorting, filtering and export are the DuckDB ones,
unmodified.

### Why a new `Options.Setup` rather than an Exec after Connect

The session is opened `ReadOnly: true` — nothing can be written back
through a `read_parquet` view, and the acceptance criterion asks for staged
mutations to be *disabled*, not to fail at commit. But the read-only guard
lives in `conn.Exec` (see [design/read-only-connections](read-only-connections.md)),
so the view could not be created through it.

`db.Options.Setup` is the answer: statements `conn.Connect` runs on the
fresh handle, after the ping and before anyone else sees the session. They
go through the same `logger.record` as everything else, so the view
statement shows up in the command log like any other statement (the project
rule), and a failing one fails the connect and closes the handle — no
half-prepared session. They deliberately bypass the read-only guard: they
are lazysql's own SQL, never the user's.

`dialRequest.setup` carries them from the UI, so a reconnect (or the schema
diff's own dial of side A) prepares the session exactly like the first
connect did.

## Alternatives rejected

- **Mark the connection ephemeral inside `config.Connection`** (a
  `toml:"-"` field) — the type would then describe two different lifetimes
  and every `Save()` path would need to remember to filter. Keeping the
  value out of `Config` entirely makes non-persistence structural.
- **Dial before the TUI starts** — errors would have nowhere to render, and
  the connect would block the first frame. Only the *path resolution*
  happens early, which is what the clean-stderr criterion actually needs.
- **Trust the extension** — `.db` alone is ambiguous, and the whole point
  of the feature is not having to tell lazysql what the file is.
- **A Parquet dialect** — see above; the file has no catalog to be a
  dialect over.
