package ui

import (
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"lazysql/internal/db"
)

// The server activity report is the data grid, read-only (issue #176):
// these cover the three halves of that — column-wise navigation, the copy
// scopes, the multi-row and block selection — plus what a refresh does to
// a cursor and a selection, and the absence of every mutating binding.

// selectionTint is the SGR parameter list a selected row's background
// sets, matched instead of the whole style the way cursorTint is.
func selectionTint() string {
	probe := lipgloss.NewStyle().Background(colorSelectionBg).Render("x")
	i := strings.Index(probe, "\x1b[")
	return probe[i+2 : i+strings.Index(probe[i:], "m")]
}

// ---------- columns ----------

// h/l walk the cell cursor across the columns, clamped at both ends.
func TestActivityColumnNavigation(t *testing.T) {
	m := openReport(t, serverModel(t, false), fixtureProcesses())
	if m.activity.grid.col != 0 {
		t.Fatalf("cursor column = %d, want the first column", m.activity.grid.col)
	}
	m = send(t, m, press('l'), press('l'))
	if m.activity.grid.col != 2 {
		t.Fatalf("column after two l = %d, want 2", m.activity.grid.col)
	}
	m = send(t, m, press('h'))
	if m.activity.grid.col != 1 {
		t.Fatalf("column after h = %d, want 1", m.activity.grid.col)
	}
	// The cursor never leaves the table, however hard the key is held.
	for range activityHeaders {
		m = send(t, m, press('h'))
	}
	if m.activity.grid.col != 0 {
		t.Fatalf("column = %d, want it clamped to the first", m.activity.grid.col)
	}
	for range activityHeaders {
		m = send(t, m, press('l'))
	}
	if want := len(activityHeaders) - 1; m.activity.grid.col != want {
		t.Fatalf("column = %d, want it clamped to %d", m.activity.grid.col, want)
	}
}

// A box too narrow for every column scrolls sideways with the cursor and
// says so, exactly like the data grid.
func TestActivityScrollsWideTables(t *testing.T) {
	m := openReport(t, serverModel(t, false), fixtureProcesses())
	out := m.activityContent(40, 20)
	if !strings.Contains(out, "h/l scrolls") {
		t.Fatalf("a 40-cell box shows no horizontal scroll hint:\n%s", out)
	}
	if !strings.Contains(out, "PID") {
		t.Fatalf("the first column is not rendered:\n%s", out)
	}
	// Walking right past the window scrolls it: the leftmost column goes.
	for range activityHeaders {
		m = send(t, m, press('l'))
	}
	out = m.activityContent(40, 20)
	if strings.Contains(out, "PID") {
		t.Fatalf("the window did not scroll away from the first column:\n%s", out)
	}
	if !strings.Contains(out, "Query") {
		t.Fatalf("the cursor column is not in the window:\n%s", out)
	}
}

// ---------- selection ----------

// ctrl+v anchors a selection over sessions, j extends it, the footer says
// so and esc clears it before it closes the report.
func TestActivitySelectionModeStartsExtendsAndClears(t *testing.T) {
	m := openReport(t, serverModel(t, false), fixtureProcesses())
	m = send(t, m, ctrlKey('v'))
	if !m.activity.grid.selecting() || len(m.activity.grid.selectedRows()) != 1 {
		t.Fatalf("ctrl+v did not anchor a selection: %+v", m.activity.grid.sel)
	}
	if !m.keys.CopySelection.Enabled() {
		t.Fatal("ctrl+c is not bound to the copy while a selection is up")
	}
	m = send(t, m, press('j'), press('j'))
	if got := len(m.activity.grid.selectedRows()); got != 3 {
		t.Fatalf("selected %d sessions, want 3", got)
	}
	if !strings.Contains(m.activityFooter(), "3 sessions selected") {
		t.Fatalf("the footer does not say a selection is up: %s", m.activityFooter())
	}
	// The selected rows are tinted the way the grid tints its own.
	if !strings.Contains(m.activityContent(120, 20), selectionTint()) {
		t.Fatal("the selected sessions carry no selection background")
	}

	m = send(t, m, special(tea.KeyEscape, 0))
	if m.activity == nil {
		t.Fatal("esc closed the report instead of clearing the selection first")
	}
	if m.activity.grid.selecting() {
		t.Fatal("esc did not clear the selection")
	}
	if m.keys.CopySelection.Enabled() {
		t.Fatal("ctrl+c stayed bound to the copy after the selection was cleared")
	}
}

