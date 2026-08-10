package ui

import (
	"fmt"
	"image/color"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"

	"lazysql/internal/config"
	"lazysql/internal/db"
	"lazysql/internal/dump"
	"lazysql/internal/history"
	"lazysql/internal/session"
	"lazysql/internal/snippets"
	"lazysql/internal/sshtunnel"
)

// Minimum usable terminal. Below this the layout math produces negative
// widths, so we render a "too small" notice instead.
const (
	minWidth  = 60
	minHeight = 18
)

// screenMode mirrors lazygit's `+`/`_` cycling: how much room the focused
// side panel gets relative to the rest of the layout.
type screenMode int

const (
	screenNormal screenMode = iota
	screenHalf
	screenFull
	screenModeCount
)

var screenModeNames = [screenModeCount]string{"normal", "half", "full"}

// screenModeFromName maps a saved state name back to a screenMode. An
// unknown or empty name (missing/corrupt state file, or a value from a
// future version) falls back to screenNormal silently.
func screenModeFromName(name string) screenMode {
	for i, n := range screenModeNames {
		if n == name {
			return screenMode(i)
		}
	}
	return screenNormal
}

// ---------- messages ----------

// Panels emit domain messages instead of mutating shared state; the root
// model reduces them. Real DB work will arrive the same way, from tea.Cmds.

// commandLogMsg appends a line to the command log under the main view.
type commandLogMsg struct{ line string }

func logCmd(format string, a ...any) tea.Cmd {
	line := fmt.Sprintf(format, a...)
	return func() tea.Msg { return commandLogMsg{line: line} }
}

// logLine is one line the command log panel renders: either a UI note
// (skip reasons, connect status, export progress — logCmd's own text) or
// an executed statement pulled from the Driver's Logger. at orders the
// two streams into one timeline; err colors the line red.
type logLine struct {
	text string
	at   time.Time
	err  bool
}

func (l logLine) render() string {
	return l.at.Format("15:04:05") + "  " + l.text
}

// commandLogEntries merges the UI's own notes with the connected
// Driver's Logger — the single choke point every Exec/Query runs
// through — into one chronological feed for the slim panel and its `@`
// expanded view. The Logger, not this slice, is what guarantees a
// statement from browsing, editing or the query editor shows up exactly
// once: nothing here re-formats SQL by hand.
func (m Model) commandLogEntries() []logLine {
	out := append([]logLine(nil), m.commandLog...)
	if m.driver != nil {
		for _, e := range m.driver.Logger().Entries() {
			out = append(out, logLine{text: sqlEntryText(e), at: e.At, err: e.Err != nil})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].at.Before(out[j].at) })
	if n := len(out); n > db.LogCapacity {
		out = out[n-db.LogCapacity:]
	}
	return out
}

// sqlEntryText spells one Logger entry the way the log already renders a
// statement: the SQL, its bound args if any, how long it took, and the
// error if it failed.
func sqlEntryText(e db.LogEntry) string {
	text := e.SQL
	if !strings.HasSuffix(text, ";") {
		text += ";"
	}
	if len(e.Args) > 0 {
		text += fmt.Sprintf("  -- args %v", e.Args)
	}
	text += "  (" + formatTook(e.Duration) + ")"
	if e.Err != nil {
		text += fmt.Sprintf("  -- FAILED: %v", e.Err)
	}
	return text
}

// focusPanelMsg moves focus to a panel.
type focusPanelMsg struct{ id panelID }

func focusCmd(id panelID) tea.Cmd {
	return func() tea.Msg { return focusPanelMsg{id: id} }
}

// restoreStartMsg kicks off the last-session restore once the shell has
// rendered, so the connections panel is on screen (and esc can cancel)
// before the dial begins.
type restoreStartMsg struct{}

func restoreStartCmd() tea.Cmd {
	return func() tea.Msg { return restoreStartMsg{} }
}

// historyEntryMsg records a statement in the query history: panel [4]
// and the on-disk history behind it. Everything lazysql executes emits
// one — a browsing page, a committed changeset, a script from the
// editor — so there is a single way into the history.
type historyEntryMsg struct{ statement string }

// historyCmd is the message as a command, for callers that only build
// tea.Cmds.
func historyCmd(statement string) tea.Cmd {
	return func() tea.Msg { return historyEntryMsg{statement: statement} }
}

// ---------- root model ----------

