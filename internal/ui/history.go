package ui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/history"
)

// The query history is the on-disk record of every statement lazysql
// executed — a browsing page, a committed changeset, a script from the
// editor — newest first. It has no panel of its own: the editor's normal
// mode opens it as a floating pane (see historypane.go) with `backspace`,
// where an entry can be re-executed directly.

// ---------- messages ----------

// historyLoadedMsg carries the history read at startup.
type historyLoadedMsg struct {
	entries []history.Entry
	err     error
}

// historyWrittenMsg reports a failed history write. A successful one
// says nothing: the model already holds the entry.
type historyWrittenMsg struct{ err error }

// ---------- commands ----------

func loadHistoryCmd() tea.Cmd {
	return func() tea.Msg {
		entries, err := history.Load()
		return historyLoadedMsg{entries: entries, err: err}
	}
}

func appendHistoryCmd(e history.Entry) tea.Cmd {
	return func() tea.Msg { return historyWrittenMsg{err: history.Append(e)} }
}

// saveHistoryCmd rewrites the file after a delete or a clear. entries
// are newest first, the order the model holds them in.
func saveHistoryCmd(entries []history.Entry) tea.Cmd {
	saved := append([]history.Entry(nil), entries...)
	return func() tea.Msg { return historyWrittenMsg{err: history.Save(saved)} }
}

// ---------- model wiring ----------

// recordHistory adds one executed statement to the front of the history
// and persists it. Re-running the newest entry does not duplicate it:
// `enter` in the history pane would otherwise grow the list by one on
// every replay, and the timestamp is the only thing that would differ.
func (m *Model) recordHistory(sql string) tea.Cmd {
	sql = trimStatement(sql)
	if sql == "" {
		return nil
	}
	engine := ""
	if m.driver != nil {
		engine = string(m.driver.Engine())
	}
	if len(m.history) > 0 && m.history[0].SQL == sql && m.history[0].Engine == engine {
		return nil
	}
	e := history.Entry{SQL: sql, Engine: engine, At: time.Now()}
	m.history = append([]history.Entry{e}, m.history...)
	if len(m.history) > history.MaxEntries {
		m.history = m.history[:history.MaxEntries]
	}
	return appendHistoryCmd(e)
}

// historyLabel is one row of the history pane: when it ran, then the
// statement on one line. The engine is deliberately not in the row — the
// statement is what the eye scans for — it is in the pane's detail area
// for the selected entry instead.
func historyLabel(e history.Entry) string {
	return e.At.Local().Format("15:04") + "  " + flatten(e.SQL)
}

// trimStatement normalizes a statement before it is stored: trailing
// whitespace and a trailing `;` are noise the editor re-adds anyway, and
// keeping them would make the same statement look like two entries.
func trimStatement(sql string) string {
	s := strings.TrimSpace(sql)
	for strings.HasSuffix(s, ";") {
		s = strings.TrimSpace(strings.TrimSuffix(s, ";"))
	}
	return s
}
