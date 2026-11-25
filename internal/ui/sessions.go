package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/db"
	"lazysql/internal/sshtunnel"
)

// Several connections can be open at once. Each owns a connSession — its
// driver and everything the user built up on it — and exactly one of them
// is on screen: the one embedded in the root Model, which is what every
// reducer and renderer reads. The others are parked. Switching swaps the
// embedded value; a reply for a parked session is reduced with that
// session swapped in for the duration of the reducer, so the same code
// serves both. See wiki/design/multiple-open-connections.md.

// sessID identifies one session for as long as the program runs. A reply
// is routed by it rather than by the connection's name, which a rename
// or a reconnect would make ambiguous.
type sessID int

// connSession is one connection's complete working state.
type connSession struct {
	id sessID

	driver db.Driver
	active string // name of the connected profile, "" when none
	// tunnel is the SSH tunnel the driver runs through, nil for a direct
	// connection. Its lifetime is the connection's: it is closed whenever
	// driver is, including on quit.
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
	// tree, the way changes shares one changeset. objects is the panel's
	// own list state — cursor, scroll offset, filter — and is what
	// Model.panels[panelObjects] points at while the session is on screen.
	tree    *objectTree
	objects *sidePanel

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

	// exports are the file transfers running through this connection's
	// driver — table export, DDL export, CSV import — one of each at
	// most. See exportsmodel.go.
	exports exportsModel

	// activity is the server activity report on screen (processes, lock
	// waits), nil when none. It takes over the main view. See activity.go.
	activity *activityView

	// plan is the EXPLAIN result on screen, nil when none. It replaces
	// the editor in the main view while panel [3] keeps the focus, and
	// `esc` dismisses it with the buffer untouched.
	plan *planView

	// query is panel [3] — the editor and its mode, the running script,
	// the transaction, parameter memory, completion and the schema and
	// highlight caches behind it. See querymodel.go.
	query queryModel

	// lastFocus is the panel of this session the keyboard was in last —
	// never panel [1], which belongs to no session. Switching back puts
	// the focus there, so the session is on screen the way it was left
	// rather than hidden behind the profile's detail view.
	lastFocus panelID
}

// busy reports whether the session has work in flight: a script, a page
// load, an analyzed plan, a transaction statement or a file transfer.
// Panel [1] marks a background session that is.
func (s *connSession) busy() bool {
	return s.query.run.running || s.grid.data.loading || s.exports.running() ||
		(s.plan != nil && s.plan.running) || (s.query.tx != nil && s.query.tx.busy != "")
}

// draining reports whether a streaming worker of the session has not
// delivered its last message yet. A session whose connection is already
// closed is kept until it has: the worker blocks on its channel until
// someone reads it.
func (s *connSession) draining() bool {
	return s.query.run.running || s.exports.running()
}

// losses names what closing the session would throw away, as verb
// phrases ready to be joined into a question. tag renders a connection
// name the way confirm modals do.
func (s *connSession) losses(tag func(string) string) []string {
	var out []string
	if tx := s.query.tx; tx != nil {
		out = append(out, fmt.Sprintf("roll back the open transaction on %s (%s)",
			tag(tx.conn), countStatements(tx.stmts)))
	}
	if n := s.grid.changes.Len(); n > 0 {
		out = append(out, "discard "+countChanges(n))
	}
	return out
}

// shutdown closes the session's transaction, driver and tunnel
// synchronously. It is for the paths that cannot wait for a tea.Cmd: quit,
// where the program may stop before a command runs, and a SQLite restore,
// which must not race a teardown still queued.
func (s *connSession) shutdown() {
	// The open transaction is rolled back explicitly, and first: Close
	// would do it too, but a handle whose BEGIN raced the quit is only
	// reachable from here.
	if tx := s.query.tx; tx != nil && tx.handle != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = tx.handle.Rollback(ctx)
		cancel()
	}
	s.query.tx = nil
	if s.driver != nil {
		s.driver.Close()
		s.driver = nil
	}
	if s.tunnel != nil {
		s.tunnel.Close()
		s.tunnel = nil
	}
	s.active = ""
}

// ---------- the session registry ----------

// blankSession builds the state of "no connection": an empty tree, grid
// and editor under a fresh id.
func (m *Model) blankSession() connSession {
	m.sessSeq++
	return connSession{
		id:      m.sessSeq,
		tree:    newObjectTree(nil),
		objects: &sidePanel{id: panelObjects, filterIn: newPanelFilterInput()},
		grid:    newGridModel(m.cfg.PageSizeOrDefault()),
		query:   newQueryModel(),
	}
}

