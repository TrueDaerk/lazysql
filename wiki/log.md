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

## 2026-08-09 — Relations tab (issue #42)

- New [design/relations-tab](design/relations-tab.md): the fifth main-view
  tab (`internal/ui/relations.go`) renders one relation's foreign keys as
  a hub — outgoing above a box with the table's name, incoming below,
  both in `column → table.column` notation. The outgoing half comes free
  with the metadata fetch; the incoming half reuses the namespace scan
  and the `refsCache` behind `G`, so the two features pay for each other,
  and `ensureMeta` now starts both instead of every call site batching
  them. `j`/`k` move one cursor over both halves and `enter` opens the
  selected table with the tab still selected — a schema walk, unfiltered,
  pushed onto the same `browseStack` `esc`/`ctrl+o` unwind. Below 56
  columns the box art is dropped for a plain list. A full-schema ERD is
  explicitly out of scope. Updated
  [design/main-view-tabs](design/main-view-tabs.md) and
  [design/foreign-key-navigation](design/foreign-key-navigation.md) to
  point at it.


## 2026-08-09 — Schema diff between two connections (issue #43)

- New [design/schema-diff](design/schema-diff.md): `D` on a connection
  diffs its schema against another connection's. The comparison model
  (`internal/db/schemadiff.go`) is a pure `db.Schema` snapshot built
  from the existing `ListRelations`/`TableColumns`/`TableIndexes`/
  `TableForeignKeys` calls — no new introspection surface. Type
  synonyms normalize only within one engine family (MariaDB folds onto
  MySQL); a cross-engine diff compares types verbatim and says so in
  its header. The UI flow (`internal/ui/schemadiff.go`) dials both
  sides fresh through the same `dial()` a connect uses and closes them
  after, streams progress export-style over a channel, and renders a
  scrollable red/green/yellow report in the main view with `y` copy
  and `E` export. Report only — no migration SQL.


## 2026-08-09 — DDL export to file, single table and whole database (issue #44)

- New [design/ddl-export](design/ddl-export.md): `E` on the DDL tab
  writes the cached `TableDDL` output verbatim to a `.sql` file,
  bypassing the streaming export worker entirely — a `CREATE TABLE`
  statement is small enough to write synchronously in the `tea.Cmd`
  closure. `E` on the Tables panel (`[3]`) exports every relation of the
  browsed database into one `.sql` file, `-- table: name` per relation,
  ordered by the new `db.DDLOrder` (`internal/db/ddlorder.go`): Kahn's
  algorithm with an alphabetical tie-break at every step, self-references
  and out-of-set foreign keys dropped before ordering, and a genuine
  cycle falling back to a fully alphabetical order with a note in the
  file header rather than failing. A relation whose DDL cannot be read
  does not abort the run — it is noted inline and tallied in the final
  command-log line. Guarded by `Model.dbDDLExport` (`dbDDLExportState`),
  mirroring `exportState`: `running` for the one-at-a-time guard, `id`
  so a stale reply cannot clobber a run started since, `cancel` for
  `resetBrowse` to call when the connection the scan reads through is
  closing — but no `X` binding, since the round trip count is one pair
  of calls per relation, not an unbounded row count. The cold-cache path
  of the DDL-tab export now defers through a dedicated `actExportDDL`
  rather than reusing `actExportTable`, so leaving the DDL tab while its
  metadata fetch is still in flight cannot make the deferred replay open
  the plain CSV/JSON/SQL export prompt instead.

## 2026-08-09 — Named query snippets (issue #45)

- New [design/named-query-snippets](design/named-query-snippets.md) with
  `internal/snippets`: named statements (`{Name, SQL, Engine,
  CreatedAt}`) as JSON Lines at
  `${XDG_STATE_HOME:-~/.local/state}/lazysql/snippets`, mode 600, next to
  the history file. The two stores share their format but not their write
  strategy — the history appends, the snippet file is always rewritten
  whole (atomic temp + rename), because a save may replace a name and the
  list is held sorted by name, neither of which an append can express. A
  duplicate name in the file is tolerated on load, last line winning.
