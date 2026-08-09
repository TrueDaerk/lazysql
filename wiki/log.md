# Update log

Chronological history of wiki changes, newest last.

## 2026-08-09

- Bundle created alongside the initial TUI shell (issue #1).
- Added [design/tui-shell-architecture](design/tui-shell-architecture.md): root
  model ownership, why side panels are cursor-over-slice rather than Bubbles
  `list`, the fixed update routing order, and the `m.modal == cur` rule that
  lets one modal replace another.
- Added [design/keybindings-single-source](design/keybindings-single-source.md):
  the `actionID` + `key.Binding` table behind key dispatch, the options bar, the
  `a` actions menu and the `?` help, plus the tests that enforce it.
- Added [reference/lipgloss-v2-sizing](reference/lipgloss-v2-sizing.md):
  discovered while debugging a side column that ended six rows short of the main
  column — lipgloss v2 `Style.Width`/`Height` are total block dimensions,
  contrary to v1 guidance.
- Added [design/db-driver-abstraction](design/db-driver-abstraction.md) with the
  `internal/db` package (issue #2): one generic `conn` over `database/sql`
  delegating to per-engine `Dialect` values; MariaDB shares the MySQL dialect;
  result cells normalized to nil/string/int64/float64/bool/time.Time.
- Added [reference/dialect-introspection-quirks](reference/dialect-introspection-quirks.md):
  PRAGMA takes no placeholders, `duckdb_constraints()` lists need server-side
  `unnest()`, Postgres DDL must be synthesized, `SHOW CREATE TABLE` scans
  positionally.
- Added [design/connection-secrets](design/connection-secrets.md) with the
  connection manager (issue #3): `internal/config` writes profiles to
  `config.toml` atomically at mode 600, `internal/secrets` keeps every password
  in the OS keyring keyed by connection name, and `ask_password` skips the
  keyring in favour of a prompt on each connect.
- Added [design/connection-form-modal](design/connection-form-modal.md): one
  reusable `formModal` instead of chained prompts, with per-field `visible`
  predicates so the engine select swaps host/port for a file path without
  losing already-typed input.
- Added [reference/dsn-formats](reference/dsn-formats.md): assembled while
  writing `db.BuildDSN` — MySQL must go through `mysql.Config.FormatDSN`,
  `parseTime=true` is required for the `ResultSet` value contract, SQLite needs
  the `file:` URI form once options appear, and the redaction mask has to be
  URL-safe.
