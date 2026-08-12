package ui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/history"
)

// The filter history is the on-disk record of the WHERE clauses that
// have been applied, keyed by connection + database + relation — the
// same Entry fields the query editor's history is filtered by, one level
// finer. It backs the recall keys of the inline `/` line and nothing
// else: it has no pane, because the one place a table's filters are
// worth reading is the line where the next one is typed.

// ---------- messages ----------

// filtersLoadedMsg carries the filter history read at startup.
type filtersLoadedMsg struct {
	entries []history.Entry
	err     error
}

// filtersWrittenMsg reports a failed write. A successful one says
// nothing: the model already holds the entry.
type filtersWrittenMsg struct{ err error }

// ---------- commands ----------

func loadFiltersCmd() tea.Cmd {
	return func() tea.Msg {
		entries, err := history.LoadFilters()
		return filtersLoadedMsg{entries: entries, err: err}
	}
}

func appendFilterCmd(e history.Entry) tea.Cmd {
	return func() tea.Msg { return filtersWrittenMsg{err: history.AppendFilter(e)} }
}

// saveFiltersCmd rewrites the file after a de-duplication or a trim.
// entries are newest first, the order the model holds them in.
func saveFiltersCmd(entries []history.Entry) tea.Cmd {
	saved := append([]history.Entry(nil), entries...)
	return func() tea.Msg { return filtersWrittenMsg{err: history.SaveFilters(saved)} }
}

// ---------- model wiring ----------

// filterScope is the relation the filter history is keyed by. ok is
// false when there is nothing to key on — no connection, or a query
// result rather than a browsed relation — which is what makes both
// recording and recall no-ops there.
func (m Model) filterScope() (conn, database, table string, ok bool) {
	if m.active == "" || !m.data.browsing() {
		return "", "", "", false
	}
	return m.active, m.data.database, m.data.table, true
}

// filterHistory is the recall list of the open relation, newest first.
func (m Model) filterHistory() []string {
	conn, database, table, ok := m.filterScope()
	if !ok {
		return nil
	}
	entries := history.InRelation(m.filters, conn, database, table)
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.SQL)
	}
	return out
}

// recordFilter adds one applied clause to the front of the open
// relation's recall list and persists it.
//
// A clause that is already in the list moves to the front rather than
// being stored twice — re-applying the same filter would otherwise push
// every other one down by a slot per press — and that is also why the
// write is sometimes a rewrite: an append can only add a line, never
// remove the older copy or the entry a full relation has grown past.
func (m *Model) recordFilter(where string) tea.Cmd {
	where = strings.TrimSpace(where)
	conn, database, table, ok := m.filterScope()
	if where == "" || !ok {
		return nil
	}
	engine := ""
	if m.driver != nil {
		engine = string(m.driver.Engine())
	}
	e := history.Entry{
		SQL:        where,
		Connection: conn,
		Engine:     engine,
		At:         time.Now(),
		Database:   database,
		Table:      table,
	}

	kept := make([]history.Entry, 0, len(m.filters)+1)
	dropped := false
	for _, old := range m.filters {
		if old.Connection == conn && old.Database == database && old.Table == table && old.SQL == where {
			dropped = true
			continue
		}
		kept = append(kept, old)
	}
	m.filters = append([]history.Entry{e}, kept...)
	trimmed := history.TrimRelation(m.filters, conn, database, table, history.MaxRelationEntries)
	if len(trimmed) != len(m.filters) {
		m.filters = trimmed
		dropped = true
	}
	if dropped {
		return saveFiltersCmd(m.filters)
	}
	return appendFilterCmd(e)
}