// Model is the single root model: it owns terminal size, focus, one child
// model per side panel, main view state, the open modal (nil = none) and the
// command log.
type Model struct {
	width, height int

	focus  panelID
	panels [panelCount]*sidePanel
	prev   []panelID // focus stack for `esc`

	modal  modal
	screen screenMode

	commandLog []logLine

	// Connection manager state. cfg is the on-disk connection list; connState
	// is the transient per-connection status the panel colors itself by.
	cfg       *config.Config
	connState map[string]connState
	driver    db.Driver
	active    string // name of the connected profile, "" when none
	// tunnel is the SSH tunnel the active driver runs through, nil for a
	// direct connection. Its lifetime is the connection's: it is closed
	// whenever driver is, including on quit.
	tunnel *sshtunnel.Tunnel

	// Browsing state: the namespace panel [3] currently shows, the
	// relations it was filled from (both kinds, one round trip) and which
	// sub-tab is selected.
	database  string
	relations []db.Relation
	tableTab  relationTab
	table     string // the relation the main view is showing

	// data is the main view's Data tab: one page of m.table.
	data dataView

	// changes is the staged changeset: edits accumulate here and only
	// execute on explicit commit. It is a pointer so every copied Model
	// shares one changeset.
	changes *db.Changeset

	// tab is the main view's selected tab and meta is the metadata the
	// three introspection tabs render.
	tab  mainTab
	meta metaView

	// Foreign-key navigation. fkCache holds one relation's constraints,
	// refsCache the whole namespace's (keyed by an fkKey with an empty
	// table) for the reverse direction, and fkLoading marks the fetches
	// in flight so a repeated key press does not stack round trips.
	// browseStack is the jump history `ctrl+o`/`esc` walk back, and
	// fkAfter is the action waiting for a fetch that has not landed yet.
	fkCache     map[fkKey][]db.ForeignKey
	refsCache   map[fkKey][]namespaceFK
	fkLoading   map[fkKey]bool
	browseStack []browseState
	fkAfter     actionID

	// export is the file export in flight, if any. At most one runs at
	// a time; `X` cancels it.
	export exportState

	// backup is the dump or restore in flight, if any — an external tool
	// (pg_dump, mysqldump) or the file engines' own SQL. Like the export
	// only one runs at a time and `X` cancels it.
	backup backupState

	// dbDDLExport guards a whole-database DDL export the same way: only
	// one runs at a time. It has no cancel key of its own — one round
	// trip per relation finishes long before a data export would — but
	// resetBrowse still cancels it when the connection it reads through
	// is closing.
	dbDDLExport dbDDLExportState

	// diff is the schema diff on screen (running or finished), nil when
	// none. It dials its own connections, so it survives disconnects.
	diff *diffView

	// plan is the EXPLAIN result on screen, nil when none. It replaces
	// the editor in the main view while panel [5] keeps the focus, and
	// `esc` dismisses it with the buffer untouched.
	plan *planView

	// history is the persistent query history behind panel [4], newest
	// first. editor is panel [5] — the buffer and its mode, which outlive
	// every focus change — and run is the script currently executing, if
	// any.
	history []history.Entry
	editor  queryEditor
	run     queryRun

	// snippets are the named statements behind the pane's Snippets
	// section, sorted by name. They are the deliberate half of the recall
	// story the history is the automatic half of.
	snippets []snippets.Snippet

	// completion is the editor's autocomplete popup, and schema the
	// column cache behind it. The cache keys itself on connection +
	// database and drops itself when either changes.
	completion completion
	schema     schemaCache

	// restoreSess is the on-disk session's target — connection, database,
	// table, tab, cursor — while startup is still dialing and navigating
	// to it. It is consumed (set nil) as each stage lands or fails, and
	// esc during the dial clears it to abort. nil means no restore is
	// configured, already finished, or never started.
	restoreSess *session.Session

	startupErr    string
	colorWarnings []string

	keys  keyMap
	help  help.Model
	style styles

	// wheel coalesces scroll bursts — mouse wheel notches and repeated
	// navigation keys alike — into one state change per frame, so fast
	// input cannot queue up behind the renderer. See mouse.go.
	wheel wheelState

	// hl caches the query editor's tokenization and wrap geometry, so a
	// pure cursor move re-styles only the visible rows instead of
	// re-highlighting the whole buffer. See highlight.go.
	hl *editorCache

	// spin animates the running indicator in the options bar. It ticks
	// only while a query runs — the message handler drops any tick that
	// outlives its run — so it costs nothing the rest of the time.
	spin spinner.Model
}

// New builds the shell and loads the saved connections. A broken or missing
// connections list never blocks startup: the error is surfaced in the
// command log and the app starts with an empty connection list. An invalid
// `[keys]` or `[theme]` section is different — those change what the app
// does when a key is pressed, so New returns an error for the caller to
// report and exit on, rather than silently running with a broken keymap.
//
// noRestore skips the last-session restore for this run — the `--no-restore`
// flag — even when the config's `restore_session` is enabled. It never
// touches the saved session file itself, only whether this run reads it.
func New(noRestore bool) (Model, error) {
	cfg, cfgErr := config.Load()
	if cfgErr != nil {
		cfg = &config.Config{}
	}

	km := newKeyMap()
	if err := applyKeyOverrides(&km, cfg.Keys); err != nil {
		return Model{}, fmt.Errorf("config: %w", err)
	}
	pal, err := resolvePalette(cfg.Theme)
	if err != nil {
		return Model{}, fmt.Errorf("config: %w", err)
	}
	applyPalette(pal)

	m := Model{
		focus:     panelConnections,
		keys:      km,
		help:      help.New(),
		style:     newStyles(),
		connState: map[string]connState{},
		fkCache:   map[fkKey][]db.ForeignKey{},
		refsCache: map[fkKey][]namespaceFK{},
		fkLoading: map[fkKey]bool{},
		changes:   db.NewChangeset(),
		cfg:       cfg,
		editor:    newQueryEditor(),
		hl:        &editorCache{},
	}
	m.spin = spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(m.style.pending))
	if cfgErr != nil {
		m.startupErr = cfgErr.Error()
	}
	m.colorWarnings = validateConnectionColors(cfg.Connections)
	for id := panelID(0); id < panelCount; id++ {
		m.panels[id] = &sidePanel{id: id}
	}
	m.panels[panelTables].tabs = relationTabNames[:]

	if st, err := config.LoadState(); err == nil && st != nil {
		m.screen = screenModeFromName(st.ScreenMode)
	}

	selectName := ""
	if !noRestore && cfg.RestoreSessionEnabled() {
		if sess, err := session.Load(); err == nil && sess != nil {
			if _, ok := cfg.Find(sess.Connection); ok {
				m.restoreSess = sess
				selectName = sess.Connection
			}
		}
	}
	m.refreshConnections(selectName)
	return m, nil
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{loadHistoryCmd(), loadSnippetsCmd()}
	if m.startupErr != "" {
		cmds = append(cmds, logCmd("-- config error: %s", m.startupErr))
	}
	for _, w := range m.colorWarnings {
		cmds = append(cmds, logCmd("-- warning: %s", w))
	}
	if m.restoreSess != nil {
		cmds = append(cmds, logCmd("-- restoring session: %s …", m.restoreSess.Connection), restoreStartCmd())
	}
	return tea.Batch(cmds...)
}

// lockMark is the read-only indicator: it precedes a read-only profile's
// name in panel [1] and marks the main view of its connection.
const lockMark = "🔒"

// tagMarker precedes a connection's name — in panel [1] and in the main
// view — when it carries a valid environment color tag (config.Connection.Color).
const tagMarker = "●"

// readOnly reports whether the live connection refuses writes. The driver
// is asked rather than the profile: the session's own guard is what
// enforces the mode, and a profile edited after connecting must not make
// the UI believe writes are back.
func (m Model) readOnly() bool { return m.driver != nil && m.driver.ReadOnly() }

// connReadOnly reports whether the named profile is configured read-only.
// It answers for connections that are not open, which is what the panel's
// lock marks need.
func (m Model) connReadOnly(name string) bool {
	c, ok := m.cfg.Find(name)
	return ok && c.ReadOnly
}

