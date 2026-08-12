package ui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

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
	start, end := columnWindow(cols, 0, 30, 0)
	if start != 0 || end >= len(cols) {
		t.Fatalf("window at the first column = [%d,%d), want a partial window from 0", start, end)
	}
	lastStart, lastEnd := columnWindow(cols, len(cols)-1, 30, 0)
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

// ---------- cursor and highlight ----------
//
// The cell cursor is only useful if the cell it points at is the cell the
// user sees highlighted: `enter`, `e`, `v` and `y` all act on
// m.data.row/col while the eye reads the tinted cell. The helpers below
// read the highlight straight out of the rendered frame, so a test can
// assert the two agree whatever the navigation, the page or the size.

// cursorTint is the SGR parameter list the cell cursor sets as its
// background. It is matched instead of the whole style because cellStyle
// adds a foreground of its own to NULL, staged and phantom cells.
func cursorTint() string {
	probe := lipgloss.NewStyle().Background(colorCellCursorBg).Render("x")
	i := strings.Index(probe, "\x1b[")
	return probe[i+2 : i+strings.Index(probe[i:], "m")]
}

// gridBox renders the grid into exactly the content box the current
// layout gives it — the frame the user is looking at.
func gridBox(m Model) string {
	w, h, ok := m.gridViewport()
	if !ok {
		return ""
	}
	return m.dataBody(w, h)
}

// highlightedCell returns the text of the cell rendered with the cursor
// tint, the body line it sits on, and how many cells carry the tint —
// which has to be exactly one while the grid has rows.
func highlightedCell(body string) (text string, line, count int) {
	tint := cursorTint()
	line = -1
	for i, l := range strings.Split(body, "\n") {
		for _, seq := range strings.Split(l, "\x1b[")[1:] {
			k := strings.Index(seq, "m")
			if k < 0 || !strings.Contains(seq[:k], tint) {
				continue
			}
			cell := seq[k+1:]
			if e := strings.Index(cell, "\x1b"); e >= 0 {
				cell = cell[:e]
			}
			text, line = strings.TrimRight(cell, " "), i
			count++
		}
	}
	return text, line, count
}

// cursorCellText is the value the cursor cell's actions work on — what
// `v` shows, `e` edits and `y` copies — formatted the way the grid
// formats it, so it can be compared with what was rendered.
func cursorCellText(m Model) (string, bool) {
	if m.data.col < 0 || m.data.col >= len(m.data.cols) {
		return "", false
	}
	if ins, ok := m.phantomAtCursor(); ok {
		v, bound := insertValueFor(ins, m.data.cols[m.data.col].Name)
		if !bound {
			return defaultText, true
		}
		return gridCellText(v, nullText), true
	}
	v, ok := m.data.cell()
	if !ok {
		return "", false
	}
	return gridCellText(v, nullText), true
}

// assertCursorRendered checks the one invariant this whole area exists
// for: the highlighted cell is the cell the cursor points at.
func assertCursorRendered(t *testing.T, m Model, what string) {
	t.Helper()
	want, ok := cursorCellText(m)
	if !ok {
		t.Fatalf("%s: cursor (%d,%d) points outside the page (%d rows, %d columns)",
			what, m.data.row, m.data.col, m.data.rowCount(), len(m.data.cols))
	}
	cols, _ := m.buildGrid()
	want = strings.TrimRight(truncate(want, cols[m.data.col].width), " ")

	got, _, n := highlightedCell(gridBox(m))
	if n != 1 {
		t.Fatalf("%s: %d cells carry the cursor tint, want exactly 1 (cursor at %d,%d)",
			what, n, m.data.row, m.data.col)
	}
	if got != want {
		t.Fatalf("%s: the highlighted cell reads %q, but the cursor is on %q at (%d,%d)",
			what, got, want, m.data.row, m.data.col)
	}
}

