package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"lazysql/internal/db"
)

// The Relations tab: a hub view of one relation's foreign keys. The
// outgoing half comes from the metadata fetch every introspection tab
// shares; the incoming half needs the namespace-wide scan
// (`ensureNamespaceFKs`) that `G` already uses, and is cached with it.
//
// `j`/`k` pick a related table and `enter` walks to it — the same jump
// history `esc`/`ctrl+o` unwind — which turns the tab into a schema
// walk rather than a static picture.

// relationsWideWidth is the narrowest main view that still gets the box
// drawing. Below it the tab degrades to a plain indented list: the hub
// box and its connectors need room around the longest edge label, and a
// broken box reads worse than no box.
const relationsWideWidth = 56

// relationsHubPad is the space between the relation's name and the hub
// box drawn around it.
const relationsHubPad = 2

// relationEdge is one foreign key as the Relations tab shows it, in
// whichever direction it points.
type relationEdge struct {
	// incoming is false for a constraint this relation declares and true
	// for one that points at it.
	incoming bool
	// database and table name the relation at the other end — the jump
	// target of `enter`.
	database string
	table    string
	// cols are the columns on this relation's side, refCols those on the
	// other side.
	cols    []string
	refCols []string
	name    string
}

// relationEdges is what the tab renders and what `j`/`k` walk: the
// outgoing constraints first, then the incoming ones, in one flat list
// so a single cursor indexes both halves.
func (m Model) relationEdges() []relationEdge {
	if !m.data.browsing() {
		return nil
	}
	var out []relationEdge
	for _, fk := range m.meta.fks {
		database, table := db.SplitQualified(fk.RefTable)
		if database == "" {
			database = m.data.database
		}
		out = append(out, relationEdge{
			database: database,
			table:    table,
			cols:     fk.Columns,
			refCols:  fk.RefColumns,
			name:     fk.Name,
		})
	}
	for _, ref := range incomingFor(m.refsCache[m.namespaceFKKey()], m.data.table) {
		out = append(out, relationEdge{
			incoming: true,
			database: m.data.database,
			table:    ref.table,
			cols:     ref.fk.Columns,
			refCols:  ref.fk.RefColumns,
			name:     ref.fk.Name,
		})
	}
	return out
}

// label spells one edge in `column → table.column` notation, read from
// the open relation outwards: an outgoing edge starts at the local
// columns, an incoming one at the referencing table's.
func (e relationEdge) label() string {
	if e.incoming {
		return qualify(e.table, e.cols) + " → " + strings.Join(e.refCols, ", ")
	}
	return strings.Join(e.cols, ", ") + " → " + qualify(e.table, e.refCols)
}

// qualify prefixes each column with its table, so a composite key reads
// as one target instead of a list of bare names.
func qualify(table string, cols []string) string {
	if len(cols) == 0 {
		return table
	}
	if len(cols) == 1 {
		return table + "." + cols[0]
	}
	return table + ".(" + strings.Join(cols, ", ") + ")"
}

// ---------- model wiring ----------

// relationsScanning reports whether the incoming half is still being
// read from the server.
func (m Model) relationsScanning() bool {
	k := m.namespaceFKKey()
	if _, ok := m.refsCache[k]; ok {
		return false
	}
	return m.fkLoading[k]
}

// selectedRelationEdge is the edge under the Relations cursor.
func (m Model) selectedRelationEdge() (relationEdge, bool) {
	edges := m.relationEdges()
	i := m.meta.row[mainTabRelations]
	if i < 0 || i >= len(edges) {
		return relationEdge{}, false
	}
	return edges[i], true
}

// walkRelation is `enter` on the Relations tab: open the related table
// with the same tab selected, so a chain of foreign keys can be walked
// one press at a time. Unlike `g`, no filter is applied — the tab is
// about the schema, not about one row — and the jump is pushed onto the
// same history `esc` and `ctrl+o` unwind.
func (m *Model) walkRelation() tea.Cmd {
	edge, ok := m.selectedRelationEdge()
	if !ok {
		return logCmd("-- walk to related table skipped: nothing selected")
	}
	if m.driver == nil {
		return logCmd("-- walk to %s skipped: not connected", edge.table)
	}
	m.pushBrowse()
	m.table = edge.table
	m.resetMeta()
	m.data = dataView{
		conn:     m.active,
		database: edge.database,
		table:    edge.table,
		req:      m.data.req,
	}
	// The [2] tree follows along whenever the target is in the browsed
	// namespace, so the shell does not claim a different relation is open.
	if edge.database == m.database {
		m.selectRelation(edge.database, edge.table)
	}
	return tea.Batch(
		logCmd("-- relations: walk to %s.%s", displayDatabase(edge.database), edge.table),
		m.reloadPage(),
		m.ensureFKs(),
		m.ensureMeta(),
	)
}

