---
type: Reference
title: Dependencies that write to the terminal, and how lazysql captures them
description: Which drivers and libraries print to stderr on their own while the TUI runs (go-sql-driver/mysql's error logger, anything using the standard log package), which were checked and found silent (pgx, modernc SQLite, DuckDB, x/crypto/ssh, internal/sshtunnel), and how db.CaptureProcessLogs plus a per-connection MySQL logger route those lines into the command log through program.Send.
tags: [tui, db, logging, command-log, mysql, terminal]
generated:
  by: claude-code/opus-5.5
  at: 2026-11-25T00:00:00Z
sources:
  - resource: github.com/go-sql-driver/mysql@v1.10.0 errors.go, dsn.go, connection.go, packets.go
    note: defaultLogger is log.New(os.Stderr, "[mysql] ", …); Config.Logger overrides it per connector
  - resource: golang.org/x/crypto@v0.54.0 ssh/handshake.go, mux.go, transport.go
    note: every log.Printf is behind a false debug constant
---

# Dependencies that write to the terminal

Bubble Tea owns the terminal and repaints only the cells that changed.
Any byte another piece of code writes to stdout/stderr lands wherever the
cursor happens to be and stays there until those cells change again —
shifted borders and stray text that only a resize cleared. lazysql's own
code never prints while the program runs (`main.go` only prints before
`Run` and after it returns), so every such write comes from a dependency.

## Findings

| Source | Writes to the terminal? | Handling |
|---|---|---|
| `go-sql-driver/mysql` | **Yes.** Its package-level `defaultLogger` writes `[mysql] <date> <file:line> <error>` to `os.Stderr` whenever a pooled connection turns out dead (`connection.go` "closing bad idle connection", `packets.go` read/write errors such as `broken pipe`, `EOF`). | Per-connection `Config.Logger` set in `mysqlDialect.openDB`, plus `mysql.SetLogger` while the TUI runs. |
| Standard `log` package | Default output is `os.Stderr`; any dependency calling `log.Printf` would print. | `log.SetOutput` redirected while the TUI runs. |
| `golang.org/x/crypto/ssh` | Only in debug builds: every `log.Printf` is guarded by `debugHandshake`/`debugMux`/`debugTransport`, all `const false`. | Not applicable; would be caught by the `log` redirect anyway. |
| `internal/sshtunnel` | No direct writes; errors are returned. A tunnel closing under a live MySQL connection surfaces as the MySQL driver's own error line. | Not applicable. |
| `jackc/pgx` (stdlib) | No: logging only through an opt-in tracer, which lazysql does not set. | Not applicable. |
| `modernc.org/sqlite` | Only in files behind the `sqlite.dmesg` build tag or `//go:build ignore`. | Not applicable. |
| `marcboeker/go-duckdb` (cgo) | No Go-side writes. The C++ library could in principle write to fd 2 directly, but no such output was observed. | Not handled; fd-level redirection would only be added if it shows up. |

Reproduced with MariaDB 11 in Docker: connect, open a table, `KILL` the
server-side connections, press `R`. Before the change the PTY stream
contained `[mysql] … connection.go:801 closing bad idle connection: EOF`
printed over the grid; after it the same line appears in the command log
and the final screen is intact.

## How the capture works

- **Per connection.** `mysqlDialect.openDB` always builds the handle from
  `mysql.ParseDSN` + `mysql.NewConnector` + `sql.OpenDB` (no longer
  `sql.Open`), because `Config.Logger` cannot be expressed in a DSN
  string. The logger carries the connection's `*db.Logger`, so the UI can
  attribute the line to the session that owns it.
- **Process-wide.** `db.CaptureProcessLogs()` (called in `main.go` around
  `Program.Run`, and only there — `--version`, usage, startup errors and
  `--debug-keys` are untouched) points `log`'s output at a line splitter
  with flags and prefix cleared, and installs the same logger as the MySQL
  package default. The returned func restores both.
- **Queue, not model.** Every captured line becomes a `db.DriverMessage`
  on a buffered channel (256). The send never blocks the driver
  goroutine; on overflow lines are dropped and the next delivered line
  says how many. `ui.ForwardDriverMessages(p.Send)` runs in its own
  goroutine and hands each one to the program as a message; the reducer
  appends it to the command log. Nothing touches the model off the
  Update goroutine.
- **Redaction.** `db.RedactSecrets` masks `user:password@` in URLs and
  MySQL DSNs and `password=` key/value pairs before a line is queued, in
  case a dependency ever quotes a DSN in an error.

See [design/command-log-panel](../design/command-log-panel.md) for how
these lines render.