// shift+arrows extend the selection here too — but `K`/`J`, the grid's
// fallbacks for them, keep meaning kill.
func TestActivityShiftSelectionKeys(t *testing.T) {
	m := openReport(t, serverModel(t, false), fixtureProcesses())
	m = send(t, m, shiftDown())
	if got := len(m.activity.grid.selectedRows()); got != 2 {
		t.Fatalf("selected %d sessions after shift+down, want 2", got)
	}
	m = send(t, m, shiftUp())
	if got := len(m.activity.grid.selectedRows()); got != 1 {
		t.Fatalf("selected %d sessions after shift+up, want 1", got)
	}
	// `K` is the kill, not an extend: it opens the confirm modal.
	m = send(t, m, press('K'))
	if _, ok := m.modal.(*confirmModal); !ok {
		t.Fatalf("`K` opened %T, want the kill confirmation", m.modal)
	}
}

// `C` anchors a column span without a shifted arrow, and h/l then move
// its open edge — the grid's block selection, unchanged.
func TestActivityBlockSelection(t *testing.T) {
	m := openReport(t, serverModel(t, false), fixtureProcesses())
	m = send(t, m, ctrlKey('v'), press('j'), press('C'), press('l'))
	if !m.activity.grid.narrowedToCols() {
		t.Fatal("`C` plus l did not narrow the selection to a block")
	}
	if got := len(m.activity.grid.selectedCols()); got != 2 {
		t.Fatalf("block covers %d columns, want 2", got)
	}
	if !strings.Contains(m.activityFooter(), "2 sessions × 2 columns selected") {
		t.Fatalf("the footer does not describe the block: %s", m.activityFooter())
	}
}

// ---------- copy ----------

// The copy menu offers the scopes the report can serve and none of the
// ones that need a table behind them.
func TestActivityCopyMenuScopes(t *testing.T) {
	m := send(t, openReport(t, serverModel(t, false), fixtureProcesses()), press('y'))
	labels := menuLabels(t, m)
	for _, want := range []string{"cell", "row — CSV", "row — JSON",
		"session list — CSV", "session list — JSON"} {
		if !hasLabel(labels, want) {
			t.Errorf("the copy menu is missing %q: %v", want, labels)
		}
	}
	for _, unwanted := range []string{"INSERT", "CREATE TABLE", "DDL", "table —"} {
		if hasLabel(labels, unwanted) {
			t.Errorf("the copy menu offers %q, which has no table behind it: %v", unwanted, labels)
		}
	}
}

// The cell scope copies the value under the cell cursor — the dash the
// table shows for an unreported column is a rendering, not a value.
func TestActivityCopyCell(t *testing.T) {
	got := fakeClipboard(t)
	m := openReport(t, serverModel(t, false), fixtureProcesses())

	m = send(t, m, press('y'), press('c'))
	if *got != "42" {
		t.Fatalf("clipboard = %q, want the PID", *got)
	}
	if !logHas(m, "copy cell sessions.PID") {
		t.Fatalf("command log = %v", m.commandLogEntries())
	}

	// The idle session reported no statement at all.
	m = openReport(t, serverModel(t, false), fixtureProcesses())
	m = toColumn(t, m, "Query")
	m = send(t, m, press('j'), press('j'), press('y'), press('c'))
	if *got != "" {
		t.Fatalf("an unreported column copied %q, want an empty string", *got)
	}
}

