package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/db"
)

// Foreign-key navigation. `g` follows the constraint the cursor column
// takes part in and opens the referenced table filtered to the row it
// points at; `G` goes the other way and lists the rows that reference
// the row under the cursor; `ctrl+o` (and `esc`) walk the jumps back.
//
// The metadata behind all three is cached per relation: a jump back and
// forth between two tables introspects each of them once.

// fkMark is what the grid header appends to a column that takes part in
// a foreign key, so the binding is discoverable without opening the
// Indexes tab.
const fkMark = " ⇒"

// namespaceFKTimeout bounds the reverse-direction scan. It is longer
// than a single introspection because the scan reads every table of the
// namespace, one round trip each.
const namespaceFKTimeout = 60 * time.Second

// browseStackMax caps the jump history. It is a navigation aid, not an
// undo log: the oldest entries fall off rather than growing without
// bound while someone walks a deep chain of references.
const browseStackMax = 32

// fkKey identifies cached foreign-key metadata. An empty table means the
// whole namespace, which is what the reverse direction is keyed by.
type fkKey struct {
	conn     string
	database string
	table    string
}

// namespaceFK is one foreign key together with the table that declares
// it. The reverse direction needs both: the constraint says which
// columns match, the table says where to jump.
type namespaceFK struct {
	table string
	fk    db.ForeignKey
}

// browseState is one entry of the jump history: the whole Data tab as it
// was left, plus which main-view tab was open. Keeping the dataView
// wholesale is what makes the filter, the sort, the page and the cell
// cursor all come back together.
type browseState struct {
	data dataView
	tab  mainTab
}

// ---------- messages ----------

// fksLoadedMsg carries the foreign keys of one relation.
type fksLoadedMsg struct {
	key fkKey
	fks []db.ForeignKey
	err error
}

// namespaceFKsLoadedMsg carries every foreign key declared in one
// namespace. tables is the list that was scanned, so tables that declare
// none are cached as such instead of being re-scanned forever.
type namespaceFKsLoadedMsg struct {
	key    fkKey
	tables []string
	refs   []namespaceFK
	err    error
}

// ---------- commands ----------

func loadFKsCmd(drv db.Driver, k fkKey) tea.Cmd {
	if drv == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), browseTimeout)
		defer cancel()
		fks, err := drv.TableForeignKeys(ctx, k.database, k.table)
		return fksLoadedMsg{key: k, fks: fks, err: err}
	}
}

// loadNamespaceFKsCmd reads the foreign keys of every table of one
// namespace. It is the reverse direction's one round trip per table:
// there is no engine-independent way to ask "who references me".
func loadNamespaceFKsCmd(drv db.Driver, k fkKey, tables []string) tea.Cmd {
	if drv == nil {
		return nil
	}
	scanned := append([]string(nil), tables...)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), namespaceFKTimeout)
		defer cancel()
		out := namespaceFKsLoadedMsg{key: k, tables: scanned}
		for _, t := range scanned {
			fks, err := drv.TableForeignKeys(ctx, k.database, t)
			if err != nil {
				out.err = fmt.Errorf("%s: %w", t, err)
				return out
			}
			for _, fk := range fks {
				out.refs = append(out.refs, namespaceFK{table: t, fk: fk})
			}
		}
		return out
	}
}

// ---------- cache ----------

// cacheFKs records one relation's foreign keys. The pointer receiver is
// what lets the lazily created map survive the Model copy Update works
// on.
func (m *Model) cacheFKs(k fkKey, fks []db.ForeignKey) {
	if k.table == "" {
		return
	}
	if m.fkCache == nil {
		m.fkCache = map[fkKey][]db.ForeignKey{}
	}
	// A relation without foreign keys caches as an empty non-nil slice,
	// so "known to have none" is distinguishable from "never fetched".
	if fks == nil {
		fks = []db.ForeignKey{}
	}
	m.fkCache[k] = fks
}