// refreshConnections rebuilds the [1] Connections panel from the config plus
// the live status map, keeping (or moving to) the named selection.
func (m *Model) refreshConnections(selectName string) {
	names := m.cfg.Names()
	status := make([]itemStatus, len(names))
	marks := map[string]string{}
	tags := map[string]color.Color{}
	for i, n := range names {
		status[i] = m.connState[n].status
		if m.connReadOnly(n) {
			marks[n] = lockMark + " "
		}
		if c, ok := m.cfg.Find(n); ok {
			if tc, ok := connTagColor(c); ok {
				tags[n] = tc
			}
		}
	}
	p := m.panels[panelConnections]
	prev := p.selected()
	p.decor = marks
	p.tagColor = tags
	p.setItemsWithStatus(names, status)
	if selectName == "" {
		selectName = prev
	}
	if selectName != "" {
		p.selectByName(selectName)
	}
}

// setConnStatus records a status transition and repaints the panel.
func (m *Model) setConnStatus(name string, st itemStatus, lastErr string) {
	if name == "" {
		return
	}
	m.connState[name] = connState{status: st, lastErr: lastErr}
	m.refreshConnections("")
}

// renameConnState follows a profile rename so its status does not stick to a
// name that no longer exists.
func (m *Model) renameConnState(oldName, newName string) {
	if oldName == "" || oldName == newName {
		return
	}
	if st, ok := m.connState[oldName]; ok {
		m.connState[newName] = st
		delete(m.connState, oldName)
	}
	if m.active == oldName {
		m.active = newName
	}
}

// resetBrowse drops everything that belonged to the previous connection.
func (m *Model) resetBrowse() {
	// Staged changes reference the connection's tables; they cannot
	// survive it. They are discarded, not committed.
	m.changes.Clear()
	// An export reads through the driver that is about to be closed.
	if m.export.running && m.export.cancel != nil {
		m.export.cancel()
	}
	// So does a whole-database DDL export.
	if m.dbDDLExport.running && m.dbDDLExport.cancel != nil {
		m.dbDDLExport.cancel()
	}
	// A dump of a file engine runs its SQL through the same driver, and
	// a tunnelled dump runs through the tunnel that is about to close.
	if m.backup.running && m.backup.cancel != nil {
		m.backup.cancel()
	}
	// So does a running script.
	if m.run.running && m.run.cancel != nil {
		m.run.cancel()
	}
	// A plan describes a statement against the connection being left.
	m.plan = nil
	m.database = ""
	m.table = ""
	m.data = dataView{}
	m.tab = mainTabData
	m.resetMeta()
	m.relations = nil
	m.tableTab = tabTables
	// The foreign-key caches and the jump history describe relations of
	// the connection being left behind.
	m.fkCache = map[fkKey][]db.ForeignKey{}
	m.refsCache = map[fkKey][]namespaceFK{}
	m.fkLoading = map[fkKey]bool{}
	m.browseStack = nil
	m.fkAfter = actNone
	if m.focus == panelMain {
		m.focus = panelTables
	}
	for _, id := range []panelID{panelDatabases, panelTables} {
		p := m.panels[id]
		p.loading = false
		p.clearFilter()
		p.setItems(nil)
	}
	m.panels[panelTables].tab = int(m.tableTab)
}

// openDatabase makes name the browsed namespace and starts the table load.
// The panel keeps its old rows until the reply lands.
func (m *Model) openDatabase(name string) tea.Cmd {
	m.database = databaseArg(name)
	// The open page belongs to the namespace we are leaving, and so does
	// every state the jump history could go back to.
	m.table = ""
	m.data = dataView{}
	m.browseStack = nil
	m.fkAfter = actNone
	m.resetMeta()
	if m.focus == panelMain {
		m.focus = panelTables
	}
	p := m.panels[panelTables]
	p.clearFilter()
	if m.driver == nil {
		return nil
	}
	m.relations = nil
	p.loading = true
	return loadRelationsCmd(m.active, m.driver, m.database)
}

// refreshRelations repaints panel [3] from the cached relation list for the
// selected sub-tab — switching tabs never hits the server.
func (m *Model) refreshRelations() {
	p := m.panels[panelTables]
	p.tab = int(m.tableTab)
	p.setItems(db.FilterRelations(m.relations, m.tableTab.kind()))
}

// reloadFocused re-runs the server query behind the focused panel.
func (m *Model) reloadFocused() tea.Cmd {
	if m.driver == nil {
		return logCmd("-- reload %s skipped: not connected", panelTitles[m.focus])
	}
	switch m.focus {
	case panelDatabases:
		m.panels[panelDatabases].loading = true
		return tea.Batch(
			logCmd("-- reload databases of %s", m.active),
			loadDatabasesCmd(m.active, m.driver),
		)
	case panelTables:
		m.panels[panelTables].loading = true
		return tea.Batch(
			logCmd("-- reload tables of %s", displayDatabase(m.database)),
			loadRelationsCmd(m.active, m.driver, m.database),
		)
	case panelMain:
		if m.tab.metadata() {
			return m.reloadMeta()
		}
		if m.data.isQuery() {
			return m.rerunQuery()
		}
		// `R` means "read it again from the server", so the cached
		// constraints behind the `⇒` marks go too.
		delete(m.fkCache, m.tableFKKey())
		return tea.Batch(m.reloadPage(), m.ensureFKs())
	}
	return logCmd("-- refresh %s", panelTitles[m.focus])
}

// displayDatabase names the browsed namespace for logs and the main view.
func displayDatabase(database string) string {
	if database == "" {
		return pseudoDatabase
	}
	return database
}

// findDatabaseDisplayName looks up the panel [2] entry (already run through
// namespaceList) whose driver argument is want — the form m.database and a
// saved session both store. openDatabase wants the display form back, not
// the driver argument, so a direct string match against want would miss the
// pseudo-database entry file engines show for "".
func findDatabaseDisplayName(dbs []string, want string) (string, bool) {
	for _, d := range dbs {
		if databaseArg(d) == want {
			return d, true
		}
	}
	return "", false
}

// selectedConnection returns the profile under the cursor of panel [1].
func (m Model) selectedConnection() (config.Connection, bool) {
	name := m.panels[panelConnections].selected()
	if name == "" {
		return config.Connection{}, false
	}
	return m.cfg.Find(name)
}

