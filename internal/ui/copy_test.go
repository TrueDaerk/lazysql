package ui

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/db"
)

// copyBrowsing is metaBrowsing with a second order row that has a NULL
// in it and a value that needs CSV quoting, so one fixture covers the
// interesting cases of all three formats.
func copyBrowsing(t *testing.T) Model {
	t.Helper()
	m := metaBrowsing(t)
	for _, stmt := range []string{
		`INSERT INTO orders (id, person_id, status) VALUES (2, 1, NULL)`,
		`INSERT INTO orders (id, person_id, status) VALUES (3, 1, 'a,b "c"')`,
	} {
		if _, err := m.driver.Exec(t.Context(), stmt); err != nil {
			t.Fatalf("fixture %q: %v", stmt, err)
		}
	}
	return send(t, m, press('R'))
}

// menuLabels is what the open modal offers, for asserting on which
// scopes the copy menu exposed.
func menuLabels(t *testing.T, m Model) []string {
	t.Helper()
	mm, ok := m.modal.(*menuModal)
	if !ok {
		t.Fatalf("modal = %T, want a menu", m.modal)
	}
	out := make([]string, 0, len(mm.entries))
	for _, e := range mm.entries {
		out = append(out, e.key+" "+e.label)
	}
	return out
}

func hasLabel(labels []string, want string) bool {
	for _, l := range labels {
		if strings.Contains(l, want) {
			return true
		}
	}
	return false
}

// `y` on the Data tab offers all three scopes; on a metadata tab the
// cell and row entries are gone, because there is no row under the
// cursor there.
func TestCopyMenuIsContextAware(t *testing.T) {
	m := send(t, copyBrowsing(t), press('y'))
	labels := menuLabels(t, m)
	for _, want := range []string{"cell", "row — CSV", "row — JSON", "row — INSERT",
		"table — CSV", "table — JSON", "table — INSERT", "CREATE TABLE + INSERTs", "DDL"} {
		if !hasLabel(labels, want) {
			t.Errorf("copy menu is missing %q: %v", want, labels)
		}
	}

	// Structure tab: no row scope.
	m = send(t, copyBrowsing(t), press(']'), press('y'))
	labels = menuLabels(t, m)
	if hasLabel(labels, "cell") || hasLabel(labels, "row — CSV") {
		t.Errorf("copy menu offers a row scope on a metadata tab: %v", labels)
	}
	if !hasLabel(labels, "table — CSV") || !hasLabel(labels, "DDL") {
		t.Errorf("copy menu lost its table scope on a metadata tab: %v", labels)
	}
}

// esc closes the copy menu without copying anything.
func TestCopyMenuEscCancels(t *testing.T) {
	got := fakeClipboard(t)
	m := send(t, copyBrowsing(t), press('y'), special(tea.KeyEscape, 0))
	if m.modal != nil {
		t.Errorf("esc left the modal open: %T", m.modal)
	}
	if *got != "" {
		t.Errorf("esc copied %q", *got)
	}
}

// The cell scope copies the raw value — and a NULL cell copies as an
// empty string, with the log saying so rather than pasting "NULL".
func TestCopyCell(t *testing.T) {
	got := fakeClipboard(t)

	// Row 0, column "status" = 'new'.
	m := send(t, copyBrowsing(t), press('l'), press('l'), press('y'), press('c'))
	if *got != "new" {
		t.Fatalf("clipboard = %q, want %q", *got, "new")
	}
	if !logContains(m, "copy cell orders.status") {
		t.Fatalf("command log = %v", m.commandLog)
	}

	// Row 1 has a NULL status.
	*got = "unset"
	m = send(t, copyBrowsing(t), press('l'), press('l'), press('j'), press('y'), press('c'))
	if *got != "" {
		t.Fatalf("NULL cell copied %q, want an empty string", *got)
	}
	if !logContains(m, "NULL → empty") {
		t.Fatalf("command log = %v", m.commandLog)
	}
}

