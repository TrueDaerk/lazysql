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
	m = send(t, m, press('2'), press('R'))
	if !m.panels[panelObjects].selectByName("grid") {
		t.Fatalf("fixture table not listed: %v", m.panels[panelObjects].items)
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

// applyWhereFilter drives the filter modal's advanced mode: open it
// with `/`, toggle to the free-text field and apply the fragment. An
// empty fragment clears the filter.
func applyWhereFilter(t *testing.T, m Model, fragment string) Model {
	t.Helper()
	m = send(t, m, press('/'))
	fm, ok := m.modal.(*filterModal)
	if !ok {
		t.Fatalf("/ opened %T, want the filter modal", m.modal)
	}
	if !fm.advanced {
		m = send(t, m, ctrl('t'))
	}
	fm.form.field(filterFieldWhere).input.SetValue(fragment)
	return send(t, m, special(tea.KeyEnter, 0))
}

// applyColumnFilter drives the modal's structured mode: cycle to the
// column and the operator, type the value, apply.
func applyColumnFilter(t *testing.T, m Model, column string, op db.FilterOp, value string) Model {
	t.Helper()
	m = send(t, m, press('/'))
	fm, ok := m.modal.(*filterModal)
	if !ok {
		t.Fatalf("/ opened %T, want the filter modal", m.modal)
	}
	col := fm.form.field(filterFieldColumn)
	if !selectChoice(col, column) {
		t.Fatalf("column %q not offered: %v", column, col.values)
	}
	if !selectChoice(fm.form.field(filterFieldOperator), string(op)) {
		t.Fatalf("operator %q not offered", op)
	}
	fm.form.field(filterFieldValue).input.SetValue(value)
	return send(t, m, special(tea.KeyEnter, 0))
}

// selectChoice moves a select field onto one of its values.
func selectChoice(f *formField, value string) bool {
	for i, v := range f.values {
		if v == value {
			f.choice = i
			return true
		}
	}
	return false
}

// The advanced mode takes a WHERE fragment, binds its value, and the
// filter then survives both paging and a sort change.
func TestFilterIsBoundAndComposesWithSortAndPaging(t *testing.T) {
	m := dataBrowsing(t)
	m = applyWhereFilter(t, m, "id > 100")

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
	m = applyWhereFilter(t, m, "")
	if m.data.filter != nil {
		t.Fatalf("filter = %+v, want it cleared", m.data.filter)
	}
}

// A fragment the parser cannot take apart still runs, but the command
// log and the status line both say it was not parameterized.
func TestVerbatimFilterWarns(t *testing.T) {
	m := dataBrowsing(t)
	m = applyWhereFilter(t, m, "id IN (1,2,3)")

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
	m = applyWhereFilter(t, m, "no_such_column IN (1, 2)")

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

// A genuinely empty table says so plainly; only a filtered view that
// matches nothing implies an active filter.
func TestEmptyTableSaysSoWithoutImplyingAFilter(t *testing.T) {
	m := browsing(t)
	ctx := context.Background()
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS nobody`,
		`CREATE TABLE nobody (id INTEGER PRIMARY KEY)`,
	} {
		if _, err := m.driver.Exec(ctx, stmt); err != nil {
			t.Fatalf("fixture %q: %v", stmt, err)
		}
	}
	m = send(t, m, press('2'), press('R'))
	if !m.panels[panelObjects].selectByName("nobody") {
		t.Fatalf("fixture table not listed: %v", m.panels[panelObjects].items)
	}
	m = send(t, m, special(tea.KeyEnter, 0))

	out := m.View().Content
	if !strings.Contains(out, "table is empty") {
		t.Errorf("view = %q, want it to say the table is empty", out)
	}
	if strings.Contains(out, "no rows match") {
		t.Errorf("view = %q, an unfiltered empty table should not imply a filter", out)
	}
}

// A filter that matches nothing still says "no rows match", since a
// filter is active and could plausibly be loosened.
func TestFilteredEmptyResultSaysNoRowsMatch(t *testing.T) {
	m := dataBrowsing(t)
	m = applyWhereFilter(t, m, "id = -1")

	out := m.View().Content
	if !strings.Contains(out, "no rows match") {
		t.Errorf("view = %q, want it to say no rows match", out)
	}
	if strings.Contains(out, "table is empty") {
		t.Errorf("view = %q, a filtered view should not say the table is empty", out)
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

// With a relation open the grid joins the tab cycle after [3]; without
// one, tab skips it.
func TestTabCycleIncludesTheGridWhenOpen(t *testing.T) {
	m := dataBrowsing(t)
	m = send(t, m, press('3'), special(tea.KeyTab, 0))
	if m.focus != panelMain {
		t.Fatalf("focus = %v, want the grid after [3]", m.focus)
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
	if m.focus != panelObjects {
		t.Fatalf("focus = %v, want %v", m.focus, panelObjects)
	}
	if !m.data.open() {
		t.Fatal("esc closed the page instead of only moving focus")
	}

	// openDatabase is what a namespace switch runs, whichever way the
	// tree reaches it.
	m = send(t, m, focusPanelMsg{id: panelObjects})
	cmd := m.openDatabase(pseudoDatabase)
	_ = drain(cmd)
	if m.data.open() {
		t.Fatal("the page survived a namespace switch")
	}
}

// Opening a second relation starts clean rather than carrying the
// previous one's filter and sort over.
func TestOpeningAnotherTableResetsQueryShape(t *testing.T) {
	m := dataBrowsing(t)
	m = applyWhereFilter(t, m, "id > 100")
	m = send(t, m, press('s'))
	if m.data.filter == nil || m.data.sort == nil {
		t.Fatal("fixture did not set up a filter and a sort")
	}

	if _, err := m.driver.Exec(context.Background(),
		`CREATE TABLE IF NOT EXISTS other (id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	m = send(t, m, press('2'), press('R'))
	m.panels[panelObjects].selectByName("other")
	m = send(t, m, special(tea.KeyEnter, 0))

	if m.data.table != "other" {
		t.Fatalf("table = %q, want other", m.data.table)
	}
	if m.data.filter != nil || m.data.sort != nil || m.data.page != 0 {
		t.Fatalf("query shape carried over: %+v", m.data)
	}
}

// `/` opens the structured modal on the cursor's column and applies a
// parameterized condition: the value is bound, the page reloads from the
// first page and the count sees the same rows.
func TestFilterModalBindsStructuredCondition(t *testing.T) {
	m := dataBrowsing(t)
	m = send(t, m, ctrl('f'))  // page 2, so applying has to return to page 1
	m = send(t, m, press('l')) // cursor onto `name`

	m2 := send(t, m, press('/'))
	fm, ok := m2.modal.(*filterModal)
	if !ok {
		t.Fatalf("/ opened %T, want the filter modal", m2.modal)
	}
	if fm.advanced {
		t.Error("the modal opened in advanced mode, want the structured one")
	}
	if got := fm.form.rawValue(filterFieldColumn); got != "name" {
		t.Errorf("column preselected = %q, want the cursor column name", got)
	}

	m = applyColumnFilter(t, m, "name", db.OpLike, "name-1%")
	if m.modal != nil {
		t.Fatal("the modal stayed open")
	}
	if m.data.filter == nil || m.data.filter.Verbatim {
		t.Fatalf("filter = %+v, want a parameterized one", m.data.filter)
	}
	if len(m.data.filter.Args) != 1 || m.data.filter.Args[0] != "name-1%" {
		t.Fatalf("args = %#v, want the pattern bound", m.data.filter.Args)
	}
	if m.data.page != 0 {
		t.Fatalf("page = %d, want the first page", m.data.page)
	}
	if !logContains(m, `WHERE "name" LIKE ?`) || !logContains(m, "-- args [name-1%]") {
		t.Fatalf("command log = %v", m.commandLog)
	}
	// name-1, name-1x and name-1xx: 1, 10–19, 100–199 = 111 rows.
	if m.data.total != 111 {
		t.Fatalf("total = %d, want the filtered count 111", m.data.total)
	}
	if !strings.Contains(m.View().Content, "where name LIKE 'name-1%'") {
		t.Error("the status line does not name the active filter")
	}
}

// A value that would end a string literal or act as a wildcard is data:
// it is bound, so it matches literally and injects nothing.
func TestFilterModalBindsQuotesAndWildcards(t *testing.T) {
	m := dataBrowsing(t)
	if _, err := m.driver.Exec(context.Background(),
		`INSERT INTO grid (id, name, note, payload) VALUES (900, 'o''%brien', NULL, NULL)`); err != nil {
		t.Fatal(err)
	}
	m = applyColumnFilter(t, m, "name", db.OpEq, `o'%brien`)
	if len(m.data.rows) != 1 {
		t.Fatalf("rows = %d, want the one literal match", len(m.data.rows))
	}

	m = applyColumnFilter(t, m, "name", db.OpEq, `x' OR 1=1 --`)
	if len(m.data.rows) != 0 {
		t.Fatalf("rows = %d, want none — the value is not SQL", len(m.data.rows))
	}
	if m.data.err != "" {
		t.Fatalf("the injected value broke the statement: %s", m.data.err)
	}
}

// IS NULL and IS NOT NULL take no value, so the modal hides the field
// and the filter carries no argument.
func TestFilterModalNullOperatorsNeedNoValue(t *testing.T) {
	m := dataBrowsing(t)
	m = send(t, m, press('/'))
	fm := m.modal.(*filterModal)
	if !selectChoice(fm.form.field(filterFieldOperator), string(db.OpIsNull)) {
		t.Fatal("IS NULL not offered")
	}
	for _, f := range fm.form.visibleFields() {
		if f.name == filterFieldValue {
			t.Error("the value field is visible for IS NULL")
		}
	}

	m = applyColumnFilter(t, m, "note", db.OpIsNull, "")
	if m.data.filter == nil || len(m.data.filter.Args) != 0 {
		t.Fatalf("filter = %+v, want no bound argument", m.data.filter)
	}
	if !logContains(m, `WHERE "note" IS NULL`) {
		t.Fatalf("command log = %v", m.commandLog)
	}
	if m.data.total != gridRows {
		t.Fatalf("total = %d, want all %d rows (note is always NULL)", m.data.total, gridRows)
	}

	m = applyColumnFilter(t, m, "note", db.OpIsNotNull, "")
	if m.data.total != 0 {
		t.Fatalf("total = %d, want none", m.data.total)
	}
}

// `F` drops the filter and reloads the unfiltered first page.
func TestClearFilterRestoresTheFullView(t *testing.T) {
	m := dataBrowsing(t)
	m = applyColumnFilter(t, m, "id", db.OpGt, "200")
	if m.data.total != 50 {
		t.Fatalf("total = %d, want 50", m.data.total)
	}

	m = send(t, m, press('F'))
	if m.data.filter != nil || m.data.conds != nil {
		t.Fatalf("filter = %+v, conds = %+v, want both cleared", m.data.filter, m.data.conds)
	}
	if m.data.total != gridRows {
		t.Fatalf("total = %d, want the unfiltered %d", m.data.total, gridRows)
	}
	if strings.Contains(m.View().Content, "where ") {
		t.Error("the status line still shows a filter")
	}

	// Clearing twice costs no round trip.
	before := len(m.commandLog)
	m = send(t, m, press('F'))
	if !logContains(m, "no filter to clear") {
		t.Fatalf("command log = %v", m.commandLog[before:])
	}
}

// Repeating `/` with the AND toggle on adds a condition to the active
// filter instead of replacing it.
func TestFilterModalAndsConditions(t *testing.T) {
	m := dataBrowsing(t)
	m = applyColumnFilter(t, m, "id", db.OpGt, "200")

	m = send(t, m, press('/'))
	fm := m.modal.(*filterModal)
	andField := fm.form.field(filterFieldAnd)
	if !fm.form.isVisible(andField) {
		t.Fatal("the AND toggle is hidden while a structured filter is active")
	}
	andField.on = true
	selectChoice(fm.form.field(filterFieldColumn), "name")
	selectChoice(fm.form.field(filterFieldOperator), string(db.OpLike))
	fm.form.field(filterFieldValue).input.SetValue("name-24%")
	m = send(t, m, special(tea.KeyEnter, 0))

	if len(m.data.conds) != 2 {
		t.Fatalf("conds = %+v, want both conditions", m.data.conds)
	}
	if !logContains(m, `WHERE "id" > ? AND "name" LIKE ?`) {
		t.Fatalf("command log = %v", m.commandLog)
	}
	// 240–249 are above 200 and match the pattern.
	if m.data.total != 10 {
		t.Fatalf("total = %d, want 10", m.data.total)
	}
}

// A value the column cannot hold is reported in the modal, which stays
// open with the rest of the form intact.
func TestFilterModalReportsBadValue(t *testing.T) {
	m := dataBrowsing(t)
	m = send(t, m, press('/'))
	fm := m.modal.(*filterModal)
	selectChoice(fm.form.field(filterFieldColumn), "id")
	fm.form.field(filterFieldValue).input.SetValue("abc")
	m = send(t, m, special(tea.KeyEnter, 0))

	if m.modal == nil {
		t.Fatal("the modal closed on an invalid value")
	}
	if fm.form.err == "" {
		t.Error("the modal shows no reason")
	}
	if m.data.filter != nil {
		t.Fatalf("filter = %+v, want the grid untouched", m.data.filter)
	}
}

// ctrl+t swaps the structured fields for the free-text fragment and
// back; esc cancels either mode without touching the grid.
func TestFilterModalTogglesAdvancedAndCancels(t *testing.T) {
	m := dataBrowsing(t)
	m = send(t, m, press('/'))
	out := m.View().Content
	for _, want := range []string{"Filter rows", "Column", "Operator", "Value", "ctrl+t advanced"} {
		if !strings.Contains(out, want) {
			t.Errorf("the modal is missing %q", want)
		}
	}

	m = send(t, m, ctrl('t'))
	fm := m.modal.(*filterModal)
	if !fm.advanced {
		t.Fatal("ctrl+t did not switch to advanced mode")
	}
	names := map[string]bool{}
	for _, f := range fm.form.visibleFields() {
		names[f.name] = true
	}
	if !names[filterFieldWhere] || names[filterFieldColumn] {
		t.Fatalf("advanced mode shows %v", names)
	}

	m = send(t, m, ctrl('t'))
	if fm.advanced {
		t.Fatal("ctrl+t does not toggle back")
	}
	m = send(t, m, special(tea.KeyEsc, 0))
	if m.modal != nil {
		t.Fatal("esc did not close the modal")
	}
	if m.data.filter != nil {
		t.Fatalf("filter = %+v, want esc to change nothing", m.data.filter)
	}
}

// A selection is a set of page row indices, so every query-shape change
// drops it — a stale index would otherwise stage a bulk edit on rows the
// user never picked. `R` is covered in copy_test.go; these are the other
// three ways the rows change under the same indices.
func TestSelectionClearedByEveryQueryShapeChange(t *testing.T) {
	for _, tc := range []struct {
		name  string
		apply func(*testing.T, Model) Model
	}{
		{"sort", func(t *testing.T, m Model) Model { return send(t, m, press('s')) }},
		{"filter", func(t *testing.T, m Model) Model { return applyWhereFilter(t, m, "id > 10") }},
		{"next page", func(t *testing.T, m Model) Model { return send(t, m, ctrl('f')) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := send(t, dataBrowsing(t), ctrl('v'), press('j'), press('j'))
			if got := len(m.data.selectedRows()); got != 3 {
				t.Fatalf("selected %d rows, want 3 before the %s", got, tc.name)
			}
			m = tc.apply(t, m)
			if m.data.selecting() {
				t.Fatalf("the selection survived the %s: %+v", tc.name, m.data.sel)
			}
			if m.keys.CopySelection.Enabled() {
				t.Fatalf("ctrl+c stayed bound to the copy across the %s", tc.name)
			}
		})
	}
}

// A query result is paged in memory rather than re-read, so setPage has
// to drop the selection the way reloadPage does.
func TestSelectionClearedOnQueryResultPaging(t *testing.T) {
	d := dataView{query: "SELECT 1", all: make([][]any, 2*dataPageSize)}
	d.setPage(0)
	d.sel = rowSelection{active: true, anchor: 1}
	d.row = 4
	d.setPage(1)
	if d.selecting() || len(d.selectedRows()) != 0 {
		t.Fatalf("selection = %+v, want it dropped with the page", d.sel)
	}
}