// Update routes in a fixed order: WindowSizeMsg → open modal (swallows all
// keys) → an open `/` filter → the query editor in insert mode → global
// keys → focused panel. A bracketed paste takes the same order through
// updatePaste, minus the steps that only make sense for a key, and a
// mouse event through updateMouse — where a click that moves the focus
// is the "global" step and the wheel is aimed by the pointer rather than
// by the focus.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case commandLogMsg:
		m.commandLog = append(m.commandLog, logLine{
			text: msg.line,
			at:   time.Now(),
			err:  strings.Contains(msg.line, "FAILED"),
		})
		if n := len(m.commandLog); n > db.LogCapacity {
			m.commandLog = m.commandLog[n-db.LogCapacity:]
		}
		return m, nil

	case historyEntryMsg:
		cmd := m.recordHistory(msg.statement)
		return m, cmd

	case historyLoadedMsg:
		if msg.err != nil {
			// A broken history file costs the panel its contents and
			// nothing else; the session keeps recording into it.
			return m, logCmd("-- read query history FAILED: %v", msg.err)
		}
		m.history = msg.entries
		return m, nil

	case historyWrittenMsg:
		if msg.err != nil {
			return m, logCmd("-- write query history FAILED: %v", msg.err)
		}
		return m, nil

	case snippetsLoadedMsg:
		if msg.err != nil {
			// A broken snippet file costs the pane its Snippets section
			// and nothing else; a save rewrites the file from scratch.
			return m, logCmd("-- read query snippets FAILED: %v", msg.err)
		}
		m.snippets = msg.list
		return m, nil

	case snippetsWrittenMsg:
		if msg.err != nil {
			return m, logCmd("-- write query snippets FAILED: %v", msg.err)
		}
		return m, nil

	case queryStmtMsg:
		if msg.id != m.run.id || !m.run.running {
			return m, nil
		}
		// Bind the command first: applyQueryStmt mutates m, and Go may
		// otherwise copy the pre-call model into the return value.
		cmd := m.applyQueryStmt(msg)
		return m, tea.Batch(cmd, waitQueryCmd(m.run.ch))

	case queryDoneMsg:
		if msg.id != m.run.id {
			return m, nil
		}
		cmd := m.finishQuery(msg)
		return m, cmd

	case spinner.TickMsg:
		// A tick that outlives its run is dropped rather than chained:
		// that is what stops the spinner without a separate "stop" message.
		if !m.run.running {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case explainDoneMsg:
		// Bind the command first: finishExplain mutates the view on m,
		// and Go may otherwise copy the pre-call model into the return.
		cmd := m.finishExplain(msg)
		return m, cmd

	case schemaColumnsMsg:
		// Bind the command first: applySchemaColumns mutates the cache on
		// m, and Go may otherwise copy the pre-call model into the return.
		cmd := m.applySchemaColumns(msg)
		return m, cmd

	case focusPanelMsg:
		m.setFocus(msg.id)
		return m, nil

	case restoreStartMsg:
		if m.restoreSess == nil {
			return m, nil
		}
		c, ok := m.cfg.Find(m.restoreSess.Connection)
		if !ok {
			name := m.restoreSess.Connection
			m.restoreSess = nil
			return m, logCmd("-- restore session: connection %q no longer exists", name)
		}
		req := dialRequest{conn: c, restore: true}
		if c.NeedsPassword() && c.AskPassword {
			prompt := newPasswordPrompt(c, func(pw string) tea.Cmd {
				next := req
				next.password, next.hasPassword = pw, true
				return redialCmd(next)
			})
			// esc on the prompt abandons the restore like any other cancel,
			// rather than leaving it to swallow an unrelated esc later.
			prompt.onCancel = func(mm *Model) { mm.restoreSess = nil }
			m.modal = prompt
			return m, nil
		}
		return m, redialCmd(req)

	case connTestedMsg:
		if msg.err != nil {
			m.setConnStatus(msg.name, statusError, msg.err.Error())
			// An SSH failure the user can answer — unknown host key,
			// missing passphrase — becomes a prompt that redials.
			if mod := sshFailureModal(msg.req, msg.err); mod != nil {
				m.modal = mod
			}
			return m, logCmd("-- test %s FAILED: %v", msg.name, msg.err)
		}
		// A successful test does not make the profile the active connection:
		// it only clears a stale error on anything that is not connected.
		if m.active != msg.name {
			m.setConnStatus(msg.name, statusIdle, "")
		}
		return m, logCmd("-- test %s ok in %s (%s)", msg.name, msg.took.Round(time.Millisecond), msg.dsn)

	case connectedMsg:
		// restoring is only true for the one dial the startup restore
		// issued, and only while it has not been abandoned — esc during
		// the dial (or cancelling its password prompt) clears restoreSess
		// before this reply can land.
		if msg.req.restore && m.restoreSess == nil {
			if msg.err == nil {
				// The dial finished after the restore was cancelled: the
				// connection is real but unwanted, so it is closed rather
				// than adopted — cancelling must actually leave the
				// connections panel disconnected.
				return m, closeSessionCmd(msg.driver, msg.tunnel)
			}
			return m, nil
		}
		restoring := msg.req.restore
		if msg.err != nil {
			if restoring {
				sess := *m.restoreSess
				m.restoreSess = nil
				return m, logCmd("-- restore session: connect %s FAILED: %v", sess.Connection, msg.err)
			}
			m.setConnStatus(msg.name, statusError, msg.err.Error())
			if mod := sshFailureModal(msg.req, msg.err); mod != nil {
				m.modal = mod
			} else {
				m.modal = &confirmModal{
					title:  "Connection failed",
					body:   fmt.Sprintf("%s: %v", msg.name, msg.err),
					danger: true,
				}
			}
			return m, logCmd("-- connect %s FAILED: %v", msg.name, msg.err)
		}
		prevDriver, prevTunnel := m.driver, m.tunnel
		if m.active != "" && m.active != msg.name {
			m.setConnStatus(m.active, statusIdle, "")
		}
		m.driver = msg.driver
		m.tunnel = msg.tunnel
		m.active = msg.name
		m.setConnStatus(msg.name, statusOK, "")
		m.resetBrowse()
		m.panels[panelDatabases].setItems(namespaceList(msg.driver.Engine(), msg.databases))
		cmds := []tea.Cmd{
			closeSessionCmd(prevDriver, prevTunnel),
			logCmd("-- connect %s (%s)", msg.name, msg.dsn),
		}
		dbs := m.panels[panelDatabases].items
		if restoring {
			if display, ok := findDatabaseDisplayName(dbs, m.restoreSess.Database); ok {
				cmds = append(cmds, m.openDatabase(display), focusCmd(panelTables))
				return m, tea.Batch(cmds...)
			}
			sess := *m.restoreSess
			m.restoreSess = nil
			cmds = append(cmds, logCmd("-- restore session: database %q not found", displayDatabase(sess.Database)))
		}
		// Single-namespace engines (SQLite, DuckDB) have nothing to pick:
		// go straight to their tables.
		if len(dbs) == 1 {
			cmds = append(cmds, m.openDatabase(dbs[0]), focusCmd(panelTables))
		} else {
			cmds = append(cmds, focusCmd(panelDatabases))
		}
		return m, tea.Batch(cmds...)

	case databasesLoadedMsg:
		// A reply for a connection that is no longer live is stale.
		if msg.conn != m.active {
			return m, nil
		}
		p := m.panels[panelDatabases]
		p.loading = false
		if msg.err != nil {
			// The panel keeps its previous content; only the log knows.
			return m, logCmd("-- list databases FAILED: %v", msg.err)
		}
		p.setItems(namespaceList(m.driver.Engine(), msg.databases))
		return m, nil

	case relationsLoadedMsg:
		if msg.conn != m.active || msg.database != m.database {
			return m, nil
		}
		p := m.panels[panelTables]
		p.loading = false
		if msg.err != nil {
			if m.restoreSess != nil {
				sess := *m.restoreSess
				m.restoreSess = nil
				return m, logCmd("-- restore session: list tables of %s FAILED: %v", displayDatabase(sess.Database), msg.err)
			}
			return m, logCmd("-- list tables of %s FAILED: %v", displayDatabase(msg.database), msg.err)
		}
		m.relations = msg.relations
		m.refreshRelations()
		if m.restoreSess != nil {
			sess := *m.restoreSess
			if sess.Table == "" {
				m.restoreSess = nil
				return m, nil
			}
			if !containsName(db.RelationNames(m.relations), sess.Table) {
				m.restoreSess = nil
				return m, logCmd("-- restore session: table %q not found in %s", sess.Table, displayDatabase(sess.Database))
			}
			// restoreSess stays set: pageLoadedMsg finishes the restore
			// with the tab and cursor once the page it names has loaded.
			return m, tea.Batch(m.openTable(sess.Table), focusCmd(panelMain))
		}
		return m, nil

	case pageLoadedMsg:
		if !m.fresh(msg.req, msg.conn, msg.table) {
			return m, nil
		}
		m.data.loading = false
		if msg.err != nil {
			// The previous page stays on screen; the grid and the log
			// both name the failure.
			m.data.err = msg.err.Error()
			if m.restoreSess != nil && m.restoreSess.Table == msg.table {
				sess := *m.restoreSess
				m.restoreSess = nil
				return m, logCmd("-- restore session: select from %s FAILED: %v", sess.Table, msg.err)
			}
			return m, logCmd("-- select from %s FAILED: %v", msg.table, msg.err)
		}
		m.data.cols = msg.result.Columns
		m.data.rows = msg.result.Rows
		if m.restoreSess != nil && m.restoreSess.Table == msg.table {
			sess := *m.restoreSess
			m.restoreSess = nil
			m.data.row, m.data.col = sess.Row, sess.Col
			m.clampCursor()
			return m, tea.Batch(
				logCmd("-- restored session: %s / %s.%s", sess.Connection, displayDatabase(sess.Database), sess.Table),
				m.setMainTab(mainTab(sess.Tab)),
			)
		}
		m.clampCursor()
		return m, nil

	case metaLoadedMsg:
		if !m.freshMeta(msg) {
			return m, nil
		}
		m.meta.loading = false
		m.meta.loaded = true
		if msg.err != nil {
			m.meta.err = msg.err.Error()
			m.meta.afterLoad = actNone
			return m, logCmd("-- introspect %s FAILED: %v", msg.table, msg.err)
		}
		m.meta.cols, m.meta.indexes, m.meta.fks = msg.cols, msg.indexes, msg.fks
		// The introspection fetch already read the foreign keys, so the
		// grid's own cache is filled from it rather than re-reading them.
		m.cacheFKs(fkKey{conn: msg.conn, database: msg.database, table: msg.table}, msg.fks)
		m.meta.ddl, m.meta.ddlErr = msg.ddl, ""
		if msg.ddlErr != nil {
			m.meta.ddlErr = msg.ddlErr.Error()
		}
		// A key pressed before the metadata existed runs now. The action
		// takes the same path it would have taken with a warm cache.
		if id := m.meta.afterLoad; id != actNone {
			m.meta.afterLoad = actNone
			mm, cmd := m.runAction(id)
			return mm, cmd
		}
		return m, nil

	case fksLoadedMsg:
		// A reply for a connection that is no longer live is stale.
		if msg.key.conn != m.active {
			return m, nil
		}
		delete(m.fkLoading, msg.key)
		if msg.err != nil {
			// Without the metadata the grid loses the `⇒` marks and the
			// follow key; browsing itself is unaffected.
			return m, logCmd("-- foreign keys of %s FAILED: %v", msg.key.table, msg.err)
		}
		m.cacheFKs(msg.key, msg.fks)
		if id := m.fkAfter; id != actNone && msg.key == m.tableFKKey() {
			m.fkAfter = actNone
			mm, cmd := m.runAction(id)
			return mm, cmd
		}
		return m, nil

	case namespaceFKsLoadedMsg:
		if msg.key.conn != m.active {
			return m, nil
		}
		delete(m.fkLoading, msg.key)
		if msg.err != nil {
			m.fkAfter = actNone
			return m, logCmd("-- scan foreign keys of %s FAILED: %v",
				displayDatabase(msg.key.database), msg.err)
		}
		if m.refsCache == nil {
			m.refsCache = map[fkKey][]namespaceFK{}
		}
		m.refsCache[msg.key] = msg.refs
		// The scan read every table's constraints, so the per-table
		// cache is filled from the same round trips.
		for _, t := range msg.tables {
			k := fkKey{conn: msg.key.conn, database: msg.key.database, table: t}
			var fks []db.ForeignKey
			for _, r := range msg.refs {
				if r.table == t {
					fks = append(fks, r.fk)
				}
			}
			m.cacheFKs(k, fks)
		}
		if id := m.fkAfter; id != actNone && msg.key == m.namespaceFKKey() {
			m.fkAfter = actNone
			mm, cmd := m.runAction(id)
			return mm, cmd
		}
		return m, nil

	case rowCountMsg:
		if !m.fresh(msg.req, msg.conn, msg.table) {
			return m, nil
		}
		if msg.err != nil {
			// A missing count only costs the "of ~N" part of the status
			// line, so it never blocks browsing.
			m.data.hasTotal = false
			return m, logCmd("-- count %s FAILED: %v", msg.table, msg.err)
		}
		m.data.total, m.data.hasTotal = msg.total, true
		return m, nil

	case changesCommittedMsg:
		if msg.err != nil {
			// The transaction rolled back: nothing was applied, and the
			// changeset survives so the user can fix and retry.
			return m, logCmd("-- COMMIT FAILED — nothing applied, changeset kept: %v", msg.err)
		}
		// The transaction itself — BEGIN, each statement, COMMIT — is
		// already in the command log: ExecTx logged it through the
		// Driver's Logger as it ran. This only feeds the query-recall
		// history, a separate concern from the audit trail.
		var cmds []tea.Cmd
		for _, s := range msg.stmts {
			cmds = append(cmds, historyCmd(s.SQL))
		}
		m.changes.Clear()
		cmds = append(cmds,
			logCmd("-- commit ok: %s applied", countChanges(len(msg.stmts))),
			m.reloadPage(),
		)
		return m, tea.Batch(cmds...)

	case copiedMsg:
		// A copy already rendered its own outcome — clipboard, OSC 52,
		// temp-file fallback or failure — so the log line is taken as it
		// is. An OSC 52 copy still has to be written to the terminal,
		// which only the program can do: hence the round trip through
		// here rather than a write from inside the copy command.
		if msg.osc52 != "" {
			return m, tea.Batch(tea.SetClipboard(msg.osc52), logCmd("%s", msg.line))
		}
		return m, logCmd("%s", msg.line)

	case exportProgressMsg:
		if msg.id != m.export.id || !m.export.running {
			return m, nil
		}
		return m, tea.Batch(
			logCmd("-- export %s: %d rows…", m.export.table, msg.rows),
			waitExportCmd(m.export.ch),
		)

	case exportDoneMsg:
		if msg.id != m.export.id {
			return m, nil
		}
		// Bind the command first: finishExport clears the in-flight
		// state on m, and Go may otherwise copy the pre-call model into
		// the return value.
		cmd := m.finishExport(msg)
		return m, cmd

	case backupPasswordMsg:
		// Bind the command first: openBackupModal sets the modal on m,
		// and Go may otherwise copy the pre-call model into the return.
		cmd := m.openBackupModal(msg.action, msg.password)
		return m, cmd

	case backupLineMsg:
		if msg.id != m.backup.id || !m.backup.running {
			return m, nil
		}
		return m, tea.Batch(
			logCmd("-- %s: %s", m.backup.action, msg.line),
			waitBackupCmd(m.backup.ch),
		)

	case backupDoneMsg:
		if msg.id != m.backup.id {
			return m, nil
		}
		// Bind the command first: finishBackup clears the in-flight state
		// on m, and Go may otherwise copy the pre-call model into the
		// return value.
		cmd := m.finishBackup(msg)
		return m, cmd

	case databaseDDLExportedMsg:
		if msg.id != m.dbDDLExport.id {
			return m, nil
		}
		cmd := m.finishDatabaseDDLExport(msg)
		return m, cmd

	case diffSecretMsg:
		// Bind the command first: promptDiffSecrets mutates m (modal or
		// diff state), and Go may otherwise copy the pre-call model.
		cmd := m.promptDiffSecrets(msg.a, msg.b)
		return m, cmd

	case schemaDiffProgressMsg:
		cmd := m.applyDiffProgress(msg)
		return m, cmd

	case schemaDiffDoneMsg:
		cmd := m.finishDiff(msg)
		return m, cmd

	case connPersistedMsg:
		if msg.err != nil {
			return m, logCmd("-- %s %s FAILED: %v", msg.verb, msg.name, msg.err)
		}
		return m, nil

	case tea.PasteMsg:
		// Bracketed paste arrives as one message rather than as the keys
		// it is made of, and is routed like one — see paste.go for why
		// it cannot simply follow the key path.
		return m.updatePaste(msg)

	case wheelFlushMsg:
		// One frame's worth of accumulated wheel notches — see mouse.go.
		cmd := m.flushWheel(msg)
		return m, cmd

	case tea.MouseMsg:
		// Clicks and the wheel take the same routing order a key does,
		// with one difference: the wheel goes to the panel under the
		// pointer, not to the focused one. See mouse.go.
		return m.updateMouse(msg)

	case tea.KeyPressMsg:
		// 1. A modal swallows every key. esc always cancels.
		if m.modal != nil {
			cur := m.modal
			shouldClose, cmd := cur.update(msg, &m)
			// Only clear if the handler didn't open a replacement modal.
			if shouldClose && m.modal == cur {
				m.modal = nil
			}
			return m, cmd
		}
		// 2. An open `/` filter captures every printable key, so digits
		// and `q` type into the pattern instead of jumping or quitting.
		if m.focus < panelCount && m.panels[m.focus].filtering {
			return m.updateFilter(msg)
		}
		// 3. The query editor in insert mode captures every key it does
		// not reserve — ahead of the global keys, or `q` would quit in
		// the middle of a statement.
		if m.focus == panelQuery && m.editor.editing {
			return m.updateEditor(msg)
		}
		// 4. Global keys.
		if handled, mm, cmd := m.updateGlobal(msg); handled {
			return mm, cmd
		}
		// 5. The focused view: the data grid, the query editor in normal
		// mode, or a side panel.
		if m.focus == panelMain {
			return m.updateData(msg)
		}
		if m.focus == panelQuery {
			return m.updateQuery(msg)
		}
		return m.updateFocused(msg)
	}
	return m, nil
}