// The row scope produces a bare CSV line, a JSON object and an INSERT —
// each with NULL spelled the way that format spells it.
func TestCopyRowFormats(t *testing.T) {
	got := fakeClipboard(t)

	// Row 1: id 2, person_id 1, status NULL.
	base := send(t, copyBrowsing(t), press('j'))

	send(t, base, press('y'), press('r'))
	if *got != "2,1," {
		t.Errorf("row CSV = %q, want %q", *got, "2,1,")
	}

	send(t, base, press('y'), press('o'))
	var obj map[string]any
	if err := json.Unmarshal([]byte(*got), &obj); err != nil {
		t.Fatalf("row JSON %q does not parse: %v", *got, err)
	}
	if v, ok := obj["status"]; !ok || v != nil {
		t.Errorf("row JSON status = %#v, want an explicit null", obj["status"])
	}
	if obj["id"] != float64(2) {
		t.Errorf("row JSON id = %#v, want the number 2", obj["id"])
	}

	send(t, base, press('y'), press('i'))
	want := `INSERT INTO "orders" ("id", "person_id", "status") VALUES (2, 1, NULL);`
	if *got != want {
		t.Errorf("row INSERT =\n%s\nwant\n%s", *got, want)
	}
}

// A staged cell edit is copied as it appears on screen, not as the
// server still has it.
func TestCopyRowUsesStagedEdits(t *testing.T) {
	got := fakeClipboard(t)

	m := send(t, copyBrowsing(t), press('l'), press('l'), press('e'))
	if m.modal == nil {
		t.Fatal("e did not open the cell editor")
	}
	m = send(t, m, press('X'), special(tea.KeyEnter, 0))
	m = send(t, m, press('y'), press('r'))
	if *got != "1,1,newX" {
		t.Errorf("row CSV = %q, want the staged value", *got)
	}
}

// The table scope streams every row of the relation, not just the
// visible page, and quotes what CSV needs quoting.
func TestCopyTableCSV(t *testing.T) {
	got := fakeClipboard(t)
	send(t, copyBrowsing(t), press('y'), press('C'))

	lines := strings.Split(strings.TrimRight(*got, "\n"), "\n")
	if len(lines) != 4 { // header + 3 rows
		t.Fatalf("table CSV has %d lines, want 4:\n%s", len(lines), *got)
	}
	if lines[0] != "id,person_id,status" {
		t.Errorf("header = %q", lines[0])
	}
	if lines[2] != "2,1," {
		t.Errorf("NULL row = %q, want an empty field", lines[2])
	}
	if !strings.Contains(*got, `"a,b ""c"""`) {
		t.Errorf("CSV did not quote the awkward value:\n%s", *got)
	}
}

