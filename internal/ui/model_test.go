package ui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/zalando/go-keyring"

	"lazysql/internal/config"
	"lazysql/internal/db"
)

// TestMain isolates the suite from the developer's real environment: the
// config file lands in a temp XDG home and the keyring is the in-memory mock,
// so no test can touch a real login keychain.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "lazysql-ui")
	if err != nil {
		panic(err)
	}
	os.Setenv("XDG_CONFIG_HOME", dir)
	// The query history is persistent; point it at the temp home too so
	// no test can append to the developer's own history file.
	os.Setenv("XDG_STATE_HOME", dir)
	// A test never writes an escape sequence to whatever terminal is
	// running it: the OSC 52 fallback is off unless a test turns it on
	// through the osc52Available seam.
	os.Setenv("LAZYSQL_NO_OSC52", "1")
	keyring.MockInit()
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// testConnections is the fixture connection list. The first entry is a real
// SQLite file under the temp config home, so connect/test flows can run for
// real without a server.
func testConnections() []config.Connection {
	return []config.Connection{
		{Name: "local-sqlite", Engine: db.EngineSQLite,
			File: filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "fixture.sqlite")},
		{Name: "local-postgres", Engine: db.EnginePostgres, Host: "localhost", Port: 5432, User: "pg"},
		{Name: "local-mysql", Engine: db.EngineMySQL, Host: "localhost", Port: 3306, User: "root"},
		{Name: "analytics", Engine: db.EngineDuckDB, File: ""},
	}
}

// press builds the KeyPressMsg for a printable rune.
func press(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

// special builds the KeyPressMsg for a non-printable key such as tab or esc.
func special(code rune, mod tea.KeyMod) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code, Mod: mod}
}

// send drives the model through a sequence of key presses, running each
// returned command synchronously so domain messages are reduced like they
// would be by the runtime.
func send(t *testing.T, m Model, msgs ...tea.Msg) Model {
	t.Helper()
	for _, msg := range msgs {
		// Each key press is driven to a standstill before the next one:
		// a message may produce commands whose messages produce more,
		// and a flow like the file export is several rounds deep.
		queue := []tea.Msg{msg}
		for round := 0; len(queue) > 0; round++ {
			if round > 10_000 {
				t.Fatalf("message cascade did not settle after %T", msg)
			}
			head := queue[0]
			queue = queue[1:]
			next, cmd := m.Update(head)
			m = next.(Model)
			queue = append(queue, drain(cmd)...)
		}
	}
	return m
}

// drain flattens a command (including batches) into the messages it produces.
func drain(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	switch msg := msg.(type) {
	case nil:
		return nil
	case tea.BatchMsg:
		var out []tea.Msg
		for _, c := range msg {
			out = append(out, drain(c)...)
		}
		return out
	default:
		return []tea.Msg{msg}
	}
}

func sized(w, h int) Model {
	m, err := New(false)
	if err != nil {
		panic(err)
	}
	m.cfg = &config.Config{Connections: testConnections()}
	// Select the first fixture explicitly: New() may have loaded a config
	// written by an earlier test, which would otherwise carry its selection
	// over into this model.
	m.refreshConnections(testConnections()[0].Name)
	seedTree(&m, []string{"users", "accounts", "sessions", "audit_log"}, nil)
	next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return next.(Model)
}

// seedTree fills the [2] Objects panel with a single-namespace fixture
// tree whose Tables category is already expanded — the shape a connected
// file engine lands in.
func seedTree(m *Model, tables, views []string) {
	m.rebuildTree([]string{pseudoDatabase})
	rels := make([]db.Relation, 0, len(tables)+len(views))
	for _, t := range tables {
		rels = append(rels, db.Relation{Name: t, Kind: db.RelationTable})
	}
	for _, v := range views {
		rels = append(rels, db.Relation{Name: v, Kind: db.RelationView})
	}
	m.applyRelations("", rels)
	m.tree.category("", catTables).expanded = true
	m.refreshTree()
}

// seedMultiTree is seedTree for a connection with several namespaces, so
// the tree keeps its database level.
func seedMultiTree(m *Model, databases []string) {
	m.rebuildTree(databases)
	m.refreshTree()
}

