package ui

import (
	"context"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/config"
	"lazysql/internal/db"
	"lazysql/internal/session"
)

// restoreFixtureConn is a dedicated SQLite profile, separate from
// testConnections()'s shared fixture: a restore test drives a real quit and
// restart, so it needs a database file (and a session.json) nothing else in
// the suite touches.
func restoreFixtureConn(t *testing.T) config.Connection {
	t.Helper()
	return config.Connection{
		Name:   "restore-src",
		Engine: db.EngineSQLite,
		File:   filepath.Join(t.TempDir(), "restore.sqlite"),
	}
}

// connectAndOpenWidgets connects the fixture, creates and populates a table,
// and browses into it — the state a real session would have accumulated
// before the user quit.
func connectAndOpenWidgets(t *testing.T, m Model) Model {
	t.Helper()
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = next.(Model)
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.active != "restore-src" {
		t.Fatalf("active = %q, want restore-src", m.active)
	}
	ctx := context.Background()
	if _, err := m.driver.Exec(ctx, `CREATE TABLE widgets (id INTEGER PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b", "c"} {
		if _, err := m.driver.Exec(ctx, `INSERT INTO widgets (name) VALUES (?)`, name); err != nil {
			t.Fatal(err)
		}
	}
	cmd := m.openTable("widgets")
	m = send(t, m, drain(cmd)...)
	if m.table != "widgets" || len(m.data.rows) != 3 {
		t.Fatalf("setup did not land on widgets with 3 rows: table=%q rows=%d", m.table, len(m.data.rows))
	}
	return m
}

// TestRestoreSessionReconnectsToTableTabAndCursor is the acceptance
// criterion end to end: quit while browsing a table, restart, land back on
// the same connection, table, tab and (clamped) cursor.
func TestRestoreSessionReconnectsToTableTabAndCursor(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	conn := restoreFixtureConn(t)
	writeConfig(t, &config.Config{Connections: []config.Connection{conn}, RestoreSession: true})

	m, err := New(false)
	if err != nil {
		t.Fatal(err)
	}
	m = connectAndOpenWidgets(t, m)
	m.data.row, m.data.col = 1, 0
	cmd := m.setMainTab(mainTabStructure)
	m = send(t, m, drain(cmd)...)
	m.saveSession()
	m.closeSession()

	// Simulate a restart: a fresh model reading the same config and the
	// session file the first run just wrote.
	m2, err := New(false)
	if err != nil {
		t.Fatal(err)
	}
	m2 = send(t, m2, drain(m2.Init())...)

	if m2.active != "restore-src" {
		t.Fatalf("active = %q, want restore-src", m2.active)
	}
	if m2.table != "widgets" {
		t.Fatalf("table = %q, want widgets", m2.table)
	}
	if m2.tab != mainTabStructure {
		t.Fatalf("tab = %v, want mainTabStructure", m2.tab)
	}
	if m2.data.row != 1 || m2.data.col != 0 {
		t.Fatalf("cursor = (%d,%d), want (1,0)", m2.data.row, m2.data.col)
	}
	if !logContains(m2, "restored session") {
		t.Fatalf("command log = %v", m2.commandLog)
	}
	if m2.restoreSess != nil {
		t.Fatal("restoreSess left set after a completed restore")
	}
}

// A saved cursor past the end of the (now smaller, or now empty) table must
// clamp rather than crash — the acceptance criterion's "best-effort" cursor
// restore.
func TestRestoreSessionClampsAnOutOfRangeCursor(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	conn := restoreFixtureConn(t)
	writeConfig(t, &config.Config{Connections: []config.Connection{conn}, RestoreSession: true})

	m, err := New(false)
	if err != nil {
		t.Fatal(err)
	}
	m = connectAndOpenWidgets(t, m)
	m.saveSession()
	m.closeSession()

	// Hand-write a session with a cursor well past the 3 rows / 2 columns
	// the table actually has.
	if err := session.Save(session.Session{
		Connection: conn.Name, Database: "", Table: "widgets",
		Tab: int(mainTabData), Row: 999, Col: 999,
	}); err != nil {
		t.Fatal(err)
	}

	m2, err := New(false)
	if err != nil {
		t.Fatal(err)
	}
	m2 = send(t, m2, drain(m2.Init())...)

	if m2.table != "widgets" {
		t.Fatalf("table = %q, want widgets", m2.table)
	}
	if m2.data.row < 0 || m2.data.row >= len(m2.data.rows) {
		t.Fatalf("row = %d not clamped to %d rows", m2.data.row, len(m2.data.rows))
	}
	if m2.data.col < 0 || m2.data.col >= len(m2.data.cols) {
		t.Fatalf("col = %d not clamped to %d cols", m2.data.col, len(m2.data.cols))
	}
}

// A deleted connection degrades to the normal start screen with a log line,
// not an error and not a hang.
func TestRestoreSessionDegradesOnDeletedConnection(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	writeConfig(t, &config.Config{Connections: nil, RestoreSession: true})
	if err := session.Save(session.Session{Connection: "gone", Table: "widgets"}); err != nil {
		t.Fatal(err)
	}

	m, err := New(false)
	if err != nil {
		t.Fatal(err)
	}
	if m.restoreSess != nil {
		t.Fatal("restoreSess set for a connection that no longer exists in config")
	}
	m = send(t, m, drain(m.Init())...)
	if m.active != "" {
		t.Fatalf("active = %q, want no connection made", m.active)
	}
}

// A table dropped since the session was saved degrades gracefully: the
// connection and database open, but nothing forces a table that is gone.
func TestRestoreSessionDegradesOnMissingTable(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	conn := restoreFixtureConn(t)
	writeConfig(t, &config.Config{Connections: []config.Connection{conn}, RestoreSession: true})

	// Connect once so the database file exists, then save a session naming
	// a table that was never created.
	m, err := New(false)
	if err != nil {
		t.Fatal(err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = next.(Model)
	m = send(t, m, special(tea.KeyEnter, 0))
	m.closeSession()
	if err := session.Save(session.Session{Connection: conn.Name, Table: "ghosts"}); err != nil {
		t.Fatal(err)
	}

	m2, err := New(false)
	if err != nil {
		t.Fatal(err)
	}
	m2 = send(t, m2, drain(m2.Init())...)

	if m2.active != conn.Name {
		t.Fatalf("active = %q, want the connection to still open", m2.active)
	}
	if m2.table != "" {
		t.Fatalf("table = %q, want no table opened", m2.table)
	}
	if !logContains(m2, `table "ghosts" not found`) {
		t.Fatalf("command log = %v", m2.commandLog)
	}
}

// esc during the dial cancels the restore: no connection is adopted, and the
// driver the background dial produced is closed rather than leaked.
func TestEscDuringRestoreDialCancels(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	conn := restoreFixtureConn(t)
	writeConfig(t, &config.Config{Connections: []config.Connection{conn}, RestoreSession: true})

	m, err := New(false)
	if err != nil {
		t.Fatal(err)
	}
	m = connectAndOpenWidgets(t, m)
	m.saveSession()
	m.closeSession()

	m2, err := New(false)
	if err != nil {
		t.Fatal(err)
	}
	if m2.restoreSess == nil {
		t.Fatal("restoreSess not set from the saved session")
	}
	next, _ := m2.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m2 = next.(Model)
	// esc before the dial (Init's restoreStartCmd) has even run.
	m2 = send(t, m2, special(tea.KeyEsc, 0))
	if m2.restoreSess != nil {
		t.Fatal("esc did not clear the pending restore")
	}
	// The dial itself, whenever it lands, must not adopt the connection.
	m2 = send(t, m2, drain(m2.Init())...)
	if m2.active != "" {
		t.Fatalf("active = %q, want the cancelled dial to leave nothing connected", m2.active)
	}
	if !logContains(m2, "restore session cancelled") {
		t.Fatalf("command log = %v", m2.commandLog)
	}
}

// restore_session = false in config skips restoration even with a valid
// session file on disk.
func TestRestoreSessionSkippedWhenConfigDisabled(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	conn := restoreFixtureConn(t)
	writeConfig(t, &config.Config{Connections: []config.Connection{conn}, RestoreSession: false})
	if err := session.Save(session.Session{Connection: conn.Name, Table: "widgets"}); err != nil {
		t.Fatal(err)
	}

	m, err := New(false)
	if err != nil {
		t.Fatal(err)
	}
	if m.restoreSess != nil {
		t.Fatal("restoreSess set despite restore_session = false")
	}
}

// A config file that never mentions restore_session at all — the state a
// fresh install starts in — must default to no restore: connecting on
// startup is a side effect the user has to opt into.
func TestRestoreSessionSkippedWhenConfigAbsent(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	conn := restoreFixtureConn(t)
	writeConfig(t, &config.Config{Connections: []config.Connection{conn}})
	if err := session.Save(session.Session{Connection: conn.Name, Table: "widgets"}); err != nil {
		t.Fatal(err)
	}

	m, err := New(false)
	if err != nil {
		t.Fatal(err)
	}
	if m.restoreSess != nil {
		t.Fatal("restoreSess set despite restore_session being absent from config")
	}
	m = send(t, m, drain(m.Init())...)
	if m.active != "" {
		t.Fatalf("active = %q, want no connection made", m.active)
	}
}

// --no-restore skips restoration for this run without touching the config.
func TestNoRestoreFlagSkipsRestoration(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	conn := restoreFixtureConn(t)
	writeConfig(t, &config.Config{Connections: []config.Connection{conn}, RestoreSession: true})
	if err := session.Save(session.Session{Connection: conn.Name, Table: "widgets"}); err != nil {
		t.Fatal(err)
	}

	m, err := New(true)
	if err != nil {
		t.Fatal(err)
	}
	if m.restoreSess != nil {
		t.Fatal("restoreSess set despite noRestore = true")
	}
}

// An AskPassword connection must still prompt before an auto-restore dials
// it — restore reuses the same password gate as an interactive connect,
// never the keyring or a stored value.
func TestRestoreSessionAsksPasswordBeforeDialing(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	conn := config.Connection{
		Name: "asks", Engine: db.EnginePostgres,
		Host: "127.0.0.1", Port: 1, User: "u", AskPassword: true,
	}
	writeConfig(t, &config.Config{Connections: []config.Connection{conn}, RestoreSession: true})
	if err := session.Save(session.Session{Connection: conn.Name, Table: "widgets"}); err != nil {
		t.Fatal(err)
	}

	m, err := New(false)
	if err != nil {
		t.Fatal(err)
	}
	if m.restoreSess == nil {
		t.Fatal("restoreSess not set from the saved session")
	}
	m = send(t, m, drain(m.Init())...)

	if m.active != "" {
		t.Fatalf("active = %q, want no dial before the password prompt", m.active)
	}
	f, ok := m.modal.(*formModal)
	if !ok {
		t.Fatalf("modal = %T, want the password prompt", m.modal)
	}
	if f.field("secret").kind != fieldPassword {
		t.Fatal("prompt field is not masked")
	}
	if m.restoreSess == nil {
		t.Fatal("restoreSess cleared before the password prompt was answered")
	}

	// esc on the prompt abandons the restore, same as esc during a plain
	// dial — it must not leave a stale restoreSess to swallow a later esc.
	m = send(t, m, special(tea.KeyEsc, 0))
	if m.modal != nil {
		t.Fatal("esc did not close the password prompt")
	}
	if m.restoreSess != nil {
		t.Fatal("restoreSess left set after cancelling the password prompt")
	}
}

// Quitting without ever connecting must not clobber a previously saved,
// still-valid session with an empty one.
func TestSaveSessionNoopsWithoutAConnection(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	conn := restoreFixtureConn(t)
	writeConfig(t, &config.Config{Connections: []config.Connection{conn}})

	want := session.Session{Connection: conn.Name, Table: "widgets", Tab: int(mainTabDDL)}
	if err := session.Save(want); err != nil {
		t.Fatal(err)
	}

	m, err := New(false)
	if err != nil {
		t.Fatal(err)
	}
	// m never connects — restoreSess is left in place, as at real startup —
	// and a quit from here must not overwrite the file it came from.
	m.saveSession()

	got, err := session.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || *got != want {
		t.Fatalf("session.Load() = %#v, want the untouched %#v", got, want)
	}
}