// repeatKey is a run of the same key press.
func repeatKey(r rune, n int) []tea.Msg {
	out := make([]tea.Msg, n)
	for i := range out {
		out[i] = press(r)
	}
	return out
}

// repeatWheel is a run of wheel notches over the middle of the grid.
func repeatWheel(button tea.MouseButton, n int) []tea.Msg {
	out := make([]tea.Msg, n)
	for i := range out {
		out[i] = tea.MouseWheelMsg{X: 70, Y: 8, Button: button}
	}
	return out
}

// Whatever the user did to get there, the highlighted cell is the cell
// every action operates on. Each step is checked in turn, so a
// regression names the key that broke it.
func TestHighlightFollowsTheCursorThroughEverySequence(t *testing.T) {
	steps := []struct {
		what string
		msgs []tea.Msg
	}{
		{"j into the page", []tea.Msg{press('j'), press('j'), press('j')}},
		{"j past the last visible row", repeatKey('j', 40)},
		{"k back inside the window", []tea.Msg{press('k'), press('k')}},
		{"l to the last column", repeatKey('l', 5)},
		{"h back to the first", repeatKey('h', 5)},
		{"shrink the terminal", []tea.Msg{tea.WindowSizeMsg{Width: 80, Height: 24}}},
		{"grow it again", []tea.Msg{tea.WindowSizeMsg{Width: 200, Height: 60}}},
		{"next page", []tea.Msg{ctrl('f')}},
		{"previous page", []tea.Msg{ctrl('b')}},
		{"sort", []tea.Msg{press('s')}},
		{"refresh", []tea.Msg{special(tea.KeyEnter, 0)}},
		{"wheel down", repeatWheel(tea.MouseWheelDown, 8)},
		{"wheel up", repeatWheel(tea.MouseWheelUp, 2)},
	}
	m := dataBrowsing(t)
	for _, step := range steps {
		m = send(t, m, step.msgs...)
		assertCursorRendered(t, m, step.what)
	}
}

// Moving up inside a scrolled window moves the highlight, not the page.
// A window derived from the cursor alone used to pin the cursor to the
// last visible row: `k` scrolled the rows under a highlight that never
// moved, and then jumped once the cursor fell inside the top window.
func TestMovingUpMovesTheHighlightNotThePage(t *testing.T) {
	m := dataBrowsing(t)
	m = send(t, m, repeatKey('j', 40)...)

	_, atBottom, _ := highlightedCell(gridBox(m))
	first := strings.Split(stripStyles(gridBox(m)), "\n")[3]

	m = send(t, m, press('k'))
	_, line, _ := highlightedCell(gridBox(m))
	if line != atBottom-1 {
		t.Fatalf("k moved the highlight from line %d to %d, want one line up", atBottom, line)
	}
	if got := strings.Split(stripStyles(gridBox(m)), "\n")[3]; got != first {
		t.Fatalf("k scrolled the page: the first row was %q, now %q", first, got)
	}
}