- The name is the identity: `SameName` folds case and trims, an existing
  name opens an overwrite confirm before anything is written, and the
  overwrite keeps the original `CreatedAt`. `Put`/`Delete` return new
  slices rather than mutating, since the model holds the list while the
  save command runs.
- The floating pane (`historyModal`) gained a `section` field with a
  per-section cursor and offset instead of a second modal: `tab` toggles
  History ↔ Snippets, both halves reusing one list/detail/footer frame
  and the same three verbs. `enter` runs through `submitQuery`, so a
  snippet with `?`/`:name` placeholders goes through the existing
  `paramsModal` and runs prepared. `d` on a snippet confirms (it is the
  only copy of that statement) while `d` on a history entry still does
  not.
- `ctrl+s` saves the buffer — bound in both editor modes, since it is not
  a character insert mode needs: `actSaveSnippet` (overridable as
  `save-snippet`) for normal mode and an explicit `updateEditor` case plus
  a `keyMap.editorInsert()` entry for insert mode, so the options bar and
  `?` document it in both. Saving does not end insert mode. `s` on a
  history entry saves it under a prompted name.
- Added [design/explain-view](design/explain-view.md) and
  [reference/explain-per-dialect](reference/explain-per-dialect.md) with the
  `ctrl+e` query plan view (issue #46): a new `Driver.Explain` delegating to an
  unexported `Dialect.explain`, and one `db.Plan` carrying whichever of three
  renderings its engine produces — node tree (PostgreSQL/MySQL JSON, SQLite
  id/parent rows), grid (MySQL's tabular fallback) or preformatted text
  (DuckDB's ASCII diagram) — so no UI code branches on the engine.
- `ANALYZE` is never sent: it executes the statement, so `ctrl+e` on a `DELETE`
  is as safe as on a `SELECT` and needs no confirm modal. MySQL's `FORMAT=JSON`
  (not PostgreSQL's `(FORMAT JSON)`) falls back to the tabular form on servers
  without it, and its JSON is walked with an order-preserving `json.Decoder`
  token pass — `map[string]any` would sort `access_type` above `table_name`.
- The plan takes over the main view while panel `[5]` keeps the focus (the
  shape the schema diff already uses over panel `[1]`), so `esc` is a free trip
  back to an untouched buffer. `db.SplitStatementSpans`/`db.StatementAt` pick
  the statement under the caret in a multi-statement buffer; `rowsScanner`
  gained `Columns()` for the result shapes EXPLAIN does not know ahead of time.

## 2026-08-09 — Cancel running queries from the UI (issue #47)

- Added [reference/query-cancellation-per-dialect](reference/query-cancellation-per-dialect.md):
  most of the cancel plumbing (`ctrl+c` → `queryRun.cancel`, the
  `context.Canceled` outcome distinguished from a real error in the command
  log, the `TestWorkerAbortsAStatementInFlight`/`TestQueryRunIsCancellable`
  tests against a recursive CTE) already existed from
  [design/query-editor-and-history](design/query-editor-and-history.md). What
  this issue added was reading each driver's source to confirm *how* it
  reacts: SQLite (`modernc.org/sqlite`) and DuckDB (`marcboeker/go-duckdb/v2`)
  both interrupt the engine in-process (`sqlite3_interrupt` /
  `mapping.Interrupt` over DuckDB's pending-result API) with no network hop;
  PostgreSQL (`pgx`) sends a real `CancelRequest` on a second connection, the
  same thing `psql`'s own `ctrl+c` does; MySQL
  (`go-sql-driver/mysql`) does *not* send a server-side `KILL QUERY` — it
  drops the client connection, so the UI is just as responsive but the
  statement can keep running server-side briefly until MySQL's own dead-socket
  detection catches it.
- Found and fixed a bug while verifying the acceptance criteria:
  `finishQuery`'s cancelled-outcome log line
  (`internal/ui/query.go`) formatted `msg.ran` through `countStatements`
  ("query cancelled after 1 statement") instead of the elapsed wall time the
  issue asked for ("query cancelled after N s"). `queryRun` gained a
  `startedAt` field so the line — and the new running indicator — can compute
  real elapsed time.
- Added the running indicator the issue also asked for: a `spinner.Model`
  (`charm.land/bubbles/v2/spinner`) on `Model.spin`, ticked by a
  `spinner.TickMsg` case in `Update` that drops the tick (and so stops the
  chain) once `m.run.running` goes false — no separate stop message needed.
  `Model.runningIndicator()` renders the spinner frame plus
  `formatElapsed(time.Since(m.run.startedAt))` and is empty when nothing
  runs; it leads the options bar's right-hand segment
  (`renderOptionsBar`) and replaced panel `[5]`'s static "running…" text.
- `loadPageCmd`/`countRowsCmd` (`internal/ui/data.go`) were checked against
  the same ask and use their own `context.WithTimeout` — they are not part of
  `queryRun` and have no cancel key of their own. Left as a follow-up rather
  than folded in here: wiring `ctrl+c` to them would mean giving page loads
  and row counts their own `queryRun`-shaped state (a `running` flag, a
  `cancel` field, options-bar wiring) for a round trip that is already capped
  at 30s, which is a bigger change than this issue's keybinding-and-feedback
  scope.

## 2026-08-09 (issue #48)

- Updated [design/copy-and-export](design/copy-and-export.md): `E` now
  exports a query-editor result, not just a browsed table, and Markdown
  joins CSV/JSON/SQL as a format (available for both scopes). Added
  `Driver.QueryStream` (`internal/db/conn.go`, backed by a new
  `streamRows` in `scan.go` shared with `scanResultSetLimit`) so a query
  result can be re-run and streamed row-by-row without materializing a
  `ResultSet` — reusing the grid's already-loaded, `maxQueryRows`-capped
  copy was not enough to cover the *whole* result. `export.StreamQuery`
  and `export.QueryRunner` are `Stream`/`Pager`'s one-shot counterparts on
  the `internal/export` side; `internal/ui/export.go` picks between them
  via a small `exportSource` closure (`tableSource`/`querySource`) so
  `exportJob`/`exportState`/`X` stay shared between both scopes.
- Added `db.SingleTableSelect` (`internal/db/singletable.go`): a
  conservative hand-rolled tokenizer that decides whether a query's
  columns map onto exactly one real table, gating whether `.sql` export
  is offered for a query result. Rejects joins, comma-joined `FROM`
  lists, subqueries in `FROM`, `UNION`/`INTERSECT`/`EXCEPT`, `GROUP BY`,
  `DISTINCT`, and any computed or aliased select item — anything it
  cannot prove safe is simply not offered as SQL.
- `y` on a query result now offers a page-scoped `page — CSV`/`page —
  JSON` copy (`copyQueryPage`, `internal/ui/copy.go`), serializing the
  loaded page already in memory with no round trip; the log line notes
  the limit and points at `E` for the full result. Discovered while
  writing its test that `y`/`yy` belongs to the query editor's vim engine
  while focus stays on panel `[5]` — pre-existing behaviour, not
  introduced here, but easy to trip over when testing a query-result copy
  without first moving focus to the grid with `tab`.
- A placeholder-bound query result now carries both its display text
  (`?`/`:name`, for the prompt/log) and its driver-executable form plus
  bound values (`queryStmtMsg.exec`/`.args` → `dataView.queryExec`/
  `.queryArgs`), so the export re-runs the *exact* statement that
  produced the result on screen instead of erroring on an unbound
  placeholder marker sent to the server.


## 2026-08-10 — Per-connection read-only mode (issue #49)

- Added [design/read-only-connections](design/read-only-connections.md)
  and [reference/read-only-per-engine](reference/read-only-per-engine.md).
- `Connection.ReadOnly` (`read_only = true`, absent means read-write) is
  carried into `db.ConnParams` and into `db.OpenOpts`, the new
  `Open`/`OpenWith` base that takes an `Options{Dial, ReadOnly}` instead
  of a bare `DialFunc`.
- Enforcement is one guard in the driver session, not a check per call
  site: `conn.Exec` and `conn.ExecTx` are refused outright, and
  `Query`/`QueryLimit`/`QueryStream` are refused when `db.IsWrite` says
  the statement writes — a data-modifying CTE returns rows, so it arrives
  through the query door, not `Exec`. Every rejection lands in the
  command log through the existing `Logger`, prefixed
  `-- REJECTED (read-only)` with `db.ErrReadOnly` as its outcome.
- `ClassifyStatement` was re-implemented on the `internal/sqlhl`
  tokenizer (`ClassifyStatementFor`, `significantTokens`), replacing the
  upper-cased substring scan `containsKeyword` did: a write verb inside a
  comment, a string literal or a quoted identifier is no longer mistaken
  for a data-modifying CTE, and `PRAGMA table_info('a=b')` is no longer
  mistaken for a `PRAGMA … = …` write. `containsKeyword`/its whole-word
  helper are gone.
- `db.IsWrite` is deliberately *not* the same question as the editor's
  read/write routing: `EXPLAIN ANALYZE` stays a read for the editor
  (there is a plan to render) but counts as a write for the guard,
  because on PostgreSQL and MySQL it executes the statement it explains.
- Discovered while wiring the engine-level flags: `SET SESSION
  transaction_read_only = 1` after `Connect` is worthless under
  `database/sql` pooling — it applies to one pooled connection and not to
  the next one dialled. The go-sql-driver DSN-parameter form (`SET` on
  every connection setup) is the only spelling that survives the pool.
  DuckDB refuses `access_mode=read_only` on an in-memory database, so
  `engineReadOnlyParams` drops the flag there rather than breaking the
  connection.
- UI side is decoration over the guard: `Model.readOnly()` asks the
  driver (not the profile, which may have been edited since connecting),
  the four staging entry points and the commit answer with `connection is
  read-only`, `submitQuery` rejects a whole script containing any write
  before the run starts, the options bar drops the write keys while `?`
  keeps them, and `sidePanel.decor` puts the 🔒 in front of a read-only
  profile's name without disturbing the filter or `selectByName`.

## 2026-08-10 — Per-connection color tags for environment safety (issue #50)

- Added [design/connection-color-tags](design/connection-color-tags.md).
  `Connection.Color` (`color = "red"`, an ANSI name/256-index/hex,
  resolved by the existing `[theme]` parser `resolveColor`) is optional
  and absent means no tag, so existing configs load unchanged.
- The tag rides the main view's top border only
  (`Style.BorderTopForeground`, lipgloss v2), never the whole border: the
  focused-panel green stays the sole "your keys go here" signal, and a
  tagged connection can be on screen unfocused (just browsing) without
  fighting it. `Model.activeTagColor` (`internal/ui/connections.go`)
  resolves the live connection's tag for `renderMainColumn`.
- Panel `[1]` marks every color-tagged connection with a `●`
  (`tagMarker`), independent of read-only's 🔒 and independent of
  whether that connection is the live one — new `sidePanel.tagColor`
  (keyed like `decor`, but rendered separately so its color survives the
  row's status/selection styling instead of being overridden). The same
  marker (`Model.tagMarkerFor`) precedes the connection name in the main
  view wherever it already appeared in text.
- The changeset commit modal and the unguarded DELETE/UPDATE confirm
  (issue #31's guard) both name the connection through
  `Model.taggedConnName` — bold, in its tag color — for salience right
  before a destructive action runs against it.
- Invalid colors never fail config load, unlike `[keys]`/`[theme]`:
  `validateConnectionColors` runs once in `New()`, and `Init()` logs one
  command-log warning per offending connection naming it; every
  render-time call site (`connTagColor`) just treats invalid/empty
  identically as "no tag".
- The connection form's "Color tag" field is a picker (`none` + the six
  named colors from the issue + `custom…`) backed by a `color_hex` text
  field that only shows for `custom` and accepts anything `resolveColor`
  does, not just hex; editing a profile whose stored value isn't one of
  the six named choices reopens with `custom…` preselected and the raw
  value carried over, so a hand-edited config value is never silently
  dropped on the next save.

## 2026-08-10 — Integrated dump/restore via external engine tools (issue #51)

- Added [design/dump-and-restore](design/dump-and-restore.md) (Playbook):
  the per-engine command matrix, the credential-file rule, the process
  lifecycle, and the tunnel exception. New concept; index updated.
- New `internal/dump` package: `Request`/`Command`, `Build` (creates the
  temporary credential file) and `Preview` (assembles the same argv with
  `CredPlaceholder` and no secret, and doubles as the upfront
  `exec.LookPath` check), per-engine builders, `.pgpass`/option-file
  writing with per-format escaping, and a runner that streams stderr,
  keeps the last ten lines for the failure message and kills the child's
  whole process group on cancel.
- `internal/sshtunnel` gained `Tunnel.Listen` — a loopback-only local
  forward. It is a deliberate exception to
  [design/ssh-tunnels](design/ssh-tunnels.md)'s "a tunnel is a transport,
  not a port" rule: an external tool cannot be handed a `DialFunc`. Its
  lifetime is one job's, and the request's host/port are substituted
  before the command is built so the `.pgpass` host field matches what
  libpq actually connected to.
- `db.ClassifyStatementFor` now reads `VACUUM INTO 'file'` and
  `EXPORT DATABASE 'dir'` as *reads*, so a read-only connection can still
  be dumped. Only those two spellings: a bare `VACUUM` rewrites in place
  and `IMPORT DATABASE` writes rows, and both stay writes. The check goes
  through `secondWordIs`, which ignores quoted spellings, mirroring the
  existing `WITH`/`PRAGMA` special cases.
- `formModal` gained an optional `body` hook rendered between the title
  and the fields; the dump/restore form uses it for a live preview of the
  command it is about to run.
- A SQLite restore closes the session synchronously before copying the
  file back — a connection left open across the overwrite could replay the
  old database's WAL onto the new one — and removes the `-wal`/`-shm`/
  `-journal` sidecars afterwards.

## 2026-08-10 — Restore last session on startup (issue #52)

- Added [design/session-restore](design/session-restore.md): the new
  `internal/session` package, why the restore chain lives on
  `Model.restoreSess` rather than the connect flow's usual closures, how
  `dialRequest.restore` plus a nilled `restoreSess` tells a cancelled dial
  apart from an adopted one, the `formModal.onCancel` hook cancelling the
  `AskPassword` path needed, and why `restore_session` is a `*bool`.
- New `internal/session` package: `Session{Connection, Database, Table,
  Tab, Row, Col}` (no credentials), `Dir`/`Path` under `XDG_STATE_HOME`
  and atomic `Save`/`SaveTo`, mirroring `internal/history`'s posture. A
  missing or corrupt file loads as `nil, nil` — a startup restore degrades
  instead of failing, and the next `Save` overwrites it.
- `config.Config` gained `RestoreSession *bool` (`restore_session` in
  TOML) and `RestoreSessionEnabled()` (nil or true → enabled) — the same
  pointer-for-omitempty trick `connectionFile`/`sshFile` already use for
  `Port`, needed here because the default has to be *true* while
  `omitempty` on a plain `bool` can't tell absent from false.
- `ui.New` takes a `noRestore bool` (the `--no-restore` CLI flag) — a
  per-run skip that never touches the config or the session file, so a
  config with restore left enabled restores again next time without the
  flag.
- `formModal` gained an `onCancel func(*Model)` hook fired from its `esc`
  case alongside the existing `onSubmit`, so the restore-triggered
  `AskPassword` prompt can drop its pending restore on cancel instead of
  leaving `restoreSess` to swallow a later, unrelated `esc`.

## 2026-08-10 — Panel titles embedded in the top border (issue #64)

- Added [design/border-embedded-panel-titles](design/border-embedded-panel-titles.md):
  the lazygit-style title-in-border treatment, why `renderTitledBox`
  rebuilds the top line from `GetBorderStyle()`/`GetBorderTopForeground()`
  instead of splicing into the rendered one, why truncation has to be
  `ansi.Truncate` (the title is pre-styled and multi-coloured), and the
  table of which main-view state supplies which border title.
- `renderTitledBox(border, title, body, w, h)` in `internal/ui/styles.go`
  is the single implementation, shared by the four side panels, the main
  view and the command log strip. It degrades to an untitled box when the
  width leaves no room for corners plus one padding rune per side.
- `github.com/charmbracelet/x/ansi` became a direct dependency; it was
  already in the module graph via lipgloss.
- Every content renderer lost its title row and gained it back as content:
  `sidePanel.render` starts at `rows := h`, `dataContent` split into a
  nesting wrapper (tab bar + body, still used for the result grid under
  the query editor) and `dataBody`, and `mainTitle(w)` now shadows the
  `mainContent` switch so the diff, the plan, the editor, the tab bar and
  the connection detail each name themselves in the border.
- Budgets that assumed a title row were corrected: `panelHeights`'
  `collapsed = 3` now buys one usable row, `dataBody` takes `h - 4`,
  `diffContent` `h - 1`, `planContent` `h - 2`, and `completionLayer`
  dropped the header offset it used to add to the caret's screen row.

## 2026-08-10 — Path completion for the connection form's File field (issue #66)

- Added [design/path-completion-in-forms](design/path-completion-in-forms.md):
  the pure-engine / presentation-glue split, the completion behaviours worth
  knowing (`~` notation preserved, dotfiles on explicit dot, case-insensitive
  fallback, trailing separator on directories), why `tab` completes only while
  candidates exist, and how the row budget keeps the modal inside the screen.
- New package `internal/pathcomplete`, ported from the Ike editor and
  unchanged apart from its package doc: `Complete`/`CompleteFrom`/`Dirs`/
  `Expand` over `Result{Candidates, Completed}`, with its tempdir-fixture
  tests.
- `formField` gained `withSuggest()` and `formModal` a `pathSuggest`; the
  connection form's `file` field opted in. `formModal.update` splits the old
  `case "tab", "down"` so `tab` can complete, and `formModal.view` renders the
  candidate rows plus a footer that stops advertising `tab` as field
  navigation while completion owns it.
- `keyMap` gained `CompletePath`/`NextField`/`PrevField` and
  `formPathComplete()`, listed by `helpGroups` on the Connections panel.

## 2026-08-10 — Persist screen mode across restarts (issue #67)

- Added [design/screen-mode-persistence](design/screen-mode-persistence.md):
  the config-vs-state split rationale, why it is a new `internal/config.State`
  file rather than `Config` or `internal/session`, and why `ScreenMode` is
  stored by name.
- `internal/config` gained `State{ScreenMode string}`, `StatePath`,
  `LoadState`/`LoadStateFrom` (never error — a bad file degrades to a zero
  `State`) and `State.Save`/`SaveTo`, at `state.toml` next to `config.toml`.
  `Config.SaveTo` and `State.SaveTo` now share a new `writeAtomicFile` helper
  instead of each having their own temp-file-plus-rename code.
- `internal/ui`: `New()` loads the saved state and applies
  `screenModeFromName` to set the initial `m.screen`; the `k.Quit` handler
  saves the current mode (by name, via `screenModeNames`) before tearing down
  the session. Unknown/missing/corrupt state silently falls back to
  `screenNormal`.
- Tests: `internal/config` covers the state round trip, a corrupt file, a
  missing file, and that saving state never touches `config.toml`;
  `internal/ui` covers `screenModeFromName`'s fallback table, a full
  quit-then-`New()` restore, and startup degrading an unknown saved value to
  `normal`.

## 2026-08-10 — Clipboard: OSC 52 copy fallback and bracketed paste (issue #76)

- Added [design/clipboard-strategy](design/clipboard-strategy.md): the
  native → OSC 52 → temp-file order and why it is that order, why the escape
  sequence has to travel back through the update loop as `copiedMsg.osc52`
  instead of being written from the copy command's goroutine, why an OSC 52
  copy is logged as *sent* rather than confirmed, the two guards on the
  fallback (128 KiB, `detectOSC52`/`LAZYSQL_NO_OSC52`), why the read side of
  OSC 52 is not used at all, and the paste routing table.
- Added [reference/osc52-and-bracketed-paste](reference/osc52-and-bracketed-paste.md):
  the exact sequence emitted (`ESC]52;c;<base64>BEL`, selection `c`, BEL
  terminator, no tmux passthrough wrapping), the per-terminal support table
  (iTerm2 off by default, Terminal.app not at all, tmux `set-clipboard`
  `external`/`on` forwarding), why the size cap exists, and that Bubble Tea v2
  enables bracketed paste for every view without being asked.
- `internal/ui/clipboard.go`: `copyOut` now returns a `copiedMsg` carrying both
  the log line and an optional OSC 52 payload; new `osc52Available`
  seam/`detectOSC52` (terminal on stdout, `TERM` not empty/`dumb`, no
  `LAZYSQL_NO_OSC52`) and `osc52Limit`. `clipboardWrite` and `spillFile` are
  unchanged.
- `internal/ui/copy.go` + `model.go`: `copyTextCmd` returns copyOut's message;
  the root's `copiedMsg` case batches `tea.SetClipboard(msg.osc52)` with the
  log line when the copy has to go out through the terminal.
- Added `internal/ui/paste.go`: `updatePaste` mirrors the key routing for
  `tea.PasteMsg` (modal → `/` filter → query editor; grid and side panels drop
  it), the optional `pasteHandler` modal interface, and `flattenPaste` for
  one-line fields. Implemented `paste` on `promptModal`, `formModal`,
  `filterModal`, `editCellModal`, `insertRowModal` and `paramsModal`. In the
  editor's normal mode the blurred textarea is focused for the insertion alone,
  the mode is kept, a pending `dd`/`yy` chord is cleared and `applyVim`
  re-clamps the caret. Vim's `p` still reads only the internal register.
- Tests: `clipboard_test.go` covers the OSC 52 fallback, the size cap sending a
  large copy to the spill instead, the spill when no mechanism exists, the
  round trip that turns `copiedMsg.osc52` into a `tea.SetClipboard` command,
  and `detectOSC52`'s environment guards; `paste_test.go` covers verbatim
  multi-line paste in both editor modes (including that `DROP TABLE dd;` opens
  no clear-buffer confirm), prompt/connection-form/filter targets, and a
  confirm modal ignoring a paste. `TestMain` sets `LAZYSQL_NO_OSC52=1` so no
  test can write an escape sequence to the developer's terminal.
- Verified in a real PTY (140×40, `creack/pty` driver): the paste in insert and
  normal mode, the pasted value reaching the connection form's Name field, a
  native macOS copy landing on the system clipboard, the same copy emitting
  `ESC]52;c;MQ==BEL` (decoded back to the cell value) once `pbcopy` is off
  `PATH`, and the temp-file spill still happening with `LAZYSQL_NO_OSC52=1`.

## 2026-08-10 — Basic mouse support (issue #77)

- Added [design/mouse-support](design/mouse-support.md): why tracking is
  cell-motion level and set on the `tea.View` rather than as a program option,
  why hit-testing recomputes the layout from `sideWidth`/`panelHeights`/
  `commandLogHeight` instead of caching panel rects, why the drill-in gesture
  is a second click rather than a double-click, why the wheel is aimed by the
  pointer while a click is aimed by focus, and the wheel-coalescing scheme that
  answers the scroll-backlog issue (#78).
- `internal/ui/view.go`: `View()` sets `v.MouseMode = tea.MouseModeCellMotion`.
- Added `internal/ui/mouse.go`: `rect`/`hit`/`hitTest`/`boxHit` geometry, the
  `wheelState` coalescer (`wheelAt`, `flushWheel`, `wheelFlushMsg` on a 16ms
  `tea.Tick`, `gen` dropping stale ticks), `applyScroll`/`scrollMain`/
  `scrollGrid`/`scrollEditor`, `clickSide`/`clickMain`/`clickGrid`, the
  optional `wheelHandler` modal interface, and `sidePanel.tabHit`/`mainTabHit`.
- `internal/ui/model.go`: `Model.wheel wheelState`, plus `tea.MouseMsg` and
  `wheelFlushMsg` cases in `Update`; the routing-order comment now names the
  mouse path.
- `scroll` implemented on `menuModal`, `cellModal`, `commandLogModal`,
  `rowDetailModal` and `historyModal`; every other modal still swallows the
  wheel so it can never reach the view behind an open popup.
- Behaviour: a click focuses a panel and selects a row, a second click on the
  selected row is `enter`, the `Tables`/`Views` and main-view tab headers are
  clickable in the box's top border, and the wheel scrolls the hovered side
  panel list, the grid's rows (clamped at the page boundary — no round trip),
  the query editor's caret, the plan and the schema diff, and a scrollable
  popup. Panel `[4]`'s side box is inert: it previews the buffer from line 1.
- Tests: `mouse_test.go` covers the layout hit map (including the too-small
  guard), click-to-focus/select/drill-in, both tab-header hit tests, the wheel
  scrolling the hovered rather than the focused panel, the coalescer (first
  event applies, the rest accumulate, the flush drains, a stale `gen` is
  ignored, an empty flush disarms), retargeting mid-burst, modal swallowing,
  the grid's cell click and page-boundary clamp, and the editor caret scroll.

## 2026-08-10

- Added [design/input-coalescing](design/input-coalescing.md) with the scroll
  backlog fix (issue #78): why every queued message costs a full View build in
  Bubble Tea v2, the measured 29 ms/event editor culprit (whole-buffer
  re-tokenization, full-buffer styling, quadratic `truncate`), the three-layer
  `editorCache`, and keyboard Down/Up routed through the wheel's coalescer.
- Cross-reference: [design/mouse-support](design/mouse-support.md)'s
  `wheelState` is now the shared coalescer for wheel notches *and* repeated
  navigation keys.
- Added [design/object-tree-panel](design/object-tree-panel.md) with the
  merged object panel (issue #79): `[2] Databases` and `[3] Tables` became one
  expandable `[2] Objects` tree (database → category → object), so the panel
  set is now `[1] Connections`, `[2] Objects`, `[3] Query` and `jump` is
  `1`–`3`. The tree is flattened into the existing cursor-over-slice panel
  (`sidePanel.rows`), so scrolling, the cursor and the selection needed no
  tree awareness; only rendering (indent + `▸`/`▾` + trailing note) and the
  filter branch on it. Categories load lazily on first expand and are cached
  until `R` drops them for the level under the cursor; `Tables` and `Views`
  still share one `ListRelations` round trip. `enter` toggles a branch,
  `l`/`h` expand/collapse lazygit-style. The `Tables`/`Views` sub-tab
  mechanism (`relationTab`, `sidePanel.tabs`, `tabHit`, the `toggle-tab` key
  action) is removed; `expand-node`/`collapse-node` replace it in `[keys]`.
- Documented the filter decision there: `/` narrows the *expanded* rows and
  keeps a branch whose subtree matches, rather than searching collapsed
  categories that have not been read from the server yet.
- Added Triggers as a first-class category, with
  [reference/trigger-introspection](reference/trigger-introspection.md):
  SQLite reads `sqlite_master`, PostgreSQL reads `pg_trigger` (one row per
  trigger, unlike `information_schema.triggers`) plus `pg_get_triggerdef`,
  MySQL/MariaDB synthesize the statement from `information_schema.triggers`
  because `SHOW CREATE TRIGGER` needs a privilege and changes column count
  across versions, and DuckDB has no triggers at all and answers the new
  `db.ErrUnsupported` sentinel. `enter` on a trigger opens its definition
  read-only in the main view (`triggerView`).
- Updated [design/catalog-browsing](design/catalog-browsing.md) with a
  superseded note (its async-load, stale-reply, pseudo-database and fuzzy
  filter rules still hold) and
  [design/db-driver-abstraction](design/db-driver-abstraction.md) with the new
  `ListTriggers`/`TriggerDDL` pair; renumbering notes added to
  [design/tui-shell-architecture](design/tui-shell-architecture.md) and the
  concepts that named `[2]`/`[3]` by number.
