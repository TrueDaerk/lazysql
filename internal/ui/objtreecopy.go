package ui

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"
)

// `y` on the [2] Objects panel: the copy menu of the node under the
// cursor, mirroring the grid's `y`. It exists so inspecting a schema
// does not have to go through opening every table in the grid first —
// see wiki/design/ddl-destinations.md.
//
// Only the two node kinds that name something with DDL behind them get
// a menu: a relation (table or view) and a namespace. A category header
// and a trigger have nothing this menu could copy, and say so in one
// line rather than opening an empty modal.

// treeCopyMenu is `y` on the Objects panel.
func (m *Model) treeCopyMenu() tea.Cmd {
	n := m.selectedNode()
	if n == nil {
		return logCmd("-- copy skipped: nothing selected in the object tree")
	}
	var entries []menuEntry
	var title string
	switch {
	case n.kind == nodeObject && n.cat.relational():
		title = "Copy — " + n.name
		entries = append(entries,
			runActionEntry("d", "DDL statement", actCopyNodeDDL),
			runActionEntry("D", "database DDL — "+displayDatabase(n.database),
				actExportDatabaseDDLClipboard),
		)
	case n.kind == nodeDatabase:
		title = "Copy — " + displayDatabase(n.database)
		entries = append(entries,
			runActionEntry("D", "database DDL", actExportDatabaseDDLClipboard),
		)
	default:
		return logCmd("-- copy skipped: %s has nothing to copy", n.name)
	}
	entries = append(entries, menuEntry{key: "esc", label: "cancel"})
	m.modal = &menuModal{title: title, entries: entries}
	return nil
}

// copyNodeDDL copies the DDL of the relation under the [2] cursor. The
// metadata cache is reused when it happens to describe that very
// relation — the table is open in the grid and the user walked back up
// to it — and the driver is asked directly otherwise, which is the whole
// point of the entry: a relation that has never been opened.
func (m Model) copyNodeDDL() tea.Cmd {
	n := m.selectedNode()
	if n == nil || n.kind != nodeObject || !n.cat.relational() {
		return logCmd("-- copy DDL skipped: no table or view selected")
	}
	if m.meta.loaded && m.meta.database == n.database && m.meta.table == n.name && m.meta.ddl != "" {
		return copyDDLCmd(n.name, m.meta.ddl)
	}
	if m.driver == nil {
		return logCmd("-- copy DDL skipped: not connected")
	}
	drv, database, table := m.driver, n.database, n.name
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), browseTimeout)
		defer cancel()
		ddl, err := drv.TableDDL(ctx, database, table)
		if err != nil {
			return copiedMsg{line: fmt.Sprintf("-- copy DDL of %s FAILED: %v", table, err)}
		}
		if ddl == "" {
			return copiedMsg{line: fmt.Sprintf(
				"-- copy DDL of %s skipped: the engine reported no CREATE statement", table)}
		}
		return copyOut("DDL of "+table, table+"-ddl.sql", ddl)
	}
}
