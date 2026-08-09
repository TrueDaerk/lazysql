package ui

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/db"
)

// The completion popup needs more schema than the panels already hold.
// Panel [3] caches the relation list of the browsed namespace — that one
// is reused as it is, because a second copy would be a second thing to
// invalidate — but column names are only fetched for the *open* relation,
// and completion needs them for every relation the buffer mentions.
//
// So this file adds one more cache, with three rules:
//
//   - It is keyed by connection + database. Nothing else identifies a
//     column list: two connections can have a table of the same name,
//     and so can two databases of one connection.
//   - It invalidates itself. syncSchema compares the key against the
//     model on every use rather than relying on every place that can
//     change the connection or the namespace to remember to clear it.
//   - It never blocks. A miss starts a tea.Cmd and the popup renders
//     what is already cached; the fetch's reply re-renders it.

// maxSchemaFetch bounds how many column lists one keystroke may request.
// A buffer with a twelve-way join would otherwise open twelve round trips
// on the first character typed. The rest are picked up by the next
// keystroke, which is soon enough for a popup.
const maxSchemaFetch = 4

// schemaCache is the completion popup's column store for one
// connection+database.
type schemaCache struct {
	conn     string
	database string

	// cols maps a relation name, spelled as ListRelations spells it, to
	// its column names. A present-but-empty entry means "fetched, and the
	// relation has no columns we could read" — it is not a miss, so it
	// does not re-fetch on every keystroke.
	cols map[string][]string
	// pending holds the relations with a fetch in flight, so a popup
	// refreshed on every keystroke issues one request per relation and
	// not one per character.
	pending map[string]bool
	// failed holds the relations whose fetch came back with an error.
	// They are not retried automatically: a relation the user cannot
	// introspect would otherwise be re-requested forever.
	failed map[string]bool

	// req is bumped whenever the cache is dropped, so a reply for the
	// namespace the user just left cannot repopulate the new one.
	req int
}

// ---------- messages ----------

// schemaColumnsMsg carries one relation's columns back to the cache.
type schemaColumnsMsg struct {
	req      int
	conn     string
	database string
	table    string
	cols     []db.Column
	err      error
}

// ---------- commands ----------

// loadSchemaColumnsCmd reads one relation's columns for the completion
// cache. It is deliberately separate from loadMetaCmd: that one fetches
// indexes, foreign keys and DDL as well, which a popup has no use for.
func loadSchemaColumnsCmd(drv db.Driver, req int, conn, database, table string) tea.Cmd {
	if drv == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), browseTimeout)
		defer cancel()
		cols, err := drv.TableColumns(ctx, database, table)
		return schemaColumnsMsg{
			req: req, conn: conn, database: database, table: table, cols: cols, err: err,
		}
	}
}

// ---------- model wiring ----------

// syncSchema drops the cache when it belongs to a connection or a
// namespace the model has moved on from, and returns the generation the
// caller's fetches should be tagged with.
//
// Invalidation lives here rather than in openDatabase and resetBrowse so
// that a future third way of changing the namespace cannot forget it.
func (m *Model) syncSchema() int {
	if m.schema.conn == m.active && m.schema.database == m.database && m.schema.cols != nil {
		return m.schema.req
	}
	m.schema = schemaCache{
		conn:     m.active,
		database: m.database,
		cols:     map[string][]string{},
		pending:  map[string]bool{},
		failed:   map[string]bool{},
		req:      m.schema.req + 1,
	}
	return m.schema.req
}

// schemaColumns returns a relation's cached column names, if any.
func (m Model) schemaColumns(table string) []string { return m.schema.cols[table] }

// ensureSchemaColumns starts a fetch for every named relation whose
// columns are not cached, not already in flight and not known to fail. It
// returns the commands, never the columns: a completion popup shows what
// is cached now and grows when the replies land.
func (m *Model) ensureSchemaColumns(tables []string) (tea.Cmd, bool) {
	req := m.syncSchema()
	if m.driver == nil {
		return nil, false
	}
	var cmds []tea.Cmd
	for _, t := range tables {
		if len(cmds) >= maxSchemaFetch {
			break
		}
		if _, ok := m.schema.cols[t]; ok || m.schema.pending[t] || m.schema.failed[t] {
			continue
		}
		m.schema.pending[t] = true
		cmds = append(cmds, loadSchemaColumnsCmd(m.driver, req, m.active, m.database, t))
	}
	loading := len(cmds) > 0
	for _, t := range tables {
		if m.schema.pending[t] {
			loading = true
		}
	}
	if len(cmds) == 0 {
		return nil, loading
	}
	return tea.Batch(cmds...), loading
}

// applySchemaColumns reduces one fetch. A reply for a superseded
// generation, connection or namespace is dropped: the cache it was meant
// for no longer exists.
func (m *Model) applySchemaColumns(msg schemaColumnsMsg) tea.Cmd {
	if msg.req != m.schema.req || msg.conn != m.schema.conn || msg.database != m.schema.database {
		return nil
	}
	delete(m.schema.pending, msg.table)
	if msg.err != nil {
		// The popup loses this relation's columns and nothing else, so the
		// failure is a log line rather than an error on screen.
		m.schema.failed[msg.table] = true
		return logCmd("-- completion: read columns of %s FAILED: %v", msg.table, msg.err)
	}
	names := make([]string, 0, len(msg.cols))
	for _, c := range msg.cols {
		names = append(names, c.Name)
	}
	m.schema.cols[msg.table] = names
	// An open popup showed what was cached when it opened; these columns
	// belong in it now, without waiting for the next keystroke.
	return m.restackCompletion()
}

// relationByName finds a relation of the browsed namespace by name,
// ignoring case. Completion matches what the user typed, and no engine
// lazysql speaks requires them to have matched the catalog's casing.
func (m Model) relationByName(name string) (db.Relation, bool) {
	for _, r := range m.relations {
		if strings.EqualFold(r.Name, name) {
			return r, true
		}
	}
	return db.Relation{}, false
}
