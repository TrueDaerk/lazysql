package ui

import (
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/history"
)

// withHistory returns a model whose panel [4] holds three entries and is
// focused, with its own history file so the assertions are not disturbed
// by what other tests recorded.
func withHistory(t *testing.T) Model {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	m := sized(120, 40)
	at := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	m.history = []history.Entry{
		{SQL: "SELECT 3", Engine: "sqlite", At: at.Add(2 * time.Minute)},
		{SQL: "UPDATE t SET a = 1", Engine: "postgres", At: at.Add(time.Minute)},
		{SQL: "SELECT 1", Engine: "sqlite", At: at},
	}
	m.refreshHistory()
	return send(t, m, press('4'))
}

func TestHistoryPanelListsNewestFirst(t *testing.T) {
	m := withHistory(t)
	items := m.panels[panelHistory].items
	if len(items) != 3 {
		t.Fatalf("panel has %d rows, want 3", len(items))
	}
	if !strings.Contains(items[0], "SELECT 3") || !strings.Contains(items[2], "SELECT 1") {
		t.Fatalf("rows = %q, want newest first", items)
	}
}

func TestHistoryEnterLoadsIntoTheEditor(t *testing.T) {
	m := withHistory(t)
	m = send(t, m, press('j'), special(tea.KeyEnter, 0))
	qm, ok := m.modal.(*queryModal)
	if !ok {
		t.Fatalf("enter opened %T, want the query editor", m.modal)
	}
	if qm.area.Value() != "UPDATE t SET a = 1" {
		t.Fatalf("editor holds %q, want the selected entry", qm.area.Value())
	}
	if m.draft != "UPDATE t SET a = 1" {
		t.Fatalf("draft = %q, want the loaded statement", m.draft)
	}
	// Loading is not running.
	if logContains(m, "rows affected") {
		t.Fatalf("enter executed the statement: %v", m.commandLog)
	}
}

func TestHistoryDeleteRemovesOneEntryAndPersists(t *testing.T) {
	m := withHistory(t)
	m = send(t, m, press('j'), press('d'))

	if len(m.history) != 2 {
		t.Fatalf("history = %#v, want the middle entry gone", m.history)
	}
	for _, e := range m.history {
		if e.SQL == "UPDATE t SET a = 1" {
			t.Fatalf("the deleted entry is still there: %#v", m.history)
		}
	}
	// The cursor walks down the list rather than snapping back to the top.
	if m.panels[panelHistory].cursor != 1 {
		t.Fatalf("cursor = %d, want to stay where the deleted row was",
			m.panels[panelHistory].cursor)
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

func TestHistoryClearAsksAndEmptiesTheFile(t *testing.T) {
	m := withHistory(t)
	m = send(t, m, press('D'))
	if _, ok := m.modal.(*confirmModal); !ok {
		t.Fatalf("D opened %T, want a confirm modal", m.modal)
	}
	if len(m.history) != 3 {
		t.Fatal("D cleared the history before it was confirmed")
	}
	m = send(t, m, special(tea.KeyEnter, 0))
	if len(m.history) != 0 || len(m.panels[panelHistory].items) != 0 {
		t.Fatalf("history = %#v, want it empty", m.history)
	}
	path, err := history.Path()
	if err != nil {
		t.Fatal(err)
	}
	saved, err := history.LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 0 {
		t.Fatalf("the file still holds %d entries", len(saved))
	}
}

func TestHistoryFuzzyFilterAndDeleteAgree(t *testing.T) {
	m := withHistory(t)
	m = send(t, m, press('/'), press('u'), press('p'), press('d'))
	items := m.panels[panelHistory].items
	if len(items) != 1 || !strings.Contains(items[0], "UPDATE") {
		t.Fatalf("filtered rows = %q, want only the UPDATE", items)
	}
	// enter leaves `/` input mode and keeps the narrowing, so `d` acts on
	// the filtered row and not on the one at that index in the full list.
	m = send(t, m, special(tea.KeyEnter, 0))
	m = send(t, m, press('d'))
	if len(m.history) != 2 {
		t.Fatalf("history = %#v, want one entry deleted", m.history)
	}
	for _, e := range m.history {
		if strings.HasPrefix(e.SQL, "UPDATE") {
			t.Fatalf("d deleted the wrong entry through the filter: %#v", m.history)
		}
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

func TestHistoryDetailShowsEngineAndTimestamp(t *testing.T) {
	m := withHistory(t)
	m = send(t, m, press('j'))
	out := m.historyDetail(80, 20)
	if !strings.Contains(out, "postgres") {
		t.Fatalf("detail is missing the engine:\n%s", out)
	}
	if !strings.Contains(out, "UPDATE t SET a = 1") {
		t.Fatalf("detail is missing the statement:\n%s", out)
	}
	if !strings.Contains(out, "2026") {
		t.Fatalf("detail is missing the timestamp:\n%s", out)
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