func (m Model) updateGlobal(msg tea.KeyPressMsg) (bool, tea.Model, tea.Cmd) {
	k := m.keys
	switch {
	// esc aborts a startup session restore that is still dialing. This
	// only runs while no modal is open (the routing above hands a modal
	// every key first), so it never fires while the AskPassword prompt
	// itself is showing — that path cancels through the prompt's own esc.
	case key.Matches(msg, k.Back) && m.restoreSess != nil:
		m.restoreSess = nil
		return true, m, logCmd("-- restore session cancelled")

	// While a script runs, ctrl+c aborts it instead of quitting. The
	// binding is disabled the rest of the time, so key.Matches only
	// takes this branch during a run.
	case key.Matches(msg, k.CancelQuery):
		cmd := m.cancelQuery()
		return true, m, cmd

	case key.Matches(msg, k.OpenEditor):
		cmd := m.openQueryEditor()
		return true, m, cmd

	case key.Matches(msg, k.CommandLog):
		m.modal = newCommandLogModal(m.commandLogEntries())
		return true, m, nil

	case key.Matches(msg, k.Quit):
		// Torn down here rather than in a tea.Cmd: tea.Quit can stop the
		// program before a batched command ever runs, which would leave the
		// SSH connection and its forwarded sockets to the OS.
		if m.cfg.RestoreSessionEnabled() {
			m.saveSession()
		}
		_ = (&config.State{ScreenMode: screenModeNames[m.screen]}).Save()
		m.closeSession()
		return true, m, tea.Quit

	case key.Matches(msg, k.Help):
		m.modal = newHelpModal(
			"Keybindings — "+panelTitles[m.focus],
			k.helpGroups(m.focus),
		)
		return true, m, nil

	case key.Matches(msg, k.Jump):
		if n := int(msg.String()[0] - '1'); n >= 0 && n < int(panelCount) {
			m.setFocus(panelID(n))
		}
		return true, m, nil

	case key.Matches(msg, k.NextPanel):
		m.setFocus(m.cycleFocus(1))
		return true, m, nil

	case key.Matches(msg, k.PrevPanel):
		m.setFocus(m.cycleFocus(-1))
		return true, m, nil

	case key.Matches(msg, k.ScreenNext):
		m.screen = (m.screen + 1) % screenModeCount
		return true, m, nil

	case key.Matches(msg, k.ScreenPrev):
		m.screen = (m.screen + screenModeCount - 1) % screenModeCount
		return true, m, nil
	}
	return false, m, nil
}

