# lazysql wiki

An [OKF 0.2](https://github.com/GoogleCloudPlatform/knowledge-catalog/blob/main/okf/SPEC.md)
knowledge bundle: every file below `wiki/` except this index and `log.md` is a
concept with YAML frontmatter. Concept IDs are bundle-relative paths without
`.md`.

## design

- [design/tui-shell-architecture](design/tui-shell-architecture.md) — Design Decision — root model, panel structs, update routing order, modal closing rule.
- [design/keybindings-single-source](design/keybindings-single-source.md) — Design Decision — one `key.Binding` table behind dispatch, options bar, actions menu and `?`.
- [design/db-driver-abstraction](design/db-driver-abstraction.md) — Design Decision — one generic Driver over `database/sql` plus a Dialect per engine; UI never imports concrete SQL drivers.
- [design/connection-secrets](design/connection-secrets.md) — Design Decision — connections in TOML, passwords only in the OS keyring or a per-connect prompt.
- [design/connection-form-modal](design/connection-form-modal.md) — Design Decision — one reusable multi-field modal with engine-driven field visibility.
- [design/catalog-browsing](design/catalog-browsing.md) — Design Decision — async catalog loads, inline fuzzy filter, tables/views sub-tabs, pseudo-database for single-namespace engines.
- [design/data-grid](design/data-grid.md) — Design Decision — one-page-at-a-time main view, derived scroll windows, the `panelMain` focus target, and the parse-first quick filter.
- [design/main-view-tabs](design/main-view-tabs.md) — Design Decision — Data/Structure/Indexes/DDL tabs behind one metadata fetch, `[`/`]` cycling instead of digits, and what survives a relation change.
- [design/staged-changeset](design/staged-changeset.md) — Design Decision — cell edits, row deletes and row inserts stage into one engine-agnostic changeset, rows are identified only by their primary key, and the commit runs every statement in one transaction.
- [design/copy-and-export](design/copy-and-export.md) — Design Decision — one incremental serializer package behind both the `y` copy menu and the `E` file export, why a clipboard copy is capped while a file export streams, how NULL is spelled per format, and how the export reports progress and cancels.
- [design/query-editor-and-history](design/query-editor-and-history.md) — Design Decision — `:` editor, why free-form results are materialized and paged in memory, dialect-aware statement splitting and read/write classification, confirmed-not-staged DML, run cancellation, and the JSON Lines history under `XDG_STATE_HOME`.
- [design/command-log-panel](design/command-log-panel.md) — Design Decision — a single `Driver.Logger()` choke point in `internal/db` replaces per-callsite SQL echoing, merged with the UI's own status notes into one feed for the slim panel and the `@` expanded, scrollable view.
- [design/ssh-tunnels](design/ssh-tunnels.md) — Design Decision — a tunnel is a transport injected under the driver as a `DialFunc`, not a local forwarded port; host keys prompt or refuse but are never silently accepted; the tunnel's lifetime is the connection's, including a synchronous teardown on quit.

## reference

- [reference/lipgloss-v2-sizing](reference/lipgloss-v2-sizing.md) — Reference — `Style.Width`/`Height` in lipgloss v2 are total block size, not content size.
- [reference/dsn-formats](reference/dsn-formats.md) — Dialect Note — per-engine DSN shapes and their escaping/URI quirks.
- [reference/dialect-introspection-quirks](reference/dialect-introspection-quirks.md) — Dialect Note — engine-specific introspection gotchas (PRAGMA placeholders, duckdb_* functions, pg_index, SHOW CREATE TABLE).
- [reference/sqlite-double-quoted-strings](reference/sqlite-double-quoted-strings.md) — Dialect Note — SQLite reads an unresolvable double-quoted identifier as a string literal, so a filter on a misspelled column matches nothing instead of erroring.
- [reference/insert-default-values](reference/insert-default-values.md) — Dialect Note — `DEFAULT VALUES` vs. MySQL's empty column list for an INSERT that names no columns.
- [reference/ssh-config-resolution](reference/ssh-config-resolution.md) — Reference — the four `~/.ssh/config` keywords lazysql honours, the profile-wins precedence rule (and why `HostName` is the exception), and the parser gotchas behind them.
- [reference/sql-literal-escaping](reference/sql-literal-escaping.md) — Dialect Note — backslash escapes in MySQL string literals but not in PostgreSQL/SQLite/DuckDB, plus per-engine boolean and timestamp literal spellings.
