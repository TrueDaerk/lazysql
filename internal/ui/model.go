package ui

import (
	"fmt"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"lazysql/internal/config"
	"lazysql/internal/db"
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

// ---------- messages ----------

// Panels emit domain messages instead of mutating shared state; the root
// model reduces them. Real DB work will arrive the same way, from tea.Cmds.

// commandLogMsg appends a line to the command log under the main view.
type commandLogMsg struct{ line string }

func logCmd(format string, a ...any) tea.Cmd {
	line := fmt.Sprintf(format, a...)
	return func() tea.Msg { return commandLogMsg{line: line} }
}

// focusPanelMsg moves focus to a panel.
type focusPanelMsg struct{ id panelID }

func focusCmd(id panelID) tea.Cmd {
	return func() tea.Msg { return focusPanelMsg{id: id} }
}

// historyEntryMsg records a statement in the query history panel.
type historyEntryMsg struct{ statement string }

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

	commandLog []string

	// Connection manager state. cfg is the on-disk connection list; connState
	// is the transient per-connection status the panel colors itself by.
	cfg       *config.Config
	connState map[string]connState
	driver    db.Driver
	active    string // name of the connected profile, "" when none

	// Browsing state: the namespace panel [3] currently shows, the
	// relations it was filled from (both kinds, one round trip) and which
	// sub-tab is selected.
	database  string
	relations []db.Relation
	tableTab  relationTab
	table     string // the relation the main view is showing

	// data is the main view's Data tab: one page of m.table.
	data dataView

	startupErr string

	keys  keyMap
	help  help.Model
	style styles
}

// New builds the shell and loads the saved connections. A broken or missing
// config never blocks startup: the error is surfaced in the command log and
// the app starts with an empty connection list.
func New() Model {
	m := Model{
		focus:     panelConnections,
		keys:      newKeyMap(),
		help:      help.New(),
		style:     newStyles(),
		connState: map[string]connState{},
	}
	for id := panelID(0); id < panelCount; id++ {
		m.panels[id] = &sidePanel{id: id}
	}
	m.panels[panelTables].tabs = relationTabNames[:]

	cfg, err := config.Load()
	if err != nil {
		cfg = &config.Config{}
		m.startupErr = err.Error()
	}
	m.cfg = cfg
	m.refreshConnections("")
	return m
}

func (m Model) Init() tea.Cmd {
	if m.startupErr != "" {
		return logCmd("-- config error: %s", m.startupErr)
	}
	return nil
}

