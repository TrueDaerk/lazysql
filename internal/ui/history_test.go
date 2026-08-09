package ui

import (
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/history"
)

// withHistoryPane returns a model with three history entries and the
// floating history pane open, reached the way a user reaches it: focus
// the editor panel and press backspace in normal mode. The history file
// is its own temp dir so the assertions are not disturbed by what other
// tests recorded.
func withHistoryPane(t *testing.T) Model {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	m := sized(120, 40)
	at := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	m.history = []history.Entry{
		{SQL: "SELECT 3", Engine: "sqlite", At: at.Add(2 * time.Minute)},
		{SQL: "UPDATE t SET a = 1", Engine: "postgres", At: at.Add(time.Minute)},
		{SQL: "SELECT 1", Engine: "sqlite", At: at},
	}
	m = send(t, m, press('4'), special(tea.KeyBackspace, 0))
	if _, ok := m.modal.(*historyModal); !ok {
		t.Fatalf("backspace opened %T, want the history pane", m.modal)
	}
	return m
}

func TestHistoryPaneListsNewestFirst(t *testing.T) {
	m := withHistoryPane(t)
	hm := m.modal.(*historyModal)
	if len(hm.entries) != 3 {
		t.Fatalf("pane has %d rows, want 3", len(hm.entries))
	}
	if hm.entries[0].SQL != "SELECT 3" || hm.entries[2].SQL != "SELECT 1" {
		t.Fatalf("rows = %#v, want newest first", hm.entries)
	}
	// The highlighted rows carry escape codes between tokens, so the view
	// is checked token-wise: the selected row (one style, contiguous) in
	// full, the others by their leading keyword.
	out := m.View().Content
	if !strings.Contains(out, "SELECT 3") || !strings.Contains(out, "UPDATE") {
		t.Fatalf("pane view is missing entries:\n%s", out)
	}
}

func TestHistoryPaneEscCloses(t *testing.T) {
	m := withHistoryPane(t)
	m = send(t, m, special(tea.KeyEscape, 0))
	if m.modal != nil {
		t.Fatalf("esc left %T open", m.modal)
	}
	if m.focus != panelQuery {
		t.Fatalf("focus = %v, want the editor the pane was opened from", m.focus)
	}
}

func TestHistoryPaneOnlyOpensFromNormalMode(t *testing.T) {
	m := sized(120, 40)
	m = send(t, m, press(':'))
	m = send(t, m, press('a'), special(tea.KeyBackspace, 0))
	if m.modal != nil {
		t.Fatalf("backspace in insert mode opened %T, want it to delete a character", m.modal)
	}
	if m.script() != "" {
		t.Fatalf("buffer = %q, want backspace to have deleted the typed rune", m.script())
	}
}

func TestHistoryPaneLoadIntoEditor(t *testing.T) {
	m := withHistoryPane(t)
	m = send(t, m, press('j'), press('e'))
	if m.modal != nil {
		t.Fatalf("e left %T open", m.modal)
	}
	if m.script() != "UPDATE t SET a = 1" {
		t.Fatalf("editor holds %q, want the selected entry", m.script())
	}
	if m.editor.editing {
		t.Fatal("loading a statement started insert mode")
	}
}

func TestHistoryPaneDeleteRemovesOneEntryAndPersists(t *testing.T) {
	m := withHistoryPane(t)
	m = send(t, m, press('j'), press('d'))

	hm := m.modal.(*historyModal)
	if len(hm.entries) != 2 {
		t.Fatalf("pane rows = %#v, want the middle entry gone", hm.entries)
	}
	if len(m.history) != 2 {
		t.Fatalf("history = %#v, want the middle entry gone", m.history)
	}
	for _, e := range m.history {
		if e.SQL == "UPDATE t SET a = 1" {
			t.Fatalf("the deleted entry is still there: %#v", m.history)
		}
	}
	path, err := history.Path()
	if err != nil {
		t.Fatal(err)
	}
	saved, err := history.LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 2 {
		t.Fatalf("the file holds %d entries, want the delete written through", len(saved))
	}
}

func TestHistoryPaneShowsEngineAndTimestamp(t *testing.T) {
	m := withHistoryPane(t)
	m = send(t, m, press('j'))
	out := m.modal.view(m.style, 120, 40)
	if !strings.Contains(out, "postgres") {
		t.Fatalf("detail is missing the engine:\n%s", out)
	}
	if !strings.Contains(out, "2026") {
		t.Fatalf("detail is missing the timestamp:\n%s", out)
	}
}

func TestHistoryPersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)

	m := sized(120, 40)
	m = send(t, m, historyEntryMsg{statement: "SELECT persisted"})
	if len(m.history) != 1 {
		t.Fatalf("history = %#v, want the recorded statement", m.history)
	}

	// A fresh model loads it back through Init, the way a restart does.
	next := sized(120, 40)
	next = send(t, next, drainInit(t, next)...)
	if len(next.history) != 1 || next.history[0].SQL != "SELECT persisted" {
		t.Fatalf("reloaded history = %#v, want the statement from the previous run", next.history)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal(err)
	}
}

// drainInit runs Init and returns the messages it produced.
func drainInit(t *testing.T, m Model) []tea.Msg {
	t.Helper()
	return drain(m.Init())
}

// The panel list has no history panel any more: digits stop at [4] and
// tab cycles the four panels without a gap.
func TestPanelNumberingHasNoHistoryPanel(t *testing.T) {
	m := sized(120, 40)
	out := m.View().Content
	if strings.Contains(out, "Query history") {
		t.Fatalf("the layout still has a history panel:\n%s", out)
	}
	if !strings.Contains(out, "[4] Query") {
		t.Fatalf("the layout has no [4] Query panel:\n%s", out)
	}
	m = send(t, m, press('5'))
	if m.focus != panelConnections {
		t.Fatalf("`5` moved focus to %v, want it ignored", m.focus)
	}
}

func TestTrimStatement(t *testing.T) {
	cases := map[string]string{
		"SELECT 1;":        "SELECT 1",
		"  SELECT 1 ;; ":   "SELECT 1",
		"SELECT 1":         "SELECT 1",
		"SELECT ';'":       "SELECT ';'",
		"   ":              "",
		"SELECT a\nFROM t": "SELECT a\nFROM t",
	}
	for in, want := range cases {
		if got := trimStatement(in); got != want {
			t.Errorf("trimStatement(%q) = %q, want %q", in, got, want)
		}
	}
}