// tableFKKey identifies the open relation for the caches.
func (m Model) tableFKKey() fkKey {
	return fkKey{conn: m.active, database: m.data.database, table: m.data.table}
}

// namespaceFKKey identifies the browsed namespace for the reverse scan.
func (m Model) namespaceFKKey() fkKey {
	return fkKey{conn: m.active, database: m.data.database}
}

// tableFKs returns the cached foreign keys of the open relation and
// whether they have been fetched at all.
func (m Model) tableFKs() ([]db.ForeignKey, bool) {
	fks, ok := m.fkCache[m.tableFKKey()]
	return fks, ok
}

// ensureFKs starts the foreign-key fetch of the open relation unless the
// cache already answers it or a fetch is already in flight.
func (m *Model) ensureFKs() tea.Cmd {
	if m.driver == nil || !m.data.browsing() {
		return nil
	}
	k := m.tableFKKey()
	if _, ok := m.fkCache[k]; ok {
		return nil
	}
	if m.fkLoading[k] {
		return nil
	}
	if m.fkLoading == nil {
		m.fkLoading = map[fkKey]bool{}
	}
	m.fkLoading[k] = true
	return loadFKsCmd(m.driver, k)
}

// ensureNamespaceFKs starts the reverse-direction scan of the browsed
// namespace, unless it is cached or already running.
func (m *Model) ensureNamespaceFKs() tea.Cmd {
	if m.driver == nil || !m.data.browsing() {
		return nil
	}
	k := m.namespaceFKKey()
	if _, ok := m.refsCache[k]; ok {
		return nil
	}
	if m.fkLoading[k] {
		return nil
	}
	tables := db.FilterRelations(m.relations, db.RelationTable)
	if len(tables) == 0 {
		return logCmd("-- referencing rows skipped: no tables listed for %s",
			displayDatabase(m.data.database))
	}
	if m.fkLoading == nil {
		m.fkLoading = map[fkKey]bool{}
	}
	m.fkLoading[k] = true
	return tea.Batch(
		logCmd("-- scan foreign keys of %d tables in %s",
			len(tables), displayDatabase(m.data.database)),
		loadNamespaceFKsCmd(m.driver, k, tables),
	)
}

// fkColumnSet is the lower-cased names of the columns that take part in
// a foreign key of the open relation, for the grid header's `⇒` mark.
func (m Model) fkColumnSet() map[string]bool {
	fks, ok := m.tableFKs()
	if !ok || len(fks) == 0 {
		return nil
	}
	set := map[string]bool{}
	for _, fk := range fks {
		for _, c := range fk.Columns {
			set[strings.ToLower(c)] = true
		}
	}
	return set
}

// ---------- following ----------

// followFK is `g`: jump to the row the cursor column's foreign key
// points at. With the metadata not yet fetched it starts the fetch and
// re-runs itself when the reply lands, so the key works on a relation
// whose Indexes tab was never opened.
func (m *Model) followFK() tea.Cmd {
	if problem := m.fkPrecondition("follow FK"); problem != "" {
		return logCmd("%s", problem)
	}
	fks, ok := m.tableFKs()
	if !ok {
		m.fkAfter = actFollowFK
		return m.ensureFKs()
	}
	col, ok := m.cursorColumnName()
	if !ok {
		return nil
	}
	matches := db.FKAt(fks, col)
	switch len(matches) {
	case 0:
		return logCmd("-- follow FK skipped: %s is not part of a foreign key", col)
	case 1:
		return m.followFKConstraint(matches[0])
	}
	// One column can belong to several constraints; the user picks.
	entries := make([]menuEntry, 0, len(matches)+1)
	for _, fk := range matches {
		target := fk
		entries = append(entries, menuEntry{
			label:  fkLabel(target),
			action: func(m *Model) tea.Cmd { return m.followFKConstraint(target) },
		})
	}
	entries = append(entries, menuEntry{key: "esc", label: "cancel"})
	m.modal = &menuModal{title: "Follow foreign key — " + col, entries: entries}
	return nil
}

