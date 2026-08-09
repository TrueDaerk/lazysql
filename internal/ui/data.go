package ui

import (
	"context"
	"fmt"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"lazysql/internal/db"
)

// dataPageSize is how many rows one page of the grid holds. The grid
// never keeps more than one page in memory, which is what makes a
// 100k-row table exactly as cheap to browse as a 100-row one.
const dataPageSize = 100

// queryTimeout bounds a page read. It is longer than the catalog
// timeout because a filtered scan of a large table legitimately takes
// longer than an introspection query.
const queryTimeout = 30 * time.Second

// dataView is the state of the main view's Data tab: one page of one
// relation plus everything that shaped the query behind it.
//
// The cell cursor (row, col) is deliberately part of this state rather
// than of the renderer: inline editing reuses it.
type dataView struct {
	// Identity of the page on screen. Replies that do not match are
	// stale and get dropped.
	conn     string
	database string
	table    string

	cols []db.Column
	rows [][]any

	// Query shape. filter and sort survive paging; the filter also
	// survives a sort change, so the three compose.
	page   int
	filter *db.Filter
	sort   *db.Sort

	// total is the COUNT(*) behind the same filter. It arrives in its
	// own round trip, so until it does the status line omits it.
	total    int64
	hasTotal bool

	// Cell cursor. The row and column scroll windows are derived from it
	// at render time rather than stored, so a resize can never leave a
	// stale offset behind.
	row, col int

	loading bool
	err     string

	// req is bumped on every reload; a reply carrying an older req
	// belongs to a query the user has already moved past.
	req int
}

// open reports whether a relation is being browsed at all.
func (d dataView) open() bool { return d.table != "" }

// offset is the row offset of the current page in the full result.
func (d dataView) offset() int { return d.page * dataPageSize }

// pageCount is how many pages the count implies; 0 when unknown.
func (d dataView) pageCount() int {
	if !d.hasTotal {
		return 0
	}
	if d.total <= 0 {
		return 1
	}
	return int((d.total + dataPageSize - 1) / dataPageSize)
}

// sortOn returns the sort direction for a column name, if it is the one
// being sorted on.
func (d dataView) sortOn(name string) (desc, ok bool) {
	if d.sort == nil || d.sort.Column != name {
		return false, false
	}
	return d.sort.Desc, true
}

// clampCursor keeps the cell cursor inside the page after it changed
// shape (new page, fewer columns, empty result).
func (d *dataView) clampCursor() {
	if d.row >= len(d.rows) {
		d.row = len(d.rows) - 1
	}
	if d.row < 0 {
		d.row = 0
	}
	if d.col >= len(d.cols) {
		d.col = len(d.cols) - 1
	}
	if d.col < 0 {
		d.col = 0
	}
}

// cell returns the value under the cursor.
func (d dataView) cell() (any, bool) {
	if d.row < 0 || d.row >= len(d.rows) {
		return nil, false
	}
	if d.col < 0 || d.col >= len(d.rows[d.row]) {
		return nil, false
	}
	return d.rows[d.row][d.col], true
}

// ---------- messages ----------

// pageLoadedMsg carries the result of one page query.
type pageLoadedMsg struct {
	req    int
	conn   string
	table  string
	result *db.ResultSet
	err    error
}

// rowCountMsg carries the COUNT(*) that runs alongside the page query.
type rowCountMsg struct {
	req   int
	conn  string
	table string
	total int64
	err   error
}

// ---------- commands ----------

func loadPageCmd(drv db.Driver, d dataView, req int) tea.Cmd {
	if drv == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
		defer cancel()
		rs, err := drv.QueryPage(ctx, d.database, d.table, d.filter, d.sort, dataPageSize, d.offset())
		return pageLoadedMsg{req: req, conn: d.conn, table: d.table, result: rs, err: err}
	}
}

