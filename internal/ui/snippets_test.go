package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/history"
	"lazysql/internal/snippets"
)

// typeInto sends a string one key press at a time, the way a prompt is
// filled in.
func typeKeys(t *testing.T, m Model, text string) Model {
	t.Helper()
	for _, r := range text {
		m = send(t, m, press(r))
	}
	return m
}

// withSnippets returns a model whose state directory is a temp dir and
// which already holds two named snippets.
func withSnippets(t *testing.T) Model {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	m := sized(120, 40)
	at := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	m.snippets = []snippets.Snippet{
		{Name: "active users", SQL: "SELECT * FROM users WHERE active", Engine: "sqlite", CreatedAt: at},
		{Name: "order by id", SQL: "SELECT *\nFROM orders\nWHERE id = :id", Engine: "sqlite", CreatedAt: at},
	}
	return m
}

// snippetsPane opens the floating pane and switches it to the Snippets
// section, the way a user does: `4`, backspace, tab.
func snippetsPane(t *testing.T, m Model) (Model, *historyModal) {
	t.Helper()
	m = send(t, m, press('4'), special(tea.KeyBackspace, 0), special(tea.KeyTab, 0))
	hm, ok := m.modal.(*historyModal)
	if !ok {
		t.Fatalf("the pane is %T, want the history pane", m.modal)
	}
	if hm.section != sectionSnippets {
		t.Fatal("tab did not switch to the Snippets section")
	}
	return m, hm
}

// ---------- saving ----------

func TestSaveSnippetFromTheEditorPersists(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)

	m := sized(120, 40)
	m = send(t, m, press(':'))
	m = typeKeys(t, m, "SELECT 1")
	// From normal mode, the way the panel's action list reaches it; the
	// insert-mode path is covered separately.
	m = send(t, m, special(tea.KeyEscape, 0), ctrl('s'))
	if _, ok := m.modal.(*promptModal); !ok {
		t.Fatalf("ctrl+s opened %T, want the name prompt", m.modal)
	}
	m = typeKeys(t, m, "one")
	m = send(t, m, special(tea.KeyEnter, 0))

	if m.modal != nil {
		t.Fatalf("saving left %T open", m.modal)
	}
	if len(m.snippets) != 1 || m.snippets[0].Name != "one" || m.snippets[0].SQL != "SELECT 1" {
		t.Fatalf("snippets = %#v, want the named buffer", m.snippets)
	}

	// A fresh model loads it back through Init, the way a restart does.
	next := sized(120, 40)
	next = send(t, next, drainInit(t, next)...)
	if len(next.snippets) != 1 || next.snippets[0].SQL != "SELECT 1" {
		t.Fatalf("reloaded snippets = %#v, want the one from the previous run", next.snippets)
	}
}

func TestSaveSnippetKeepsInsertModeAndTheBuffer(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := sized(120, 40)
	m = send(t, m, press(':'))
	m = typeKeys(t, m, "SELECT 2")
	m = send(t, m, ctrl('s'))
	m = typeKeys(t, m, "two")
	m = send(t, m, special(tea.KeyEnter, 0))

	if !m.editor.editing {
		t.Error("saving a snippet ended insert mode")
	}
	if m.script() != "SELECT 2" {
		t.Errorf("buffer = %q, want the statement untouched", m.script())
	}
}

func TestSaveSnippetRefusesAnEmptyBuffer(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := sized(120, 40)
	m = send(t, m, press('4'), ctrl('s'))
	if m.modal != nil {
		t.Fatalf("ctrl+s on an empty buffer opened %T, want nothing to save", m.modal)
	}
	if len(m.snippets) != 0 {
		t.Fatalf("snippets = %#v, want none", m.snippets)
	}
}

func TestSaveSnippetRefusesAnEmptyName(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := sized(120, 40)
	m = send(t, m, press(':'))
	m = typeKeys(t, m, "SELECT 3")
	m = send(t, m, ctrl('s'), special(tea.KeyEnter, 0))
	if len(m.snippets) != 0 {
		t.Fatalf("snippets = %#v, want an unnamed save refused", m.snippets)
	}
}

