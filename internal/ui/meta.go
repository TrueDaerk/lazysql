package ui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/db"
)

// mainTab is one view of the open relation. The Data tab is the paged
// grid; the other three are introspection views that share a single
// metadata fetch.
type mainTab int

const (
	mainTabData mainTab = iota
	mainTabStructure
	mainTabIndexes
	mainTabDDL
	mainTabCount
)

var mainTabNames = [mainTabCount]string{"Data", "Structure", "Indexes", "DDL"}

// metadata reports whether the tab is served by the metadata fetch
// rather than by the paged data query.
func (t mainTab) metadata() bool { return t != mainTabData }

// metaView is the state behind the Structure, Indexes and DDL tabs.
// All three come from one round trip: a table's columns, indexes,
// foreign keys and DDL are read together the first time any of those
// tabs is opened, and reused until the relation changes or the user
// asks for a reload.
type metaView struct {
	// Identity of the metadata on screen; replies that do not match
	// belong to a relation the user has already moved past.
	conn     string
	database string
	table    string

	cols    []db.Column
	indexes []db.Index
	fks     []db.ForeignKey
	ddl     string

	// ddlErr is kept apart from err: an engine can describe a relation
	// perfectly well and still refuse to produce a CREATE statement for
	// it, which costs the DDL tab and nothing else.
	ddlErr string

	loaded  bool
	loading bool
	err     string

	// row is the cursor (Structure) or the scroll offset (Indexes, DDL)
	// of each tab, so switching tabs back and forth keeps the position.
	row [mainTabCount]int

	// afterLoad is the action to re-run once the metadata lands, so keys
	// that need it work on a tab that has never been opened: `y` needs
	// the DDL, and `e`/`d`/`n`/`D` need the primary key and the column
	// list. actNone means nothing is waiting.
	afterLoad actionID

	req int
}

// ---------- messages ----------

// metaLoadedMsg carries one metadata fetch: everything the three
// introspection tabs render.
type metaLoadedMsg struct {
	req      int
	conn     string
	database string
	table    string

	cols    []db.Column
	indexes []db.Index
	fks     []db.ForeignKey
	ddl     string
	ddlErr  error
	err     error
}

// ---------- commands ----------

// loadMetaCmd reads the columns, indexes, foreign keys and DDL of one
// relation. The three introspection tabs are cheap enough to fetch
// together that splitting them into separate round trips would only buy
// a more complicated loading state.
func loadMetaCmd(drv db.Driver, v metaView, req int) tea.Cmd {
	if drv == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), browseTimeout)
		defer cancel()

		out := metaLoadedMsg{req: req, conn: v.conn, database: v.database, table: v.table}
		if out.cols, out.err = drv.TableColumns(ctx, v.database, v.table); out.err != nil {
			return out
		}
		if out.indexes, out.err = drv.TableIndexes(ctx, v.database, v.table); out.err != nil {
			return out
		}
		if out.fks, out.err = drv.TableForeignKeys(ctx, v.database, v.table); out.err != nil {
			return out
		}
		out.ddl, out.ddlErr = drv.TableDDL(ctx, v.database, v.table)
		return out
	}
}

// copyDDLCmd puts a DDL statement on the system clipboard and reports
// the outcome in the command log.
func copyDDLCmd(table, ddl string) tea.Cmd {
	return func() tea.Msg {
		if err := clipboardWrite(ddl); err != nil {
			return commandLogMsg{line: fmt.Sprintf("-- copy DDL of %s FAILED: %v", table, err)}
		}
		return commandLogMsg{
			line: fmt.Sprintf("-- copy DDL of %s to clipboard (%d bytes)", table, len(ddl)),
		}
	}
}

// ---------- model wiring ----------

// resetMeta drops the cached metadata because the relation under it
// changed. The selected tab survives — walking down a table list with
// the Structure tab open is the point of having tabs — but everything
// that described the previous relation, scroll positions included, goes.
func (m *Model) resetMeta() {
	m.meta = metaView{req: m.meta.req}
}

// ensureMeta starts the metadata fetch when the selected tab needs it
// and nothing has loaded it yet. It is a no-op on the Data tab, on an
// in-flight load and on already-loaded metadata.
func (m *Model) ensureMeta() tea.Cmd {
	if !m.tab.metadata() || m.driver == nil || !m.data.open() {
		return nil
	}
	if m.meta.loaded || m.meta.loading {
		return nil
	}
	return m.startMetaLoad()
}