// A cursor deep in a scrolled page keeps its cell across a resize: the
// window is clamped around the cursor rather than re-derived from it.
func TestResizeKeepsTheCursorUnderTheHighlight(t *testing.T) {
	m := dataBrowsing(t)
	m = send(t, m, repeatKey('j', 60)...)
	before, _ := cursorCellText(m)

	for _, size := range [][2]int{{60, 18}, {200, 60}, {100, 30}, {61, 19}} {
		m = send(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		assertCursorRendered(t, m, fmt.Sprintf("resize to %dx%d", size[0], size[1]))
		if got, _ := cursorCellText(m); got != before {
			t.Fatalf("resize to %dx%d moved the cursor from %q to %q",
				size[0], size[1], before, got)
		}
	}
}

// The horizontal-scroll hint takes a body row like every other line, so
// a grid that scrolls sideways renders one row of the page less — rather
// than one line more than the box holds, which pushed the command log
// and the options bar off the bottom of the screen.
func TestGridNeverRendersMoreLinesThanItsBox(t *testing.T) {
	m := dataBrowsing(t)
	for _, size := range [][2]int{{60, 18}, {80, 24}, {100, 30}, {200, 60}, {61, 19}} {
		m = send(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		w, h, ok := m.gridViewport()
		if !ok {
			t.Fatalf("%dx%d: the grid is not on screen", size[0], size[1])
		}
		if got := lipgloss.Height(m.dataBody(w, h)); got != h {
			t.Errorf("%dx%d: the grid rendered %d lines into a %d-line box",
				size[0], size[1], got, h)
		}
		if got := lipgloss.Height(m.View().Content); got != size[1] {
			t.Errorf("%dx%d: the frame is %d lines tall", size[0], size[1], got)
		}
	}
}

// Discarding the changeset takes the phantom rows with it. A cursor
// standing on one of them has to come back onto a row that still exists,
// or every action after it works on nothing while the grid shows no
// highlight at all.
func TestDiscardingChangesBringsTheCursorBack(t *testing.T) {
	m := dataBrowsing(t)
	m, f := insertForm(t, m, 'n')
	setField(f, "name", "phantom")
	m = send(t, m, special(tea.KeyEnter, 0))

	m.data.row = len(m.data.rows)
	m.clampCursor()
	assertCursorRendered(t, m, "on the phantom row")

	m = send(t, m, press('U'), press('y'))
	if m.changes.Len() != 0 {
		t.Fatalf("changeset = %d after U, want it discarded", m.changes.Len())
	}
	if m.data.row >= len(m.data.rows) {
		t.Fatalf("cursor row = %d, want it back inside the %d-row page",
			m.data.row, len(m.data.rows))
	}
	assertCursorRendered(t, m, "after discarding the changeset")
}

// The row window scrolls as little as it can and never hides the cursor,
// whatever offset it starts from — including one left over from a taller
// terminal or a longer page.
func TestRowWindowClampsAroundTheCursor(t *testing.T) {
	cases := []struct {
		what                 string
		n, cursor, rows, off int
		wantStart, wantEnd   int
	}{
		{"short page fits whole", 3, 1, 10, 0, 0, 3},
		{"cursor inside the window stays put", 100, 12, 10, 8, 8, 18},
		{"cursor below scrolls down minimally", 100, 20, 10, 8, 11, 21},
		{"cursor above scrolls up minimally", 100, 4, 10, 8, 4, 14},
		{"offset past the end is pulled back", 100, 95, 10, 400, 90, 100},
		{"offset left over from a shorter page", 100, 20, 5, 60, 20, 25},
		{"empty page", 0, 0, 10, 3, 0, 0},
		{"no room", 100, 20, 0, 0, 0, 0},
	}
	for _, c := range cases {
		start, end := rowWindow(c.n, c.cursor, c.rows, c.off)
		if start != c.wantStart || end != c.wantEnd {
			t.Errorf("%s: rowWindow(%d,%d,%d,%d) = [%d,%d), want [%d,%d)",
				c.what, c.n, c.cursor, c.rows, c.off, start, end, c.wantStart, c.wantEnd)
		}
		if c.n > 0 && c.rows > 0 && (c.cursor < start || c.cursor >= end) {
			t.Errorf("%s: window [%d,%d) hides the cursor row %d", c.what, start, end, c.cursor)
		}
	}
}

// The column window follows the same rule: it packs from the offset it is
// given and only scrolls far enough to keep the cursor column on screen.
func TestColumnWindowClampsAroundTheCursor(t *testing.T) {
	cols := make([]gridColumn, 8)
	for i := range cols {
		cols[i].width = 10
	}
	// Three ten-cell columns and their two separators fill 32 cells.
	const w = 32

	if start, end := columnWindow(cols, 0, w, 0); start != 0 || end != 3 {
		t.Errorf("first column: window = [%d,%d), want [0,3)", start, end)
	}
	if start, end := columnWindow(cols, 4, w, 3); start != 3 || end != 6 {
		t.Errorf("cursor inside the window: [%d,%d), want [3,6)", start, end)
	}
	if start, end := columnWindow(cols, 7, w, 3); start != 5 || end != 8 {
		t.Errorf("cursor to the right: [%d,%d), want [5,8)", start, end)
	}
	if start, end := columnWindow(cols, 1, w, 5); start != 1 || end != 4 {
		t.Errorf("cursor to the left: [%d,%d), want [1,4)", start, end)
	}
	// A column wider than the box still renders, alone.
	wide := []gridColumn{{width: 4}, {width: 80}}
	if start, end := columnWindow(wide, 1, 10, 0); start != 1 || end != 2 {
		t.Errorf("oversized column: [%d,%d), want [1,2)", start, end)
	}
}

// stripStyles drops the SGR sequences from a rendered block.
func stripStyles(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			in = true
		case in && r == 'm':
			in = false
		case !in:
			b.WriteRune(r)
		}
	}
	return b.String()
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

// shiftDown/shiftUp build the KeyPressMsg Bubble Tea v2 reports for
// shift+down/shift+up on terminals that support it.
func shiftDown() tea.KeyPressMsg { return special(tea.KeyDown, tea.ModShift) }
func shiftUp() tea.KeyPressMsg   { return special(tea.KeyUp, tea.ModShift) }

// shift+down from a normal grid state (no selection running) anchors one at
// the cursor row and moves, the same as ctrl+v followed by j — and further
// presses extend it exactly like j does.
func TestShiftDownStartsAndExtendsSelection(t *testing.T) {
	m := dataBrowsing(t)
	if m.data.selecting() {
		t.Fatalf("selection already running before any shift+down")
	}
	m = send(t, m, shiftDown())
	if !m.data.selecting() {
		t.Fatalf("shift+down did not start a selection")
	}
	if got := len(m.data.selectedRows()); got != 2 {
		t.Fatalf("selected %d rows after one shift+down, want 2", got)
	}
	if !m.keys.CopySelection.Enabled() {
		t.Fatalf("ctrl+c was not enabled by shift+down")
	}

	m = send(t, m, shiftDown())
	if got := len(m.data.selectedRows()); got != 3 {
		t.Fatalf("selected %d rows after a second shift+down, want 3", got)
	}

	m = send(t, m, shiftUp())
	if got := len(m.data.selectedRows()); got != 2 {
		t.Fatalf("selected %d rows after shift+up shrank it, want 2", got)
	}
}

// shift+up from a normal grid state anchors upward instead, so it is not
// just an alias for shift+down.
func TestShiftUpStartsSelectionUpward(t *testing.T) {
	m := send(t, dataBrowsing(t), press('j'), press('j'), press('j'))
	if m.data.row != 3 {
		t.Fatalf("cursor row = %d, want 3 before shift+up", m.data.row)
	}
	m = send(t, m, shiftUp())
	if !m.data.selecting() {
		t.Fatalf("shift+up did not start a selection")
	}
	if got := len(m.data.selectedRows()); got != 2 {
		t.Fatalf("selected %d rows after one shift+up, want 2", got)
	}
	if m.data.row != 2 {
		t.Fatalf("cursor row = %d, want 2 after shift+up moved it", m.data.row)
	}
}

// esc clears a shift-started selection the same way it clears a ctrl+v one.
func TestEscClearsAShiftStartedSelection(t *testing.T) {
	m := send(t, dataBrowsing(t), shiftDown(), shiftDown())
	if !m.data.selecting() {
		t.Fatalf("shift+down did not start a selection")
	}
	m = send(t, m, special(tea.KeyEsc, 0))
	if m.data.selecting() {
		t.Fatalf("esc did not clear the shift-started selection: %+v", m.data.sel)
	}
	if m.keys.CopySelection.Enabled() {
		t.Fatalf("ctrl+c stayed bound to the copy after esc cleared the selection")
	}
}

// Once a ctrl+v selection is running, shift+up/shift+down extend or shrink
// it exactly like k/j do — the alternate starting gesture does not change
// how the running selection behaves.
func TestShiftExtendsACtrlVSelectionLikeVimKeys(t *testing.T) {
	m := send(t, dataBrowsing(t), ctrl('v'), shiftDown(), shiftDown())
	if got := len(m.data.selectedRows()); got != 3 {
		t.Fatalf("selected %d rows after ctrl+v and two shift+down, want 3", got)
	}
}

// shiftLeft/shiftRight are the sideways half of the gesture, as a
// terminal that reports modified arrows delivers them.
func shiftLeft() tea.KeyPressMsg  { return special(tea.KeyLeft, tea.ModShift) }
func shiftRight() tea.KeyPressMsg { return special(tea.KeyRight, tea.ModShift) }

// shift+right with nothing selected anchors a selection at the cursor
// cell and narrows it to columns in one gesture: one row, two columns.
func TestShiftRightStartsAColumnSelection(t *testing.T) {
	m := send(t, dataBrowsing(t), shiftRight())
	if !m.data.selecting() {
		t.Fatalf("shift+right did not start a selection")
	}
	if got := len(m.data.selectedRows()); got != 1 {
		t.Fatalf("selected %d rows, want the cursor row alone", got)
	}
	if got := m.data.selectedCols(); len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Fatalf("selected columns = %v, want the first two", got)
	}
	if m.data.col != 1 {
		t.Fatalf("cursor column = %d, want it moved to 1", m.data.col)
	}
	if !m.data.narrowedToCols() {
		t.Fatalf("the selection is not reported as narrowed to columns")
	}
	if !m.keys.CopySelection.Enabled() {
		t.Fatalf("ctrl+c was not enabled by shift+right")
	}
}

