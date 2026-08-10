package ui

import (
	"errors"
	"strings"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/db"
)

// The [2] Objects panel: one tree over everything a connection holds,
// replacing the separate Databases and Tables panels and the Tables/Views
// sub-tabs those had. Three levels:
//
//	▾ app_dev
//	    ▾ Tables
//	        users
//	    ▸ Views
//	    ▸ Triggers
//
// A single-namespace connection (SQLite, DuckDB with nothing attached)
// drops the top level: its categories are the roots, because a database
// level with exactly one entry is a keystroke that carries no choice.
//
// The tree itself is never rendered as a tree. flatten turns the expanded
// nodes into a list of rows, which is what the panel's plain
// cursor-over-slice rendering, its scroll window and its cursor work on —
// so the whole panel gained a tree without any of that gaining tree
// awareness. See wiki/design/object-tree-panel.md.

// objectCategory is the middle level of the tree: what kind of object the
// nodes under it are.
type objectCategory int

const (
	catTables objectCategory = iota
	catViews
	catTriggers
	categoryCount
)

var categoryNames = [categoryCount]string{"Tables", "Views", "Triggers"}

// relationKind maps a category onto the driver's relation kind. Triggers
// are not relations, so they have none.
func (c objectCategory) relationKind() (db.RelationKind, bool) {
	switch c {
	case catTables:
		return db.RelationTable, true
	case catViews:
		return db.RelationView, true
	}
	return 0, false
}

// relational reports whether the category is filled by ListRelations —
// the one round trip Tables and Views share.
func (c objectCategory) relational() bool {
	_, ok := c.relationKind()
	return ok
}

// nodeKind is a node's level in the tree.
type nodeKind int

const (
	nodeDatabase nodeKind = iota
	nodeCategory
	nodeObject
)

// treeNode is one node of the [2] Objects tree.
type treeNode struct {
	kind nodeKind
	// name is the row's text: the namespace's display name, the category
	// label, or the object's name.
	name string
	// database is the driver argument of the owning namespace — the same
	// form m.database and a saved session store, so a load reply can be
	// matched to the node that asked for it. The pseudo-database entry
	// has an empty one.
	database string
	// cat is the category this node is (nodeCategory) or belongs to
	// (nodeObject).
	cat objectCategory
	// table is the relation a trigger fires on, empty for everything else.
	table string

	expanded bool
	// loading marks an introspection query in flight, loaded that one has
	// landed — which is what makes a second expand free until a refresh
	// drops it again.
	loading bool
	loaded  bool
	err     string
	// hint is a note that is not a failure: an engine that has no such
	// object kind at all. It is kept apart from err so the row is not
	// painted red for something that is working as intended.
	hint string

	children []*treeNode
}

// leaf reports whether the node has nothing to expand.
func (n *treeNode) leaf() bool { return n.kind == nodeObject }

// objectTree is the whole tree plus the listings its nodes were built
// from. The caches are keyed by the driver's database argument and are
// what "loaded until refresh" means: a collapse and re-expand costs
// nothing, and only `R` drops them.
type objectTree struct {
	roots []*treeNode
	// single records that the connection has one namespace and the
	// database level was skipped.
	single    bool
	relations map[string][]db.Relation
	triggers  map[string][]db.Trigger
}

// newObjectTree builds the collapsed tree for a connection's namespaces.
// displays are the entries namespaceList produced, so a file engine's
// pseudo-database is already folded in.
func newObjectTree(displays []string) *objectTree {
	t := &objectTree{
		relations: map[string][]db.Relation{},
		triggers:  map[string][]db.Trigger{},
	}
	if len(displays) == 1 {
		t.single = true
		t.roots = categoryNodes(databaseArg(displays[0]))
		return t
	}
	for _, d := range displays {
		t.roots = append(t.roots, &treeNode{
			kind:     nodeDatabase,
			name:     d,
			database: databaseArg(d),
			children: categoryNodes(databaseArg(d)),
		})
	}
	return t
}

// categoryNodes are the fixed middle level. They are built eagerly —
// there is nothing to ask the server about the *set* of categories — so
// expanding a database never costs a round trip.
func categoryNodes(database string) []*treeNode {
	out := make([]*treeNode, 0, categoryCount)
	for c := objectCategory(0); c < categoryCount; c++ {
		out = append(out, &treeNode{
			kind:     nodeCategory,
			name:     categoryNames[c],
			database: database,
			cat:      c,
		})
	}
	return out
}

// databaseNames lists the namespaces the tree knows, in display form.
func (t *objectTree) databaseNames() []string {
	if t == nil {
		return nil
	}
	if t.single {
		if len(t.roots) == 0 {
			return nil
		}
		return []string{displayDatabase(t.roots[0].database)}
	}
	out := make([]string, 0, len(t.roots))
	for _, n := range t.roots {
		out = append(out, n.name)
	}
	return out
}

