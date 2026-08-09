package ui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/db"
)

// browseTimeout bounds catalog reads so a hung server cannot leave a panel
// stuck in its loading state forever.
const browseTimeout = 15 * time.Second

// relationTab is a sub-tab of the [3] Tables panel.
type relationTab int

const (
	tabTables relationTab = iota
	tabViews
	relationTabCount
)

var relationTabNames = [relationTabCount]string{"Tables", "Views"}

// kind maps the sub-tab onto the driver's relation kind.
func (t relationTab) kind() db.RelationKind {
	if t == tabViews {
		return db.RelationView
	}
	return db.RelationTable
}

// ---------- messages ----------

// databasesLoadedMsg carries the result of a [2] Databases (re)load.
type databasesLoadedMsg struct {
	conn      string // the connection the load was started for
	databases []string
	err       error
}

// relationsLoadedMsg carries the result of a [3] Tables (re)load. Both
// sub-tabs come from the same round trip, so switching tabs needs no query.
type relationsLoadedMsg struct {
	conn      string
	database  string
	relations []db.Relation
	err       error
}

// ---------- commands ----------

// loadDatabasesCmd lists the namespaces of the live connection.
func loadDatabasesCmd(conn string, drv db.Driver) tea.Cmd {
	if drv == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), browseTimeout)
		defer cancel()
		dbs, err := drv.ListDatabases(ctx)
		return databasesLoadedMsg{conn: conn, databases: dbs, err: err}
	}
}

// loadRelationsCmd lists the tables and views of one namespace.
func loadRelationsCmd(conn string, drv db.Driver, database string) tea.Cmd {
	if drv == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), browseTimeout)
		defer cancel()
		rels, err := drv.ListRelations(ctx, database)
		return relationsLoadedMsg{conn: conn, database: database, relations: rels, err: err}
	}
}

// ---------- pseudo-database ----------

// pseudoDatabase is what single-database engines show in panel [2] when the
// driver reports no namespace at all, so the panel is never empty and `enter`
// still has something to drill into.
const pseudoDatabase = "(default)"

// namespaceList normalizes a driver listing for display: file-based engines
// with nothing to show get the single pseudo entry.
func namespaceList(engine db.Engine, dbs []string) []string {
	// SQLite and DuckDB name their one namespace ("main", "memory", the
	// file stem …); that name carries no choice, so it collapses into the
	// pseudo entry. Attached databases still list normally.
	if db.FileBased(engine) && len(dbs) <= 1 {
		return []string{pseudoDatabase}
	}
	return dbs
}

// databaseArg turns a panel selection into the driver argument: the pseudo
// entry means "the connection's current/default namespace".
func databaseArg(name string) string {
	if name == pseudoDatabase {
		return ""
	}
	return name
}
