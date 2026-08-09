# lazysql wiki

An [OKF 0.2](https://github.com/GoogleCloudPlatform/knowledge-catalog/blob/main/okf/SPEC.md)
knowledge bundle: every file below `wiki/` except this index and `log.md` is a
concept with YAML frontmatter. Concept IDs are bundle-relative paths without
`.md`.

## design

- [design/tui-shell-architecture](design/tui-shell-architecture.md) — Design Decision — root model, panel structs, update routing order, modal closing rule.
- [design/keybindings-single-source](design/keybindings-single-source.md) — Design Decision — one `key.Binding` table behind dispatch, options bar, actions menu and `?`.
- [design/db-driver-abstraction](design/db-driver-abstraction.md) — Design Decision — one generic Driver over `database/sql` plus a Dialect per engine; UI never imports concrete SQL drivers.

## reference

- [reference/lipgloss-v2-sizing](reference/lipgloss-v2-sizing.md) — Reference — `Style.Width`/`Height` in lipgloss v2 are total block size, not content size.
- [reference/dialect-introspection-quirks](reference/dialect-introspection-quirks.md) — Dialect Note — engine-specific introspection gotchas (PRAGMA placeholders, duckdb_* functions, pg_index, SHOW CREATE TABLE).