// The table filter and sort carry into a copy: what is copied is what
// the grid is showing.
func TestCopyTableRespectsFilter(t *testing.T) {
	got := fakeClipboard(t)
	m := applyColumnFilter(t, copyBrowsing(t), "id", db.OpEq, "2")
	send(t, m, press('y'), press('C'))

	lines := strings.Split(strings.TrimRight(*got, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("filtered CSV has %d lines, want 2:\n%s", len(lines), *got)
	}
	if !strings.HasPrefix(lines[1], "2,") {
		t.Errorf("filtered CSV row = %q", lines[1])
	}
}

// The CREATE TABLE + INSERTs variant works from the Data tab, where the
// metadata has not been fetched yet: the copy waits for it.
func TestCopyTableSchemaFetchesDDL(t *testing.T) {
	got := fakeClipboard(t)
	m := send(t, copyBrowsing(t), press('y'), press('A'))
	if m.tab != mainTabData {
		t.Errorf("the copy changed the tab to %v", m.tab)
	}
	if !strings.Contains(*got, "CREATE TABLE") {
		t.Fatalf("clipboard = %q", *got)
	}
	if !strings.Contains(*got, `INSERT INTO "orders"`) {
		t.Fatalf("clipboard has no INSERTs:\n%s", *got)
	}
	if i, j := strings.Index(*got, "CREATE TABLE"), strings.Index(*got, "INSERT INTO"); i > j {
		t.Errorf("the DDL comes after the INSERTs")
	}
}

// A whole-table JSON copy is a valid array of typed objects.
func TestCopyTableJSON(t *testing.T) {
	got := fakeClipboard(t)
	send(t, copyBrowsing(t), press('y'), press('O'))

	var decoded []map[string]any
	if err := json.Unmarshal([]byte(*got), &decoded); err != nil {
		t.Fatalf("table JSON does not parse: %v\n%s", err, *got)
	}
	if len(decoded) != 3 {
		t.Fatalf("decoded %d rows, want 3", len(decoded))
	}
	if decoded[1]["status"] != nil {
		t.Errorf("NULL decoded as %#v", decoded[1]["status"])
	}
}

// A query result offers a page scope instead of a table scope: it has
// no relation behind it to re-read from the server. `y` is vim's own
// yank while focus stays on the editor panel, so the copy menu is
// reached from the grid — `tab` moves focus there the same way it does
// after any other run whose result the user wants to act on.
func TestCopyMenuOffersPageScopeForQueryResults(t *testing.T) {
	m := runQuery(t, queryable(t), "SELECT id, name FROM q")
	m = send(t, m, special(tea.KeyTab, 0), press('y'))
	labels := menuLabels(t, m)
	for _, want := range []string{"cell", "row — CSV", "row — JSON", "page — CSV", "page — JSON array"} {
		if !hasLabel(labels, want) {
			t.Errorf("copy menu is missing %q: %v", want, labels)
		}
	}
	for _, unwanted := range []string{"row — INSERT", "table —", "DDL"} {
		if hasLabel(labels, unwanted) {
			t.Errorf("copy menu offers %q for a query result: %v", unwanted, labels)
		}
	}
}

// The page-scope copy is limited to the rows already loaded — not the
// whole result a big query might match — and the log line says so.
func TestCopyQueryPageIsLimitedToTheLoadedPage(t *testing.T) {
	got := fakeClipboard(t)
	m := queryable(t)
	if _, err := m.driver.Exec(t.Context(),
		`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i < 150)
		 INSERT INTO q (id, name) SELECT i + 100, 'bulk' FROM n`); err != nil {
		t.Fatal(err)
	}
	m = runQuery(t, m, "SELECT id FROM q ORDER BY id")
	if len(m.data.rows) != dataPageSize {
		t.Fatalf("loaded page has %d rows, want the grid page size %d", len(m.data.rows), dataPageSize)
	}

	m = send(t, m, special(tea.KeyTab, 0), press('y'), press('C'))
	lines := strings.Split(strings.TrimRight(*got, "\n"), "\n")
	if len(lines) != dataPageSize+1 {
		t.Fatalf("copied %d lines, want the page size plus a header", len(lines))
	}
	if !logContains(m, "loaded page only") || !logContains(m, "E exports the full result") {
		t.Fatalf("command log = %v", m.commandLog)
	}
}

// The page-scope JSON copy is a valid array of typed objects, the same
// as the table scope's.
func TestCopyQueryPageJSON(t *testing.T) {
	got := fakeClipboard(t)
	m := runQuery(t, queryable(t), "SELECT id, name FROM q")
	send(t, m, special(tea.KeyTab, 0), press('y'), press('O'))

	var decoded []map[string]any
	if err := json.Unmarshal([]byte(*got), &decoded); err != nil {
		t.Fatalf("page JSON does not parse: %v\n%s", err, *got)
	}
	if len(decoded) != 3 {
		t.Fatalf("decoded %d rows, want 3", len(decoded))
	}
}

// With no clipboard around, a copy still succeeds: the text lands in a
// temp file and the log names the path.
func TestCopyDegradesToTempFile(t *testing.T) {
	prev := clipboardWrite
	clipboardWrite = func(string) error { return os.ErrNotExist }
	t.Cleanup(func() { clipboardWrite = prev })
	spilled := fakeSpill(t)

	m := send(t, copyBrowsing(t), press('y'), press('r'))
	if *spilled == "" {
		t.Fatal("nothing was written to the fallback file")
	}
	if !logContains(m, "no clipboard") {
		t.Fatalf("command log = %v", m.commandLog)
	}
}

// ---------- the multi-row selection ----------

// ctrlKey is a control chord as the terminal delivers it, for the two
// keys the selection flow binds: ctrl+v and ctrl+c.
func ctrlKey(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Mod: tea.ModCtrl}
}

// selectRows enters selection mode on the copy fixture and extends it
// over n rows, leaving the cursor on the last one.
func selectRows(t *testing.T, n int) Model {
	t.Helper()
	m := send(t, copyBrowsing(t), ctrlKey('v'))
	for i := 1; i < n; i++ {
		m = send(t, m, press('j'))
	}
	if got := len(m.data.selectedRows()); got != n {
		t.Fatalf("selected %d rows, want %d", got, n)
	}
	return m
}

