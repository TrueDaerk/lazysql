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
- Added [design/copy-and-export](design/copy-and-export.md) with the copy
  menu and the file export (issue #9): a new `internal/export` package
  holds a three-method incremental `Writer` (`Begin`/`Row`/`End`) per
  format and a `Stream` that walks `Driver.QueryPage` a page at a time,
  so a 100k-row export costs one page of memory and inherits the grid's
  filter and sort for free. `y` opens a context-aware menu (cell, row as
  CSV/JSON/INSERT, table as CSV/JSON/INSERTs/CREATE+INSERTs, DDL); `E`
  prompts for a path and infers the format from `.csv`/`.json`/`.sql`.
- Recorded the decisions that were not obvious while implementing it:
  NULL is an empty CSV field (not MySQL's unportable `\N`), JSON `null`
  and SQL `NULL`; a whole-table *clipboard* copy is capped at 5000 rows
  because the clipboard cannot be streamed, and reaching the cap is
  logged rather than silent; `.sql` exports INSERTs only, with
  `CREATE TABLE + INSERTs` left as a copy-menu entry so one extension
  never means two things; and the copy menu's JSON entry is `o`, not
  `j`, because `menuModal` moves its own cursor with `j`/`k`.
- Added [reference/sql-literal-escaping](reference/sql-literal-escaping.md):
  generated INSERTs inline their values, so MySQL/MariaDB need the
  backslash doubled (`NO_BACKSLASH_ESCAPES` off by default) while
  PostgreSQL/SQLite/DuckDB must not have it touched; booleans are `1`/`0`
  on MySQL and `TRUE`/`FALSE` elsewhere; and a `DATETIME` literal rejects
  the RFC 3339 `T` and zone suffix. Everything lazysql *executes* stays
  parameterized — `db.QuoteLiteral` exists only for text handed to the
  user.
- A copy now degrades instead of failing: with no clipboard (SSH, a bare
  tty, a container) `copyOut` spills the text to a temp file and the log
  names the path. The export worker streams progress through an
  unbuffered channel with `X` cancelling its context between pages and
  between rows, and a cancelled or failed export removes its partial
  file. `X` is bound with `key.WithDisabled()` and enabled only while an
  export runs, so the options bar and `?` never advertise a dead key.
- Deepened the `send` test driver from three hard-coded rounds to a
  queue that runs a key press's message cascade to a standstill: the
  export flow is four rounds deep (prompt → worker start → progress →
  done) and the old driver silently dropped the last one.
- Added [design/query-editor-and-history](design/query-editor-and-history.md)
  with the query editor (issue #10): `:` opens a `textarea` modal, `ctrl+r`
  runs, `esc` keeps the draft. Free-form results cannot be re-issued with a
  different `OFFSET` without rewriting user SQL, so they are materialized once
  through the new `Driver.QueryLimit` (capped at 10 000 rows, truncation
  reported) and paged in memory; `dataView.table` and `dataView.query` are
  mutually exclusive, and every table-scoped guard moved from `open()` to the
  new `browsing()`.
- `db.SplitStatements` is a dialect-aware lexer rather than a split on `;`:
  backticks and `#` comments are MySQL's, `$tag$` bodies PostgreSQL's, and only
  MySQL reads `\'` as an escape. `db.ClassifyStatement` errs towards write, with
  `WITH` demoted when the script modifies data and `PRAGMA x = y` counted as a
  write; `EXPLAIN` stays a read despite PostgreSQL's `EXPLAIN ANALYZE`.
- Editor DML bypasses the changeset — a statement the user wrote already carries
  its own row identity — and is gated by a confirm modal that prints the exact
  write statements. A run stops at the first failure.
- Discovered while testing cancellation: the query worker must *not* guard its
  per-statement send with `select … case <-ctx.Done()` the way the export worker
  guards progress. The message reporting the cancelled statement is produced
  after the context is already dead, so the guard swallowed exactly that
  message. A plain blocking send is safe because the root drains until
  `queryDoneMsg`.
- `ctrl+c` cancels a run instead of quitting: `CancelQuery` is bound with
  `key.WithDisabled()`, enabled only while a run is in flight, and its case sits
  before `Quit` in `updateGlobal`, so `key.Matches` resolves the conflict with
  no extra state check.
- Added `internal/history`: JSON Lines at
  `${XDG_STATE_HOME:-~/.local/state}/lazysql/history`, mode 0600, append-only
  with atomic rewrites for delete/clear, unparsable lines skipped, compacted to
  1000 entries on load. Panel [4] now renders it newest-first with `enter` (load
  into the editor), `x` (run), `d` (delete), `D` (clear) and `/`. `sidePanel`
  gained `sourceIndex` so `d` targets the right entry while a filter is active —
  two runs of the same statement render as identical rows.
- Added [design/command-log-panel](design/command-log-panel.md) (issue #11):
  `internal/db/logger.go` adds a `Logger` ring buffer (`LogCapacity` 500) that
  `conn` records into from its four `database/sql` choke points — `Exec`,
  `ExecTx`, `QueryLimit` and the introspection `querierAdapter.QueryContext` —
  so every statement is captured exactly once regardless of which UI path
  triggered it, including the introspection queries that were never logged
  before. Removed the ~dozen UI call sites that hand-echoed SQL text
  (`data.go`'s page/count log lines, `model.go`'s `BEGIN`/statement/`COMMIT`
  triplet, `query.go`'s per-statement echo) now that the Driver logs them
  itself.
- `Model.commandLogEntries()` merges that `Logger` with the UI's own
  status-note stream (`commandLog []logLine`, timestamped, `err` set by a
  `"FAILED"` substring) by timestamp into one feed. `@` opens a
  `commandLogModal` (`esc`/`j`/`k`/`pgup`/`pgdown`/`g`/`G`) that is a snapshot
  like every other modal — modals render from their own state, not the live
  `Model`, so a statement logged while it is open needs a close/reopen to
  show up.
- Added [design/ssh-tunnels](design/ssh-tunnels.md) with SSH tunnel support
  (issue #12): `internal/sshtunnel` opens the bastion connection and hands out
  forwarded `net.Conn`s; `internal/db` grew a `DialFunc` seam (`OpenWith`, a
  per-`Dialect` `openDB`) that injects it into the concrete driver —
  `mysql.RegisterDialContext` plus a DSN net rewrite for MySQL,
  `pgx.ConnConfig.DialFunc` behind `stdlib.RegisterConnConfig` for PostgreSQL,
  refused outright for SQLite/DuckDB. No local forwarded port is ever bound.
  Both driver registrations are process-global maps, so each connection takes a
  unique `lazysql-tunnel-<n>` name and drops it in `conn.Close`; pgx also gets a
  `LookupFunc` that passes the hostname through unresolved, since resolution
  belongs on the far side of the tunnel.
- Host keys are checked against `known_hosts` and never silently accepted: an
  unknown key is an `*UnknownHostKeyError` the UI turns into a fingerprint
  confirm modal (accept appends to `known_hosts`, then redials), a changed key
  is a `*HostKeyMismatchError` whose modal deliberately has no confirm action.
  `ssh.NewClientConn` wraps the callback's error, so the callback records its
  own and `Open` returns that — otherwise the UI would only see "handshake
  failed" and could not prompt.
- The SSH password and key passphrase share one keyring slot,
  `secrets.SSHKey(name)` (`<name>#ssh`); `secrets.Rename` and the new
  `secrets.Forget` move and remove both slots together. `config.Connection`
  gained an `SSH` table (with the same pointer-`Port` encoding shim the database
  port uses) and `UsesSSH`/`NeedsSSHSecret` predicates.
- `Model.tunnel` lives beside `Model.driver` and the two are torn down together
  by `closeSessionCmd`, driver first. Quit calls `closeSession()`
  *synchronously*: `tea.Quit` can stop the program before a batched `tea.Cmd`
  runs, which would leak the SSH connection.
- Added [reference/ssh-config-resolution](reference/ssh-config-resolution.md):
  only `HostName`/`User`/`Port`/`IdentityFile` are honoured, the profile wins
  except that `HostName` always overrides (an alias is not a resolvable name),
  and `(*ssh_config.Config).Get` must be used rather than the package-level
  `Get`, which would invent a `22`/`~/.ssh/identity` default for every host.
- Added `internal/sshtunnel/sshtest`, a real in-process SSH server used by both
  the tunnel tests and the `internal/db` end-to-end tests, where a minimal
  MySQL and PostgreSQL server behind the tunnel answers the first client packet
  with a protocol error — so the assertion is that the server's own message
  comes back through the tunnel. `TestCloseLeavesNoLeaks` covers the
  no-goroutine/socket-leak acceptance criterion.
- Added [design/configurable-keys-and-theme](design/configurable-keys-and-theme.md)
  with `[keys]`/`[theme]` config support (issue #13): `config.Config` gained
  `Keys`/`Theme` (`map[string]string`); `internal/ui/keys.go` gained
  `keyMap.slots()`/`applyKeyOverrides` and `internal/ui/theme.go` gained
  `palette`/`presets`/`resolveColor`/`resolvePalette`/`applyPalette`, since the
  valid action/color names are `internal/ui`'s to define, not
  `internal/config`'s. `ui.New()` now returns `(Model, error)` — an unknown
  action name or an invalid color is a startup error (`main.go` prints it and
  exits 1) rather than the silent-degrade treatment a broken connections list
  gets.
- Split the single `colorRed` into `colorDeleted` (dropped/staged-for-delete
  rows, the danger-confirm modal border) and `colorError` (error text,
  `statusError`) so `[theme]` can retint them independently; the two grid
  background shades and the list-selection background became
  `colorRowCursorBg`/`colorCellCursorBg`/`colorSelectionBg` for the same
  reason. `default` keeps the original low ANSI indexes; `light` hardcodes
  hex/256 values, since low ANSI indexes are whatever the terminal defines
  and cannot be trusted to stay readable on a white background.
- Fixed two bugs found while wiring this up: `Config.Clone()` only
  deep-copied `Connections`, so a saved clone shared its `Keys`/`Theme` maps
  with the original; and `configFile` (the TOML-encoding shadow struct)
  didn't carry `Keys`/`Theme` at all, so saving a connection through the UI
  would have silently dropped a hand-edited `[keys]`/`[theme]` section from
  disk. `TestSavingConnectionsPreservesKeysAndTheme` covers the round trip.
- Added [design/ci-and-release-pipeline](design/ci-and-release-pipeline.md)
  with `ci.yml`/`release.yml`/`.goreleaser.yaml` (issue #14): CI runs
  `go vet`/`go build`/`go test` natively on `ubuntu-latest` and `macos-latest`.
  Release builds all four `darwin`/`linux` × `amd64`/`arm64` targets each on a
  runner whose native OS/arch matches, including GitHub's hosted
  `ubuntu-24.04-arm` runner for `linux/arm64` — so DuckDB's CGO requirement
  never needs cross-compiling and never needs the build-tag gating the issue
  anticipated. `goreleaser build --single-target` (not `goreleaser release`,
  which cannot split a build across runners on the OSS tier) produces each
  binary with `internal/version.Version` stamped via `-ldflags` from the
  pushed tag; the workflow archives/checksums each leg with plain
  `tar`/`shasum` and `softprops/action-gh-release` assembles the one GitHub
  release with GitHub-generated notes. `internal/version.Version` (default
  `"dev"`) now also renders in the options bar next to the screen mode and
  app name, and `lazysql --version` prints it.
- Added [design/query-editor-panel](design/query-editor-panel.md) with the
  move of the SQL editor from a modal to the permanent panel `[5] Query`
  (issue #28): `panelQuery` joined the panel enum, `Model.draft` and the
  `queryModal` type are gone, and `Model.editor` (a `queryEditor` holding
  the textarea and its mode) is now the single copy of the buffer.
  `Model.Update` gained one routing step — the editor in insert mode
  captures keys ahead of the globals, since a permanently focused
  textarea would otherwise eat `q`, `?` and the panel digits.
  `focusResult` replaced the unconditional `setFocus(panelMain)` of the
  three `showQuery*` reducers: a run started in `[5]` keeps its focus,
  and its main view stacks the buffer over the result, while a run from
  the history panel or a grid re-run still hands the grid the focus.
  Unclaimed normal-mode keys fall through to `updateData` so paging and
  the tabs work without leaving the editor, and `submitQuery` no longer
  writes to the buffer, so `x` on `[4]` cannot overwrite a half-written
  statement. New `[keys]` actions: `edit-query`, `run-editor`,
  `clear-query`, `leave-insert`; `jump` grew to `1`–`5`. Updated
  [design/tui-shell-architecture](design/tui-shell-architecture.md) (the
  routing order and the not-a-`sidePanel` exception) and
  [design/query-editor-and-history](design/query-editor-and-history.md)
  (which still describes what a *run* does, now with the modal wording
  corrected).
- Added [design/sql-syntax-highlighting](design/sql-syntax-highlighting.md)
  with SQL highlighting in the query editor (issue #29). New package
  `internal/sqlhl`: a hand-written scanner (chosen over chroma, which
  would drag a lexer registry and a second style system in for one
  language) whose two rules are that it never fails and never loses a
  byte — tokens tile the input, so `Kinds` can hand the renderer one kind
  per rune. Dialect differences that actually matter turned out to be
  four: `"x"` is a string in MySQL and an identifier elsewhere,
  backslashes escape only in MySQL literals, `#` comments only in MySQL,
  and block comments nest only in PostgreSQL/DuckDB. The bigger surprise
  was on the UI side: the Bubbles textarea styles a line whole, with no
  hook for part of one, so `internal/ui/highlight.go` now draws the
  buffer — gutter, wrapping, colours, cursor — while the textarea stays
  the input model. The rule that keeps them from fighting is that only
  one may wrap: `newQueryEditor` pins the textarea's width at 500 cells
  and clears its prompt and line numbers, so its cursor moves by logical
  lines and the display wrapping is lazysql's. The scroll offset is
  derived from the cursor rather than stored, so no edit made from
  elsewhere can leave it stale. Five new `[theme]` slots (`sql-keyword`,
  `sql-string`, `sql-number`, `sql-comment`, `sql-placeholder`);
  identifiers and operators stay uncoloured on purpose, and delimited
  identifiers borrow `accent`.
- Added [design/schema-aware-autocomplete](design/schema-aware-autocomplete.md)
  with completion in the query editor (issue #30). The popup is a
  `lipgloss` layer, not a `modal`: a modal takes every key, and a
  completion list exists precisely *while* the user is typing, so it
  claims four keys in `updateEditor` and lets everything else through.
  Anchoring it cost the only real refactor — the compositor places by
  absolute cell, so `sideWidth`, `mainColumnRect` and
  `commandLogHeight` came out of `View`/`renderMainColumn` and
  `renderEditor` now returns the caret's row and column inside the block
  it draws, measured with `lipgloss.Width` because a wide rune is two
  cells. `placePopup` (below, slid left, flipped above) is a pure
  function and unit-tested as one: with the editor capped at half the
  main view, the flip case is unreachable by resizing. Suggestions come
  from a token scan rather than a parser — `sqlhl.Tokenize` already
  reads half-written SQL, so matching its identifier tokens against the
  relation list is enough to know which tables a statement touches, and
  a string literal spelling a table name correctly does not count.
  Ranking is prefix-before-substring, then schema-before-keywords: a
  user knows `SELECT`, and does not remember whether the column is
  `customer_name`. Two new exports on `internal/sqlhl` (`Keywords`,
  `IsKeyword`) share one list between the highlighter and the popup, so
  a dialect's keyword gains both at once and the per-driver difference
  is free. The column cache is keyed by connection+database and
  invalidates itself in `syncSchema` rather than trusting every caller
  to clear it; a generation counter drops replies for a namespace the
  user has left; a miss never blocks, and `restackCompletion` fills the
  open popup in when the fetch lands, carrying the selection by name.
  Quoting is `Dialect.QuoteIdent` but only where required, plus one
  dialect rule worth remembering: PostgreSQL folds unquoted identifiers,
  so a mixed-case name has to be quoted there and nowhere else. New
  `[keys]` actions: `complete`, `complete-next`, `complete-prev`,
  `accept-completion`, `close-completion`. One behaviour change to an
  existing flow — `esc` now closes the popup before it leaves insert
  mode — which is the issue's requirement and is asserted in
  [design/query-editor-panel](design/query-editor-panel.md)'s tests.
- Updated [design/query-editor-and-history](design/query-editor-and-history.md)
  §3 (issue #31): the blanket "this executes immediately, there is nothing to
  roll back" confirm modal on every editor DML/DDL statement is gone —
  ordinary writes (`INSERT`, `CREATE TABLE`, a guarded `UPDATE`/`DELETE`) now
  run unasked, still logged, still unstaged. The one case still confirmed is a
  `DELETE`/`UPDATE` with neither `WHERE` nor `LIMIT` at its own level, which
  the new `db.FindUnguardedWrites` (`internal/db/dml_guard.go`) detects by
  tokenizing with `internal/sqlhl` rather than substring search, so a comment
  or string literal mentioning "where" does not suppress the warning and a
  `WHERE` inside a subquery does not guard the statement around it. The modal
  names the affected table (best-effort, from the same token scan) and warns
  once for the whole multi-statement buffer if any statement matches.
- Extended [design/data-grid](design/data-grid.md) with table borders (issue
  #32): the result grid now draws a `│` separator between columns and a
  `─`/`┼` rule under the header, per row rather than via lipgloss's `Table`
  component, since the grid's own paging and per-cell state (cursor, NULL,
  staged edit, pending delete/insert) do not fit that component's model. New
  style `gridSeparator` reuses `colorMuted` rather than a dedicated palette
  slot, so it already tracks `[theme]`'s `border-blurred` on both presets.
  `gridHeader` grew from two lines to three (name, type, rule) and
  `dataContent`'s `bodyRows` budget shrank by one to match; `colGap`'s width
  math was untouched since the separator fills the same one cell the old gap
  did.
- Added [design/vim-mode-query-editor](design/vim-mode-query-editor.md)
  (issue #33): panel [5]'s normal mode is now a vim dialect — h/j/k/l,
  w/b, 0/$, gg/G, i/a/o/O, x/dd/yy/p — implemented as a pure buffer
  engine in `internal/ui/vim.go` over the v2 textarea rather than by
  adopting vimtea (Bubbletea v1, owns its own rendering). Two-key chords
  hold pending state that any other key or a panel switch resets; undo
  is omitted (no textarea history to call into). Updated
  [design/query-editor-panel](design/query-editor-panel.md): the vim
  layer claims h/l, y, d, x, p ahead of the grid fall-through, and `a`
  is append rather than the actions menu in this one panel.

## 2026-08-09 — Floating history pane and placeholder execution (issue #34)

- Added [design/history-pane-and-placeholders](design/history-pane-and-placeholders.md):
  panel `[4] Query history` is removed — the history opens as a floating
  pane (`historyModal`) from the editor's normal mode with `backspace`;
  `enter` executes the selected entry, `e` loads it, `d` deletes it. The
  editor renumbered `[5]` → `[4]`, `jump` shrank to `1`–`4`, and the
  `[keys]` actions `load-query`/`run-query`/`delete-history`/`clear-history`
  were replaced by `history`. New `internal/db/placeholders.go` extracts
  `?`/`:name` placeholders via the `sqlhl` tokenizer (strings, comments,
  `::` casts and `$1`/`@var` excluded) and rewrites them to the dialect's
  marker, binding entered values only as parameters. `submitQuery` owns
  the prompt for single-statement scripts, so history runs, `ctrl+r` and
  grid re-runs all share it. Storage format unchanged.
- Updated [design/query-editor-panel](design/query-editor-panel.md),
  [design/query-editor-and-history](design/query-editor-and-history.md)
  and [design/tui-shell-architecture](design/tui-shell-architecture.md)
  for the renumbering and the pane.

## 2026-08-09 — Row filtering via the filter modal (issue #37)

- Updated [design/data-grid](design/data-grid.md): `/` (and still `f`)
  opens `filterModal` — a `formModal` with column, operator and value
  fields, starting on the cell cursor's column, hiding the value for
  `IS [NOT] NULL` and offering to `AND` onto an active structured
  filter; `ctrl+t` toggles the free-text `WHERE` mode the filter used to
  be, and `F` clears the filter. New `db.BuildFilter`/`db.FilterCond`
  quote the identifier per dialect and bind every value as a parameter;
  what it binds *as* comes from the column's declared type
  (`db.typeClass`, whole-name matching so `point`/`interval` are not
  read as integers), with `LIKE` always text and an unparseable value
  reported on the modal's error line. `dataView` gained `conds` so a
  repeated `/` can add to the filter rather than replace it.

## 2026-08-09 — Cell detail popup (issue #39)

- New [design/cell-detail-popup](design/cell-detail-popup.md):
  `cellModal` now classifies a cell's raw bytes (`classifyCell` in
  `internal/ui/celldetail.go`) rather than trusting the declared column
  type — valid JSON (object/array root) pretty-prints, invalid UTF-8
  hex-dumps (`hexDumpLines`, `hexdump -C` style), everything else stays
  plain text. The title now reads `table.column — type, N bytes`
  (`(json)`/`(binary)` appended when the detected kind adds
  information); `y` inside the modal copies `rawText` — the untouched
  value — through the same `clipboardWrite` seam
  [copy-and-export](copy-and-export.md) uses, without closing the
  modal. Updated [design/data-grid](design/data-grid.md) to point at
  the new concept instead of duplicating it.

## 2026-08-09 — Follow foreign keys from the data grid (issue #40)

- New [design/foreign-key-navigation](design/foreign-key-navigation.md):
  `g` follows the constraint the cursor column takes part in, `G` lists
  the tables referencing the row under the cursor, and `ctrl+o` (or
  `esc`, before it leaves the grid) walks the jumps back. New
  `db.FKFilter`/`db.SplitQualified`/`db.FKAt`
  (`internal/db/foreignkey.go`) build the jump's WHERE clause with
  dialect-quoted identifiers and every value bound as a parameter — one
  term per column pair for a composite key — and refuse a NULL value,
  which is why a NULL cell logs `customer_id is NULL` instead of showing
  an empty grid. `internal/ui/foreignkey.go` adds the per-relation FK
  cache (filled by `openTable`, by the Structure/Indexes/DDL metadata
  fetch, and by the namespace scan behind `G`), the `⇒` header mark, and
  a `browseState` stack that stores the whole `dataView` so the previous
  table, filter, sort, page and cell cursor all come back together.
  Updated [design/data-grid](design/data-grid.md) to point at the new
  concept.

## 2026-08-09 — Vertical row detail view (issue #41)

- New [design/row-detail-view](design/row-detail-view.md): `x` opens
  `rowDetailModal` (`internal/ui/rowdetail.go`), a scrollable
  name/type/value list for the cursor row — psql's `\x` for tables too
  wide for the grid. It builds its fields from the same staged-change
  detection `buildGrid` uses (a phantom INSERT's unbound columns render
  `DEFAULT`, a staged edit keeps its yellow tint, a row staged for
  deletion strikes the whole list through), and `v` on a field opens the
  existing cell detail popup rather than a second renderer. Updated
  [design/data-grid](design/data-grid.md) to point at the new concept.