func TestDuplicateNamePromptsBeforeOverwrite(t *testing.T) {
	m := withSnippets(t)
	m = send(t, m, press(':'))
	m = typeKeys(t, m, "SELECT replacement")
	m = send(t, m, ctrl('s'))
	m = typeKeys(t, m, "active users")
	m = send(t, m, special(tea.KeyEnter, 0))

	cm, ok := m.modal.(*confirmModal)
	if !ok {
		t.Fatalf("an existing name opened %T, want the overwrite confirm", m.modal)
	}
	if !strings.Contains(cm.body, "active users") {
		t.Errorf("the confirm does not name the snippet: %q", cm.body)
	}
	if got, _ := snippets.Find(m.snippets, "active users"); got.SQL == "SELECT replacement" {
		t.Fatal("the snippet was overwritten before the confirm was answered")
	}

	m = send(t, m, press('y'))
	got, ok := snippets.Find(m.snippets, "active users")
	if !ok || got.SQL != "SELECT replacement" {
		t.Fatalf("after the confirm: %#v, want the replacement statement", got)
	}
	if len(m.snippets) != 2 {
		t.Fatalf("snippets = %#v, want the overwrite not to add an entry", m.snippets)
	}
	// The name is the identity; the creation date says how long it has
	// been in use, so an overwrite keeps it.
	if !got.CreatedAt.Equal(time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("CreatedAt = %v, want the original creation time", got.CreatedAt)
	}
}

func TestOverwriteCancelKeepsTheOldStatement(t *testing.T) {
	m := withSnippets(t)
	m = send(t, m, press(':'))
	m = typeKeys(t, m, "SELECT replacement")
	m = send(t, m, ctrl('s'))
	m = typeKeys(t, m, "active users")
	m = send(t, m, special(tea.KeyEnter, 0), special(tea.KeyEscape, 0))

	got, _ := snippets.Find(m.snippets, "active users")
	if got.SQL != "SELECT * FROM users WHERE active" {
		t.Fatalf("SQL = %q, want the cancelled overwrite to have changed nothing", got.SQL)
	}
}

func TestSaveSnippetFromAHistoryEntry(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := sized(120, 40)
	m.history = []history.Entry{
		{SQL: "SELECT from history", Engine: "sqlite", At: time.Now()},
	}
	m = send(t, m, press('4'), special(tea.KeyBackspace, 0), press('s'))
	if _, ok := m.modal.(*promptModal); !ok {
		t.Fatalf("`s` in the history section opened %T, want the name prompt", m.modal)
	}
	m = typeKeys(t, m, "kept")
	m = send(t, m, special(tea.KeyEnter, 0))

	got, ok := snippets.Find(m.snippets, "kept")
	if !ok || got.SQL != "SELECT from history" {
		t.Fatalf("snippets = %#v, want the history entry saved under its name", m.snippets)
	}
}

// ---------- the pane's Snippets section ----------

func TestPaneTabSwitchesSections(t *testing.T) {
	m := withSnippets(t)
	m, hm := snippetsPane(t, m)
	out := hm.view(m.style, 120, 40)
	if !strings.Contains(out, "active users") || !strings.Contains(out, "order by id") {
		t.Fatalf("the Snippets section is missing its names:\n%s", out)
	}

	m = send(t, m, special(tea.KeyTab, 0))
	if m.modal.(*historyModal).section != sectionHistory {
		t.Fatal("a second tab did not switch back to the history")
	}
}

func TestSnippetsSectionShowsAnEmptyHint(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := sized(120, 40)
	m, hm := snippetsPane(t, m)
	if out := hm.view(m.style, 120, 40); !strings.Contains(out, "no snippets yet") {
		t.Fatalf("an empty Snippets section shows:\n%s", out)
	}
}