// category returns one namespace's category node.
func (t *objectTree) category(database string, cat objectCategory) *treeNode {
	if t == nil {
		return nil
	}
	for _, n := range t.categoryNodes(database) {
		if n.cat == cat {
			return n
		}
	}
	return nil
}

// categoryNodes returns every category node of one namespace.
func (t *objectTree) categoryNodes(database string) []*treeNode {
	if t == nil {
		return nil
	}
	if t.single {
		if len(t.roots) > 0 && t.roots[0].database == database {
			return t.roots
		}
		return nil
	}
	for _, n := range t.roots {
		if n.database == database {
			return n.children
		}
	}
	return nil
}

// flatten walks the expanded nodes into the row list the panel renders.
func (t *objectTree) flatten() []treeRow {
	if t == nil {
		return nil
	}
	var out []treeRow
	var walk func(nodes []*treeNode, depth int)
	walk = func(nodes []*treeNode, depth int) {
		for _, n := range nodes {
			out = append(out, treeRow{node: n, depth: depth})
			if n.expanded {
				walk(n.children, depth+1)
			}
		}
	}
	walk(t.roots, 0)
	return out
}

// parentOf returns the node one level up, nil for a root.
func (t *objectTree) parentOf(want *treeNode) *treeNode {
	if t == nil || want == nil {
		return nil
	}
	var find func(nodes []*treeNode, parent *treeNode) *treeNode
	find = func(nodes []*treeNode, parent *treeNode) *treeNode {
		for _, n := range nodes {
			if n == want {
				return parent
			}
			if got := find(n.children, n); got != nil {
				return got
			}
		}
		return nil
	}
	return find(t.roots, nil)
}

// ---------- flattened rows ----------

// treeRow is one visible line: the node and how deep it sits.
type treeRow struct {
	node  *treeNode
	depth int
}

// treeIndent is how far one level is pushed in. Two cells leaves room
// for the chevron without pushing a third-level object name off a narrow
// side column.
const treeIndent = 2

// prefix is the indent and the expand marker the row is drawn with. A
// leaf gets blanks where a branch has its chevron, so names in one
// category still line up.
func (r treeRow) prefix() string {
	indent := strings.Repeat(" ", treeIndent*r.depth)
	switch {
	case r.node.leaf():
		return indent + "  "
	case r.node.expanded:
		return indent + "▾ "
	default:
		return indent + "▸ "
	}
}

// noteStyle names which of the panel's styles a row's trailing note is
// rendered in, so panel.render does not have to know what the note says.
type noteStyle int

const (
	noteMuted noteStyle = iota
	notePending
	noteDanger
)

// note is the trailing annotation: the load state of a branch, or the
// relation a trigger fires on.
func (r treeRow) note() (string, noteStyle) {
	n := r.node
	switch {
	case n.loading:
		return "loading…", notePending
	case n.err != "":
		return n.err, noteDanger
	case n.hint != "":
		return n.hint, noteMuted
	case n.kind == nodeCategory && n.loaded && len(n.children) == 0:
		return "(none)", noteMuted
	case n.kind == nodeObject && n.cat == catTriggers && n.table != "":
		return "on " + n.table, noteMuted
	}
	return "", noteMuted
}

// ---------- panel glue ----------

// setTreeRows replaces the panel's contents with a flattened tree,
// keeping the cursor on keep when that node is still in it. The rows are
// what makes the panel a tree panel: the filter and the renderer both
// switch behaviour on their presence.
func (p *sidePanel) setTreeRows(rows []treeRow, keep *treeNode) {
	names := make([]string, 0, len(rows))
	for _, r := range rows {
		names = append(names, r.node.name)
	}
	p.rows = rows
	p.all = names
	p.allStatus = nil
	p.applyFilter()
	if !p.selectNode(keep) {
		p.cursor, p.offset = 0, 0
	}
}

// selectNode moves the cursor onto a node and reports whether the node is
// visible at all. Nodes are matched by identity, not by name: two
// namespaces can hold a table of the same name.
func (p *sidePanel) selectNode(want *treeNode) bool {
	if want == nil {
		return false
	}
	for i := range p.items {
		if n := p.nodeAt(i); n == want {
			p.cursor = i
			p.move(0)
			return true
		}
	}
	p.move(0)
	return false
}

// nodeAt returns the node behind visible row i, nil when the panel is not
// a tree or the row does not exist.
func (p *sidePanel) nodeAt(i int) *treeNode {
	if p.rows == nil {
		return nil
	}
	src := p.sourceIndex(i)
	if src < 0 || src >= len(p.rows) {
		return nil
	}
	return p.rows[src].node
}