func (m Model) updateFocused(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := m.keys
	p := m.panels[m.focus]

	// An open schema diff owns the report keys while panel [1] is
	// focused; anything it does not claim falls through to the panel.
	if m.focus == panelConnections && m.diff != nil {
		if mm, cmd, handled := m.updateDiffKeys(msg); handled {
			return mm, cmd
		}
	}

	switch {
	// Down/Up go through the wheel's coalescer rather than moving the
	// cursor here: a held key repeats faster than the app can render, and
	// routing the repeats into wheelAt collapses the backlog the same way
	// a wheel burst is collapsed. A single press still moves exactly one
	// row — the first event of a burst is applied immediately.
	case key.Matches(msg, k.Down):
		cmd := m.wheelAt(scrollTarget{zone: zoneSide, panel: m.focus}, 1)
		return m, cmd
	case key.Matches(msg, k.Up):
		cmd := m.wheelAt(scrollTarget{zone: zoneSide, panel: m.focus}, -1)
		return m, cmd

	case key.Matches(msg, k.Enter):
		// Connecting can open a password prompt, which drillIn (a plain
		// tea.Cmd) cannot do — route panel [1] through the action instead.
		if m.focus == panelConnections {
			return m.runAction(actConnect)
		}
		// Bind the command first: drillIn mutates m and Go would
		// otherwise copy the pre-call model into the return value.
		cmd := m.drillIn()
		return m, cmd

	case key.Matches(msg, k.Back):
		// esc first drops an active filter; only an unfiltered panel
		// hands the key on to the focus stack.
		if p.filter != "" {
			p.clearFilter()
			return m, nil
		}
		if n := len(m.prev); n > 0 {
			back := m.prev[n-1]
			m.prev = m.prev[:n-1]
			m.focus = back
		}
		return m, nil

	case key.Matches(msg, k.Actions):
		if len(k.panelActions(m.focus)) > 0 {
			m.modal = m.actionsMenu()
		}
		return m, nil
	}

	// Panel-specific actions, dispatched through the same table the options
	// bar and the actions menu are built from.
	for _, a := range k.panelActions(m.focus) {
		if key.Matches(msg, a.binding) {
			return m.runAction(a.id)
		}
	}
	return m, nil
}