// The column span grows and shrinks around its anchor, exactly like the
// row span does: shift+left after two shift+rights walks back.
func TestShiftLeftShrinksAndCrossesTheColumnAnchor(t *testing.T) {
	m := send(t, dataBrowsing(t), shiftRight(), shiftRight())
	if got := len(m.data.selectedCols()); got != 3 {
		t.Fatalf("selected %d columns after two shift+right, want 3", got)
	}
	m = send(t, m, shiftLeft())
	if got := len(m.data.selectedCols()); got != 2 {
		t.Fatalf("selected %d columns after shift+left shrank it, want 2", got)
	}
	// Back past the anchor: the span flips to the other side rather than
	// collapsing to nothing.
	m = send(t, m, shiftLeft(), shiftLeft())
	if got := m.data.selectedCols(); len(got) != 1 || got[0] != 0 {
		t.Fatalf("selected columns = %v, want only the anchor column", got)
	}
}

// A row selection stays full-width until a column is anchored — the
// block is opt-in, so ctrl+v and shift+down keep meaning whole rows.
func TestRowSelectionCoversEveryColumnUntilNarrowed(t *testing.T) {
	m := send(t, dataBrowsing(t), ctrl('v'), shiftDown())
	if m.data.narrowedToCols() {
		t.Fatalf("a plain row selection reports itself as narrowed")
	}
	if got, want := len(m.data.selectedCols()), len(m.data.cols); got != want {
		t.Fatalf("selected %d columns, want all %d", got, want)
	}
	m = send(t, m, shiftRight())
	if !m.data.narrowedToCols() || len(m.data.selectedCols()) != 2 {
		t.Fatalf("shift+right did not narrow the running selection: %+v", m.data.sel)
	}
	if got := len(m.data.selectedRows()); got != 2 {
		t.Fatalf("selected %d rows, want the row span left alone", got)
	}
}

