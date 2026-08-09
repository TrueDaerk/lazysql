# CLAUDE.md

Guidance for Claude Code when working in this repository.

## What this is

lazysql — a lazygit-style TUI for managing SQL databases (MySQL, MariaDB, PostgreSQL, SQLite, DuckDB). Written in Go with Bubbletea v2.

## Tech stack

- Go (see `go.mod` for version)
- `charm.land/bubbletea/v2` — TUI framework (Elm architecture). **v2, not v1** — module paths and APIs differ; never write Bubbletea imports or signatures from v1 memory.
- `charm.land/bubbles/v2` — components (list, table, textinput, key, help)
- `charm.land/lipgloss/v2` — styling, layout, layer compositor for modals
- Database drivers: `go-sql-driver/mysql`, `jackc/pgx`, `mattn/go-sqlite3` (or `modernc.org/sqlite`), `marcboeker/go-duckdb`

When building TUI code, use the `tui:lazygit-style` skill — it contains the Bubbletea v2 API reference and a working skeleton.

## Architecture rules

- **One root model** owns terminal size, focused panel, one child model per side panel, main view state, and the open modal (nil = none).
- **Update routing order**: WindowSizeMsg → open modal (swallows all keys) → global keys (`q`, `?`, digits, `tab`) → focused panel.
- **Actions are messages**: panels emit domain messages; the root reduces them. All DB work runs in `tea.Cmd`s — never block `Update`.
- **Driver abstraction**: all database access goes through a driver interface in an `internal/db` package. UI code never imports a concrete SQL driver. Dialect differences (introspection queries, quoting, LIMIT syntax) live behind that interface.
- **Staged mutations**: cell edits / row deletes / inserts accumulate in a pending changeset; SQL is only executed on explicit commit. Never auto-execute destructive SQL.
- **Command log**: every executed SQL statement must be appended to the command log panel.

## Layout & UX conventions (lazygit design language)

- Left column: numbered side panels `[1] Connections`, `[2] Databases`, `[3] Tables`, `[4] Query history`. Right: large main view. Bottom: options bar for the current context.
- Exactly one focused panel (green border, bold title); main view reflects its selection.
- `1`–`4` jump, `tab` cycles, `j`/`k` move, `enter` drills in, `esc` backs out, `?` help, `q` quit.
- Every interactive flow is a centered modal (confirm, menu, prompt). `esc` always cancels.
- Every key in the options bar must be bound; every binding must appear in `?`. Keep one source of truth (`key.Binding` slices per panel).

## Verification

- `go vet ./...` and `go build ./...` must pass before finishing any task.
- `go test ./...` for packages with tests; driver code needs unit tests against SQLite/DuckDB (in-process, no server required).
- Run the TUI in a real PTY when possible; check resize behavior and the tiny-terminal guard.

## Security

- Never write passwords to config files or logs — use the OS keyring (`zalando/go-keyring`).
- All user-supplied values in generated SQL must be parameterized or properly escaped per dialect. Identifiers get dialect-correct quoting.