// updateFilter is the inline `/` editor of the focused panel: every
// keystroke re-narrows the list, esc restores it, enter keeps the filter and
// hands the panel's normal keys back.
func (m Model) updateFilter(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	p := m.panels[m.focus]
	switch msg.Code {
	case tea.KeyEscape:
		p.clearFilter()
	case tea.KeyEnter:
		p.filtering = false
	case tea.KeyBackspace:
		if r := []rune(p.filter); len(r) > 0 {
			p.setFilter(string(r[:len(r)-1]))
		}
	case tea.KeyUp:
		p.move(-1)
	case tea.KeyDown:
		p.move(1)
	default:
		if msg.Text != "" {
			p.setFilter(p.filter + msg.Text)
		}
	}
	return m, nil
}

// runAction performs a context action. Both a key press and an entry in the
// `a` actions menu reach the panel behaviour through here.
func (m Model) runAction(id actionID) (Model, tea.Cmd) {
	// The main view's tab actions run first: they mean the same thing
	// on every tab. The data grid's own actions live with the grid.
	if mm, cmd, handled := m.metaActions(id); handled {
		return mm, cmd
	}
	if mm, cmd, handled := m.copyActions(id); handled {
		return mm, cmd
	}
	if mm, cmd, handled := m.dataActions(id); handled {
		return mm, cmd
	}
	switch id {
	case actConnect:
		return m.dialSelected(false)

	case actTestConnection:
		return m.dialSelected(true)

	case actNewConnection:
		m.modal = newConnectionForm("New connection", config.Connection{}, "")

	case actEditConnection:
		if c, ok := m.selectedConnection(); ok {
			m.modal = newConnectionForm("Edit connection — "+c.Name, c, c.Name)
		}

	case actDropConnection:
		if c, ok := m.selectedConnection(); ok {
			name := c.Name
			m.modal = &confirmModal{
				title: "Remove connection",
				body: fmt.Sprintf(
					"Remove %q from config.toml and delete its keyring entry?", name),
				danger: true,
				onConfirm: func(m *Model) tea.Cmd {
					if !m.cfg.Remove(name) {
						return nil
					}
					var closeCmd tea.Cmd
					if m.active == name {
						closeCmd = closeSessionCmd(m.driver, m.tunnel)
						m.driver, m.tunnel, m.active = nil, nil, ""
						m.resetBrowse()
					}
					delete(m.connState, name)
					m.refreshConnections("")
					return tea.Batch(
						closeCmd,
						forgetCmd(m.cfg.Clone(), name),
						logCmd("-- remove connection %s", name),
					)
				},
			}
		}

	case actSchemaDiff:
		cmd := m.openSchemaDiff()
		return m, cmd

	case actRefresh:
		return m, m.reloadFocused()

	case actFilter:
		// The filter is inline, not a modal: typing narrows the panel on
		// every keystroke and esc restores the full list.
		m.panels[m.focus].filtering = true

	case actToggleTab:
		m.tableTab = (m.tableTab + 1) % relationTabCount
		m.panels[panelTables].clearFilter()
		m.refreshRelations()

	case actExportDatabaseDDL:
		cmd := m.startDatabaseDDLExport()
		return m, cmd

	case actBackup:
		cmd := m.openBackupMenu()
		return m, cmd

	case actDumpDatabase:
		cmd := m.startBackup(dump.Dump)
		return m, cmd

	case actRestoreDump:
		cmd := m.startBackup(dump.Restore)
		return m, cmd

	case actCancelBackup:
		cmd := m.cancelBackup()
		return m, cmd

	case actHistory:
		m.modal = newHistoryModal(m.history, m.snippets, m.sqlDialect())

	case actSaveSnippet:
		cmd := m.promptSaveSnippet(m.script())
		return m, cmd

	case actEditQuery:
		m.setEditing(true)

	case actRunEditor:
		cmd := m.submitQuery(m.script())
		return m, cmd

	case actExplainQuery:
		cmd := m.explainQuery()
		return m, cmd

	case actClearQuery:
		cmd := m.clearQuery()
		return m, cmd
	}
	return m, nil
}