// startMetaLoad fires the fetch unconditionally. Callers decide whether
// the cache made it unnecessary.
func (m *Model) startMetaLoad() tea.Cmd {
	if m.driver == nil || !m.data.open() {
		return nil
	}
	m.meta.req++
	m.meta.conn, m.meta.database, m.meta.table = m.active, m.data.database, m.data.table
	m.meta.loaded, m.meta.loading = false, true
	m.meta.err = ""
	return tea.Batch(
		logCmd("-- introspect %s.%s (columns, indexes, foreign keys, DDL)",
			displayDatabase(m.meta.database), m.meta.table),
		loadMetaCmd(m.driver, m.meta, m.meta.req),
	)
}

// reloadMeta forces a fresh fetch; it is what `R` does on the three
// introspection tabs.
func (m *Model) reloadMeta() tea.Cmd {
	m.meta.loaded = false
	m.meta.loading = false
	return m.ensureMeta()
}

// freshMeta reports whether a reply still belongs to the relation on
// screen.
func (m Model) freshMeta(msg metaLoadedMsg) bool {
	return msg.req == m.meta.req && msg.conn == m.active && msg.table == m.data.table
}

// setMainTab switches tabs and loads the metadata the new one needs.
func (m *Model) setMainTab(t mainTab) tea.Cmd {
	m.tab = (t + mainTabCount) % mainTabCount
	return m.ensureMeta()
}

// metaActions handles the main view's tab actions. It runs before the
// data grid's own actions so a key means the same thing on every tab.
func (m Model) metaActions(id actionID) (Model, tea.Cmd, bool) {
	switch id {
	case actPrevMainTab:
		cmd := m.setMainTab(m.tab - 1)
		return m, cmd, true
	case actNextMainTab:
		cmd := m.setMainTab(m.tab + 1)
		return m, cmd, true
	case actCopyDDL:
		cmd := m.copyDDL()
		return m, cmd, true
	}
	return m, nil, false
}

// copyDDL copies the open relation's DDL, fetching it first when no tab
// has needed it yet.
func (m *Model) copyDDL() tea.Cmd {
	if !m.data.open() {
		return logCmd("-- copy DDL skipped: no relation open")
	}
	if m.meta.loaded {
		if m.meta.ddl == "" {
			return logCmd("-- copy DDL of %s skipped: %s", m.data.table, m.ddlProblem())
		}
		return copyDDLCmd(m.data.table, m.meta.ddl)
	}
	if m.driver == nil {
		return logCmd("-- copy DDL skipped: not connected")
	}
	// Nothing cached yet: fetch, then copy when the reply lands. `y`
	// works from the Data tab too, where ensureMeta would not fetch, so
	// the load is started directly.
	return m.deferUntilMeta(actCopyDDL)
}

// deferUntilMeta parks an action until the metadata fetch lands and
// starts that fetch when it is not already running.
func (m *Model) deferUntilMeta(id actionID) tea.Cmd {
	m.meta.afterLoad = id
	if m.meta.loading {
		return nil
	}
	return m.startMetaLoad()
}

// ddlProblem names why the DDL tab has nothing to show.
func (m Model) ddlProblem() string {
	switch {
	case m.meta.ddlErr != "":
		return m.meta.ddlErr
	case m.meta.err != "":
		return m.meta.err
	default:
		return "the engine reported no CREATE statement"
	}
}

// updateMetaKeys is the key handler of the focused main view on the
// three introspection tabs: j/k walks the rows of Structure and scrolls
// Indexes and DDL.
func (m Model) updateMetaKeys(delta int) (Model, tea.Cmd) {
	n := m.metaRowCount()
	row := m.meta.row[m.tab] + delta
	if row >= n {
		row = n - 1
	}
	if row < 0 {
		row = 0
	}
	m.meta.row[m.tab] = row
	return m, nil
}

// metaRowCount is how far the current tab can be scrolled.
func (m Model) metaRowCount() int {
	switch m.tab {
	case mainTabStructure:
		return len(m.meta.cols)
	case mainTabIndexes:
		return len(m.meta.indexes) + len(m.meta.fks)
	case mainTabDDL:
		return len(strings.Split(m.meta.ddl, "\n"))
	}
	return 0
}
