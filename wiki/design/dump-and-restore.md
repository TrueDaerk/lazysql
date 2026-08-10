---
type: Playbook
title: Dump and restore via the engine's own tools
description: Per-engine command construction for pg_dump/psql, mysqldump/mysql and mariadb-dump/mariadb, the credential-file rule that keeps passwords out of argv and the environment, the SQL-only path for SQLite and DuckDB, and how a tunnelled connection gets a real local endpoint.
tags: [backup, dump, restore, security, credentials, ssh, process-lifecycle]
generated:
  by: claude-code/opus-5
  at: 2026-08-10T00:00:00Z
sources:
  - resource: "GitHub issue TrueDaerk/lazysql#51"
  - resource: "https://www.postgresql.org/docs/current/libpq-pgpass.html"
  - resource: "https://dev.mysql.com/doc/refman/8.4/en/option-files.html"
  - resource: "https://duckdb.org/docs/stable/sql/statements/export.html"
  - resource: "https://sqlite.org/lang_vacuum.html"
---

# Dump and restore via the engine's own tools

`B` on panel `[1]` or `[2]` opens a two-entry menu — dump or restore —
and each opens a form showing the exact command that is about to run, with
an editable arguments line and a path field. `internal/dump` builds the
command; `internal/ui/dumprestore.go` runs it and reduces its output into
the command log.

## Why an external tool at all

lazysql could serialize a database itself — `internal/export` already
streams CSV/JSON/SQL. It deliberately does not: a backup has to be
restorable, and only the engine's own tool knows about sequences, extension
objects, storage parameters, collations, definers and every other thing a
row-by-row exporter drops on the floor. The job here is to *invoke* those
tools correctly, not to reimplement them.

## Per-engine command matrix

| Engine | Dump | Restore | Kind |
| --- | --- | --- | --- |
| PostgreSQL | `pg_dump --host --port --dbname --no-password [--username] [--schema] --file=OUT` | `psql --host --port --dbname --no-password [--username] --set=ON_ERROR_STOP=1 --file=IN` | external process |
| MySQL | `mysqldump [--defaults-extra-file] --host --port [--user] --result-file=OUT DB` | `mysql [--defaults-extra-file] --host --port [--user] DB` with the dump on **stdin** | external process |
| MariaDB | `mariadb-dump`, falling back to `mysqldump` | `mariadb`, falling back to `mysql` | external process |
| SQLite | `VACUUM INTO 'OUT'` | copy the file back over the database file | Driver SQL / file copy |
| DuckDB | `EXPORT DATABASE 'DIR'` | `IMPORT DATABASE 'DIR'` | Driver SQL |

Details that are not obvious from the table:

- **`--no-password` is mandatory, not decorative.** Without it, libpq
  prompts on the terminal when the credential file does not match — and the
  TUI owns the terminal, so the child would block forever with no visible
  prompt. `-w` turns that into an immediate, reportable failure.
- **`--set=ON_ERROR_STOP=1` on restore.** `psql` otherwise keeps going
  after a failed statement and still exits 0, which would report a
  half-applied database as a success.
- **`--defaults-extra-file` must be argv[1].** MySQL's option parser only
  accepts it before every other option; anywhere else it is an "unknown
  variable" error. `buildMySQL` therefore appends it before anything else.
- **`mysql` has no `--file`.** The restore feeds the dump on stdin, which
  is why `Command` carries a `StdinPath` at all. `mysqldump` *does* have
  `--result-file`, and it is preferred over shell redirection because it
  keeps the tool responsible for its own newline handling on Windows.
- **MariaDB tries its native names first.** Recent builds have dropped the
  `mysqldump`/`mysql` compatibility symlinks, and older ones do not have
  `mariadb-dump`, so both spellings are candidates and the first one on
  `PATH` wins.
- **DuckDB's paths are directories.** `EXPORT DATABASE` writes
  `schema.sql`, `load.sql` and one file per table, so the prompt asks for a
  directory and `DefaultExtension` reports `""` for it.

## Credential hygiene

The rule: **no password in argv, and none in the environment either.**

`ps` shows another process's argv to every user on a Unix box, so
`--password=hunter2` leaks the secret for the whole run. The environment is
only slightly better — `/proc/<pid>/environ`, core dumps and crash
reporters all reach it — so `PGPASSWORD` is not used at all.

Instead, `internal/dump/cred.go` writes a temporary file with `0600`
permissions and points the tool at it:

- PostgreSQL gets a one-line `.pgpass` (`host:port:database:user:password`)
  named by `PGPASSFILE`. Each field escapes `\` and `:`, because libpq
  reads a bare colon as the field separator and silently ignores a
  malformed line — the failure mode would be a mysterious auth error, not a
  parse error.
- MySQL/MariaDB get a `[client]` option file with a double-quoted
  `password=` value escaping `\` and `"`. Quoting matters for more than
  spaces: an unquoted `#` starts a comment mid-password.

