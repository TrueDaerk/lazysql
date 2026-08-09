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
- Added [design/catalog-browsing](design/catalog-browsing.md) with the database
  and table panels (issue #4): catalog reads run as `tea.Cmd`s whose replies are
  dropped when stale, `[3]` gained `Tables`/`Views` sub-tabs fed by one
  `ListRelations` round trip, `/` filters inline by case-insensitive
  subsequence, and SQLite/DuckDB collapse to a `(default)` pseudo-database that
  maps back to the driver's empty-string namespace.
- Updated [design/db-driver-abstraction](design/db-driver-abstraction.md) and
  [reference/dialect-introspection-quirks](reference/dialect-introspection-quirks.md)
  for the `listTables` → `listRelations` dialect change and the per-engine
  spellings of the table/view kind column.
- Added [design/data-grid](design/data-grid.md) with the paginated main view
  (issue #5): one 100-row page in memory at a time, the page query and its
  `COUNT(*)` as two independent commands so a slow count never blocks the grid,
  row and column scroll windows derived from the cell cursor instead of stored,
  and `panelMain` as a focus target that has no number and no `Model.panels`
  slot but shares the one keybinding table.
- Documented the quick filter's parse-first rule in the same concept:
  `db.ParseFilter` rewrites recognised `column <op> <literal>` chains into
  quoted identifiers plus bound placeholders, and only an unrecognised fragment
  stays verbatim — flagged in `Filter.Verbatim`, warned about in the command
  log and marked `where (verbatim)` in the status line. `Driver.QueryPage` now
  takes a `*db.Filter` so no caller can assemble its own WHERE text.
- Added [reference/sqlite-double-quoted-strings](reference/sqlite-double-quoted-strings.md):
  found while writing the filter tests — SQLite reads a double-quoted token that
  matches no column as a string literal, so `"typo" = 1` quietly matches nothing
  instead of erroring. Safety is unaffected (the value is still bound), but a
  missing column is not a way to provoke a query failure there.
- Added [design/main-view-tabs](design/main-view-tabs.md) with the
  `Data | Structure | Indexes | DDL` main view (issue #6): the three
  introspection tabs share one lazy metadata fetch and one staleness
  check, `metaView.ddlErr` is kept apart from `metaView.err` so a
  missing `CREATE` statement costs only the DDL tab, and the tabs cycle
  with `[`/`]` because `1`–`4` are global panel jumps that the main
  view must not redefine. Selecting another relation keeps the tab and
  drops everything else.
- Extended the driver for those tabs: `Column.Extra`, a `ForeignKey`
  type and `Driver.TableForeignKeys` across all five engines, and
  `synthesizeDDL` now emits foreign key constraints and secondary
  `CREATE INDEX` statements — so PostgreSQL's synthesized DDL says as
  much as MySQL's `SHOW CREATE TABLE`.
- Updated
  [reference/dialect-introspection-quirks](reference/dialect-introspection-quirks.md)
  with what that cost per engine: SQLite hides `AUTOINCREMENT` in the
  stored DDL text and names no foreign key constraint, DuckDB spells
  the referenced table only inside `constraint_text`, PostgreSQL needs
  `conkey`/`confkey` unnested *together* and its action codes cast
  `::text` for pgx, and MySQL splits foreign keys across
  `key_column_usage` and `referential_constraints`.
- Added [design/staged-changeset](design/staged-changeset.md) with the
  staged editing feature (issue #7): `e` records a `db.CellChange`
  instead of executing, `db.Changeset` keys pending changes by
  `(database, table, column, typed pk values)` so re-edits replace and
  restoring the original unstages, `UpdateSQL` renders per-dialect
  parameterized UPDATEs, and `Driver.ExecTx` commits everything in one
  transaction — failure rolls back and keeps the changeset. Tables
  without a declared primary key are not editable at all.
- Extended [design/staged-changeset](design/staged-changeset.md) with
  whole-row operations (issue #8): `d` stages a `db.RowDelete`, `n` opens
  an insert form that stages a `db.RowInsert`, and `D` opens the same
  form prefilled from the row under the cursor with its key cleared.
  All three share the existing `db.Changeset`, which now holds one
  ordered `[]db.Change` behind a `key()`/`target()`/`Statement(dialect)`
  interface — so commit order is staging order across kinds and `c`
  still runs everything in a single `ExecTx` transaction.
- Recorded the two decisions that were not obvious while implementing
  it: staging a delete drops that row's pending cell edits (and `e` on a
  row staged for deletion is refused), and a staged insert has no natural
  identity, so the changeset assigns it a monotonic `ID` that `u`
  unstages by. The grid grew a `rowKind` per rendered row — struck-through
  red deletes, green phantom insert rows appended after the page with
  `DEFAULT` in every omitted column — and `dataView.extraRows`, which
  `Model.clampCursor` re-derives from the changeset so the cursor can
  reach a row that does not exist in the database yet.
- Replaced `metaView.copyAfterLoad`/`editAfterLoad` with a single
  `afterLoad actionID` (with an `actNone` zero value): four keys now need
  the metadata before they can open anything, and replaying the action
  through `runAction` keeps a cold key press identical to a warm one.
- Added [reference/insert-default-values](reference/insert-default-values.md):
  an INSERT that names no columns is `DEFAULT VALUES` in PostgreSQL,
  SQLite and DuckDB and `() VALUES ()` in MySQL/MariaDB, and each form is
  a syntax error in the other family.