// The row scope copies one session as a CSV line and as a JSON object,
// with the header names as keys.
func TestActivityCopyRow(t *testing.T) {
	got := fakeClipboard(t)
	base := openReport(t, serverModel(t, false), fixtureProcesses())

	send(t, base, press('y'), press('r'))
	if !strings.HasPrefix(*got, "42,app,shop,") {
		t.Fatalf("row CSV = %q, want the session's columns", *got)
	}

	send(t, base, press('y'), press('o'))
	var obj map[string]any
	if err := json.Unmarshal([]byte(*got), &obj); err != nil {
		t.Fatalf("row JSON %q does not parse: %v", *got, err)
	}
	if obj["PID"] != "42" || obj["Query"] != "SELECT * FROM orders" {
		t.Fatalf("row JSON = %v, want the session's own values", obj)
	}
}

// The selection scopes copy every marked session — CSV with a header,
// JSON as one array — and the column scope copies one column of them.
func TestActivityCopySelection(t *testing.T) {
	got := fakeClipboard(t)
	base := send(t, openReport(t, serverModel(t, false), fixtureProcesses()),
		ctrlKey('v'), press('j'))

	m := send(t, base, ctrlKey('c'), press('r'))
	lines := strings.Split(strings.TrimRight(*got, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("selection CSV = %q, want a header and two sessions", *got)
	}
	if !strings.HasPrefix(lines[0], "PID,User,Database") {
		t.Fatalf("selection CSV header = %q", lines[0])
	}
	if !logHas(m, "2 selected sessions") {
		t.Fatalf("command log = %v", m.commandLogEntries())
	}

	send(t, base, ctrlKey('c'), press('o'))
	var arr []map[string]any
	if err := json.Unmarshal([]byte(*got), &arr); err != nil {
		t.Fatalf("selection JSON %q does not parse: %v", *got, err)
	}
	if len(arr) != 2 || arr[0]["PID"] != "42" || arr[1]["PID"] != "43" {
		t.Fatalf("selection JSON = %v, want both selected sessions", arr)
	}

	// The column scope: the cursor column's value in every selected row.
	send(t, base, press('y'), press('c'))
	if *got != "42\n43\n" {
		t.Fatalf("column copy = %q, want one PID per line", *got)
	}
}

// A block copy carries only the columns the block covers.
func TestActivityCopyBlockNarrowsColumns(t *testing.T) {
	got := fakeClipboard(t)
	m := send(t, openReport(t, serverModel(t, false), fixtureProcesses()),
		ctrlKey('v'), press('j'), press('C'), press('l'))
	send(t, m, ctrlKey('c'), press('r'))
	lines := strings.Split(strings.TrimRight(*got, "\n"), "\n")
	if lines[0] != "PID,User" {
		t.Fatalf("block CSV header = %q, want only the two selected columns", lines[0])
	}
}

// The list scope copies every session on screen, selection or not.
func TestActivityCopyWholeList(t *testing.T) {
	got := fakeClipboard(t)
	m := send(t, openReport(t, serverModel(t, false), fixtureProcesses()),
		press('y'), press('C'))
	lines := strings.Split(strings.TrimRight(*got, "\n"), "\n")
	if len(lines) != 1+len(fixtureProcesses()) {
		t.Fatalf("list CSV = %q, want a header and every session", *got)
	}
	if !logHas(m, "4 sessions of local-mysql") {
		t.Fatalf("command log = %v", m.commandLogEntries())
	}
}

// ---------- read-only ----------

// Nothing that stages or applies a change is bound, offered or
// documented while the report has the focus.
func TestActivityHasNoWriteBindings(t *testing.T) {
	m := openReport(t, serverModel(t, false), fixtureProcesses())

	bar := m.renderOptionsBar()
	help := send(t, m, press('?'))
	out := help.modal.view(help.style, 120, 60)
	for _, gone := range []string{"edit cell", "stage row delete", "insert row",
		"duplicate row", "commit staged changes", "discard"} {
		if strings.Contains(bar, gone) {
			t.Errorf("the options bar offers %q on a read-only report:\n%s", gone, bar)
		}
		if strings.Contains(out, gone) {
			t.Errorf("`?` documents %q on a read-only report:\n%s", gone, out)
		}
	}

	// And the keys themselves do nothing rather than reaching the grid.
	for _, k := range []rune{'e', 'd', 'n', 'D', 'c'} {
		after := send(t, m, press(k))
		if after.modal != nil {
			t.Errorf("`%c` opened %T on the read-only report", k, after.modal)
		}
		if after.changes.Len() != 0 {
			t.Errorf("`%c` staged a change on the read-only report", k)
		}
	}
}

// The grid keys the report does claim are all in `?`.
func TestActivityGridKeysAreOffered(t *testing.T) {
	m := openReport(t, serverModel(t, false), fixtureProcesses())
	help := send(t, m, press('?'))
	out := help.modal.view(help.style, 120, 60)
	for _, want := range []string{"prev column", "next column", "select rows",
		"select columns", "copy…", "row detail"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q is missing from the report's help:\n%s", want, out)
		}
	}
}

