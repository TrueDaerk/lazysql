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

// ---------- messages ----------

// databasesLoadedMsg carries the result of a namespace (re)load — the top
// level of the [2] Objects tree.
type databasesLoadedMsg struct {
	conn      string // the connection the load was started for
	databases []string
	err       error
}

// relationsLoadedMsg carries the result of one namespace's relation
// listing. The Tables and Views categories come from the same round trip,
// so expanding either fills both.
type relationsLoadedMsg struct {
	conn      string
	database  string
	relations []db.Relation
	err       error
}

// triggersLoadedMsg carries the result of one namespace's trigger
// listing — the lazy load behind the Triggers category.
type triggersLoadedMsg struct {
	conn     string
	database string
	triggers []db.Trigger
	err      error
}

// triggerDDLMsg carries one trigger's definition for the read-only main
// view. req drops a reply for a trigger the cursor has already left.
type triggerDDLMsg struct {
	conn     string
	database string
	name     string
	req      int
	ddl      string
	err      error
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

// loadTriggersCmd lists the triggers of one namespace.
func loadTriggersCmd(conn string, drv db.Driver, database string) tea.Cmd {
	if drv == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), browseTimeout)
		defer cancel()
		trs, err := drv.ListTriggers(ctx, database)
		return triggersLoadedMsg{conn: conn, database: database, triggers: trs, err: err}
	}
}

// loadTriggerDDLCmd reads one trigger's definition for the main view.
func loadTriggerDDLCmd(conn string, drv db.Driver, database, name string, req int) tea.Cmd {
	if drv == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), browseTimeout)
		defer cancel()
		ddl, err := drv.TriggerDDL(ctx, database, name)
		return triggerDDLMsg{
			conn: conn, database: database, name: name, req: req, ddl: ddl, err: err}
	}
}

// ---------- pseudo-database ----------

// pseudoDatabase is what single-database engines show as the [2] tree's
// namespace when the driver reports no namespace at all, so the tree is
// never empty and the browsed namespace always has a name.
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

// scopeDatabases restricts a namespace listing to the database the profile
// pins, on engines where the listing enumerates server databases at all
// (MySQL/MariaDB — see db.DatabaseNamespaces). A pinned database missing from
// the listing is kept anyway: SHOW DATABASES hides namespaces the user lacks
// privileges to see but may still be allowed to use, and a truly wrong name
// surfaces as an error on expand.
func (m *Model) scopeDatabases(conn string, dbs []string) []string {
	c, ok := m.cfg.Find(conn)
	if !ok || c.Database == "" || !db.DatabaseNamespaces(c.Engine) {
		return dbs
	}
	for _, d := range dbs {
		if d == c.Database {
			return []string{d}
		}
	}
	return []string{c.Database}
}
