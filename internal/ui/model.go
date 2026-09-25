package ui

import (
	"errors"
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

// render expands tabs: truncate measures a tab as one cell, the terminal
// draws up to eight, and the overshoot wraps inside the log's box.
func (l logLine) render() string {
	return l.at.Format("15:04:05") + "  " + strings.ReplaceAll(l.text, "\t", "    ")
}

// commandLogEntries merges the UI's own notes with the connected
// Driver's Logger — the single choke point every Exec/Query runs
// through — into one chronological feed for the slim panel and its `@`
// expanded view. The Logger, not this slice, is what guarantees a
// statement from browsing, editing or the query editor shows up exactly
// once: nothing here re-formats SQL by hand.
//
// Catalog introspection the Driver ran on its own behalf is left out
// unless showIntrospection is on — except when it failed: an error the
// user cannot see is worse than noise.
func (m Model) commandLogEntries() []logLine {
	out := append([]logLine(nil), m.commandLog...)
	if m.driver != nil {
		for _, e := range m.driver.Logger().Entries() {
			if e.Introspection && !m.showIntrospection && (e.Err == nil || cancelled(e.Err)) {
				continue
			}
			// A superseded page or count query is not a failed one: it is
			// logged, but neither spelled nor coloured as a failure.
			out = append(out, logLine{text: sqlEntryText(e), at: e.At, err: e.Err != nil && !cancelled(e.Err)})
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
	switch {
	case cancelled(e.Err):
		// A newer request for the same view stopped this one. Saying so
		// is the point: the log is where "pressing s again re-issued the
		// page query" is visible at all.
		text += "  -- cancelled (superseded)"
	case e.Err != nil:
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

// historyEntryMsg records a statement in the query history: the pane
// behind `H` and the on-disk history under it. Only statements the user
// submitted may emit one — an editor run or a re-run from the history
// pane. Generated SQL (browsing pages, introspection, committed
// changesets) stays out: it would bury the handful of typed queries the
// history exists for, and it is all in the command log anyway.
type historyEntryMsg struct{ statement string }

// ---------- root model ----------

// Model is the single root model: it owns terminal size, focus, one child
// model per side panel, main view state, the open modal (nil = none) and the
// command log. The larger feature states hang off it as sub-models — grid,
// query, exports — that are plain structs the root routes to, not
// tea.Models; see wiki/design/tui-shell-architecture.md.
type Model struct {
	width, height int

	focus  panelID
	panels [panelCount]*sidePanel
	prev   []panelID // focus stack for `esc`

	modal  modal
	screen screenMode
	// logCollapsed hides the command log strip under the main view,
	// handing its rows to the main view box instead. `@`/`L` still opens
	// the full log modal while it is collapsed. Persisted alongside
	// screen the same way — see wiki/design/collapsible-command-log.md.
	logCollapsed bool

	commandLog []logLine
	// showIntrospection reveals the Driver's own catalog queries in the
	// command log; they are hidden by default. See commandLogEntries.
	showIntrospection bool

	// Connection manager state. cfg is the on-disk connection list; connState
	// is the transient per-connection status the panel colors itself by.
	cfg       *config.Config
	connState map[string]connState
	// ephem is the file opened for this run only — `lazysql <file>` or `o`
	// in panel [1] — and nil for the ordinary saved-connections view. It
	// lives here rather than in cfg because it is never persisted: see
	// ephemeral.go.
	ephem  *ephemeralConn
	driver db.Driver
	active string // name of the connected profile, "" when none
	// tunnel is the SSH tunnel the active driver runs through, nil for a
	// direct connection. Its lifetime is the connection's: it is closed
	// whenever driver is, including on quit.
	tunnel *sshtunnel.Tunnel

	// Browsing state: the namespace the main view belongs to, its relation
	// listing (both kinds, one round trip) and the relation on screen.
	// relations is a mirror of the [2] tree's cache for that namespace —
	// syncRelations is the one place it is filled.
	database  string
	relations []db.Relation
	table     string // the relation the main view is showing

	// tree is the [2] Objects panel's model: databases, their object
	// categories and the objects themselves, with the listings each was
	// lazily loaded from. It is a pointer so every copied Model shares one
	// tree, the way changes shares one changeset.
	tree *objectTree

	// trigger is the read-only trigger definition on the main view, nil
	// when none is open. triggerReq drops a reply for a trigger that has
	// already been left. See triggerview.go.
	trigger    *triggerView
	triggerReq int

	// tab is the main view's selected tab and meta is the metadata the
	// three introspection tabs render.
	tab  mainTab
	meta metaView

	// grid is the main view's Data tab — the page on screen, its
	// queries, filter line, staged changeset and foreign-key navigation.
	// See grid.go.
	grid gridModel

	// exports are the file transfers in flight — table export, dump or
	// restore, whole-database DDL export — one of each at most. See
	// exportsmodel.go.
	exports exportsModel

	// diff is the schema diff on screen (running or finished), nil when
	// none. It dials its own connections, so it survives disconnects.
	diff *diffView

	// activity is the server activity report on screen (processes, lock
	// waits), nil when none. Like the diff it takes over the main view
	// while panel [1] keeps the focus. See activity.go.
	activity *activityView

	// plan is the EXPLAIN result on screen, nil when none. It replaces
	// the editor in the main view while panel [3] keeps the focus, and
	// `esc` dismisses it with the buffer untouched.
	plan *planView

	// query is panel [3] — the editor and its mode, the running script,
	// history, snippets, parameter memory, completion and the schema and
	// highlight caches behind it. See querymodel.go.
	query queryModel

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
		grid:      newGridModel(cfg.PageSizeOrDefault()),
		query:     newQueryModel(),
		cfg:       cfg,
	}
	m.spin = spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(m.style.pending))
	if cfgErr != nil {
		m.startupErr = cfgErr.Error()
	}
	m.colorWarnings = validateConnectionColors(cfg.Connections)
	for id := panelID(0); id < panelCount; id++ {
		m.panels[id] = &sidePanel{id: id, filterIn: newPanelFilterInput()}
	}
	m.tree = newObjectTree(nil)

	if st, err := config.LoadState(); err == nil && st != nil {
		m.screen = screenModeFromName(st.ScreenMode)
		m.logCollapsed = st.LogCollapsed
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
	cmds := []tea.Cmd{loadHistoryCmd(), loadFiltersCmd(), loadSnippetsCmd()}
	if m.startupErr != "" {
		cmds = append(cmds, logCmd("-- config error: %s", m.startupErr))
	}
	for _, w := range m.colorWarnings {
		cmds = append(cmds, logCmd("-- warning: %s", w))
	}
	if m.restoreSess != nil {
		cmds = append(cmds, logCmd("-- restoring session: %s …", m.restoreSess.Connection), restoreStartCmd())
	}
	// A file named on the command line is dialled from here, not from
	// OpenFileOnStart: the connect reports through the normal UI, so it
	// has to start once the program is running.
	if m.ephem != nil {
		cmds = append(cmds,
			logCmd("-- open %s (%s, ephemeral)", m.ephem.path, m.ephem.engineLabel()),
			redialCmd(m.dialRequestFor(m.ephem.conn, false)))
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
	c, ok := m.findConn(name)
	return ok && c.ReadOnly
}

// refreshConnections rebuilds the [1] Connections panel from the ephemeral
// file (when one is open) plus the config, and the live status map, keeping
// (or moving to) the named selection.
func (m *Model) refreshConnections(selectName string) {
	names := m.connNames()
	status := make([]itemStatus, len(names))
	marks := map[string]string{}
	notes := map[string]string{}
	tags := map[string]color.Color{}
	live := make(map[string]bool, len(names))
	for i, n := range names {
		live[n] = true
		status[i] = m.connState[n].status
		if m.connReadOnly(n) {
			marks[n] = lockMark + " "
		}
		if m.isEphemeral(n) {
			notes[n] = ephemeralTag
		}
		if c, ok := m.findConn(n); ok {
			if tc, ok := connTagColor(c); ok {
				tags[n] = tc
			}
		}
	}
	// A status belongs to a row: one whose connection is gone — removed,
	// renamed, or an ephemeral file that was closed — is dropped here
	// rather than lingering to color a later namesake.
	for n := range m.connState {
		if !live[n] {
			delete(m.connState, n)
		}
	}
	p := m.panels[panelConnections]
	prev := p.selected()
	p.decor = marks
	p.suffix = notes
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
	m.grid.changes.Clear()
	// An export reads through the driver that is about to be closed, and
	// so does a whole-database DDL export. A dump of a file engine runs
	// its SQL through the same driver, and a tunnelled dump runs through
	// the tunnel that is about to close.
	m.exports.cancelAll()
	// So does a running script.
	if m.query.run.running && m.query.run.cancel != nil {
		m.query.run.cancel()
	}
	// And so do the page and count queries of the grid: the driver they
	// were issued on is about to close.
	m.grid.stopPageQueries()
	// A plan describes a statement against the connection being left.
	m.plan = nil
	// So do the sessions of the server it was read from — and closing the
	// view is what stops its auto-refresh.
	m.closeActivity()
	// So does a trigger definition.
	m.trigger = nil
	m.database = ""
	m.table = ""
	m.grid.data = dataView{}
	m.closeFilterInput()
	m.tab = mainTabData
	m.resetMeta()
	m.relations = nil
	// The foreign-key caches and the jump history describe relations of
	// the connection being left behind.
	m.grid.fkCache = map[fkKey][]db.ForeignKey{}
	m.grid.refsCache = map[fkKey][]namespaceFK{}
	m.grid.fkLoading = map[fkKey]bool{}
	m.grid.clearBrowse()
	if m.focus == panelMain {
		m.focus = panelObjects
	}
	// rebuildTree drops the tree, its cached listings and the panel's
	// filter; only the in-flight marker is not its business.
	m.panels[panelObjects].loading = false
	m.rebuildTree(nil)
}

// openDatabase makes name the browsed namespace and expands its Tables
// category, which is what starts the listing. The tree keeps its old rows
// until the reply lands.
func (m *Model) openDatabase(name string) tea.Cmd {
	m.database = databaseArg(name)
	// The open page belongs to the namespace we are leaving, and so does
	// every state the jump history could go back to.
	m.table = ""
	m.grid.data = dataView{}
	m.closeFilterInput()
	m.trigger = nil
	m.grid.clearBrowse()
	m.resetMeta()
	m.syncRelations()
	if m.focus == panelMain {
		m.focus = panelObjects
	}
	var cmds []tea.Cmd
	if !m.tree.single {
		for _, root := range m.tree.roots {
			if root.database == m.database {
				cmds = append(cmds, m.expandNode(root))
			}
		}
	}
	if c := m.tree.category(m.database, catTables); c != nil {
		cmds = append(cmds, m.expandNode(c))
	}
	m.refreshTree()
	return tea.Batch(cmds...)
}

// reloadFocused re-runs the server query behind the focused panel.
func (m *Model) reloadFocused() tea.Cmd {
	if m.driver == nil {
		return logCmd("-- reload %s skipped: not connected", panelTitles[m.focus])
	}
	switch m.focus {
	case panelObjects:
		return m.reloadNode()
	case panelMain:
		if m.tab.metadata() {
			return m.reloadMeta()
		}
		if m.grid.data.isQuery() {
			return m.rerunQuery()
		}
		// `R` means "read it again from the server", so the cached
		// constraints behind the `⇒` marks go too.
		delete(m.grid.fkCache, m.tableFKKey())
		return tea.Batch(m.reloadPage(), m.ensureFKs())
	}
	return logCmd("-- refresh %s", panelTitles[m.focus])
}

// engineName is the live connection's engine as the UI spells it, and a
// placeholder when nothing is connected — a load reply can outlive the
// driver it was started on.
func (m Model) engineName() string {
	if m.driver == nil {
		return "this engine"
	}
	return m.driver.Dialect().DisplayName()
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
	return m.findConn(name)
}

// Update routes in a fixed order: WindowSizeMsg → open modal (swallows all
// keys) → an open `/` filter (a panel's pattern or the grid's WHERE
// line) → the query editor in insert mode → global keys → focused panel.
// A bracketed paste takes the same order through
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
		m.query.history = msg.entries
		return m, nil

	case historyWrittenMsg:
		if msg.err != nil {
			return m, logCmd("-- write query history FAILED: %v", msg.err)
		}
		return m, nil

	case filtersLoadedMsg:
		if msg.err != nil {
			// A broken filter file costs `/` its recall list and nothing
			// else; the session keeps recording into it.
			return m, logCmd("-- read filter history FAILED: %v", msg.err)
		}
		m.grid.filters = msg.entries
		return m, nil

	case filtersWrittenMsg:
		if msg.err != nil {
			return m, logCmd("-- write filter history FAILED: %v", msg.err)
		}
		return m, nil

	case snippetsLoadedMsg:
		if msg.err != nil {
			// A broken snippet file costs the pane its Snippets section
			// and nothing else; a save rewrites the file from scratch.
			return m, logCmd("-- read query snippets FAILED: %v", msg.err)
		}
		m.query.snippets = msg.list
		return m, nil

	case snippetsWrittenMsg:
		if msg.err != nil {
			return m, logCmd("-- write query snippets FAILED: %v", msg.err)
		}
		return m, nil

	case queryStmtMsg:
		if msg.id != m.query.run.id || !m.query.run.running {
			return m, nil
		}
		// Bind the command first: applyQueryStmt mutates m, and Go may
		// otherwise copy the pre-call model into the return value.
		cmd := m.applyQueryStmt(msg)
		return m, tea.Batch(cmd, waitQueryCmd(m.query.run.ch))

	case queryDoneMsg:
		if msg.id != m.query.run.id {
			return m, nil
		}
		cmd := m.finishQuery(msg)
		return m, cmd

	case spinner.TickMsg:
		// A tick that outlives its run is dropped rather than chained:
		// that is what stops the spinner without a separate "stop" message.
		if !m.query.run.running {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case activityLoadedMsg:
		// Bind the command first: applyActivity mutates the view on m,
		// and Go may otherwise copy the pre-call model into the return.
		cmd := m.applyActivity(msg)
		return m, cmd

	case activityTickMsg:
		cmd := m.activityTick(msg)
		return m, cmd

	case activityKilledMsg:
		cmd := m.finishKill(msg)
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
		// A test fired from the connection form reports into that form —
		// the profile may be unsaved, so there is no panel row to color.
		// A reply landing after the form closed is dropped.
		if msg.req.form != nil {
			if m.modal != msg.req.form {
				return m, nil
			}
			f := msg.req.form
			if msg.err != nil {
				f.info = ""
				f.err = msg.err.Error()
				return m, logCmd("-- test %s FAILED: %v", msg.name, msg.err)
			}
			f.info = fmt.Sprintf("✓ ok in %s", msg.took.Round(time.Millisecond))
			return m, logCmd("-- test %s ok in %s (%s)", msg.name, msg.took.Round(time.Millisecond), msg.dsn)
		}
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
		dbs := namespaceList(msg.driver.Engine(), m.scopeDatabases(msg.name, msg.databases))
		m.rebuildTree(dbs)
		cmds := []tea.Cmd{
			closeSessionCmd(prevDriver, prevTunnel),
			logCmd("-- connect %s (%s)", msg.name, msg.dsn),
		}
		if restoring {
			if display, ok := findDatabaseDisplayName(dbs, m.restoreSess.Database); ok {
				cmds = append(cmds, m.openDatabase(display), focusCmd(panelObjects))
				return m, tea.Batch(cmds...)
			}
			sess := *m.restoreSess
			m.restoreSess = nil
			cmds = append(cmds, logCmd("-- restore session: database %q not found", displayDatabase(sess.Database)))
		}
		// Single-namespace engines (SQLite, DuckDB) have nothing to pick:
		// the tree skips the database level entirely, so its Tables
		// category is opened straight away.
		if len(dbs) == 1 {
			cmds = append(cmds, m.openDatabase(dbs[0]))
		}
		cmds = append(cmds, focusCmd(panelObjects))
		return m, tea.Batch(cmds...)

	case databasesLoadedMsg:
		// A reply for a connection that is no longer live is stale.
		if msg.conn != m.active {
			return m, nil
		}
		p := m.panels[panelObjects]
		p.loading = false
		if msg.err != nil {
			// The tree keeps its previous content; only the log knows.
			return m, logCmd("-- list databases FAILED: %v", msg.err)
		}
		dbs := namespaceList(m.driver.Engine(), m.scopeDatabases(m.active, msg.databases))
		// A re-listing drops every cached object list with it: that is
		// what makes `R` on the database level a real refresh.
		m.rebuildTree(dbs)
		m.syncRelations()
		if len(dbs) == 1 {
			return m, m.openDatabase(dbs[0])
		}
		return m, nil

	case relationsLoadedMsg:
		if msg.conn != m.active {
			return m, nil
		}
		if msg.err != nil {
			m.categoryFailed(msg.database, catTables, msg.err)
			m.refreshTree()
			if m.restoreSess != nil {
				sess := *m.restoreSess
				m.restoreSess = nil
				return m, logCmd("-- restore session: list tables of %s FAILED: %v", displayDatabase(sess.Database), msg.err)
			}
			return m, logCmd("-- list tables of %s FAILED: %v", displayDatabase(msg.database), msg.err)
		}
		m.applyRelations(msg.database, msg.relations)
		m.refreshTree()
		if m.restoreSess != nil && msg.database == m.database {
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

	case tableStatsLoadedMsg:
		// Sizes are decoration: a stale connection or a failed statistics
		// query leaves the tree exactly as it was — unannotated, never
		// broken. The failed statement is already in the command log.
		if msg.conn != m.active || msg.err != nil {
			return m, nil
		}
		m.applyTableStats(msg.database, msg.stats)
		m.refreshTree()
		return m, nil

	case triggersLoadedMsg:
		if msg.conn != m.active {
			return m, nil
		}
		if msg.err != nil {
			m.categoryFailed(msg.database, catTriggers, msg.err)
			m.refreshTree()
			if errors.Is(msg.err, db.ErrUnsupported) {
				return m, logCmd("-- %s has no triggers", m.engineName())
			}
			return m, logCmd("-- list triggers of %s FAILED: %v",
				displayDatabase(msg.database), msg.err)
		}
		m.applyTriggers(msg.database, msg.triggers)
		m.refreshTree()
		return m, nil

	case triggerDDLMsg:
		// Bind the command first: applyTriggerDDL replaces the view on m,
		// and Go may otherwise copy the pre-call model into the return.
		cmd := m.applyTriggerDDL(msg)
		return m, cmd

	case pageLoadedMsg:
		if !m.fresh(msg.req, msg.conn, msg.table) {
			return m, nil
		}
		m.grid.pageQueryDone(msg.req, true)
		m.grid.data.loading = false
		if cancelled(msg.err) {
			// The query was stopped on purpose — by the view closing,
			// since a newer request would have bumped req and this reply
			// would not be fresh. The page on screen stays as it is, and
			// the loading marker goes: a cancellation must never leave
			// the grid waiting forever for a reply that will not come.
			return m, nil
		}
		if msg.err != nil {
			// The previous page stays on screen; the grid and the log
			// both name the failure.
			m.grid.data.err = msg.err.Error()
			if m.restoreSess != nil && m.restoreSess.Table == msg.table {
				sess := *m.restoreSess
				m.restoreSess = nil
				return m, logCmd("-- restore session: select from %s FAILED: %v", sess.Table, msg.err)
			}
			return m, logCmd("-- select from %s FAILED: %v", msg.table, msg.err)
		}
		m.grid.data.cols = msg.result.Columns
		m.grid.data.rows = msg.result.Rows
		if m.restoreSess != nil && m.restoreSess.Table == msg.table {
			sess := *m.restoreSess
			m.restoreSess = nil
			m.grid.data.row, m.grid.data.col = sess.Row, sess.Col
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
		m.grid.cacheFKs(fkKey{conn: msg.conn, database: msg.database, table: msg.table}, msg.fks)
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
		delete(m.grid.fkLoading, msg.key)
		if msg.err != nil {
			// Without the metadata the grid loses the `⇒` marks and the
			// follow key; browsing itself is unaffected.
			return m, logCmd("-- foreign keys of %s FAILED: %v", msg.key.table, msg.err)
		}
		m.grid.cacheFKs(msg.key, msg.fks)
		if id := m.grid.fkAfter; id != actNone && msg.key == m.tableFKKey() {
			m.grid.fkAfter = actNone
			mm, cmd := m.runAction(id)
			return mm, cmd
		}
		return m, nil

	case namespaceFKsLoadedMsg:
		if msg.key.conn != m.active {
			return m, nil
		}
		delete(m.grid.fkLoading, msg.key)
		if msg.err != nil {
			m.grid.fkAfter = actNone
			return m, logCmd("-- scan foreign keys of %s FAILED: %v",
				displayDatabase(msg.key.database), msg.err)
		}
		if m.grid.refsCache == nil {
			m.grid.refsCache = map[fkKey][]namespaceFK{}
		}
		m.grid.refsCache[msg.key] = msg.refs
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
			m.grid.cacheFKs(k, fks)
		}
		if id := m.grid.fkAfter; id != actNone && msg.key == m.namespaceFKKey() {
			m.grid.fkAfter = actNone
			mm, cmd := m.runAction(id)
			return mm, cmd
		}
		return m, nil

	case rowCountMsg:
		if !m.fresh(msg.req, msg.conn, msg.table) {
			return m, nil
		}
		m.grid.pageQueryDone(msg.req, false)
		if cancelled(msg.err) {
			// Cancelled with its page query; the total the grid has
			// stays whatever it was.
			return m, nil
		}
		if msg.err != nil {
			// A missing count only costs the "of ~N" part of the status
			// line, so it never blocks browsing.
			m.grid.data.hasTotal = false
			return m, logCmd("-- count %s FAILED: %v", msg.table, msg.err)
		}
		m.grid.data.total, m.grid.data.hasTotal = msg.total, true
		return m, nil

	case changesCommittedMsg:
		if msg.err != nil {
			// The transaction rolled back: nothing was applied, and the
			// changeset survives so the user can fix and retry. An engine
			// that commits DDL on its own (MySQL, MariaDB) may have applied
			// some of it anyway, so the schema is re-read all the same.
			if len(msg.schema) > 0 && m.driver != nil && !db.TransactionalDDL(m.driver.Engine()) {
				return m, tea.Batch(
					logCmd("-- COMMIT FAILED — changeset kept; %s may have applied the DDL before the failure: %v",
						m.driver.Dialect().DisplayName(), msg.err),
					m.afterSchemaCommit(msg.schema))
			}
			return m, logCmd("-- COMMIT FAILED — nothing applied, changeset kept: %v", msg.err)
		}
		// The transaction itself — BEGIN, each statement, COMMIT — is
		// already in the command log: ExecTx logged it through the
		// Driver's Logger as it ran. It is not recorded in the query
		// history: those statements are generated from the changeset,
		// not typed, and re-running an old UPDATE/DELETE from the
		// history pane is a footgun.
		var cmds []tea.Cmd
		m.grid.changes.Clear()
		// The phantom rows of the staged inserts are gone with it, and
		// the fresh page is still a round trip away: the cursor cannot be
		// left standing on one of them in the meantime.
		m.clampCursor()
		cmds = append(cmds, logCmd("-- commit ok: %s applied", countChanges(len(msg.stmts))))
		if len(msg.schema) > 0 {
			// The schema moved: the tree, the metadata and the page are
			// re-read, and a relation the commit dropped is closed.
			cmds = append(cmds, m.afterSchemaCommit(msg.schema))
		} else {
			cmds = append(cmds, m.reloadPage())
		}
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
		if msg.id != m.exports.file.id || !m.exports.file.running {
			return m, nil
		}
		return m, tea.Batch(
			logCmd("-- export %s: %d rows…", m.exports.file.table, msg.rows),
			waitExportCmd(m.exports.file.ch),
		)

	case exportDoneMsg:
		if msg.id != m.exports.file.id {
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
		if msg.id != m.exports.backup.id || !m.exports.backup.running {
			return m, nil
		}
		return m, tea.Batch(
			logCmd("-- %s: %s", m.exports.backup.action, msg.line),
			waitBackupCmd(m.exports.backup.ch),
		)

	case backupDoneMsg:
		if msg.id != m.exports.backup.id {
			return m, nil
		}
		// Bind the command first: finishBackup clears the in-flight state
		// on m, and Go may otherwise copy the pre-call model into the
		// return value.
		cmd := m.finishBackup(msg)
		return m, cmd

	case importSetupMsg:
		// Bind the command first: openImportSettings sets the modal on m.
		cmd := m.openImportSettings(msg)
		return m, cmd

	case importProgressMsg:
		if msg.id != m.exports.csv.id || !m.exports.csv.running {
			return m, nil
		}
		return m, tea.Batch(
			logCmd("-- import into %s: %d rows%s…", m.exports.csv.table, msg.rows, percentSuffix(msg.done, msg.size)),
			waitImportCmd(m.exports.csv.ch),
		)

	case importDoneMsg:
		if msg.id != m.exports.csv.id {
			return m, nil
		}
		cmd := m.finishImport(msg)
		return m, cmd

	case databaseDDLExportedMsg:
		if msg.id != m.exports.ddl.id {
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
		// 2b. The data grid's own `/` — the inline WHERE line — captures
		// them for the same reason: a clause is text, not commands.
		if m.filterInputOpen() {
			return m.updateFilterInput(msg)
		}
		// 3. The query editor in insert mode captures every key it does
		// not reserve — ahead of the global keys, or `q` would quit in
		// the middle of a statement.
		if m.focus == panelQuery && m.query.editor.editing {
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
		m.modal = newCommandLogModal(m)
		return true, m, nil

	case key.Matches(msg, k.LogIntrospection):
		m.showIntrospection = !m.showIntrospection
		return true, m, nil

	case key.Matches(msg, k.ToggleCommandLog):
		m.logCollapsed = !m.logCollapsed
		return true, m, nil

	// With rows marked in the data grid, ctrl+c copies the selection
	// instead of quitting. CopySelection is enabled only while a
	// selection is up, so key.Matches only takes this branch then — and
	// it is matched here, ahead of Quit, because the global keys run
	// before the focused view ever sees the key. A running query still
	// wins above: cancelling it is the more urgent reading of ctrl+c,
	// and the copy is one esc-free key press away afterwards.
	case m.focus == panelMain && key.Matches(msg, k.CopySelection):
		cmd := m.copySelectionMenu()
		return true, m, cmd

	case key.Matches(msg, k.Quit):
		if n := m.grid.changes.Len(); n > 0 {
			m.modal = &confirmModal{
				title:  "Quit",
				body:   fmt.Sprintf("Quit and discard %s? They are not saved on exit.", countChanges(n)),
				danger: true,
				onConfirm: func(mm *Model) tea.Cmd {
					mm.quit()
					return tea.Quit
				},
			}
			return true, m, nil
		}
		m.quit()
		return true, m, tea.Quit

	case key.Matches(msg, k.Help):
		title, groups := "Keybindings — "+panelTitles[m.focus], k.helpGroups(m.focus)
		// The report is focused in the main view rather than in a panel, so
		// `?` lists what it binds instead of the grid's actions — the same
		// split the options bar makes.
		if m.activityFocused() {
			title, groups = "Keybindings — Server activity", m.activityHelpGroups()
		}
		m.modal = newHelpModal(title, groups)
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

	// An open schema diff owns its keys while panel [1] is focused;
	// anything it does not claim falls through to the panel. The activity
	// report used to be routed here the same way — it is not any more: it
	// is focused in the main view (updateData dispatches it), so panel [1]
	// keeps every one of its own keys while the list is on screen. See
	// wiki/design/server-activity-focus.md; the diff is still an overlay
	// because it has no cursor of its own to hand the main view.
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
		return m.activateSelection()

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

// activateSelection acts on the focused panel's current selection the way
// `enter` does: panel [1] connects (which can open a password prompt, so it
// routes through the action rather than drillIn's plain tea.Cmd), panel [2]
// drills into the tree.
func (m Model) activateSelection() (Model, tea.Cmd) {
	if m.focus == panelConnections {
		return m.runAction(actConnect)
	}
	// Bind the command first: drillIn mutates m and Go would otherwise
	// copy the pre-call model into the return value.
	cmd := m.drillIn()
	return m, cmd
}

// updateFilter is the inline `/` editor of the focused panel: every edit
// re-narrows the list, esc restores it. Cursor movement, editing at the
// cursor and word/line delete are textinput's own keymap; this only
// intercepts the keys that mean something else here — esc/enter close the
// line rather than doing nothing, and up/down move the list cursor
// (textinput has no rows to walk) rather than a suggestion list it never
// shows. Enter's behavior also depends on whether the user has already
// navigated the filtered list (arrow keys or the mouse wheel, tracked by
// navigated): if so, the selection is unambiguous and enter confirms the
// filter *and* activates the selection in one step; otherwise it only
// confirms the filter, so a first enter after typing a pattern that
// already narrows to one obvious row does not surprise the user by
// jumping straight in. See wiki/design/panel-filter-enter.md.
func (m Model) updateFilter(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	p := m.panels[m.focus]
	switch msg.Code {
	case tea.KeyEscape:
		p.clearFilter()
		return m, nil
	case tea.KeyEnter:
		p.filtering = false
		if p.navigated {
			p.navigated = false
			return m.activateSelection()
		}
		return m, nil
	case tea.KeyUp:
		p.navigated = true
		p.move(-1)
		return m, nil
	case tea.KeyDown:
		p.navigated = true
		p.move(1)
		return m, nil
	// pgup/pgdown page the filtered rows the same as unfiltered browsing;
	// ctrl+f/ctrl+b are left alone here — textinput already binds them to
	// move the pattern's own cursor, and typing must not be disturbed.
	case tea.KeyPgDown:
		p.navigated = true
		p.move(m.sidePanelPageSize())
		return m, nil
	case tea.KeyPgUp:
		p.navigated = true
		p.move(-m.sidePanelPageSize())
		return m, nil
	}
	before := p.filterIn.Value()
	var cmd tea.Cmd
	p.filterIn, cmd = p.filterIn.Update(msg)
	if v := p.filterIn.Value(); v != before {
		p.setFilter(v)
	}
	return m, cmd
}

// runAction performs a context action. Both a key press and an entry in the
// `a` actions menu reach the panel behaviour through here.
func (m Model) runAction(id actionID) (Model, tea.Cmd) {
	// The server activity report owns the main view while it is focused
	// there, so the grid actions it shares with the Data tab act on its
	// own read-only grid rather than on the page behind it.
	if m.activityFocused() {
		if mm, cmd, handled := m.activityDispatch(id); handled {
			return mm, cmd
		}
	}
	// The main view's tab actions run first: they mean the same thing
	// on every tab. The data grid's own actions live with the grid.
	if mm, cmd, handled := m.metaActions(id); handled {
		return mm, cmd
	}
	if mm, cmd, handled := m.copyActions(id); handled {
		return mm, cmd
	}
	switch id {
	case actSchemaMenu:
		cmd := m.openObjectSchemaMenu()
		return m, cmd
	case actTableSchemaMenu:
		cmd := m.openTableSchemaMenu()
		return m, cmd
	}
	if mm, cmd, handled := m.dataActions(id); handled {
		return mm, cmd
	}
	switch id {
	case actConnect:
		return m.dialSelected(false)

	case actDisconnect:
		c, ok := m.selectedConnection()
		if !ok {
			return m, nil
		}
		if c.Name != m.active {
			return m, logCmd("-- disconnect: %s is not the active connection", c.Name)
		}
		driver, tunnel := m.driver, m.tunnel
		name := m.active
		m.driver, m.tunnel, m.active = nil, nil, ""
		m.resetBrowse()
		m.setConnStatus(name, statusIdle, "")
		if m.isEphemeral(name) {
			// An ephemeral connection has nothing to go back to being:
			// disconnecting it drops the row and leaves the panel showing
			// the saved profiles alone.
			m.dropEphemeral()
		}
		return m, tea.Batch(closeSessionCmd(driver, tunnel), logCmd("-- disconnect %s", name))

	case actTestConnection:
		return m.dialSelected(true)

	case actNewConnection:
		// Step one of the create flow: pick the engine, get that engine's
		// form — see newConnectionWizard.
		m.modal = newConnectionWizard()

	case actOpenFile:
		m.modal = newOpenFileModal()

	case actEditConnection:
		if c, ok := m.selectedConnection(); ok {
			if m.isEphemeral(c.Name) {
				return m, logCmd("-- %s is ephemeral: nothing to edit (n saves a profile)", c.Name)
			}
			m.modal = newConnectionForm("Edit connection — "+c.Name, c, c.Name)
		}

	case actDuplicateConnection:
		if c, ok := m.selectedConnection(); ok {
			if m.isEphemeral(c.Name) {
				return m, logCmd("-- %s is ephemeral: not in config.toml", c.Name)
			}
			source := c.Name
			c.Name = duplicateName(m.cfg, c.Name)
			m.modal = newDuplicateConnectionForm("Duplicate connection — "+source, c, source)
		}

	case actDropConnection:
		if c, ok := m.selectedConnection(); ok {
			name := c.Name
			if m.isEphemeral(name) {
				// There is nothing on disk to remove: x (disconnect) is
				// what makes an ephemeral connection go away.
				return m, logCmd("-- %s is ephemeral: press x to close it", name)
			}
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

	case actServerActivity:
		cmd := m.openActivity()
		return m, cmd

	case actKillProcess:
		cmd := m.killSelectedProcess()
		return m, cmd

	case actActivityAuto:
		cmd := m.toggleActivityAuto()
		return m, cmd

	case actMoveConnUp:
		// The ephemeral row is not part of the saved order; MoveUp/MoveDown
		// simply do not find it in the config and report false.
		if c, ok := m.selectedConnection(); ok && !m.isEphemeral(c.Name) && m.cfg.MoveUp(c.Name) {
			m.refreshConnections(c.Name)
			return m, reorderCmd(m.cfg.Clone(), c.Name)
		}

	case actMoveConnDown:
		if c, ok := m.selectedConnection(); ok && !m.isEphemeral(c.Name) && m.cfg.MoveDown(c.Name) {
			m.refreshConnections(c.Name)
			return m, reorderCmd(m.cfg.Clone(), c.Name)
		}

	case actRefresh:
		if n := m.grid.changes.Len(); n > 0 {
			m.modal = &confirmModal{
				title:  "Refresh",
				body:   fmt.Sprintf("Reload from the server and discard %s?", countChanges(n)),
				danger: true,
				onConfirm: func(mm *Model) tea.Cmd {
					mm.grid.changes.Clear()
					mm.clampCursor()
					return mm.reloadFocused()
				},
			}
			return m, nil
		}
		return m, m.reloadFocused()

	case actFilter:
		// The filter is inline, not a modal: typing narrows the panel on
		// every keystroke and esc restores the full list.
		m.panels[m.focus].startFilter()

	case actPageDown:
		m.panels[m.focus].move(m.sidePanelPageSize())

	case actPageUp:
		m.panels[m.focus].move(-m.sidePanelPageSize())

	case actExpandNode:
		cmd := m.expandSelected()
		return m, cmd

	case actCollapseNode:
		m.collapseSelected()

	case actExportDatabaseDDL:
		cmd := m.startDatabaseDDLExport()
		return m, cmd

	case actExportDatabaseDDLFile:
		cmd := m.promptDatabaseDDLExportPath()
		return m, cmd

	case actExportDatabaseDDLClipboard:
		cmd := m.copyDatabaseDDL()
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

	case actImportCSV:
		cmd := m.startImport()
		return m, cmd

	case actCancelImport:
		cmd := m.cancelImport()
		return m, cmd

	case actHistory:
		m.modal = newHistoryModal(history.ForConnection(m.query.history, m.active), m.query.snippets, m.sqlDialect(), m.keys)

	case actSaveSnippet:
		cmd := m.promptSaveSnippet(m.script())
		return m, cmd

	case actEditQuery:
		m.setEditing(true)

	case actRunEditor:
		cmd := m.submitQuery(m.script())
		return m, cmd

	case actRunStatement:
		cmd := m.runStatementAtCursor()
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
	req := m.dialRequestFor(c, test)
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

// quit saves session/screen state and tears down the connection. Called
// synchronously (not as a tea.Cmd) because tea.Quit can stop the program
// before a batched command ever runs, which would leave the SSH connection
// and its forwarded sockets to the OS.
func (m *Model) quit() {
	if m.cfg.RestoreSessionEnabled() {
		m.saveSession()
	}
	_ = (&config.State{ScreenMode: screenModeNames[m.screen], LogCollapsed: m.logCollapsed}).Save()
	m.closeSession()
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
	// An ephemeral connection is never restored: it is not in the config,
	// so a restore could not find it, and writing it would also clobber
	// the last real session with one.
	if m.active == "" || m.isEphemeral(m.active) {
		return
	}
	_ = session.Save(session.Session{
		Connection: m.active,
		Database:   m.database,
		Table:      m.table,
		Tab:        int(m.tab),
		Row:        m.grid.data.row,
		Col:        m.grid.data.col,
	})
}

// drillIn is `enter` on the object tree: a branch expands or collapses, a
// relation opens in the main view and a trigger shows its definition
// there.
func (m *Model) drillIn() tea.Cmd {
	if m.focus != panelObjects {
		return nil
	}
	n := m.selectedNode()
	if n == nil {
		return nil
	}
	if !n.leaf() {
		return m.toggleNode(n)
	}
	if n.cat == catTriggers {
		return tea.Batch(m.openTrigger(n), focusCmd(panelMain))
	}
	// Opening a relation loads its first page and hands focus to the
	// grid; `esc` there comes straight back here.
	return tea.Batch(m.openObject(n), focusCmd(panelMain))
}

// openObject opens a table or a view from the tree, switching the browsed
// namespace first when the object lives in another one.
func (m *Model) openObject(n *treeNode) tea.Cmd {
	var cmds []tea.Cmd
	if n.database != m.database {
		m.database = n.database
		m.syncRelations()
		// The jump history and the schema cache describe the namespace
		// being left.
		m.grid.clearBrowse()
		cmds = append(cmds, logCmd("USE %s;", displayDatabase(n.database)))
	}
	m.trigger = nil
	return tea.Batch(append(cmds, m.openTable(n.name))...)
}

// cycleFocus is the `tab` order: the numbered panels, and the main view
// too whenever it has something to show. With nothing open the main view
// has no cursor to hand over, so tab skips it.
func (m Model) cycleFocus(delta int) panelID {
	n := int(panelCount)
	if m.grid.data.open() || m.trigger != nil || m.activity != nil {
		n++
	}
	cur := int(m.focus)
	if cur >= n {
		cur = int(panelObjects)
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
	m.query.editor.pending = 0
	// Nor does a half-typed WHERE clause survive leaving the grid: the
	// line only takes keys while the grid has them, so one left open
	// elsewhere would be a caret nothing types into.
	if id != panelMain {
		m.closeFilterInput()
	}
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