// dialSelected connects to (or, when test is set, only probes) the profile
// under the cursor. Every dial runs in a tea.Cmd; when the profile asks for
// its password on connect a prompt modal is opened first and the dial is
// deferred until the prompt is submitted.
func (m Model) dialSelected(test bool) (Model, tea.Cmd) {
	c, ok := m.selectedConnection()
	if !ok {
		return m, nil
	}
	req := dialRequest{conn: c, test: test}
	if c.NeedsPassword() && c.AskPassword {
		m.modal = newPasswordPrompt(c, func(pw string) tea.Cmd {
			next := req
			next.password, next.hasPassword = pw, true
			return redialCmd(next)
		})
		return m, nil
	}
	if !test {
		m.setConnStatus(c.Name, statusPending, "")
	}
	return m, redialCmd(req)
}

// closeSession tears down the active driver and its tunnel synchronously.
// Used on quit, where a tea.Cmd is not guaranteed to run.
func (m *Model) closeSession() {
	if m.driver != nil {
		m.driver.Close()
		m.driver = nil
	}
	if m.tunnel != nil {
		m.tunnel.Close()
		m.tunnel = nil
	}
	m.active = ""
}

// saveSession writes the current connection, database, table, tab and grid
// cursor as the session to restore on the next startup. Nothing is written
// when there is no live connection — a quit from the bare connections panel
// must not clobber a real session with an empty one. Best-effort: a write
// failure has nowhere to report to on the way out, so it is dropped.
func (m Model) saveSession() {
	if m.active == "" {
		return
	}
	_ = session.Save(session.Session{
		Connection: m.active,
		Database:   m.database,
		Table:      m.table,
		Tab:        int(m.tab),
		Row:        m.data.row,
		Col:        m.data.col,
	})
}

// drillIn is `enter`: move one step deeper in the connection → database →
// table chain, and record the resulting statement in the history panel.
func (m *Model) drillIn() tea.Cmd {
	sel := m.panels[m.focus].selected()
	if sel == "" {
		return nil
	}
	switch m.focus {
	case panelDatabases:
		return tea.Batch(logCmd("USE %s;", sel), m.openDatabase(sel), focusCmd(panelTables))
	case panelTables:
		// Opening a relation loads its first page and hands focus to the
		// grid; `esc` there comes straight back here.
		return tea.Batch(m.openTable(sel), focusCmd(panelMain))
	}
	return nil
}

// cycleFocus is the `tab` order: the four numbered panels, and the main
// view too whenever a relation is open in it. Without an open relation
// the grid has nothing to show, so tab skips it.
func (m Model) cycleFocus(delta int) panelID {
	n := int(panelCount)
	if m.data.open() {
		n++
	}
	cur := int(m.focus)
	if cur >= n {
		cur = int(panelTables)
	}
	return panelID(((cur+delta)%n + n) % n)
}

func (m *Model) setFocus(id panelID) {
	if id == m.focus {
		return
	}
	m.prev = append(m.prev, m.focus)
	if len(m.prev) > 16 {
		m.prev = m.prev[len(m.prev)-16:]
	}
	m.focus = id
	// A half-typed dd/yy/gg does not survive leaving the editor: coming
	// back and pressing `d` must not complete a chord started before the
	// detour.
	m.editor.pending = 0
}

// actionsMenu is the `a` popup: one scrollable entry per binding of the
// focused panel, driven by the same key.Binding slices as the options bar.
func (m Model) actionsMenu() modal {
	var entries []menuEntry
	for _, a := range m.keys.panelActions(m.focus) {
		if !a.binding.Enabled() {
			continue
		}
		id := a.id
		entries = append(entries, menuEntry{
			key:   a.binding.Help().Key,
			label: a.binding.Help().Desc,
			action: func(m *Model) tea.Cmd {
				next, cmd := m.runAction(id)
				*m = next
				return cmd
			},
		})
	}
	entries = append(entries, menuEntry{key: "esc", label: "cancel"})
	return &menuModal{title: "Actions — " + panelTitles[m.focus], entries: entries}
}
