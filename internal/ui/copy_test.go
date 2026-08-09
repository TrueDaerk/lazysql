package ui

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
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
	m := send(t, copyBrowsing(t), press('f'))
	m = send(t, m, press('i'), press('d'), press(' '), press('='), press(' '), press('2'),
		special(tea.KeyEnter, 0))
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
