package ui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/db"
)

// gridRows is deliberately more than two pages, so paging has somewhere
// to go and the last page is a partial one.
const gridRows = 250

// dataBrowsing returns a model with the `grid` fixture table open in the
// main view. The fixture has a NULL column, a JSON column and a column
// wide enough to force truncation.
func dataBrowsing(t *testing.T) Model {
	t.Helper()
	m := browsing(t)
	ctx := context.Background()
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS grid`,
		`CREATE TABLE grid (id INTEGER PRIMARY KEY, name TEXT, note TEXT, payload TEXT)`,
		`INSERT INTO grid (id, name, note, payload)
		 WITH RECURSIVE seq(n) AS (
			SELECT 1 UNION ALL SELECT n + 1 FROM seq WHERE n < 250
		 )
		 SELECT n, 'name-' || n, NULL,
		        '{"n":' || n || ',"label":"a rather long payload value for column truncation"}'
		 FROM seq`,
	} {
		if _, err := m.driver.Exec(ctx, stmt); err != nil {
			t.Fatalf("fixture %q: %v", stmt, err)
		}
	}
	m = send(t, m, press('3'), press('R'))
	if !m.panels[panelTables].selectByName("grid") {
		t.Fatalf("fixture table not listed: %v", m.panels[panelTables].items)
	}
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.focus != panelMain {
		t.Fatalf("focus = %v, want the data grid", m.focus)
	}
	return m
}

// ctrl builds the KeyPressMsg for a control chord.
func ctrl(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Mod: tea.ModCtrl}
}

// Opening a relation fetches exactly one page, whatever the table's
// size, and reports the total separately.
func TestOpenTableFetchesOnePage(t *testing.T) {
	m := dataBrowsing(t)
	if got := len(m.data.rows); got != dataPageSize {
		t.Fatalf("rows in memory = %d, want %d", got, dataPageSize)
	}
	if !m.data.hasTotal || m.data.total != gridRows {
		t.Fatalf("total = %d (known=%v), want %d", m.data.total, m.data.hasTotal, gridRows)
	}
	if got := len(m.data.cols); got != 4 {
		t.Fatalf("columns = %d, want 4", got)
	}
	if !logContains(m, "LIMIT 100 OFFSET 0") {
		t.Fatalf("command log = %v", m.commandLog)
	}
}

// The grid shows the header, the types, the values, a dim NULL and a
// status line naming the page.
func TestGridRendersHeaderRowsAndStatus(t *testing.T) {
	m := dataBrowsing(t)
	out := m.View().Content
	for _, want := range []string{"id", "name", "payload", "name-1", nullText, "rows 1–100 of ~250", "(page 1/3)"} {
		if !strings.Contains(out, want) {
			t.Errorf("view is missing %q", want)
		}
	}
}

// Paging moves by whole pages, keeps to the ends, and asks the server
// for the matching OFFSET.
func TestPagingWalksAndClamps(t *testing.T) {
	m := dataBrowsing(t)
	m = send(t, m, ctrl('f'))
	if m.data.page != 1 {
		t.Fatalf("page = %d, want 1", m.data.page)
	}
	if !logContains(m, "LIMIT 100 OFFSET 100") {
		t.Fatalf("command log = %v", m.commandLog)
	}
	if !strings.Contains(m.View().Content, "rows 101–200 of ~250") {
		t.Fatal("status line does not follow the page")
	}

	// Last page is partial.
	m = send(t, m, ctrl('f'))
	if got := len(m.data.rows); got != 50 {
		t.Fatalf("last page rows = %d, want 50", got)
	}
	// And there is nothing past it.
	m = send(t, m, ctrl('f'))
	if m.data.page != 2 {
		t.Fatalf("page = %d, want to stop at the last page", m.data.page)
	}

	m = send(t, m, special(tea.KeyPgUp, 0), special(tea.KeyPgUp, 0), special(tea.KeyPgUp, 0))
	if m.data.page != 0 {
		t.Fatalf("page = %d, want to stop at the first page", m.data.page)
	}
}

// `s` cycles the column under the cursor through ASC, DESC and back to
// unsorted, and the ordering reaches the query.
func TestSortCyclesAndReachesTheQuery(t *testing.T) {
	m := dataBrowsing(t)
	m = send(t, m, press('l')) // cursor onto `name`
	if m.data.col != 1 {
		t.Fatalf("column cursor = %d, want 1", m.data.col)
	}

	m = send(t, m, press('s'))
	if m.data.sort == nil || m.data.sort.Column != "name" || m.data.sort.Desc {
		t.Fatalf("sort = %+v, want name ASC", m.data.sort)
	}
	if !logContains(m, `ORDER BY "name" ASC`) {
		t.Fatalf("command log = %v", m.commandLog)
	}
	// Text ordering, so name-1 sorts before name-10 and name-2.
	if got := m.data.rows[0][1]; got != "name-1" {
		t.Fatalf("first row = %v, want name-1", got)
	}
	if !strings.Contains(m.View().Content, "▲") {
		t.Error("ascending sort is not marked in the header")
	}

	m = send(t, m, press('s'))
	if m.data.sort == nil || !m.data.sort.Desc {
		t.Fatalf("sort = %+v, want name DESC", m.data.sort)
	}
	if !logContains(m, `ORDER BY "name" DESC`) {
		t.Fatalf("command log = %v", m.commandLog)
	}
	if got := m.data.rows[0][1]; got != "name-99" {
		t.Fatalf("first row = %v, want name-99", got)
	}

	m = send(t, m, press('s'))
	if m.data.sort != nil {
		t.Fatalf("sort = %+v, want the third press to clear it", m.data.sort)
	}
}

// `f` takes a WHERE fragment, binds its value, and the filter then
// survives both paging and a sort change.
func TestFilterIsBoundAndComposesWithSortAndPaging(t *testing.T) {
	m := dataBrowsing(t)
	m = send(t, m, press('f'))
	p, ok := m.modal.(*promptModal)
	if !ok {
		t.Fatalf("f opened %T, want the WHERE prompt", m.modal)
	}
	p.input.SetValue("id > 100")
	m = send(t, m, special(tea.KeyEnter, 0))

	if m.modal != nil {
		t.Fatal("prompt stayed open")
	}
	if m.data.filter == nil || m.data.filter.Verbatim {
		t.Fatalf("filter = %+v, want a parameterized one", m.data.filter)
	}
	if !logContains(m, `WHERE "id" > ?`) || !logContains(m, "-- args [100]") {
		t.Fatalf("command log = %v", m.commandLog)
	}
	if m.data.total != gridRows-100 {
		t.Fatalf("total = %d, want the filtered count %d", m.data.total, gridRows-100)
	}

	// Paging keeps the filter …
	m = send(t, m, ctrl('f'))
	if m.data.filter == nil || m.data.filter.Raw != "id > 100" {
		t.Fatalf("filter lost across a page turn: %+v", m.data.filter)
	}
	if !logContains(m, `WHERE "id" > ? LIMIT 100 OFFSET 100`) {
		t.Fatalf("command log = %v", m.commandLog)
	}

	// … and so does sorting, which also returns to the first page.
	m = send(t, m, press('s'))
	if m.data.filter == nil || m.data.filter.Raw != "id > 100" {
		t.Fatalf("filter lost across a sort: %+v", m.data.filter)
	}
	if m.data.page != 0 {
		t.Fatalf("page = %d, want a sort to return to the first page", m.data.page)
	}
	if !logContains(m, `WHERE "id" > ? ORDER BY "id" ASC LIMIT 100 OFFSET 0`) {
		t.Fatalf("command log = %v", m.commandLog)
	}

	// An empty fragment clears it.
	m = send(t, m, press('f'))
	m.modal.(*promptModal).input.SetValue("")
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.data.filter != nil {
		t.Fatalf("filter = %+v, want it cleared", m.data.filter)
	}
}

// A fragment the parser cannot take apart still runs, but the command
// log and the status line both say it was not parameterized.
func TestVerbatimFilterWarns(t *testing.T) {
	m := dataBrowsing(t)
	m = send(t, m, press('f'))
	m.modal.(*promptModal).input.SetValue("id IN (1,2,3)")
	m = send(t, m, special(tea.KeyEnter, 0))

	if m.data.filter == nil || !m.data.filter.Verbatim {
		t.Fatalf("filter = %+v, want a verbatim one", m.data.filter)
	}
	if !logContains(m, "WARNING") || !logContains(m, "could not be parameterized") {
		t.Fatalf("command log = %v", m.commandLog)
	}
	if len(m.data.rows) != 3 {
		t.Fatalf("rows = %d, want the 3 the fragment selects", len(m.data.rows))
	}
	if !strings.Contains(m.View().Content, "where (verbatim)") {
		t.Error("the grid does not flag the verbatim filter")
	}
}

// A bad fragment leaves the previous page on screen and reports why.
func TestBrokenFilterKeepsThePageAndReports(t *testing.T) {
	m := dataBrowsing(t)
	m = send(t, m, press('f'))
	m.modal.(*promptModal).input.SetValue("no_such_column IN (1, 2)")
	m = send(t, m, special(tea.KeyEnter, 0))

	if len(m.data.rows) != dataPageSize {
		t.Fatalf("rows = %d, want the previous page to survive", len(m.data.rows))
	}
	if m.data.err == "" {
		t.Fatal("the grid does not know the query failed")
	}
	if !logContains(m, "FAILED") {
		t.Fatalf("command log = %v", m.commandLog)
	}
	if m.data.loading {
		t.Fatal("loading flag survived the error")
	}
}

// `h`/`l` walk the cell cursor and clamp at both ends; `j`/`k` walk rows.
func TestCellCursorMovesAndClamps(t *testing.T) {
	m := dataBrowsing(t)
	m = send(t, m, press('l'), press('l'), press('l'), press('l'), press('l'))
	if got := m.data.col; got != len(m.data.cols)-1 {
		t.Fatalf("column = %d, want it clamped to %d", got, len(m.data.cols)-1)
	}
	m = send(t, m, press('h'), press('h'), press('h'), press('h'), press('h'))
	if m.data.col != 0 {
		t.Fatalf("column = %d, want 0", m.data.col)
	}
	m = send(t, m, press('j'), press('j'))
	if m.data.row != 2 {
		t.Fatalf("row = %d, want 2", m.data.row)
	}
	m = send(t, m, press('k'), press('k'), press('k'), press('k'))
	if m.data.row != 0 {
		t.Fatalf("row = %d, want 0", m.data.row)
	}
}

// `v` shows the untruncated value, pretty-printing JSON.
func TestViewCellPrettyPrintsJSON(t *testing.T) {
	m := dataBrowsing(t)
	m = send(t, m, press('l'), press('l'), press('l'), press('v')) // payload column
	c, ok := m.modal.(*cellModal)
	if !ok {
		t.Fatalf("v opened %T, want the cell modal", m.modal)
	}
	if len(c.lines) < 3 {
		t.Fatalf("cell body = %v, want indented JSON", c.lines)
	}
	if !strings.Contains(c.title, "json") {
		t.Errorf("title = %q, want it to mention json", c.title)
	}
	body := strings.Join(c.lines, "\n")
	if !strings.Contains(body, `"label"`) || !strings.Contains(body, "truncation") {
		t.Errorf("cell body does not hold the whole value: %q", body)
	}
	m = send(t, m, special(tea.KeyEscape, 0))
	if m.modal != nil {
		t.Fatal("esc did not close the cell modal")
	}
}

// A NULL cell reads as NULL rather than as an empty popup.
func TestViewCellShowsNull(t *testing.T) {
	m := dataBrowsing(t)
	m = send(t, m, press('l'), press('l'), press('v')) // note column, all NULL
	c, ok := m.modal.(*cellModal)
	if !ok {
		t.Fatalf("v opened %T, want the cell modal", m.modal)
	}
	if len(c.lines) != 1 || c.lines[0] != nullText {
		t.Fatalf("cell body = %v, want [%s]", c.lines, nullText)
	}
}

// The header sets itself off from the data with a rule whose `┼`
// junctions line up with the `│` separators in the name and type rows
// above it.
func TestGridHeaderRuleAndSeparators(t *testing.T) {
	m := dataBrowsing(t)
	cols, _ := m.buildGrid()
	header := m.gridHeader(cols, 0, 200)
	lines := strings.Split(header, "\n")
	if len(lines) != 3 {
		t.Fatalf("header = %d lines, want name/type/rule", len(lines))
	}
	want := len(cols) - 1
	if got := strings.Count(lines[0], colSepChar); got != want {
		t.Fatalf("name row has %d separators, want %d", got, want)
	}
	if got := strings.Count(lines[1], colSepChar); got != want {
		t.Fatalf("type row has %d separators, want %d", got, want)
	}
	if got := strings.Count(lines[2], ruleJunction); got != want {
		t.Fatalf("rule has %d junctions, want %d", got, want)
	}
	if !strings.Contains(lines[2], ruleChar) {
		t.Fatal("rule line has no horizontal rule characters")
	}
}

// Data rows carry the same column separator as the header.
func TestGridRowHasColumnSeparators(t *testing.T) {
	m := dataBrowsing(t)
	cols, kinds := m.buildGrid()
	row := m.gridRow(cols, 0, 0, kinds[0], 200)
	if want := len(cols) - 1; strings.Count(row, colSepChar) != want {
		t.Fatalf("row has %d separators, want %d", strings.Count(row, colSepChar), want)
	}
}

// A wide table scrolls sideways: the window follows the cursor and the
// grid says which columns are on screen.
func TestWideTableScrollsHorizontally(t *testing.T) {
	m := dataBrowsing(t)
	// Narrow enough that four columns cannot share the main view.
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = next.(Model)

	cols, _ := m.buildGrid()
	start, end := columnWindow(cols, 0, 30)
	if start != 0 || end >= len(cols) {
		t.Fatalf("window at the first column = [%d,%d), want a partial window from 0", start, end)
	}
	lastStart, lastEnd := columnWindow(cols, len(cols)-1, 30)
	if len(cols)-1 < lastStart || len(cols)-1 >= lastEnd {
		t.Fatalf("window [%d,%d) does not contain the cursor column %d", lastStart, lastEnd, len(cols)-1)
	}
	if lastStart == 0 {
		t.Fatal("window did not scroll to reach the last column")
	}

	m = send(t, m, press('l'), press('l'), press('l'))
	if out := m.View().Content; !strings.Contains(out, "columns ") {
		t.Error("the grid does not report the visible column range")
	}
}

// The grid survives every terminal size the shell claims to support.
func TestGridRendersAtManySizes(t *testing.T) {
	m := dataBrowsing(t)
	for _, size := range [][2]int{{60, 18}, {80, 24}, {200, 60}, {61, 19}} {
		for _, mode := range []screenMode{screenNormal, screenHalf, screenFull} {
			next, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			m = next.(Model)
			m.screen = mode
			if out := m.View().Content; out == "" {
				t.Fatalf("%dx%d mode %v rendered nothing", size[0], size[1], mode)
			}
		}
	}
}

// A reply for a query the user has already moved past is dropped.
func TestStalePageReplyIsIgnored(t *testing.T) {
	m := dataBrowsing(t)
	before := len(m.data.rows)
	m = send(t, m, pageLoadedMsg{
		req:    m.data.req - 1,
		conn:   m.active,
		table:  m.data.table,
		result: &db.ResultSet{Columns: []db.Column{{Name: "ghost"}}, Rows: [][]any{{1}}},
	})
	if len(m.data.rows) != before || len(m.data.cols) == 1 {
		t.Fatal("a stale page reply landed")
	}
	m = send(t, m, rowCountMsg{req: m.data.req, conn: "other-connection", table: m.data.table, total: 7})
	if m.data.total != gridRows {
		t.Fatalf("total = %d, want the stale count dropped", m.data.total)
	}
}

// With a relation open the grid joins the tab cycle after [4]; without
// one, tab skips it.
func TestTabCycleIncludesTheGridWhenOpen(t *testing.T) {
	m := dataBrowsing(t)
	m = send(t, m, press('4'), special(tea.KeyTab, 0))
	if m.focus != panelMain {
		t.Fatalf("focus = %v, want the grid after [4]", m.focus)
	}
	m = send(t, m, special(tea.KeyTab, 0))
	if m.focus != panelConnections {
		t.Fatalf("focus = %v, want to wrap to [1]", m.focus)
	}
	m = send(t, m, special(tea.KeyTab, tea.ModShift))
	if m.focus != panelMain {
		t.Fatalf("shift+tab: focus = %v, want the grid", m.focus)
	}

	m.data = dataView{}
	m.focus = panelQuery
	m = send(t, m, special(tea.KeyTab, 0))
	if m.focus != panelConnections {
		t.Fatalf("focus = %v, want tab to skip a closed grid", m.focus)
	}
}

// esc leaves the grid for the panel that opened it, and switching
// namespaces closes the page altogether.
func TestEscLeavesGridAndDatabaseSwitchClosesIt(t *testing.T) {
	m := dataBrowsing(t)
	m = send(t, m, special(tea.KeyEscape, 0))
	if m.focus != panelTables {
		t.Fatalf("focus = %v, want %v", m.focus, panelTables)
	}
	if !m.data.open() {
		t.Fatal("esc closed the page instead of only moving focus")
	}

	m = send(t, m, press('2'), special(tea.KeyEnter, 0))
	if m.data.open() {
		t.Fatal("the page survived a namespace switch")
	}
}

// Opening a second relation starts clean rather than carrying the
// previous one's filter and sort over.
func TestOpeningAnotherTableResetsQueryShape(t *testing.T) {
	m := dataBrowsing(t)
	m = send(t, m, press('f'))
	m.modal.(*promptModal).input.SetValue("id > 100")
	m = send(t, m, special(tea.KeyEnter, 0), press('s'))
	if m.data.filter == nil || m.data.sort == nil {
		t.Fatal("fixture did not set up a filter and a sort")
	}

	if _, err := m.driver.Exec(context.Background(),
		`CREATE TABLE IF NOT EXISTS other (id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	m = send(t, m, press('3'), press('R'))
	m.panels[panelTables].selectByName("other")
	m = send(t, m, special(tea.KeyEnter, 0))

	if m.data.table != "other" {
		t.Fatalf("table = %q, want other", m.data.table)
	}
	if m.data.filter != nil || m.data.sort != nil || m.data.page != 0 {
		t.Fatalf("query shape carried over: %+v", m.data)
	}
}