Both engines refuse a credential file that is group- or world-readable, so
the `0600` is enforced by the tool as well as by us. `Command.Cleanup`
removes the file as soon as the child exits, on every path including
cancellation and a failed build — the secret is on disk for the lifetime of
one process and no longer.

Nothing else ever sees it. `dump.Preview` builds the same argv with
`CredPlaceholder` (`<credential-file>`) in place of a real path and blanks
the password first, so the modal preview, the command log line and the
`ErrMissingTool` message are all safe to render verbatim.

## Tool availability

`dump.Preview` resolves the binary through `exec.LookPath` before the modal
opens, so a machine without `pg_dump` gets a log line naming the binary and
saying it is not on `PATH` — not a form that fails after being filled in,
and not a crash. When an engine has several candidate names (MariaDB), the
error names all of them.

## Process lifecycle

`dump.Run` starts the tool with `Setpgid`, streams stderr line by line into
the command log (pg_dump and mysqldump both report progress there), keeps
the last ten lines for the failure message, and reaps the process with
`Wait`.

Cancelling kills the **process group**, not the process: `pg_dump` forks
helpers, and killing only the parent would leave them writing into the
output file lazysql is about to delete. SIGTERM is sent first so a tool
that cleans up gets the chance, then SIGKILL. Windows has no equivalent —
`CREATE_NEW_PROCESS_GROUP` plus `Process.Kill` is the closest it offers, and
a grandchild there may survive.

A cancelled or failed **dump** has its output file removed: a truncated
dump is worse than no dump, because it looks complete. A cancelled
**restore** is not undone — half a database is not something deleting a
file can fix — so the log says so explicitly.

## SQLite and DuckDB take a different path

Neither needs an external binary, so their jobs run through the connected
`Driver` and land in the command log through `Driver.Logger()` like every
other statement — no second way for a statement to be echoed.

Two consequences:

- **`VACUUM INTO` and `EXPORT DATABASE` had to be reclassified as reads.**
  `db.ClassifyStatementFor` errs towards `StatementWrite` for anything it
  does not recognize, which would have made the read-only guard refuse to
  *back up* a read-only connection — exactly backwards. Both are now read
  keywords, but only in the one spelling that dumps: a bare `VACUUM`
  rewrites the database in place and `IMPORT DATABASE` writes rows, so both
  stay writes. See `secondWordIs` in `internal/db/statements.go`.
- **A SQLite restore closes the session first.** It is a file copy over the
  live database file; a connection left open across it could replay the old
  database's write-ahead log onto the new one. The model tears the session
  down synchronously, copies through a temp file in the destination
  directory (so a failure part-way leaves the old database intact), removes
  the `-wal`/`-shm`/`-journal` sidecars, and tells the user to reconnect.

A **restore** is refused outright on a read-only connection, before any
engine sees it.

## Tunnelled connections

[design/ssh-tunnels](ssh-tunnels.md) makes the point that lazysql's tunnel
is a transport injected under the driver as a `DialFunc`, deliberately
*not* a local forwarded port. An external tool cannot be handed a Go
dialer, so this is the one feature that needs the exception.

`Tunnel.Listen(remote)` (`internal/sshtunnel/forward.go`) opens a listener
on `127.0.0.1:0` — loopback only, since the port speaks for the jump host's
network — and splices each accepted connection onto a channel through the
existing tunnel, half-closing each direction so a client that stops writing
(psql reading a dump from stdin) makes the server see EOF. The job
substitutes `127.0.0.1` and the forward's port into the request *before*
building the command, so the credential file is written for the forwarded
endpoint too: libpq matches the `.pgpass` host field against the host it
actually connected to.

The forward's lifetime is one job's. It is closed when the worker returns,
and the tunnel's own `Close` is unchanged.

## Confirmation and the typed word

Every other mutation in lazysql stages into a changeset and is committed
explicitly. A restore cannot work that way — the tool applies it in one
shot — so the guard is a typed confirmation: the form does not submit until
the database name has been typed into a field of its own. For a file
engine, "the database name" is the database file's base name, which is what
the form's placeholder shows.

## Where the pieces live

- `internal/dump/dump.go` — `Request`, `Command`, `Build`, `Preview`, the
  per-engine builders, tool candidates.
- `internal/dump/cred.go` — `.pgpass` and option-file writing and escaping.
- `internal/dump/run.go` — process execution, stderr streaming, exit codes.
- `internal/dump/proc_unix.go`, `proc_windows.go` — process-group kill.
- `internal/sshtunnel/forward.go` — the local forward.
- `internal/ui/dumprestore.go` — menu, form, worker, log reduction.
