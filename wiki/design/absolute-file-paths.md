---
type: Design Decision
title: Resolve SQLite/DuckDB file paths to absolute at form-submit time, not at load
description: issue #157 — a relative or `~` File path is expanded and made absolute in Connection.toConnection() before it reaches Config.Upsert, so a saved profile keeps resolving to the same file regardless of lazysql's later working directory; existing relative paths on disk are left untouched until the user re-saves.
tags: [config, connections, sqlite, duckdb]
generated:
  by: claude-code/sonnet-5
  at: 2026-08-16T00:00:00Z
---

# Resolve SQLite/DuckDB file paths to absolute at form-submit time, not at load

`Connection.File` (`internal/config/config.go`) used to persist whatever the
user typed. A relative path like `./out/db.duckdb` only resolved while
lazysql happened to run from that project directory — starting it anywhere
else silently broke the connection. Fixed by resolving to an absolute path
once, at the connection-form submit boundary, not by rewriting paths at
load.

## Where the resolution happens

`config.ResolveFilePath(path string) (string, error)` expands a leading `~`
against `os.UserHomeDir()` and then calls `filepath.Abs`, which resolves a
relative path against the process's current working directory. It is called
from `formModal.toConnection()` in `internal/ui/connections.go`, right after
`c.File` is read off the form field and the file-derived default name is
computed — name derivation happens first because the basename is identical
either way, and the placeholder promise ("empty = named after the file") is
about what the user typed, not the resolved path.

An empty `File` is left empty: for DuckDB that means in-memory, and turning
it into the cwd would silently create an on-disk file the user never asked
for.

`toConnection()` backs both the save path and `ctrl+t` (test connection), so
testing now dials the same absolute path that save would persist — no
behavior split between the two.

## Why not resolve at load instead

The obvious alternative — expand relative paths every time `Config.Load`
runs — was rejected: it would leave every *existing* config working (today's
behavior, "relative resolves against lazysql's cwd") but never *fix* it,
since the resolved value only lives in memory and the file on disk stays
relative forever. Resolving at save is the only point where "this is what
the user just told us, expand it once and remember it" is true; after that,
`config.toml` holds the actual answer and no runtime cwd-dependent step is
needed to open the connection.

## Why no migration rewrite on load

`LoadFrom` deliberately does not walk existing connections and rewrite
relative `File` values to absolute, matching the project's "never rewrite a
hand-edited config without user action" posture (see
[design/connection-secrets](connection-secrets.md) and
[design/configurable-keys-and-theme](configurable-keys-and-theme.md) for the
same posture applied to secrets and `[keys]`/`[theme]`). A relative path
already on disk keeps resolving against cwd exactly as before — unchanged
behavior, not a regression — until the user re-opens that profile's form and
saves it, at which point `toConnection()` makes it absolute. This also means
a config file checked into a dotfiles repo and shared across machines with
different home directory layouts is not silently pinned to one machine's
absolute paths behind the user's back.

## Interaction with path completion

`internal/pathcomplete` (see
[design/path-completion-in-forms](path-completion-in-forms.md)) is untouched:
its `Expand` runs against whatever partial text is currently typed, purely
for candidate lookup, and every candidate is rendered back in the user's own
notation (a leading `~` stays `~` while typing). Resolution to an absolute,
persisted value only happens once, in `toConnection()`, after the user
submits — so the field still shows and completes `~`/relative notation while
editing, and only the value written to `config.toml` (and shown back on
re-edit, via `draftFrom`) is absolute.