// rowAt is nodeAt with the depth kept, for the renderer.
func (p *sidePanel) rowAt(i int) (treeRow, bool) {
	if p.rows == nil {
		return treeRow{}, false
	}
	src := p.sourceIndex(i)
	if src < 0 || src >= len(p.rows) {
		return treeRow{}, false
	}
	return p.rows[src], true
}

// treeKeep is the tree's answer to the fuzzy filter: a row survives when
// its own name matches, or when any row of its subtree does. Filtering
// therefore narrows what is *on screen* — the expanded levels — rather
// than searching the whole catalog, and a match never loses the branch it
// hangs under. Collapsed contents are not searched: they have not been
// read from the server yet, so there is nothing to match against.
func (p *sidePanel) treeKeep(i int) bool {
	if fuzzyMatch(p.filter, p.rows[i].node.name) {
		return true
	}
	depth := p.rows[i].depth
	for j := i + 1; j < len(p.rows) && p.rows[j].depth > depth; j++ {
		if fuzzyMatch(p.filter, p.rows[j].node.name) {
			return true
		}
	}
	return false
}

// ---------- model wiring ----------

// selectedNode is the tree node under the [2] panel's cursor.
func (m Model) selectedNode() *treeNode {
	p := m.panels[panelObjects]
	return p.nodeAt(p.cursor)
}

// refreshTree re-flattens the tree into panel [2], keeping the cursor
// where it was. Every expand, collapse and load reply ends here.
func (m *Model) refreshTree() {
	p := m.panels[panelObjects]
	m.panels[panelObjects].setTreeRows(m.tree.flatten(), p.nodeAt(p.cursor))
}

// rebuildTree replaces the tree with one over the given namespaces,
// dropping every cached listing with it. displays are namespaceList's
// output.
func (m *Model) rebuildTree(displays []string) {
	m.tree = newObjectTree(displays)
	m.panels[panelObjects].clearFilter()
	m.refreshTree()
}

// toggleNode is `enter` on a branch: expand it (loading its contents the
// first time) or collapse it.
func (m *Model) toggleNode(n *treeNode) tea.Cmd {
	if n == nil || n.leaf() {
		return nil
	}
	if n.expanded {
		n.expanded = false
		m.refreshTree()
		return nil
	}
	cmd := m.expandNode(n)
	m.refreshTree()
	return cmd
}

// expandNode opens a branch and starts its load if it has never been
// read. It does not repaint — callers batch that with whatever else they
// changed.
func (m *Model) expandNode(n *treeNode) tea.Cmd {
	if n == nil || n.leaf() {
		return nil
	}
	n.expanded = true
	if n.kind != nodeCategory || n.loaded || n.loading {
		return nil
	}
	return m.loadCategory(n)
}

// loadCategory starts the introspection query behind one category. It
// always runs in a tea.Cmd: expanding a node must never block Update, so
// the node carries a loading marker until the reply lands. The statement
// itself reaches the command log through the Driver's Logger, like every
// other query — nothing is spelled out by hand here.
//
// Tables and Views share one round trip — ListRelations returns both —
// so expanding either marks both as loading and one reply fills the two.
func (m *Model) loadCategory(n *treeNode) tea.Cmd {
	if n == nil || n.kind != nodeCategory {
		return nil
	}
	if m.driver == nil {
		n.err = "not connected"
		return nil
	}
	n.err = ""
	if !n.cat.relational() {
		n.loading = true
		return loadTriggersCmd(m.active, m.driver, n.database)
	}
	for _, c := range m.tree.categoryNodes(n.database) {
		if c.cat.relational() {
			c.loading, c.err = true, ""
		}
	}
	return loadRelationsCmd(m.active, m.driver, n.database)
}

// reloadNode is `R` on the tree: it re-reads whatever level the cursor
// sits on. On a database node that is the namespace list itself; on a
// category or an object it is that category's contents, cache dropped.
func (m *Model) reloadNode() tea.Cmd {
	n := m.selectedNode()
	if n == nil || n.kind == nodeDatabase {
		m.panels[panelObjects].loading = true
		return tea.Batch(
			logCmd("-- reload databases of %s", m.active),
			loadDatabasesCmd(m.active, m.driver),
		)
	}
	cat := n
	if n.kind == nodeObject {
		cat = m.tree.category(n.database, n.cat)
	}
	if cat == nil {
		return nil
	}
	// Dropping loaded is what makes this a reload rather than a no-op:
	// the cache is authoritative until exactly here.
	for _, c := range m.tree.categoryNodes(cat.database) {
		if c == cat || (cat.cat.relational() && c.cat.relational()) {
			c.loaded = false
		}
	}
	cmd := m.loadCategory(cat)
	m.refreshTree()
	return tea.Batch(
		logCmd("-- reload %s of %s",
			strings.ToLower(categoryNames[cat.cat]), displayDatabase(cat.database)),
		cmd,
	)
}