// refreshConnections rebuilds the [1] Connections panel from the config plus
// the live status map, keeping (or moving to) the named selection.
func (m *Model) refreshConnections(selectName string) {
	names := m.cfg.Names()
	status := make([]itemStatus, len(names))
	for i, n := range names {
		status[i] = m.connState[n].status
	}
	p := m.panels[panelConnections]
	prev := p.selected()
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
	m.database = ""
	m.table = ""
	m.data = dataView{}
	m.relations = nil
	m.tableTab = tabTables
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
	// The open page belongs to the namespace we are leaving.
	m.table = ""
	m.data = dataView{}
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
		return m.reloadPage()
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

// selectedConnection returns the profile under the cursor of panel [1].
func (m Model) selectedConnection() (config.Connection, bool) {
	name := m.panels[panelConnections].selected()
	if name == "" {
		return config.Connection{}, false
	}
	return m.cfg.Find(name)
}

// Update routes in a fixed order: WindowSizeMsg → open modal (swallows all
// keys) → global keys → focused panel.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case commandLogMsg:
		m.commandLog = append(m.commandLog, msg.line)
		if n := len(m.commandLog); n > 200 {
			m.commandLog = m.commandLog[n-200:]
		}
		return m, nil

	case historyEntryMsg:
		h := m.panels[panelHistory]
		h.setItems(append(h.items, msg.statement))
		return m, nil

	case focusPanelMsg:
		m.setFocus(msg.id)
		return m, nil

	case connTestedMsg:
		if msg.err != nil {
			m.setConnStatus(msg.name, statusError, msg.err.Error())
			return m, logCmd("-- test %s FAILED: %v", msg.name, msg.err)
		}
		// A successful test does not make the profile the active connection:
		// it only clears a stale error on anything that is not connected.
		if m.active != msg.name {
			m.setConnStatus(msg.name, statusIdle, "")
		}
		return m, logCmd("-- test %s ok in %s (%s)", msg.name, msg.took.Round(time.Millisecond), msg.dsn)

	case connectedMsg:
		if msg.err != nil {
			m.setConnStatus(msg.name, statusError, msg.err.Error())
			m.modal = &confirmModal{
				title:  "Connection failed",
				body:   fmt.Sprintf("%s: %v", msg.name, msg.err),
				danger: true,
			}
			return m, logCmd("-- connect %s FAILED: %v", msg.name, msg.err)
		}
		prev := m.driver
		if m.active != "" && m.active != msg.name {
			m.setConnStatus(m.active, statusIdle, "")
		}
		m.driver = msg.driver
		m.active = msg.name
		m.setConnStatus(msg.name, statusOK, "")
		m.resetBrowse()
		m.panels[panelDatabases].setItems(namespaceList(msg.driver.Engine(), msg.databases))
		cmds := []tea.Cmd{
			closeDriverCmd(prev),
			logCmd("-- connect %s (%s)", msg.name, msg.dsn),
		}
		// Single-namespace engines (SQLite, DuckDB) have nothing to pick:
		// go straight to their tables.
		if dbs := m.panels[panelDatabases].items; len(dbs) == 1 {
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
			return m, logCmd("-- list tables of %s FAILED: %v", displayDatabase(msg.database), msg.err)
		}
		m.relations = msg.relations
		m.refreshRelations()
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
			return m, logCmd("-- select from %s FAILED: %v", msg.table, msg.err)
		}
		m.data.cols = msg.result.Columns
		m.data.rows = msg.result.Rows
		m.data.clampCursor()
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

	case connPersistedMsg:
		if msg.err != nil {
			return m, logCmd("-- %s %s FAILED: %v", msg.verb, msg.name, msg.err)
		}
		return m, nil

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
		// 3. Global keys.
		if handled, mm, cmd := m.updateGlobal(msg); handled {
			return mm, cmd
		}
		// 4. The focused view: the data grid, or a side panel.
		if m.focus == panelMain {
			return m.updateData(msg)
		}
		return m.updateFocused(msg)
	}
	return m, nil
}

func (m Model) updateGlobal(msg tea.KeyPressMsg) (bool, tea.Model, tea.Cmd) {
	k := m.keys
	switch {
	case key.Matches(msg, k.Quit):
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

	switch {
	case key.Matches(msg, k.Down):
		p.move(1)
		return m, nil
	case key.Matches(msg, k.Up):
		p.move(-1)
		return m, nil

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
	// The data grid's actions live with the grid.
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
						closeCmd = closeDriverCmd(m.driver)
						m.driver, m.active = nil, ""
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

	case actRunQuery:
		if sel := m.panels[panelHistory].selected(); sel != "" {
			return m, logCmd("%s", sel)
		}

	case actClearHistory:
		m.modal = &confirmModal{
			title:  "Clear history",
			body:   "Discard every entry in the query history?",
			danger: true,
			onConfirm: func(m *Model) tea.Cmd {
				m.panels[panelHistory].setItems(nil)
				return logCmd("-- clear query history")
			},
		}
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
	run := func(pw string, hasPW bool) tea.Cmd {
		if test {
			return tea.Batch(logCmd("-- test %s …", c.Name), testConnCmd(c, pw, hasPW))
		}
		return tea.Batch(logCmd("-- connecting %s …", c.Name), connectCmd(c, pw, hasPW))
	}
	if c.NeedsPassword() && c.AskPassword {
		m.modal = newPasswordPrompt(c, func(pw string) tea.Cmd { return run(pw, true) })
		return m, nil
	}
	if !test {
		m.setConnStatus(c.Name, statusPending, "")
	}
	return m, run("", false)
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
	case panelHistory:
		return logCmd("%s", sel)
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