// treeSelect moves the [2] cursor onto a named row.
func treeSelect(t *testing.T, m Model, name string) Model {
	t.Helper()
	p := m.panels[panelObjects]
	if !p.selectByName(name) {
		t.Fatalf("tree row %q not found in %v", name, p.items)
	}
	return m
}

func TestNumberKeysJumpToPanel(t *testing.T) {
	m := sized(120, 40)
	for i, r := range []rune{'2', '3', '1'} {
		m = send(t, m, press(r))
		want := panelID(r - '1')
		if m.focus != want {
			t.Fatalf("step %d: focus = %v, want %v", i, m.focus, want)
		}
	}
}

func TestTabCyclesPanels(t *testing.T) {
	m := sized(120, 40)
	for i := 0; i < int(panelCount); i++ {
		want := panelID((i + 1) % int(panelCount))
		m = send(t, m, special(tea.KeyTab, 0))
		if m.focus != want {
			t.Fatalf("after %d tabs: focus = %v, want %v", i+1, m.focus, want)
		}
	}
	m = send(t, m, special(tea.KeyTab, tea.ModShift))
	if m.focus != panelQuery {
		t.Fatalf("shift+tab: focus = %v, want %v", m.focus, panelQuery)
	}
}

func TestCursorMovesWithinPanel(t *testing.T) {
	m := sized(120, 40)
	m = send(t, m, press('j'), press('j'))
	if got := m.panels[panelConnections].cursor; got != 2 {
		t.Fatalf("cursor = %d, want 2", got)
	}
	m = send(t, m, press('k'))
	if got := m.panels[panelConnections].cursor; got != 1 {
		t.Fatalf("cursor = %d, want 1", got)
	}
	// Cursor is clamped at both ends.
	m = send(t, m, press('k'), press('k'), press('k'))
	if got := m.panels[panelConnections].cursor; got != 0 {
		t.Fatalf("cursor = %d, want 0", got)
	}
}

func TestModalSwallowsBackgroundKeys(t *testing.T) {
	m := sized(120, 40)
	m = send(t, m, press('?'))
	if m.modal == nil {
		t.Fatal("? did not open the help modal")
	}
	// j/2 would move the cursor / switch panels if they leaked through.
	m = send(t, m, press('j'), press('2'))
	if m.focus != panelConnections {
		t.Fatalf("focus leaked through modal: %v", m.focus)
	}
	if got := m.panels[panelConnections].cursor; got != 0 {
		t.Fatalf("cursor leaked through modal: %d", got)
	}
	m = send(t, m, special(tea.KeyEscape, 0))
	if m.modal != nil {
		t.Fatal("esc did not close the modal")
	}
}

func TestEscCancelsEveryModal(t *testing.T) {
	cases := []struct {
		name string
		open func(Model) Model
	}{
		{"help", func(m Model) Model { m.modal = newHelpModal("t", m.keys.helpGroups(m.focus)); return m }},
		{"menu", func(m Model) Model { m.modal = m.actionsMenu(); return m }},
		{"confirm", func(m Model) Model { m.modal = &confirmModal{title: "t", body: "b"}; return m }},
		{"prompt", func(m Model) Model { m.modal = newPromptModal("t", "", "", nil); return m }},
		// The create-mode form's esc retreats to the engine picker rather
		// than closing (see TestEscBacksOutStepByStep); the edit form and
		// the picker itself cancel outright.
		{"form", func(m Model) Model {
			m.modal = newConnectionForm("t", config.Connection{Name: "x"}, "x")
			return m
		}},
		{"engine picker", func(m Model) Model { m.modal = newConnectionWizard(); return m }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.open(sized(120, 40))
			m = send(t, m, special(tea.KeyEscape, 0))
			if m.modal != nil {
				t.Fatal("esc did not cancel the modal")
			}
		})
	}
}

func TestConfirmModalRunsActionOnEnter(t *testing.T) {
	m := sized(120, 40)
	before := len(m.panels[panelConnections].items)
	m = send(t, m, press('d')) // remove connection
	if m.modal == nil {
		t.Fatal("d did not open the confirm modal")
	}
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.modal != nil {
		t.Fatal("confirm modal stayed open")
	}
	if got := len(m.panels[panelConnections].items); got != before-1 {
		t.Fatalf("items = %d, want %d", got, before-1)
	}
	if len(m.commandLog) == 0 {
		t.Fatal("action was not written to the command log")
	}
}

