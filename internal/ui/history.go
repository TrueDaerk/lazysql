package ui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/history"
)

// Panel [4] Query history is the on-disk history rendered newest first.
// Every statement lazysql executes — a browsing page, a committed
// changeset, a script from the editor — lands here through
// historyEntryMsg, so the panel is a record of the session and of every
// session before it.
//
// The model keeps the entries themselves and derives the panel's rows
// from them: the panel is a list of strings, but `enter` has to reload
// the statement exactly as it was typed, newlines included.

// ---------- messages ----------

// historyLoadedMsg carries the history read at startup.
type historyLoadedMsg struct {
	entries []history.Entry
	err     error
}

// historyWrittenMsg reports a failed history write. A successful one
// says nothing: the panel already shows the entry.
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
// `enter` on the history panel would otherwise grow the list by one on
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
	m.refreshHistory()
	return appendHistoryCmd(e)
}

// refreshHistory repaints panel [4] from m.history, keeping the cursor
// on the entry it was on when that entry is still there.
func (m *Model) refreshHistory() {
	p := m.panels[panelHistory]
	labels := make([]string, len(m.history))
	for i, e := range m.history {
		labels[i] = historyLabel(e)
	}
	// A new entry goes to the front, so index-based restoration would
	// drift; setItems restores by name, which is what the panel already
	// does everywhere else.
	p.setItems(labels)
}

// historyLabel is one row of the panel: when it ran, then the statement
// on one line. The engine is deliberately not in the row — the side
// column is narrow and the statement is what the eye scans for — it is
// in the main view's detail for the selected entry instead.
func historyLabel(e history.Entry) string {
	return e.At.Local().Format("15:04") + "  " + flatten(e.SQL)
}

// selectedHistory returns the entry under the cursor of panel [4]. The
// panel indexes into its filtered rows, so the position is translated
// back through the filter rather than matched by text: two runs of the
// same statement produce the same row.
func (m Model) selectedHistory() (history.Entry, bool) {
	i := m.panels[panelHistory].sourceIndex(m.panels[panelHistory].cursor)
	if i < 0 || i >= len(m.history) {
		return history.Entry{}, false
	}
	return m.history[i], true
}

// deleteHistoryEntry is `d` on panel [4]. One line is cheap to lose and
// cheap to recreate, so it goes without a confirmation; `D` clears the
// whole history and does ask.
func (m *Model) deleteHistoryEntry() tea.Cmd {
	p := m.panels[panelHistory]
	i := p.sourceIndex(p.cursor)
	if i < 0 || i >= len(m.history) {
		return logCmd("-- no history entry under the cursor")
	}
	e := m.history[i]
	row := p.cursor
	m.history = append(m.history[:i:i], m.history[i+1:]...)
	m.refreshHistory()
	// setItems restores the selection by name, and the name just went
	// away; deleting a run of entries should walk down the list rather
	// than jump back to the top after every keystroke.
	p.cursor = row
	p.move(0)
	return tea.Batch(
		saveHistoryCmd(m.history),
		logCmd("-- delete history entry: %s", truncate(flatten(e.SQL), 60)),
	)
}

// clearHistory is `D`: drop every entry, on disk too.
func (m *Model) clearHistory() tea.Cmd {
	n := len(m.history)
	m.history = nil
	m.panels[panelHistory].clearFilter()
	m.refreshHistory()
	return tea.Batch(
		saveHistoryCmd(nil),
		logCmd("-- clear query history (%d entries)", n),
	)
}

// historyDetail is the main view while panel [4] is focused: the
// selected statement in full, with the engine and timestamp the row has
// no room for.
func (m Model) historyDetail(w, h int) string {
	lines := []string{
		m.style.titleFocused.Render(panelTitles[panelHistory]) + m.style.muted.Render(" — main view"),
		"",
	}
	e, ok := m.selectedHistory()
	if !ok {
		lines = append(lines, m.style.muted.Render(
			"no history yet — press : to open the query editor"))
		return joinTruncated(lines, w, h)
	}
	engine := e.Engine
	if engine == "" {
		engine = "(unknown)"
	}
	lines = append(lines,
		"engine  "+engine,
		"ran at  "+e.At.Local().Format(time.RFC1123),
		fmt.Sprintf("entry   %d of %d", m.panels[panelHistory].cursor+1, len(m.history)),
		"",
	)
	lines = append(lines, strings.Split(e.SQL, "\n")...)
	lines = append(lines, "",
		m.style.muted.Render("enter loads it into the editor · x runs it · d deletes it"))
	return joinTruncated(lines, w, h)
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