// ---------- refresh ----------

// `R` re-reads the process list rather than reloading whatever relation
// the main view is not showing.
func TestActivityRefreshKeyReReadsTheList(t *testing.T) {
	m := openReport(t, serverModel(t, false), fixtureProcesses())
	before := m.activity.id
	m = send(t, m, press('R'))
	if m.activity.id == before {
		t.Fatal("`R` started no new read of the process list")
	}
	// The list is still what it was: `R` re-reads, it does not reload the
	// relation the main view is not showing.
	if len(m.activity.rows) != len(fixtureProcesses()) {
		t.Fatalf("`R` left %d sessions listed, want the list it re-read",
			len(m.activity.rows))
	}
}

// A refresh re-anchors the cursor on the session it was on, even when
// sessions above it have ended.
func TestActivityRefreshKeepsTheCursorOnItsSession(t *testing.T) {
	m := openReport(t, serverModel(t, false), fixtureProcesses())
	m = send(t, m, press('j'), press('j')) // session 44
	if p, _ := m.activity.selected(); p.ID != "44" {
		t.Fatalf("cursor is on session %s, want 44", p.ID)
	}
	// Sessions 42 and 43 have ended; 44 is now the first row.
	m = send(t, m, activityLoadedMsg{
		id: m.activity.id, conn: m.active, rows: fixtureProcesses()[2:]})
	if p, _ := m.activity.selected(); p.ID != "44" {
		t.Fatalf("cursor moved to session %s, want it to follow 44", p.ID)
	}
}

// A cursor whose session has ended cannot be re-anchored: it stays at the
// index it had, clamped into whatever came back.
func TestActivityRefreshClampsAVanishedCursor(t *testing.T) {
	m := openReport(t, serverModel(t, false), fixtureProcesses())
	m = send(t, m, press('G')) // session 45, the last row
	m = send(t, m, activityLoadedMsg{
		id: m.activity.id, conn: m.active, rows: fixtureProcesses()[:2]})
	if got := m.activity.grid.row; got != 1 {
		t.Fatalf("cursor = %d, want it clamped into the shorter list", got)
	}
}

// A selection survives a refresh by session ID — and is dropped, rather
// than silently re-cut over other sessions, when an end of it has gone.
func TestActivityRefreshReanchorsTheSelection(t *testing.T) {
	m := openReport(t, serverModel(t, false), fixtureProcesses())
	m = send(t, m, ctrlKey('v'), press('j')) // sessions 42 and 43

	// The idle session ends; the selected two are untouched, and the
	// selection still names them rather than the rows they used to be at.
	all := fixtureProcesses()
	m = send(t, m, activityLoadedMsg{
		id: m.activity.id, conn: m.active, rows: []db.Process{all[0], all[1], all[3]}})
	sel := m.activity.grid.selectedRows()
	if len(sel) != 2 {
		t.Fatalf("selected %v after the refresh, want both sessions kept", sel)
	}

	// Now the anchor itself ends: the selection goes rather than covering
	// sessions the user never marked.
	m = send(t, m, activityLoadedMsg{
		id: m.activity.id, conn: m.active, rows: all[1:]})
	if m.activity.grid.selecting() {
		t.Fatal("the selection survived its anchor session ending")
	}
	if m.keys.CopySelection.Enabled() {
		t.Fatal("ctrl+c stayed bound to the copy after the selection was dropped")
	}
}