func TestConfirmModalDeleteRemovesProfile(t *testing.T) {
	m := sized(120, 40)
	m = send(t, m, press('d'), special(tea.KeyEnter, 0))
	if _, ok := m.cfg.Find("local-sqlite"); ok {
		t.Fatal("profile survived the delete confirmation")
	}
	if _, ok := m.connState["local-sqlite"]; ok {
		t.Fatal("status entry survived the delete")
	}
}

// `n` opens the engine picker; picking SQLite yields that engine's form,
// and filling name + file persists a SQLite profile with no password
// anywhere in the config.
func TestNewConnectionFormAddsProfile(t *testing.T) {
	m := sized(120, 40)
	before := len(m.panels[panelConnections].items)
	m = send(t, m, press('n'))
	if _, ok := m.modal.(*enginePickerModal); !ok {
		t.Fatalf("n opened %T, want the engine picker", m.modal)
	}
	m = send(t, m, pickEngineKey(t, m, db.EngineSQLite))
	form, ok := m.modal.(*formModal)
	if !ok {
		t.Fatalf("picking an engine opened %T, want *formModal", m.modal)
	}
	if got := db.Engine(form.rawValue("engine")); got != db.EngineSQLite {
		t.Fatalf("form engine = %q, want sqlite", got)
	}
	form.field("name").input.SetValue("scratch")
	form.field("file").input.SetValue(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "scratch.sqlite"))

	m = send(t, m, special(tea.KeyEnter, 0))
	if m.modal != nil {
		t.Fatalf("form stayed open: %v", form.err)
	}
	if got := len(m.panels[panelConnections].items); got != before+1 {
		t.Fatalf("items = %d, want %d", got, before+1)
	}
	c, ok := m.cfg.Find("scratch")
	if !ok || c.Engine != db.EngineSQLite {
		t.Fatalf("profile = %+v, want a SQLite entry", c)
	}
}

// pickEngineKey is the digit that selects an engine in the open picker.
func pickEngineKey(t *testing.T, m Model, e db.Engine) tea.KeyPressMsg {
	t.Helper()
	p, ok := m.modal.(*enginePickerModal)
	if !ok {
		t.Fatalf("modal is %T, want the engine picker", m.modal)
	}
	for i, c := range p.choices {
		if c.engine == e {
			return press(rune('1' + i))
		}
	}
	t.Fatalf("engine %q is not offered by the picker", e)
	return tea.KeyPressMsg{}
}

// `e` opens the form prefilled; renaming replaces the profile in place rather
// than adding a second one, and carries its status across.
func TestEditConnectionRenamesInPlace(t *testing.T) {
	m := sized(120, 40)
	m.connState["local-sqlite"] = connState{status: statusOK}
	m.active = "local-sqlite"
	before := len(m.cfg.Connections)

	m = send(t, m, press('e'))
	form, ok := m.modal.(*formModal)
	if !ok {
		t.Fatalf("e opened %T, want *formModal", m.modal)
	}
	if got := form.field("name").value(); got != "local-sqlite" {
		t.Fatalf("form name = %q, want the selected profile", got)
	}
	form.field("name").input.SetValue("renamed")
	m = send(t, m, special(tea.KeyEnter, 0))

	if len(m.cfg.Connections) != before {
		t.Fatalf("connections = %d, want %d (rename, not add)", len(m.cfg.Connections), before)
	}
	if _, ok := m.cfg.Find("local-sqlite"); ok {
		t.Fatal("old name survived the rename")
	}
	if m.connState["renamed"].status != statusOK || m.active != "renamed" {
		t.Fatalf("status did not follow the rename: %+v active=%q", m.connState, m.active)
	}
}

// A form that fails validation stays open and reports why.
func TestConnectionFormRejectsBadPort(t *testing.T) {
	m := sized(120, 40)
	m = send(t, m, press('n'))
	m = send(t, m, pickEngineKey(t, m, db.EnginePostgres))
	form := m.modal.(*formModal)
	form.field("name").input.SetValue("bad")
	form.field("port").input.SetValue("not-a-number")

	m = send(t, m, special(tea.KeyEnter, 0))
	if m.modal == nil {
		t.Fatal("form closed despite an invalid port")
	}
	if !strings.Contains(form.err, "port") {
		t.Fatalf("err = %q, want a port complaint", form.err)
	}
}