// show puts s on screen. It is the only place the embedded session is
// replaced, so panel [2] and the bindings whose enabled state follows the
// session can never be left describing another one.
func (m *Model) show(s connSession) {
	m.connSession = s
	m.panels[panelObjects] = s.objects
	m.syncSessionKeys()
}

// syncSessionKeys derives the bindings that are only live while the
// session on screen has something for them to act on.
func (m *Model) syncSessionKeys() {
	m.keys.CancelQuery.SetEnabled(m.query.run.running ||
		(m.plan != nil && m.plan.running && m.plan.cancel != nil))
	m.keys.CancelExport.SetEnabled(m.exports.file.running)
	m.keys.CancelImport.SetEnabled(m.exports.csv.running)
	m.syncCopySelectionKey()
}

// park moves the session on screen to the background and leaves a blank
// one in its place. A session with no connection is only kept while a
// worker of it is still draining.
func (m *Model) park() {
	// What was waiting on the user's next key does not outlive the
	// detour: a deferred action must not fire, and a popup must not be
	// open, on a session nobody is looking at.
	m.meta.afterLoad, m.grid.fkAfter = actNone, actNone
	m.closeFilterInput()
	m.query.completion = completion{}
	if m.active != "" || m.draining() {
		s := m.connSession
		m.parked = append(m.parked, &s)
	}
	m.show(m.blankSession())
}

// unpark takes s out of the background list.
func (m *Model) unpark(s *connSession) {
	for i, p := range m.parked {
		if p == s {
			m.parked = append(m.parked[:i:i], m.parked[i+1:]...)
			return
		}
	}
}

// parkedByName is the background session connected to the named profile,
// nil when there is none.
func (m Model) parkedByName(name string) *connSession {
	if name == "" {
		return nil
	}
	for _, s := range m.parked {
		if s.active == name {
			return s
		}
	}
	return nil
}

func (m Model) parkedByID(id sessID) *connSession {
	for _, s := range m.parked {
		if s.id == id {
			return s
		}
	}
	return nil
}

// connected reports whether the named profile has an open session, on
// screen or in the background.
func (m Model) connected(name string) bool {
	if name == "" {
		return false
	}
	return m.active == name || m.parkedByName(name) != nil
}

// openSessions lists every session with a live connection, the one on
// screen first.
func (m *Model) openSessions() []*connSession {
	var out []*connSession
	if m.driver != nil {
		out = append(out, &m.connSession)
	}
	for _, s := range m.parked {
		if s.driver != nil {
			out = append(out, s)
		}
	}
	return out
}

// sessionOf is the open session of the named profile, nil when it is not
// connected.
func (m *Model) sessionOf(name string) *connSession {
	if name == "" {
		return nil
	}
	if m.active == name {
		return &m.connSession
	}
	return m.parkedByName(name)
}

// switchTo puts the named background session on screen exactly as it was
// left. Nothing is dialled and nothing is reloaded.
func (m *Model) switchTo(name string) tea.Cmd {
	s := m.parkedByName(name)
	if s == nil {
		return nil
	}
	m.unpark(s)
	m.park()
	m.show(*s)
	m.refreshConnections("")
	m.focusSession()
	cmds := []tea.Cmd{logCmd("-- switch to %s", name)}
	// The animations are global and only ever run for the session on
	// screen, so one that was left running is started again here.
	if m.query.run.running {
		cmds = append(cmds, m.spin.Tick)
	}
	if v := m.activity; v != nil && v.auto {
		v.gen++
		cmds = append(cmds, activityTickCmd(v.gen))
	}
	return tea.Batch(cmds...)
}

// focusSession moves the keyboard from panel [1] into the session on
// screen: to the panel it was last in, or to the object tree when that
// one has nothing to show any more.
func (m *Model) focusSession() {
	to := m.lastFocus
	mainOpen := m.grid.data.open() || m.trigger != nil || m.activity != nil
	if to == panelConnections || (to == panelMain && !mainOpen) {
		to = panelObjects
	}
	m.setFocus(to)
}

// trackFocus remembers where in the session on screen the keyboard is.
func (m *Model) trackFocus() {
	if m.focus != panelConnections {
		m.lastFocus = m.focus
	}
}

// inBackground runs fn with the parked session s in the on-screen slot
// and puts everything back afterwards. The reducers and actions fn calls
// are the ordinary ones: they cannot tell the difference, which is the
// point. What they do to the shell rather than to the session — a modal,
// a focus change — is undone: a session nobody is looking at must not
// take the keyboard. The returned command is scoped to s.
func (m *Model) inBackground(s *connSession, fn func(*Model) tea.Cmd) tea.Cmd {
	shown, modal, focus, prev := m.connSession, m.modal, m.focus, m.prev
	name := s.active
	m.show(*s)
	m.background = true
	cmd := fn(m)
	m.background = false
	if m.active != "" {
		name = m.active
	}
	*s = m.connSession
	m.show(shown)
	m.modal, m.focus, m.prev = modal, focus, prev
	// A session whose connection was closed in there is done as soon as
	// nothing of it is still draining.
	if s.active == "" && !s.draining() {
		m.unpark(s)
	}
	m.refreshConnections("")
	return scopeCmd(s.id, name, true, cmd)
}