// ctrl+v anchors a selection, j extends it and esc ends it — and while
// it is up, ctrl+c is bound to the copy rather than to quit.
func TestSelectionModeStartsExtendsAndClears(t *testing.T) {
	m := send(t, copyBrowsing(t), ctrlKey('v'))
	if !m.data.selecting() || len(m.data.selectedRows()) != 1 {
		t.Fatalf("ctrl+v did not anchor a selection: %+v", m.data.sel)
	}
	if !m.keys.CopySelection.Enabled() {
		t.Fatal("ctrl+c is not bound to the copy while a selection is up")
	}

	m = send(t, m, press('j'), press('j'))
	if got := m.data.selectedRows(); len(got) != 3 || got[0] != 0 || got[2] != 2 {
		t.Fatalf("j did not extend the selection: %v", got)
	}
	// Moving back up shrinks it again: the anchor stays put.
	m = send(t, m, press('k'))
	if got := len(m.data.selectedRows()); got != 2 {
		t.Fatalf("k left %d rows selected, want 2", got)
	}

	m = send(t, m, special(tea.KeyEscape, 0))
	if m.data.selecting() || len(m.data.selectedRows()) != 0 {
		t.Fatal("esc did not clear the selection")
	}
	if m.keys.CopySelection.Enabled() {
		t.Fatal("ctrl+c stayed bound to the copy after the selection was cleared")
	}
}

// A selection anchored below the cursor selects upwards too: the range
// is between the anchor and the cursor, whichever way round they are.
func TestSelectionExtendsUpwards(t *testing.T) {
	m := send(t, copyBrowsing(t), press('j'), press('j'), ctrlKey('v'), press('k'))
	if got := m.data.selectedRows(); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("upwards selection = %v, want rows 1 and 2", got)
	}
}

// A reload puts different rows under the same indices, so the selection
// does not survive one.
func TestSelectionIsClearedByAReload(t *testing.T) {
	m := selectRows(t, 2)
	m = send(t, m, press('R'))
	if m.data.selecting() {
		t.Fatal("the selection survived a reload")
	}
	if m.keys.CopySelection.Enabled() {
		t.Fatal("ctrl+c stayed bound to the copy across a reload")
	}
}

// With a selection up, ctrl+c opens the copy menu instead of quitting,
// and the menu offers the two selection scopes.
func TestCtrlCOpensTheSelectionCopyMenu(t *testing.T) {
	m := send(t, selectRows(t, 2), ctrlKey('c'))
	labels := menuLabels(t, m)
	for _, want := range []string{"2 selected rows — CSV", "2 selected rows — JSON",
		"2 selected rows — INSERT", "column values of selection"} {
		if !hasLabel(labels, want) {
			t.Errorf("selection copy menu is missing %q: %v", want, labels)
		}
	}
	// The single-row scopes are gone: with rows marked, "row" is not what
	// the user means any more.
	if hasLabel(labels, "row — CSV") {
		t.Errorf("the selection menu still offers the single-row scope: %v", labels)
	}
	// The table scopes stay: they do not depend on the selection.
	if !hasLabel(labels, "table — CSV") {
		t.Errorf("the selection menu dropped the table scope: %v", labels)
	}
}

// Without a selection ctrl+c still quits, exactly as before.
func TestCtrlCWithoutASelectionStillQuits(t *testing.T) {
	m := copyBrowsing(t)
	m.focus = panelMain
	handled, _, cmd := m.updateGlobal(ctrlKey('c'))
	if !handled {
		t.Fatal("ctrl+c was not handled at all")
	}
	if cmd == nil {
		t.Fatal("ctrl+c did not quit with no selection")
	}
	if _, isQuit := cmd().(tea.QuitMsg); !isQuit {
		t.Fatalf("ctrl+c produced %T, want a quit", cmd())
	}
}