// followFKConstraint jumps along one constraint, reading the key values
// from the row under the cursor.
func (m *Model) followFKConstraint(fk db.ForeignKey) tea.Cmd {
	vals, err := m.cursorRowValues(fk.Columns)
	if err != nil {
		return logCmd("-- follow FK skipped: %v", err)
	}
	f, err := db.FKFilter(m.driver.Dialect(), fk.RefColumns, vals)
	if err != nil {
		return logCmd("-- follow FK skipped: %v", err)
	}
	database, table := db.SplitQualified(fk.RefTable)
	if database == "" {
		database = m.data.database
	}
	return m.jumpTo(database, table, f)
}

// fkLabel names one constraint the way the menus show it.
func fkLabel(fk db.ForeignKey) string {
	label := fmt.Sprintf("%s → %s (%s)",
		strings.Join(fk.Columns, ", "), fk.RefTable, strings.Join(fk.RefColumns, ", "))
	if fk.Name != "" {
		label += "  " + fk.Name
	}
	return label
}

// ---------- reverse direction ----------

// showIncomingRefs is `G`: list the tables whose foreign keys point at
// the row under the cursor, and jump to the matching rows of the one
// that is picked.
func (m *Model) showIncomingRefs() tea.Cmd {
	if problem := m.fkPrecondition("referencing rows"); problem != "" {
		return logCmd("%s", problem)
	}
	refs, ok := m.refsCache[m.namespaceFKKey()]
	if !ok {
		m.fkAfter = actIncomingRefs
		return m.ensureNamespaceFKs()
	}
	incoming := incomingFor(refs, m.data.table)
	if len(incoming) == 0 {
		return logCmd("-- no table in %s references %s",
			displayDatabase(m.data.database), m.data.table)
	}
	entries := make([]menuEntry, 0, len(incoming)+1)
	for _, ref := range incoming {
		target := ref
		label := fmt.Sprintf("%s (%s → %s)", target.table,
			strings.Join(target.fk.Columns, ", "), strings.Join(target.fk.RefColumns, ", "))
		// The values come from this row, so an entry whose key columns
		// are NULL or off-page is listed and says why it cannot run
		// rather than silently missing.
		if _, err := m.cursorRowValues(target.fk.RefColumns); err != nil {
			reason := err.Error()
			entries = append(entries, menuEntry{
				label:  label + " — unavailable: " + reason,
				action: func(m *Model) tea.Cmd { return logCmd("-- referencing rows skipped: %s", reason) },
			})
			continue
		}
		entries = append(entries, menuEntry{
			label:  label,
			action: func(m *Model) tea.Cmd { return m.jumpToReferencing(target) },
		})
	}
	entries = append(entries, menuEntry{key: "esc", label: "cancel"})
	m.modal = &menuModal{title: "Referenced by — " + m.data.table, entries: entries}
	return nil
}

// jumpToReferencing opens the child table filtered to the rows whose
// foreign key points at the row under the cursor.
func (m *Model) jumpToReferencing(ref namespaceFK) tea.Cmd {
	vals, err := m.cursorRowValues(ref.fk.RefColumns)
	if err != nil {
		return logCmd("-- referencing rows skipped: %v", err)
	}
	f, err := db.FKFilter(m.driver.Dialect(), ref.fk.Columns, vals)
	if err != nil {
		return logCmd("-- referencing rows skipped: %v", err)
	}
	return m.jumpTo(m.data.database, ref.table, f)
}

// incomingFor picks the namespace's constraints that point at one table.
// The comparison is on the bare name: an engine may report the target
// schema-qualified even inside the namespace being browsed.
func incomingFor(refs []namespaceFK, table string) []namespaceFK {
	var out []namespaceFK
	for _, r := range refs {
		if _, target := db.SplitQualified(r.fk.RefTable); strings.EqualFold(target, table) {
			out = append(out, r)
		}
	}
	return out
}

// ---------- jumping and coming back ----------