// closeShown closes the connection of the session in the on-screen slot:
// it stops what runs on it, drops its transaction and staged changes and
// leaves the "no connection" state behind. why goes into the log line of
// a transaction that is rolled back by it.
func (m *Model) closeShown(why string) tea.Cmd {
	driver, tunnel, name := m.driver, m.tunnel, m.active
	if name == "" {
		return nil
	}
	txCmd := m.dropTx(why)
	m.driver, m.tunnel, m.active = nil, nil, ""
	m.resetBrowse()
	// A dump that borrowed this session's driver or tunnel dies with it.
	if m.backup.running && m.backup.session == name && m.backup.cancel != nil {
		m.backup.cancel()
	}
	return tea.Batch(txCmd, closeSessionCmd(driver, tunnel))
}

// endSession closes the named profile's session wherever it is.
func (m *Model) endSession(name, why string) tea.Cmd {
	if name != "" && m.active == name {
		return m.closeShown(why)
	}
	s := m.parkedByName(name)
	if s == nil {
		return nil
	}
	return m.inBackground(s, func(bm *Model) tea.Cmd { return bm.closeShown(why) })
}

// shutSession closes the named profile's session synchronously, for the
// one caller that cannot let the teardown trail behind as a command.
func (m *Model) shutSession(name string) {
	teardown := func(mm *Model) tea.Cmd {
		mm.connSession.shutdown()
		mm.resetBrowse()
		return nil
	}
	if name != "" && m.active == name {
		teardown(m)
	} else if s := m.parkedByName(name); s != nil {
		m.inBackground(s, teardown)
	}
	m.setConnStatus(name, statusIdle, "")
}

// disconnect is `x`: close the named session and say so.
func (m *Model) disconnect(name string) tea.Cmd {
	if !m.connected(name) {
		return nil
	}
	closeCmd := m.endSession(name, "disconnect")
	m.setConnStatus(name, statusIdle, "")
	if m.isEphemeral(name) {
		// An ephemeral connection has nothing to go back to being:
		// disconnecting it drops the row and leaves the panel showing
		// the saved profiles alone.
		m.dropEphemeral()
	}
	return tea.Batch(closeCmd, logCmd("-- disconnect %s", name))
}

// loseSession closes the session in the on-screen slot because its
// connection is gone, and marks the profile failed. It only ever runs for
// a background session: the one on screen shows its own errors.
func (m *Model) loseSession(err error) tea.Cmd {
	name := m.active
	lost := m.losses(func(n string) string { return n })
	note := ""
	if len(lost) > 0 {
		note = " — had to " + strings.Join(lost, " and ")
	}
	closeCmd := m.closeShown("connection lost")
	m.connState[name] = connState{status: statusError, lastErr: err.Error()}
	return tea.Batch(closeCmd,
		logCmd("-- connection %s lost — FAILED: %v%s", name, err, note))
}

// sessionLosses is what closing the named session would throw away, ""
// when nothing.
func (m *Model) sessionLosses(name string) string {
	s := m.sessionOf(name)
	if s == nil {
		return ""
	}
	return strings.Join(s.losses(m.taggedConnName), " and ")
}

// lossNote is the paragraph a confirmation that closes the named
// connection adds when that throws work away.
func (m *Model) lossNote(name string) string {
	s := m.sessionOf(name)
	if s == nil {
		return ""
	}
	var lines []string
	if tx := s.query.tx; tx != nil {
		lines = append(lines, fmt.Sprintf("The open transaction on it (%s) is rolled back.", countStatements(tx.stmts)))
	}
	if n := s.grid.changes.Len(); n > 0 {
		lines = append(lines, fmt.Sprintf("Its %s are discarded.", countChanges(n)))
	}
	if len(lines) == 0 {
		return ""
	}
	return "\n\n" + strings.Join(lines, " ")
}

// ---------- routing replies ----------

// sessionMsg carries a reply to the session whose command produced it.
type sessionMsg struct {
	id  sessID
	msg tea.Msg
}