// The cell tint follows the block: a column the selection left out is
// not painted as selected.
func TestOnlySelectedColumnsAreTinted(t *testing.T) {
	m := send(t, dataBrowsing(t), shiftDown(), shiftRight())
	if !m.data.cellSelected(0, 0) || !m.data.cellSelected(1, 1) {
		t.Fatalf("cells inside the block are not selected: %+v", m.data.sel)
	}
	if m.data.cellSelected(1, 2) {
		t.Fatalf("a column outside the block is painted as selected")
	}
	if m.data.cellSelected(2, 0) {
		t.Fatalf("a row outside the block is painted as selected")
	}
}

// The unshifted fallbacks exist for terminals that never report
// shift+arrows: V anchors the rows, J/K extend them, C anchors the
// column span and plain l/h move its open edge.
func TestUnshiftedSelectionFallbackKeys(t *testing.T) {
	m := send(t, dataBrowsing(t), press('V'), press('J'), press('C'), press('l'))
	if got := len(m.data.selectedRows()); got != 2 {
		t.Fatalf("V then J selected %d rows, want 2", got)
	}
	if got := len(m.data.selectedCols()); got != 2 {
		t.Fatalf("C then l selected %d columns, want 2", got)
	}
	m = send(t, m, press('K'), press('h'))
	if got := len(m.data.selectedRows()); got != 1 {
		t.Fatalf("K shrank the selection to %d rows, want 1", got)
	}
	if got := len(m.data.selectedCols()); got != 1 {
		t.Fatalf("h shrank the selection to %d columns, want 1", got)
	}
}