// jumpTo opens another relation with a filter already applied, after
// pushing where the jump came from onto the history.
func (m *Model) jumpTo(database, table string, f *db.Filter) tea.Cmd {
	m.pushBrowse()
	m.table = table
	m.tab = mainTabData
	m.resetMeta()
	m.data = dataView{
		conn:     m.active,
		database: database,
		table:    table,
		filter:   f,
		req:      m.data.req,
	}
	// The [2] tree follows along whenever the target is in the browsed
	// namespace, so the shell does not claim a different relation is open.
	if database == m.database {
		m.selectRelation(database, table)
	}
	return tea.Batch(
		logCmd("-- follow to %s.%s WHERE %s", displayDatabase(database), table, f.Raw),
		m.reloadPage(),
		m.ensureFKs(),
		m.ensureMeta(),
	)
}

// pushBrowse records the open page so a jump can be undone.
func (m *Model) pushBrowse() {
	if !m.data.browsing() {
		return
	}
	m.browseStack = append(m.browseStack, browseState{data: m.data, tab: m.tab})
	if n := len(m.browseStack); n > browseStackMax {
		m.browseStack = m.browseStack[n-browseStackMax:]
	}
}

// browseBack pops one jump: the previous relation comes back with its
// filter, sort, page and cell cursor. The rows themselves are re-read —
// the page may have changed while the user was away — but the stored
// ones stay on screen until the reply lands.
func (m *Model) browseBack() tea.Cmd {
	n := len(m.browseStack)
	if n == 0 {
		return logCmd("-- no earlier table to go back to")
	}
	st := m.browseStack[n-1]
	m.browseStack = m.browseStack[:n-1]

	req := m.data.req
	m.data = st.data
	m.data.req = req
	m.data.loading = false
	m.data.err = ""
	m.table = st.data.table
	m.tab = st.tab
	m.resetMeta()
	if st.data.database == m.database {
		m.selectRelation(st.data.database, st.data.table)
	}
	return tea.Batch(
		logCmd("-- back to %s.%s", displayDatabase(st.data.database), st.data.table),
		m.reloadPage(),
		m.ensureFKs(),
		m.ensureMeta(),
	)
}

// ---------- shared helpers ----------

// fkPrecondition returns the log line explaining why an FK action cannot
// run, or "" when it can.
func (m Model) fkPrecondition(what string) string {
	switch {
	case !m.data.browsing():
		return "-- " + what + " skipped: no relation open"
	case m.driver == nil:
		return "-- " + what + " skipped: not connected"
	case m.tab.metadata():
		return "-- " + what + " skipped: not on the Data tab"
	case m.onPhantomRow():
		return "-- " + what + " skipped: the row is a staged insert"
	case len(m.data.rows) == 0:
		return "-- " + what + " skipped: no rows on this page"
	}
	return ""
}

// cursorColumnName is the name of the column the cell cursor sits on.
func (m Model) cursorColumnName() (string, bool) {
	if m.data.col < 0 || m.data.col >= len(m.data.cols) {
		return "", false
	}
	return m.data.cols[m.data.col].Name, true
}

// cursorRowValues reads the cursor row's values for the named columns.
// A NULL names the column it was found in: that message is the whole
// reason a NULL foreign key does nothing instead of showing an empty
// grid.
func (m Model) cursorRowValues(cols []string) ([]any, error) {
	if m.data.row < 0 || m.data.row >= len(m.data.rows) {
		return nil, fmt.Errorf("no row under the cursor")
	}
	row := m.data.rows[m.data.row]
	out := make([]any, len(cols))
	for i, name := range cols {
		idx := -1
		for j, c := range m.data.cols {
			if strings.EqualFold(c.Name, name) {
				idx = j
				break
			}
		}
		if idx < 0 || idx >= len(row) {
			return nil, fmt.Errorf("%s is not on this page", name)
		}
		if row[idx] == nil {
			return nil, fmt.Errorf("%s is NULL", name)
		}
		out[i] = row[idx]
	}
	return out, nil
}