// The engine choice happens first and decides which form is built: each
// engine gets exactly its own fields, and ctrl+e trades the form for the
// picker again without losing what was typed.
func TestEngineChoiceSwitchesFormFields(t *testing.T) {
	m := sized(120, 40)
	m = send(t, m, press('n'))
	m = send(t, m, pickEngineKey(t, m, db.EnginePostgres))
	form := m.modal.(*formModal)
	if !hasVisibleField(form, "host") || hasVisibleField(form, "file") {
		t.Fatal("postgres form should show host and hide file")
	}
	form.field("name").input.SetValue("kept")

	// ctrl+e reopens the picker; picking DuckDB rebuilds the form for the
	// file family, keeping the typed name.
	m = send(t, m, tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	m = send(t, m, pickEngineKey(t, m, db.EngineDuckDB))
	form = m.modal.(*formModal)
	if hasVisibleField(form, "host") || !hasVisibleField(form, "file") {
		t.Fatal("duckdb form should show file and hide host")
	}
	if hasVisibleField(form, "password") || hasVisibleField(form, "ssh") {
		t.Fatal("file-based engines must not offer password or SSH fields")
	}
	if got := form.field("name").value(); got != "kept" {
		t.Fatalf("name = %q after the engine change, want the typed value kept", got)
	}
}

func hasVisibleField(f *formModal, name string) bool {
	for _, fl := range f.visibleFields() {
		if fl.name == name {
			return true
		}
	}
	return false
}

// Connecting is asynchronous and reports through messages; a real SQLite file
// exercises the whole path without a server.
func TestConnectSetsStatusAndListsDatabases(t *testing.T) {
	m := sized(120, 40)
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.active != "local-sqlite" {
		t.Fatalf("active = %q, want local-sqlite", m.active)
	}
	if got := m.connState["local-sqlite"].status; got != statusOK {
		t.Fatalf("status = %v, want statusOK", got)
	}
	// SQLite is a single-namespace engine: the tree skips the database
	// level and opens straight into its tables.
	if !m.tree.single {
		t.Fatalf("tree kept a database level: %v", m.tree.databaseNames())
	}
	if got := m.panels[panelObjects].items; len(got) == 0 || got[0] != "Tables" {
		t.Fatalf("objects panel = %v, want the Tables category first", got)
	}
	if m.focus != panelObjects {
		t.Fatalf("focus = %v, want %v", m.focus, panelObjects)
	}
	if !logContains(m, "-- connect local-sqlite") {
		t.Fatalf("command log = %v", m.commandLog)
	}
}

// A dial that cannot succeed must not wedge the UI: it tints the row red and
// surfaces the error.
func TestFailedConnectMarksProfileRed(t *testing.T) {
	m := sized(120, 40)
	m.cfg = &config.Config{Connections: []config.Connection{
		{Name: "nope", Engine: db.EngineSQLite, File: filepath.Join(os.TempDir(), "no-such-dir", "x.sqlite")},
	}}
	m.refreshConnections("")
	m = send(t, m, special(tea.KeyEnter, 0))
	if got := m.connState["nope"].status; got != statusError {
		t.Fatalf("status = %v, want statusError", got)
	}
	if m.modal == nil {
		t.Fatal("failure did not surface in a modal")
	}
	if m.connState["nope"].lastErr == "" {
		t.Fatal("no error recorded for the failed profile")
	}
}

// `t` probes without adopting the profile as the active connection.
func TestTestConnectionLogsResult(t *testing.T) {
	m := sized(120, 40)
	m = send(t, m, press('t'))
	if m.active != "" {
		t.Fatalf("active = %q, want a test not to connect", m.active)
	}
	if !logContains(m, "-- test local-sqlite ok") {
		t.Fatalf("command log = %v", m.commandLog)
	}
}

// "ask on connect" defers the dial until the password prompt is submitted.
func TestAskOnConnectOpensPasswordPrompt(t *testing.T) {
	m := sized(120, 40)
	m.cfg = &config.Config{Connections: []config.Connection{
		{Name: "asks", Engine: db.EnginePostgres, Host: "127.0.0.1", Port: 1, User: "u", AskPassword: true},
	}}
	m.refreshConnections("")
	next, cmd := m.Update(special(tea.KeyEnter, 0))
	m = next.(Model)
	if cmd != nil {
		t.Fatal("dial started before the password was entered")
	}
	f, ok := m.modal.(*formModal)
	if !ok {
		t.Fatalf("modal = %T, want the password prompt", m.modal)
	}
	if f.field("secret").kind != fieldPassword {
		t.Fatal("prompt field is not masked")
	}
}

func logContains(m Model, want string) bool {
	for _, l := range m.commandLogEntries() {
		if strings.Contains(l.text, want) {
			return true
		}
	}
	return false
}

func TestActionsMenuDispatchesSameActionAsKey(t *testing.T) {
	m := sized(120, 40)
	m = send(t, m, press('a'))
	mm, ok := m.modal.(*menuModal)
	if !ok {
		t.Fatalf("a opened %T, want *menuModal", m.modal)
	}
	// First entry on Connections is "connect".
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.modal != nil {
		t.Fatal("menu stayed open after enter")
	}
	if m.focus != panelObjects {
		t.Fatalf("focus = %v, want %v (connect should drill in)", m.focus, panelObjects)
	}
	if len(mm.entries) < 2 {
		t.Fatalf("menu had %d entries, want the panel's actions", len(mm.entries))
	}
}

func TestDrillInLogsAndRecordsHistory(t *testing.T) {
	// Drilling in from [1] Connections opens a live connection, which the
	// dedicated connect tests cover; this one starts from a connected
	// model and walks database -> table -> data.
	m := browsing(t)
	if _, err := m.driver.Exec(context.Background(),
		`CREATE TABLE IF NOT EXISTS drill (id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	m = send(t, m, press('2'), press('R')) // re-read the Tables category
	if m.focus != panelObjects {
		t.Fatalf("focus = %v, want %v", m.focus, panelObjects)
	}
	if !m.panels[panelObjects].selectByName("drill") {
		t.Fatalf("drill not listed: %v", m.panels[panelObjects].items)
	}
	m = send(t, m, special(tea.KeyEnter, 0)) // table -> data grid

	if m.focus != panelMain {
		t.Fatalf("focus = %v, want the data grid", m.focus)
	}
	// The entry holds the statement as it ran.
	if len(m.history) != 1 || !strings.Contains(m.history[0].SQL, "SELECT * FROM ") {
		t.Fatalf("history = %v, want one SELECT entry", m.history)
	}
	if !logContains(m, `SELECT * FROM "drill" LIMIT 100 OFFSET 0;`) {
		t.Fatalf("command log = %v", m.commandLog)
	}
	if !logContains(m, `SELECT COUNT(*) FROM "drill";`) {
		t.Fatalf("command log is missing the count: %v", m.commandLog)
	}
}

func TestEscBacksOutOfFocusStack(t *testing.T) {
	m := sized(120, 40)
	m = send(t, m, press('2'), special(tea.KeyEscape, 0))
	if m.focus != panelConnections {
		t.Fatalf("focus = %v, want %v", m.focus, panelConnections)
	}
}

func TestScreenModeCycles(t *testing.T) {
	m := sized(120, 40)
	for _, want := range []screenMode{screenHalf, screenFull, screenNormal} {
		m = send(t, m, press('+'))
		if m.screen != want {
			t.Fatalf("screen = %v, want %v", m.screen, want)
		}
	}
	m = send(t, m, press('_'))
	if m.screen != screenFull {
		t.Fatalf("screen = %v, want %v", m.screen, screenFull)
	}
}

func TestScreenModeFromNameFallsBackOnUnknown(t *testing.T) {
	cases := map[string]screenMode{
		"normal": screenNormal,
		"half":   screenHalf,
		"full":   screenFull,
		"":       screenNormal,
		"bogus":  screenNormal,
		"NORMAL": screenNormal,
	}
	for name, want := range cases {
		if got := screenModeFromName(name); got != want {
			t.Errorf("screenModeFromName(%q) = %v, want %v", name, got, want)
		}
	}
}

// removeState clears any state.toml left at the test's XDG_CONFIG_HOME, so
// one test's saved screen mode never leaks into the next New().
func removeState(t *testing.T) {
	t.Helper()
	path, err := config.StatePath()
	if err != nil {
		t.Fatalf("config.StatePath: %v", err)
	}
	os.Remove(path)
	t.Cleanup(func() { os.Remove(path) })
}

// Quitting must persist the current screen mode, and a later New() must
// restore it — the acceptance criterion for issue #67.
func TestQuitPersistsScreenModeForNextStartup(t *testing.T) {
	removeState(t)
	m := sized(120, 40)
	m = send(t, m, press('+'), press('+')) // normal -> half -> full
	if m.screen != screenFull {
		t.Fatalf("screen = %v, want %v", m.screen, screenFull)
	}
	send(t, m, press('q'))

	st, err := config.LoadState()
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if st.ScreenMode != "full" {
		t.Fatalf("saved ScreenMode = %q, want %q", st.ScreenMode, "full")
	}

	restarted, err := New(false)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	if restarted.screen != screenFull {
		t.Fatalf("restarted screen = %v, want %v", restarted.screen, screenFull)
	}
}

// q with a pending changeset must confirm before quitting, and must not
// persist state or tear down the connection until confirmed — the guard
// from issue #103.
func TestQuitWithStagedChangesAsksToConfirm(t *testing.T) {
	removeState(t)
	m := dataBrowsing(t)
	m = send(t, m, press('l'))
	m = stageEdit(t, m, "tmp")
	if m.changes.Len() != 1 {
		t.Fatalf("changeset = %d, want 1", m.changes.Len())
	}

	m = send(t, m, press('q'))
	cm, ok := m.modal.(*confirmModal)
	if !ok {
		t.Fatalf("q opened %T, want a confirmation", m.modal)
	}
	if !strings.Contains(cm.body, "1 staged change") {
		t.Fatalf("body = %q", cm.body)
	}
	if m.driver == nil {
		t.Fatal("q must not tear down the connection before confirmation")
	}

	// esc keeps the changeset and the program running.
	m = send(t, m, special(tea.KeyEscape, 0))
	if m.changes.Len() != 1 {
		t.Fatal("esc on the quit confirmation dropped the changeset")
	}

	m = send(t, m, press('q'), special(tea.KeyEnter, 0))
	if m.driver != nil {
		t.Fatal("confirmed quit did not tear down the connection")
	}
}

// An unknown value in the state file must never block startup: it degrades
// to screenNormal like a missing file would.
func TestNewFallsBackToNormalOnUnknownSavedScreenMode(t *testing.T) {
	removeState(t)
	st := &config.State{ScreenMode: "gigantic"}
	if err := st.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	m, err := New(false)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	if m.screen != screenNormal {
		t.Fatalf("screen = %v, want %v", m.screen, screenNormal)
	}
}

func TestPanelHeightsFillBody(t *testing.T) {
	m := sized(120, 40)
	for _, mode := range []screenMode{screenNormal, screenHalf} {
		m.screen = mode
		hs := m.panelHeights(39)
		sum := 0
		for _, h := range hs {
			if h < 3 {
				t.Fatalf("mode %v: panel height %d too small", mode, h)
			}
			sum += h
		}
		if sum != 39 {
			t.Fatalf("mode %v: heights sum to %d, want 39", mode, sum)
		}
	}
}

// lipgloss v2 treats Style.Width/Height as the total block size (borders
// included) and never pads beyond the content, so layout math must ask for
// the full box. Guard against the side column drifting out of alignment.
func TestRenderedBlocksMatchRequestedSize(t *testing.T) {
	m := sized(120, 34)
	bodyH := 33
	heights := m.panelHeights(bodyH)
	total := 0
	for id := panelID(0); id < panelCount; id++ {
		block := m.renderPanel(id, 40, heights[id])
		if got := lipgloss.Height(block); got != heights[id] {
			t.Errorf("panel %v height = %d, want %d", panelTitles[id], got, heights[id])
		}
		if got := lipgloss.Width(block); got != 40 {
			t.Errorf("panel %v width = %d, want 40", panelTitles[id], got)
		}
		total += lipgloss.Height(block)
	}
	if total != bodyH {
		t.Errorf("side column height = %d, want %d", total, bodyH)
	}
	if got := lipgloss.Height(m.renderMainColumn(80, bodyH)); got != bodyH {
		t.Errorf("main column height = %d, want %d", got, bodyH)
	}
}

func TestTinyTerminalDoesNotPanic(t *testing.T) {
	for _, size := range [][2]int{{0, 0}, {1, 1}, {20, 5}, {59, 17}} {
		m := sized(size[0], size[1])
		v := m.View()
		if size[0] >= minWidth && size[1] >= minHeight {
			continue
		}
		if size[0] > 0 && !strings.Contains(v.Content, "too small") {
			t.Fatalf("%dx%d: expected the too-small view", size[0], size[1])
		}
	}
}

func TestRendersAtManySizes(t *testing.T) {
	m := sized(120, 40)
	for _, size := range [][2]int{{60, 18}, {80, 24}, {200, 60}, {61, 19}} {
		for _, mode := range []screenMode{screenNormal, screenHalf, screenFull} {
			next, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			m = next.(Model)
			m.screen = mode
			if out := m.View().Content; out == "" {
				t.Fatalf("%dx%d mode %v rendered nothing", size[0], size[1], mode)
			}
		}
	}
}

// The options bar and `?` must describe the same bindings, and every one of
// them must actually be handled.
func TestOptionsBarAndHelpShareOneSource(t *testing.T) {
	k := newKeyMap()
	for id := panelID(0); id <= panelMain; id++ {
		helpKeys := map[string]bool{}
		for _, group := range k.helpGroups(id) {
			for _, b := range group.bindings {
				helpKeys[b.Help().Key] = true
			}
		}
		for _, b := range k.optionsBarBindings(id) {
			if !helpKeys[b.Help().Key] {
				t.Errorf("panel %v: options bar shows %q which `?` omits", panelTitles[id], b.Help().Key)
			}
		}
	}
}

func TestEveryDocumentedKeyIsBound(t *testing.T) {
	k := newKeyMap()
	global := map[string]bool{}
	for _, b := range append(k.global(), k.navigation()...) {
		for _, ks := range b.Keys() {
			global[ks] = true
		}
	}
	for id := panelID(0); id <= panelMain; id++ {
		actions := map[string]bool{}
		for _, a := range k.panelActions(id) {
			for _, ks := range a.binding.Keys() {
				actions[ks] = true
			}
		}
		actions[k.Actions.Keys()[0]] = true
		// The query editor's mode groups — vim normal mode, insert mode,
		// the completion popup, the history pane and the result-grid
		// fall-through — are dispatched by updateQuery, updateEditor, the
		// pane modal and updateData rather than through the action table,
		// since they only mean anything inside the buffer or the pane.
		// They are bound; they are just not actions.
		if id == panelQuery {
			groups := append(k.editorNormal(), k.editorInsert()...)
			groups = append(groups, k.editorCompletion()...)
			groups = append(groups, k.historyPane()...)
			groups = append(groups, k.queryResultKeys()...)
			for _, b := range groups {
				for _, ks := range b.Keys() {
					actions[ks] = true
				}
			}
		}
		// The date picker's keys are dispatched inside the modal, like the
		// pane's and the popup's, and the inline WHERE line's inside
		// updateFilterInput: bound, just not actions.
		if id == panelMain {
			for _, b := range append(k.datePicker(), k.filterInput()...) {
				for _, ks := range b.Keys() {
					actions[ks] = true
				}
			}
		}
		// Same for the connection form's contract and the engine picker's,
		// dispatched inside formModal.update / enginePickerModal.update.
		if id == panelConnections {
			for _, b := range append(k.connFormKeys(), k.enginePickerKeys()...) {
				for _, ks := range b.Keys() {
					actions[ks] = true
				}
			}
		}

		for _, group := range k.helpGroups(id) {
			for _, b := range group.bindings {
				for _, ks := range b.Keys() {
					if !global[ks] && !actions[ks] {
						t.Errorf("panel %v: `?` documents %q but nothing binds it", panelTitles[id], ks)
					}
				}
			}
		}
	}
}

// Panel action keys must not shadow the global keys handled before them.
func TestPanelActionsDoNotShadowGlobalKeys(t *testing.T) {
	k := newKeyMap()
	globals := append(k.global(), k.navigation()...)
	for id := panelID(0); id <= panelMain; id++ {
		for _, a := range k.panelActions(id) {
			for _, g := range globals {
				for _, gk := range g.Keys() {
					if key.Matches(tea.KeyPressMsg{Code: rune(gk[0]), Text: gk}, a.binding) && len(gk) == 1 {
						t.Errorf("panel %v: action %q shadows global %q", panelTitles[id], a.binding.Help().Key, gk)
					}
				}
			}
		}
	}
}