// sessionScoped reports whether msg is the reply of work done on one
// connection — a query, a listing, a transfer — and therefore belongs to
// the session that started it, on screen or not. It is the one list of
// those types: a reply missing here would be reduced into whatever
// session happens to be on screen when it lands.
func sessionScoped(msg tea.Msg) bool {
	switch msg.(type) {
	case databasesLoadedMsg, relationsLoadedMsg, tableStatsLoadedMsg,
		triggersLoadedMsg, triggerDDLMsg,
		pageLoadedMsg, rowCountMsg, metaLoadedMsg,
		fksLoadedMsg, namespaceFKsLoadedMsg, schemaColumnsMsg,
		changesCommittedMsg,
		queryStmtMsg, queryDoneMsg, historyEntryMsg,
		txBegunMsg, txCommittedMsg, txRolledBackMsg,
		explainDoneMsg,
		activityLoadedMsg, activityKilledMsg, activityTickMsg,
		exportProgressMsg, exportDoneMsg, databaseDDLExportedMsg,
		importSetupMsg, importProgressMsg, importDoneMsg:
		return true
	}
	return false
}

// scopeCmd ties the messages cmd produces to session id: a session-scoped
// reply is wrapped so Update can route it, and a log note is attributed
// to conn. A batch is scoped command by command. background drops what
// only makes sense for the session on screen.
func scopeCmd(id sessID, conn string, background bool, cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		switch msg := cmd().(type) {
		case nil:
			return nil
		case tea.BatchMsg:
			out := make(tea.BatchMsg, 0, len(msg))
			for _, c := range msg {
				if c != nil {
					out = append(out, scopeCmd(id, conn, background, c))
				}
			}
			return out
		case commandLogMsg:
			if msg.conn == "" {
				msg.conn = conn
			}
			return msg
		case focusPanelMsg:
			if background {
				return nil
			}
			return msg
		default:
			if sessionScoped(msg) {
				return sessionMsg{id: id, msg: msg}
			}
			return msg
		}
	}
}

// noteOwner is the connection a log note issued while reducing msg
// belongs to: the one on screen for input aimed at a session's panels
// and for a session's own replies, none for panel [1] — whose notes name
// their connection themselves — and for everything global.
func (m Model) noteOwner(msg tea.Msg) string {
	switch msg.(type) {
	case tea.KeyPressMsg, tea.PasteMsg, tea.MouseMsg, wheelFlushMsg:
		if m.focus == panelConnections {
			return ""
		}
		return m.active
	}
	if sessionScoped(msg) {
		return m.active
	}
	return ""
}

// updateSession reduces a reply into the session it belongs to. For the
// one on screen that is the ordinary path; a background session is put
// in the on-screen slot for the reducer and taken out again, and a
// reply for a session that no longer exists is dropped.
func (m Model) updateSession(sm sessionMsg) (tea.Model, tea.Cmd) {
	if sm.id == m.id {
		conn := m.active
		next, cmd := m.reduce(sm.msg)
		mm := next.(Model)
		mm.trackFocus()
		return mm, scopeCmd(sm.id, conn, false, cmd)
	}
	s := m.parkedByID(sm.id)
	if s == nil {
		return m, releaseStale(sm.msg)
	}
	cmd := m.inBackground(s, func(bm *Model) tea.Cmd {
		next, cmd := bm.reduce(sm.msg)
		*bm = next.(Model)
		// A dead connection is found out by the reply that needed it.
		// The session on screen keeps its error on screen; one in the
		// background is closed and marked instead.
		if err := lostConnection(sm.msg); err != nil && bm.driver != nil {
			return tea.Batch(cmd, bm.loseSession(err))
		}
		return cmd
	})
	return m, cmd
}

// releaseStale frees what a reply for a vanished session still holds: a
// transaction handle whose BEGIN outlived its session is rolled back.
func releaseStale(msg tea.Msg) tea.Cmd {
	if b, ok := msg.(txBegunMsg); ok && b.tx != nil {
		return rollbackTxCmd(b.tx, "stale")
	}
	return nil
}

// lostConnection is the error of a reply that says the connection itself
// is gone, nil for every other outcome.
func lostConnection(msg tea.Msg) error {
	var err error
	switch msg := msg.(type) {
	case databasesLoadedMsg:
		err = msg.err
	case relationsLoadedMsg:
		err = msg.err
	case pageLoadedMsg:
		err = msg.err
	case rowCountMsg:
		err = msg.err
	case metaLoadedMsg:
		err = msg.err
	case queryStmtMsg:
		err = msg.err
	case txBegunMsg:
		err = msg.err
	case explainDoneMsg:
		err = msg.err
	case activityLoadedMsg:
		err = msg.err
	case changesCommittedMsg:
		err = msg.err
	}
	if db.ConnectionLost(err) {
		return err
	}
	return nil
}