func countRowsCmd(drv db.Driver, d dataView, req int) tea.Cmd {
	if drv == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
		defer cancel()
		n, err := drv.CountRows(ctx, d.database, d.table, d.filter)
		return rowCountMsg{req: req, conn: d.conn, table: d.table, total: n, err: err}
	}
}

// ---------- model wiring ----------

// openTable starts browsing a relation from its first page. Filter and
// sort belong to the relation that was open, so they do not carry over.
func (m *Model) openTable(name string) tea.Cmd {
	m.table = name
	// The metadata described the relation being closed. The selected
	// tab survives — walking a table list with Structure open is the
	// point of the tabs — but its contents and scroll positions do not.
	m.resetMeta()
	if m.driver == nil {
		m.data = dataView{}
		return logCmd("-- open %s skipped: not connected", name)
	}
	m.data = dataView{
		conn:     m.active,
		database: m.database,
		table:    name,
		req:      m.data.req,
	}
	// The Data tab always loads: it backs the row count in the status
	// line and is where `esc`-and-back lands.
	return tea.Batch(m.reloadPage(), m.ensureMeta())
}

// reloadPage re-runs the page query and the count for the current
// filter/sort/page, and logs the exact SQL both of them execute.
func (m *Model) reloadPage() tea.Cmd {
	if m.driver == nil || !m.data.open() {
		return nil
	}
	m.data.req++
	m.data.loading = true
	m.data.err = ""
	d := m.data
	dialect := m.driver.Dialect()

	pageSQL := db.PageSQL(dialect, d.database, d.table, d.filter, d.sort, dataPageSize, d.offset())
	countSQL := db.CountSQL(dialect, d.database, d.table, d.filter)

	var cmds []tea.Cmd
	// The warning goes first so it is not lost above the statement it
	// is about.
	if d.filter != nil && d.filter.Verbatim {
		cmds = append(cmds, logCmd(
			"-- WARNING: filter %q could not be parameterized — appended verbatim", d.filter.Raw))
	}
	cmds = append(cmds,
		logCmd("%s", sqlLogLine(pageSQL, d.filter)),
		logCmd("%s", sqlLogLine(countSQL, d.filter)),
		func() tea.Msg { return historyEntryMsg{statement: pageSQL + ";"} },
		loadPageCmd(m.driver, d, m.data.req),
		countRowsCmd(m.driver, d, m.data.req),
	)
	return tea.Batch(cmds...)
}

// sqlLogLine renders a statement for the command log, with its bound
// arguments spelled out after it.
func sqlLogLine(query string, f *db.Filter) string {
	if f == nil || len(f.Args) == 0 {
		return query + ";"
	}
	return fmt.Sprintf("%s;  -- args %v", query, f.Args)
}

// fresh reports whether a reply still belongs to the page on screen.
func (m Model) fresh(req int, conn, table string) bool {
	return req == m.data.req && conn == m.active && table == m.data.table
}

// setDataFilter applies a new WHERE fragment and returns to page one.
// An empty fragment clears the filter.
func (m *Model) setDataFilter(raw string) tea.Cmd {
	if !m.data.open() || m.driver == nil {
		return nil
	}
	m.data.filter = db.ParseFilter(m.driver.Dialect(), raw)
	m.data.page = 0
	m.data.row = 0
	return m.reloadPage()
}

// toggleSort cycles the column under the cursor through ASC, DESC and
// unsorted. The filter is untouched, so the two compose.
func (m *Model) toggleSort() tea.Cmd {
	if !m.data.open() || m.data.col >= len(m.data.cols) {
		return nil
	}
	name := m.data.cols[m.data.col].Name
	switch {
	case m.data.sort == nil || m.data.sort.Column != name:
		m.data.sort = &db.Sort{Column: name}
	case !m.data.sort.Desc:
		m.data.sort = &db.Sort{Column: name, Desc: true}
	default:
		m.data.sort = nil
	}
	// A different ordering makes the old offset meaningless.
	m.data.page = 0
	m.data.row = 0
	return m.reloadPage()
}