// The row formats of a selection are the row formats of one row applied
// to the set: a CSV copy carries a header, a JSON copy is one array and
// the INSERTs are one statement per row.
func TestCopySelectionRowFormats(t *testing.T) {
	got := fakeClipboard(t)
	base := selectRows(t, 2)

	send(t, base, ctrlKey('c'), press('r'))
	lines := strings.Split(strings.TrimRight(*got, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("selection CSV has %d lines, want a header and 2 rows:\n%s", len(lines), *got)
	}
	if lines[0] != "id,person_id,status" {
		t.Errorf("selection CSV header = %q", lines[0])
	}
	if lines[1] != "1,1,new" || lines[2] != "2,1," {
		t.Errorf("selection CSV rows = %q, %q", lines[1], lines[2])
	}

	send(t, base, ctrlKey('c'), press('o'))
	var decoded []map[string]any
	if err := json.Unmarshal([]byte(*got), &decoded); err != nil {
		t.Fatalf("selection JSON does not parse: %v\n%s", err, *got)
	}
	if len(decoded) != 2 {
		t.Fatalf("selection JSON has %d objects, want 2", len(decoded))
	}
	if decoded[1]["status"] != nil {
		t.Errorf("NULL decoded as %#v", decoded[1]["status"])
	}

	m := send(t, base, ctrlKey('c'), press('i'))
	stmts := strings.Split(strings.TrimRight(*got, "\n"), "\n")
	if len(stmts) != 2 {
		t.Fatalf("selection INSERTs = %d statements, want 2:\n%s", len(stmts), *got)
	}
	for _, s := range stmts {
		if !strings.HasPrefix(s, `INSERT INTO "orders"`) || !strings.HasSuffix(s, ";") {
			t.Errorf("malformed INSERT: %q", s)
		}
	}
	if !logContains(m, "2 selected rows of orders as SQL") {
		t.Fatalf("command log = %v", m.commandLog)
	}
}

// The column scope copies the cursor column's value in every selected
// row, one per line, with a NULL copied as an empty line.
func TestCopySelectionColumnValues(t *testing.T) {
	got := fakeClipboard(t)
	// Cursor on "status": row 0 is 'new', row 1 is NULL, row 2 is quoted.
	m := send(t, copyBrowsing(t), press('l'), press('l'),
		ctrlKey('v'), press('j'), press('j'), ctrlKey('c'), press('c'))

	if want := "new\n\na,b \"c\"\n"; *got != want {
		t.Errorf("column values = %q, want %q", *got, want)
	}
	if !logContains(m, "orders.status of 3 selected rows") || !logContains(m, "1 NULL → empty") {
		t.Fatalf("command log = %v", m.commandLog)
	}
}

// A staged edit is copied as the grid shows it, for a selection exactly
// as for a single row.
func TestCopySelectionUsesStagedEdits(t *testing.T) {
	got := fakeClipboard(t)

	m := send(t, copyBrowsing(t), press('l'), press('l'), press('e'))
	if m.modal == nil {
		t.Fatal("e did not open the cell editor")
	}
	m = send(t, m, press('X'), special(tea.KeyEnter, 0))
	m = send(t, m, ctrlKey('v'), press('j'), ctrlKey('c'), press('c'))
	if want := "newX\n\n"; *got != want {
		t.Errorf("column values = %q, want the staged value first", *got)
	}
}

// A query result can be selected and copied too — with no INSERT scope,
// since there is no relation to insert into.
func TestCopySelectionOfAQueryResult(t *testing.T) {
	got := fakeClipboard(t)
	m := runQuery(t, queryable(t), "SELECT id, name FROM q")
	m = send(t, m, special(tea.KeyTab, 0), ctrlKey('v'), press('j'), ctrlKey('c'))

	labels := menuLabels(t, m)
	if !hasLabel(labels, "2 selected rows — CSV") {
		t.Fatalf("query-result selection menu = %v", labels)
	}
	if hasLabel(labels, "selected rows — INSERT") {
		t.Errorf("the selection menu offers INSERTs for a query result: %v", labels)
	}

	send(t, m, press('r'))
	if lines := strings.Split(strings.TrimRight(*got, "\n"), "\n"); len(lines) != 3 {
		t.Fatalf("query selection CSV has %d lines, want a header and 2 rows:\n%s", len(lines), *got)
	}
}

// The selection keys are documented where every other grid key is: the
// options bar and `?` both read the panel's action table.
func TestSelectionKeysAreDocumented(t *testing.T) {
	m := selectRows(t, 2)
	documented := map[string]bool{}
	for _, group := range m.keys.helpGroups(panelMain) {
		for _, b := range group.bindings {
			documented[b.Help().Key] = true
		}
	}
	for _, want := range []string{"ctrl+v", "ctrl+c"} {
		if !documented[want] {
			t.Errorf("`?` does not document %q while a selection is up", want)
		}
	}
	bar := map[string]bool{}
	for _, b := range m.optionsBarBindings() {
		if b.Enabled() {
			bar[b.Help().Key] = true
		}
	}
	if !bar["ctrl+c"] {
		t.Error("the options bar does not offer ctrl+c during selection mode")
	}
}