// `C` is a toggle like ctrl+v: pressing it again widens the selection
// back to every column without dropping the selected rows.
func TestSelectColumnsTogglesTheSpanOff(t *testing.T) {
	m := send(t, dataBrowsing(t), press('V'), press('j'), press('C'), press('l'))
	if !m.data.narrowedToCols() {
		t.Fatalf("C did not anchor a column span: %+v", m.data.sel)
	}
	m = send(t, m, press('C'))
	if m.data.narrowedToCols() {
		t.Fatalf("a second C did not drop the column span: %+v", m.data.sel)
	}
	if got := len(m.data.selectedRows()); got != 2 {
		t.Fatalf("a second C dropped the rows too: %d selected, want 2", got)
	}
}

// `C` with nothing selected anchors both ends at once — it is the
// sideways `ctrl+v`, not something that needs one first.
func TestSelectColumnsStartsASelection(t *testing.T) {
	m := send(t, dataBrowsing(t), press('C'))
	if !m.data.selecting() || !m.data.narrowedToCols() {
		t.Fatalf("C did not start a block selection: %+v", m.data.sel)
	}
	if got := len(m.data.selectedRows()); got != 1 {
		t.Fatalf("C selected %d rows, want the cursor row alone", got)
	}
	if got := len(m.data.selectedCols()); got != 1 {
		t.Fatalf("C selected %d columns, want the cursor column alone", got)
	}
}

// Clearing the selection drops the column span with it, so the next
// ctrl+v starts full-width again.
func TestClearingDropsTheColumnSpan(t *testing.T) {
	m := send(t, dataBrowsing(t), shiftRight(), special(tea.KeyEsc, 0), ctrl('v'))
	if m.data.narrowedToCols() {
		t.Fatalf("the column span survived esc: %+v", m.data.sel)
	}
}

// A query result is paged in memory rather than re-read, so setPage has
// to drop the selection the way reloadPage does.
func TestSelectionClearedOnQueryResultPaging(t *testing.T) {
	d := dataView{query: "SELECT 1", all: make([][]any, 2*dataPageSize)}
	d.setPage(0)
	d.sel = gridSelection{active: true, anchor: 1}
	d.row = 4
	d.setPage(1)
	if d.selecting() || len(d.selectedRows()) != 0 {
		t.Fatalf("selection = %+v, want it dropped with the page", d.sel)
	}
}
