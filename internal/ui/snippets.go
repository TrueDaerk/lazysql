package ui

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/snippets"
)

// Named snippets are the counterpart of the query history: the history
// records everything and prunes itself, a snippet is a statement the user
// deliberately kept under a name. They live in the same state directory
// (internal/snippets) and surface in the same floating pane, which `tab`
// switches between its History and Snippets sections.
//
// Only the SQL text is stored. A snippet never carries a connection, a
// password or any other credential.

// ---------- messages ----------

// snippetsLoadedMsg carries the snippets read at startup.
type snippetsLoadedMsg struct {
	list []snippets.Snippet
	err  error
}

// snippetsWrittenMsg reports a failed snippet write. A successful one
// says nothing: the model already holds the list it saved.
type snippetsWrittenMsg struct{ err error }

// ---------- commands ----------

func loadSnippetsCmd() tea.Cmd {
	return func() tea.Msg {
		list, err := snippets.Load()
		return snippetsLoadedMsg{list: list, err: err}
	}
}

// saveSnippetsCmd rewrites the file. Every change — a save, an overwrite,
// a delete — goes through it: the file is a whole list, not a journal.
func saveSnippetsCmd(list []snippets.Snippet) tea.Cmd {
	saved := append([]snippets.Snippet(nil), list...)
	return func() tea.Msg { return snippetsWrittenMsg{err: snippets.Save(saved)} }
}

// ---------- save flow ----------

// promptSaveSnippet is `ctrl+s` in the editor and `s` on a history entry:
// ask for a name, then store the statement under it. An existing name
// asks before it is replaced — a snippet is kept work, and overwriting it
// silently would lose a statement the user cannot get back from the
// history once it has aged out.
func (m *Model) promptSaveSnippet(sql string) tea.Cmd {
	sql = trimStatement(sql)
	if sql == "" {
		return logCmd("-- save snippet skipped: nothing to save")
	}
	engine := ""
	if m.driver != nil {
		engine = string(m.driver.Engine())
	}
	m.modal = newPromptModal("Save snippet", "name", "", func(mm *Model, name string) tea.Cmd {
		if name == "" {
			return logCmd("-- save snippet skipped: no name given")
		}
		if _, exists := snippets.Find(mm.snippets, name); exists {
			mm.modal = &confirmModal{
				title: "Overwrite snippet",
				body:  fmt.Sprintf("A snippet named %q already exists. Replace its statement?", name),
				onConfirm: func(mmm *Model) tea.Cmd {
					return mmm.storeSnippet(name, sql, engine)
				},
			}
			return nil
		}
		return mm.storeSnippet(name, sql, engine)
	})
	return nil
}

// storeSnippet writes one snippet into the model and to disk. An
// overwrite keeps the original creation time: the name is the identity,
// and the date says how long it has been in use.
func (m *Model) storeSnippet(name, sql, engine string) tea.Cmd {
	s := snippets.Snippet{Name: name, SQL: sql, Engine: engine, CreatedAt: time.Now()}
	if old, ok := snippets.Find(m.snippets, name); ok && !old.CreatedAt.IsZero() {
		s.CreatedAt = old.CreatedAt
	}
	list, replaced := snippets.Put(m.snippets, s)
	m.snippets = list
	verb := "save"
	if replaced {
		verb = "overwrite"
	}
	return tea.Batch(
		saveSnippetsCmd(m.snippets),
		logCmd("-- %s snippet %s: %s", verb, name, truncate(flatten(sql), 60)),
	)
}

// deleteSnippet removes one snippet from the model and the file. The
// confirmation is the pane's; by here the user has answered it.
func (m *Model) deleteSnippet(name string) tea.Cmd {
	list, deleted := snippets.Delete(m.snippets, name)
	if !deleted {
		return nil
	}
	m.snippets = list
	return tea.Batch(
		saveSnippetsCmd(m.snippets),
		logCmd("-- delete snippet %s", name),
	)
}