// applyRelations fills the Tables and Views categories of one namespace
// from a single listing, and caches it.
func (m *Model) applyRelations(database string, rels []db.Relation) {
	m.tree.relations[database] = rels
	for _, c := range m.tree.categoryNodes(database) {
		kind, ok := c.cat.relationKind()
		if !ok {
			continue
		}
		c.children = objectNodes(database, c.cat, db.FilterRelations(rels, kind))
		c.loading, c.loaded, c.err, c.hint = false, true, "", ""
	}
	m.syncRelations()
}

// applyTriggers fills the Triggers category of one namespace.
func (m *Model) applyTriggers(database string, trs []db.Trigger) {
	m.tree.triggers[database] = trs
	c := m.tree.category(database, catTriggers)
	if c == nil {
		return
	}
	c.children = make([]*treeNode, 0, len(trs))
	for _, tr := range trs {
		c.children = append(c.children, &treeNode{
			kind:     nodeObject,
			name:     tr.Name,
			database: database,
			cat:      catTriggers,
			table:    tr.Table,
		})
	}
	c.loading, c.loaded, c.err, c.hint = false, true, "", ""
}

// categoryFailed puts a load failure on the node that asked for it: the
// tree keeps whatever it had and the row says why it is stale.
func (m *Model) categoryFailed(database string, cat objectCategory, err error) {
	for _, c := range m.tree.categoryNodes(database) {
		if c.cat != cat && !(cat.relational() && c.cat.relational()) {
			continue
		}
		c.loading = false
		c.err, c.hint = "failed", ""
		if errors.Is(err, db.ErrUnsupported) {
			// Not a failure but a fact about the engine — DuckDB has no
			// triggers at all — so the node says so, stays loaded and is
			// not painted red.
			c.loaded, c.err, c.hint = true, "", "not supported"
			c.children = nil
		}
	}
}

// objectNodes builds the leaf level of one category.
func objectNodes(database string, cat objectCategory, names []string) []*treeNode {
	out := make([]*treeNode, 0, len(names))
	for _, name := range names {
		out = append(out, &treeNode{
			kind:     nodeObject,
			name:     name,
			database: database,
			cat:      cat,
		})
	}
	return out
}

// syncRelations keeps m.relations pointed at the browsed namespace's
// listing. Everything outside the tree — the DDL export, the completion
// cache, the namespace-wide foreign-key scan — reads that one field, so
// it is filled from the tree's cache in exactly this place.
func (m *Model) syncRelations() {
	if m.tree == nil {
		m.relations = nil
		return
	}
	m.relations = m.tree.relations[m.database]
}

// selectObject moves the tree cursor onto one object, expanding the
// branches above it. It reports whether the object was in the tree: the
// callers that follow a jump elsewhere (a foreign key, the Relations tab)
// use it to keep the panel in step, and simply do nothing when the
// namespace has not been read yet.
func (m *Model) selectObject(database string, cat objectCategory, name string) bool {
	c := m.tree.category(database, cat)
	if c == nil {
		return false
	}
	var want *treeNode
	for _, child := range c.children {
		if child.name == name {
			want = child
			break
		}
	}
	if want == nil {
		return false
	}
	c.expanded = true
	if !m.tree.single {
		for _, root := range m.tree.roots {
			if root.database == database {
				root.expanded = true
			}
		}
	}
	m.refreshTree()
	return m.panels[panelObjects].selectNode(want)
}

// selectRelation is selectObject for a relation whose kind the caller
// does not know: a foreign-key jump or a Relations-tab walk lands on
// whatever the target happens to be.
func (m *Model) selectRelation(database, name string) bool {
	return m.selectObject(database, catTables, name) ||
		m.selectObject(database, catViews, name)
}

// expandSelected is `l`: open the branch under the cursor, or — when it
// is already open — step onto its first child. On a leaf it does nothing,
// which is what makes `l` safe to hold down.
func (m *Model) expandSelected() tea.Cmd {
	p := m.panels[panelObjects]
	n := m.selectedNode()
	if n == nil {
		return nil
	}
	if n.leaf() {
		return nil
	}
	if n.expanded {
		if len(n.children) > 0 {
			p.move(1)
		}
		return nil
	}
	cmd := m.expandNode(n)
	m.refreshTree()
	return cmd
}

// collapseSelected is `h`: close the branch under the cursor, or — when
// it is already closed or is a leaf — step out to its parent.
func (m *Model) collapseSelected() {
	p := m.panels[panelObjects]
	n := m.selectedNode()
	if n == nil {
		return
	}
	if !n.leaf() && n.expanded {
		n.expanded = false
		m.refreshTree()
		return
	}
	if parent := m.tree.parentOf(n); parent != nil {
		p.selectNode(parent)
	}
}