// ---------- rendering ----------

// relationsLines is the Relations tab: the outgoing constraints above
// the open relation, the incoming ones below it.
func (m Model) relationsLines(w, h int) []string {
	edges := m.relationEdges()
	cursor := m.meta.row[mainTabRelations]
	wide := w >= relationsWideWidth

	// The two halves are indexed by one cursor, so an edge's position in
	// the flat list is what selects it, not its position in its half.
	outgoing, incoming := 0, 0
	for _, e := range edges {
		if e.incoming {
			incoming++
		} else {
			outgoing++
		}
	}

	var lines []string
	// cursorLine is where the selected edge landed, so the window can
	// follow it through the headings and box art between the two halves.
	cursorLine := 0
	appendEdge := func(e relationEdge, i int, last bool) {
		if i == cursor {
			cursorLine = len(lines)
		}
		lines = append(lines, m.relationLine(e, i == cursor, last, wide, w))
	}

	lines = append(lines, m.style.title.Render(truncate("Outgoing — "+m.data.table+" references", w)))
	if outgoing == 0 {
		lines = append(lines, m.style.muted.Render("  none — this relation declares no foreign key"))
	}
	for i := 0; i < outgoing; i++ {
		appendEdge(edges[i], i, i == outgoing-1)
	}

	lines = append(lines, m.hubLines(w, wide)...)

	lines = append(lines, m.style.title.Render(truncate("Incoming — referenced by", w)))
	switch {
	case m.relationsScanning():
		lines = append(lines, m.style.pending.Render("  scanning the foreign keys of "+
			displayDatabase(m.data.database)+"…"))
	case incoming == 0:
		lines = append(lines, m.style.muted.Render("  none — no table in "+
			displayDatabase(m.data.database)+" references it"))
	}
	for i := outgoing; i < len(edges); i++ {
		appendEdge(edges[i], i, i == len(edges)-1)
	}

	body := maxInt(h-1, 0)
	start, end := rowWindow(len(lines), cursorLine, body)
	out := append([]string(nil), lines[start:end]...)
	return append(out, m.style.keyHint.Render(truncate(
		"j/k select · enter walks to the table · esc back · R re-scans", w)))
}

// relationLine renders one edge: its branch marker, the label and the
// constraint name where the engine reports one.
func (m Model) relationLine(e relationEdge, selected, last, wide bool, w int) string {
	marker := "- "
	if wide {
		marker = "  ├─ "
		if last {
			marker = "  └─ "
		}
	}
	text := marker + e.label()
	if e.name != "" {
		text += "  " + e.name
	}
	text = truncate(text, w)
	if selected && m.focus == panelMain {
		return m.style.rowCursor.Render(text)
	}
	return text
}

// hubLines draws the open relation between the two halves: a box with
// the arrows that say which way each half points, or — on a terminal too
// narrow for the box — the bare name.
func (m Model) hubLines(w int, wide bool) []string {
	name := m.data.table
	if !wide {
		return []string{"", m.style.titleFocused.Render(truncate(name, w)), ""}
	}
	inner := lipgloss.Width(name) + 2*relationsHubPad
	box := []string{
		"┌" + strings.Repeat("─", inner) + "┐",
		"│" + strings.Repeat(" ", relationsHubPad) + name + strings.Repeat(" ", relationsHubPad) + "│",
		"└" + strings.Repeat("─", inner) + "┘",
	}
	pad := maxInt((w-inner-2)/2, 0)
	indent := strings.Repeat(" ", pad)
	// The stem sits on the box's centre column, not the screen's: a box
	// wider than the space left of it would otherwise float off its own
	// arrows.
	stem := strings.Repeat(" ", pad+(inner+2)/2)

	out := []string{stem + "▲"}
	for _, l := range box {
		out = append(out, truncate(indent+l, w))
	}
	return append(out, stem+"▼", "")
}