func TestSnippetLoadIntoEditor(t *testing.T) {
	m := withSnippets(t)
	m, _ = snippetsPane(t, m)
	m = send(t, m, press('j'), press('e'))
	if m.modal != nil {
		t.Fatalf("`e` left %T open", m.modal)
	}
	if m.script() != "SELECT *\nFROM orders\nWHERE id = :id" {
		t.Fatalf("editor holds %q, want the selected snippet", m.script())
	}
	if m.editor.editing {
		t.Error("loading a snippet started insert mode")
	}
}

func TestSnippetEnterRunsThroughSubmitQuery(t *testing.T) {
	m := withSnippets(t)
	m, _ = snippetsPane(t, m)
	// Disconnected, `enter` cannot run anything — what it must not do is
	// load the buffer instead, which is `e`'s job.
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.modal != nil {
		t.Fatalf("`enter` left %T open", m.modal)
	}
	if m.script() != "" {
		t.Fatalf("buffer = %q, want `enter` to run rather than load", m.script())
	}
	if log := strings.Join(logText(m), "\n"); !strings.Contains(log, "not connected") {
		t.Fatalf("the run did not go through submitQuery:\n%s", log)
	}
}

func TestSnippetDeleteAsksAndPersists(t *testing.T) {
	m := withSnippets(t)
	m, _ = snippetsPane(t, m)
	m = send(t, m, press('d'))

	if _, ok := m.modal.(*confirmModal); !ok {
		t.Fatalf("`d` opened %T, want a confirm before a snippet is lost", m.modal)
	}
	if len(m.snippets) != 2 {
		t.Fatal("the snippet was deleted before the confirm was answered")
	}

	m = send(t, m, press('y'))
	if len(m.snippets) != 1 || m.snippets[0].Name != "order by id" {
		t.Fatalf("snippets = %#v, want only the other one left", m.snippets)
	}
	path, err := snippets.Path()
	if err != nil {
		t.Fatal(err)
	}
	saved, err := snippets.LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 1 || saved[0].Name != "order by id" {
		t.Fatalf("the file holds %#v, want the delete written through", saved)
	}
}

func TestSnippetDeleteCancelKeepsIt(t *testing.T) {
	m := withSnippets(t)
	m, _ = snippetsPane(t, m)
	m = send(t, m, press('d'), special(tea.KeyEscape, 0))
	if len(m.snippets) != 2 {
		t.Fatalf("snippets = %#v, want the cancelled delete to have changed nothing", m.snippets)
	}
}

func TestSnippetsPaneKeepsAPerSectionCursor(t *testing.T) {
	m := withSnippets(t)
	m.history = []history.Entry{
		{SQL: "SELECT 1", Engine: "sqlite", At: time.Now()},
		{SQL: "SELECT 2", Engine: "sqlite", At: time.Now()},
	}
	m, _ = snippetsPane(t, m)
	m = send(t, m, press('j'))
	hm := m.modal.(*historyModal)
	if hm.cursor[sectionSnippets] != 1 {
		t.Fatalf("snippet cursor = %d, want the second row", hm.cursor[sectionSnippets])
	}
	m = send(t, m, special(tea.KeyTab, 0))
	hm = m.modal.(*historyModal)
	if hm.cursor[sectionHistory] != 0 {
		t.Fatalf("history cursor = %d, want its own position", hm.cursor[sectionHistory])
	}
	m = send(t, m, special(tea.KeyTab, 0))
	if m.modal.(*historyModal).cursor[sectionSnippets] != 1 {
		t.Fatal("switching sections lost the snippet cursor")
	}
}

// The save key must be documented where every other editor key is.
func TestSaveSnippetIsDocumented(t *testing.T) {
	k := newKeyMap()
	var inActions bool
	for _, a := range k.panelActions(panelQuery) {
		if a.id == actSaveSnippet {
			inActions = true
		}
	}
	if !inActions {
		t.Error("the editor's action list has no save-snippet entry")
	}
	var inInsert bool
	for _, b := range k.editorInsert() {
		if b.Help().Key == k.SaveSnippet.Help().Key {
			inInsert = true
		}
	}
	if !inInsert {
		t.Error("insert mode's key list does not document ctrl+s")
	}
}