// turnPage moves by whole pages. It refuses to walk off either end so a
// mistyped page key cannot leave the grid on an empty page.
func (m *Model) turnPage(delta int) tea.Cmd {
	if !m.data.open() {
		return nil
	}
	next := m.data.page + delta
	if next < 0 {
		return nil
	}
	if next > m.data.page {
		if m.data.hasTotal && next*dataPageSize >= int(m.data.total) {
			return logCmd("-- already on the last page")
		}
		if !m.data.hasTotal && len(m.data.rows) < dataPageSize {
			return logCmd("-- already on the last page")
		}
	}
	if next == m.data.page {
		return nil
	}
	m.data.page = next
	m.data.row = 0
	return m.reloadPage()
}

// updateData is the key handler of the focused main view. It runs in
// place of updateFocused, so navigation keys mean cells here.
func (m Model) updateData(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := m.keys
	switch {
	case key.Matches(msg, k.Down):
		if m.tab.metadata() {
			return m.updateMetaKeys(1)
		}
		m.data.row++
		m.data.clampCursor()
		return m, nil
	case key.Matches(msg, k.Up):
		if m.tab.metadata() {
			return m.updateMetaKeys(-1)
		}
		m.data.row--
		m.data.clampCursor()
		return m, nil
	case key.Matches(msg, k.Back):
		m.focusBack()
		return m, nil
	case key.Matches(msg, k.Enter):
		// enter re-runs whatever the visible tab shows: the relation is
		// already open, so the only thing left to drill into is fresh
		// data.
		if m.tab.metadata() {
			cmd := m.reloadMeta()
			return m, cmd
		}
		cmd := m.reloadPage()
		return m, cmd
	case key.Matches(msg, k.Actions):
		m.modal = m.actionsMenu()
		return m, nil
	}
	for _, a := range k.panelActions(panelMain) {
		if key.Matches(msg, a.binding) {
			return m.runAction(a.id)
		}
	}
	return m, nil
}

// focusBack pops the focus stack, the same way `esc` does in a panel.
func (m *Model) focusBack() {
	if n := len(m.prev); n > 0 {
		back := m.prev[n-1]
		m.prev = m.prev[:n-1]
		m.focus = back
		return
	}
	m.focus = panelTables
}

// dataActions handles the main view's context actions. It is called
// from runAction so a key press and the `a` menu share one path.
func (m Model) dataActions(id actionID) (Model, tea.Cmd, bool) {
	switch id {
	case actColLeft:
		m.data.col--
		m.data.clampCursor()
	case actColRight:
		m.data.col++
		m.data.clampCursor()
	case actNextPage:
		cmd := m.turnPage(1)
		return m, cmd, true
	case actPrevPage:
		cmd := m.turnPage(-1)
		return m, cmd, true
	case actSortColumn:
		cmd := m.toggleSort()
		return m, cmd, true
	case actWhereFilter:
		cur := ""
		if m.data.filter != nil {
			cur = m.data.filter.Raw
		}
		m.modal = newPromptModal(
			"Filter rows — WHERE",
			"id > 100 AND name LIKE 'a%'",
			cur,
			func(mm *Model, value string) tea.Cmd { return mm.setDataFilter(value) },
		)
	case actViewCell:
		v, ok := m.data.cell()
		if !ok {
			return m, nil, true
		}
		name := ""
		if m.data.col < len(m.data.cols) {
			name = m.data.cols[m.data.col].Name
		}
		m.modal = newCellModal(name, v)
	case actEditCell:
		cmd := m.startEdit()
		return m, cmd, true
	case actCommitChanges:
		cmd := m.openCommitModal()
		return m, cmd, true
	case actUnstageCell:
		cmd := m.unstageAtCursor()
		return m, cmd, true
	case actDiscardChanges:
		cmd := m.confirmDiscard()
		return m, cmd, true
	default:
		return m, nil, false
	}
	return m, nil, true
}
