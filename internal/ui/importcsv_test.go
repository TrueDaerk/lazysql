package ui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// writeCSV puts content in a temp file and returns its path.
func writeCSV(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "in.csv")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// openImportSettingsFor selects table in [2], presses `I` and submits
// path, landing on the settings form.
func openImportSettingsFor(t *testing.T, m Model, table, path string) (Model, *formModal) {
	t.Helper()
	m = send(t, m, press('2'))
	if !m.panels[panelObjects].selectByName(table) {
		t.Fatalf("%s not listed in [2]", table)
	}
	m = send(t, m, press('I'))
	f, ok := m.modal.(*formModal)
	if !ok || f.field("path") == nil {
		t.Fatalf("I opened %T, want the path form", m.modal)
	}
	if !f.field("path").suggest {
		t.Error("the path field has no path completion")
	}
	f.field("path").input.SetValue(path)
	m = send(t, m, special(tea.KeyEnter, 0))
	f, ok = m.modal.(*formModal)
	if !ok || f.field("mapping") == nil {
		t.Fatalf("modal after the path = %T; log = %v", m.modal, backupLogText(m))
	}
	return m, f
}

func peopleCount(t *testing.T, m Model) int64 {
	t.Helper()
	n, err := m.driver.CountRows(context.Background(), "", "people", nil)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// The settings form shows the detected delimiter, header and mapping, and
// previews the rows converted to the column types before anything runs.
func TestImportSettingsShowGuessesAndPreview(t *testing.T) {
	m := metaBrowsing(t)
	path := writeCSV(t, "email;id\nb@example.com;7\n\"c;d@example.com\";8\n")
	m, f := openImportSettingsFor(t, m, "people", path)

	if got := f.rawValue("delim"); got != ";" {
		t.Errorf("delimiter = %q, want ;", got)
	}
	if !f.field("header").on {
		t.Error("the header line was not detected")
	}
	if got := f.rawValue("mapping"); got != "email, id" {
		t.Errorf("mapping = %q", got)
	}
	body := strings.Join(f.body(f), "\n")
	for _, want := range []string{"delimiter \";\"", "header line", "1→email TEXT", "2→id INTEGER", "c;d@exampl"} {
		if !strings.Contains(body, want) {
			t.Errorf("preview lacks %q:\n%s", want, body)
		}
	}
	if peopleCount(t, m) != 1 {
		t.Fatal("rows were inserted before the import was confirmed")
	}

	// Correcting the mapping changes the preview, still without running.
	f.field("mapping").input.SetValue("email")
	if body := strings.Join(f.body(f), "\n"); !strings.Contains(body, "2→skip") {
		t.Errorf("corrected mapping not previewed:\n%s", body)
	}
}

func TestImportCommitsAndLogs(t *testing.T) {
	m := metaBrowsing(t)
	path := writeCSV(t, "email\nb@example.com\n\"multi\nline, with comma\"\n")
	m, _ = openImportSettingsFor(t, m, "people", path)
	m = send(t, m, special(tea.KeyEnter, 0))

	if m.modal != nil {
		t.Fatalf("modal still open: %T", m.modal)
	}
	if m.exports.csv.running {
		t.Error("the import is still marked as running")
	}
	if got := peopleCount(t, m); got != 3 {
		t.Fatalf("people = %d rows, want 3; log = %v", got, backupLogText(m))
	}
	for _, want := range []string{"BEGIN", `INSERT INTO "people" ("email") VALUES (?)`, "COMMIT", "2 rows from"} {
		if !logContains(m, want) {
			t.Errorf("command log lacks %q: %v", want, backupLogText(m))
		}
	}
}

// A row the engine refuses mid-file rolls everything back and the log
// names the row, its line and the reason.
func TestImportMidFileFailureRollsBack(t *testing.T) {
	m := metaBrowsing(t)
	// a@example.com already exists and email is UNIQUE.
	path := writeCSV(t, "email\nnew@example.com\na@example.com\nlater@example.com\n")
	m, _ = openImportSettingsFor(t, m, "people", path)
	m = send(t, m, special(tea.KeyEnter, 0))

	if got := peopleCount(t, m); got != 1 {
		t.Fatalf("people = %d rows, want the 1 it had", got)
	}
	if !logContains(m, "import into people FAILED: row 2 (line 3)") || !logContains(m, "UNIQUE") ||
		!logContains(m, "rolled back") {
		t.Fatalf("command log = %v", backupLogText(m))
	}
	if !logContains(m, "ROLLBACK") {
		t.Fatalf("no ROLLBACK in the log: %v", backupLogText(m))
	}
}

// A field that does not fit its column stops the import with its line and
// column rather than being coerced.
func TestImportTypeMismatchIsReported(t *testing.T) {
	m := metaBrowsing(t)
	path := writeCSV(t, "id,email\n10,x@example.com\neleven,y@example.com\n")
	m, _ = openImportSettingsFor(t, m, "people", path)
	m = send(t, m, special(tea.KeyEnter, 0))

	if got := peopleCount(t, m); got != 1 {
		t.Fatalf("people = %d rows, want the 1 it had", got)
	}
	if !logContains(m, `line 3, column id (INTEGER): "eleven" is not an integer`) {
		t.Fatalf("command log = %v", backupLogText(m))
	}
}

func TestImportCancelRollsBack(t *testing.T) {
	m := metaBrowsing(t)
	path := writeCSV(t, "email\nb@example.com\n")
	m, _ = openImportSettingsFor(t, m, "people", path)
	// Hold the worker's command back so the cancel lands before it runs.
	next, cmd := m.Update(special(tea.KeyEnter, 0))
	m = next.(Model)
	if !m.exports.csv.running {
		t.Fatal("the import did not start")
	}
	if !m.keys.CancelImport.Enabled() {
		t.Error("X is not enabled while the import runs")
	}
	m = send(t, m, press('X'))
	m = send(t, m, drain(cmd)...)
	if m.exports.csv.running {
		t.Fatal("the import is still running after the cancel")
	}
	if got := peopleCount(t, m); got != 1 {
		t.Fatalf("people = %d rows after the cancel, want 1", got)
	}
	if !logContains(m, "cancelled") || !logContains(m, "rolled back") {
		t.Fatalf("command log = %v", backupLogText(m))
	}
}

func TestImportRefusedOnReadOnly(t *testing.T) {
	m := readOnlyGrid(t)
	m = send(t, m, press('2'))
	m.panels[panelObjects].selectByName("grid")
	m = send(t, m, press('I'))
	if m.modal != nil {
		t.Fatalf("a read-only connection opened %T", m.modal)
	}
	if !logContains(m, "import blocked") {
		t.Fatalf("command log = %v", backupLogText(m))
	}
}

func TestImportNeedsATable(t *testing.T) {
	m := metaBrowsing(t)
	m = send(t, m, press('2'))
	m.panels[panelObjects].cursor = 0 // the namespace row
	m = send(t, m, press('I'))
	if m.modal != nil {
		t.Fatalf("I on a namespace opened %T", m.modal)
	}
	if !logContains(m, "import skipped") {
		t.Fatalf("command log = %v", backupLogText(m))
	}
}

// DuckDB through the whole flow: no header, positional mapping, a quoted
// field holding the delimiter and a newline.
func TestImportIntoDuckDB(t *testing.T) {
	m := duckBrowsing(t)
	m = send(t, m, press('2'), press('R'))
	path := writeCSV(t, "3,\"x, y\nz\"\n4,plain\n")
	m, f := openImportSettingsFor(t, m, "widgets", path)
	if f.field("header").on {
		t.Error("a headerless file was read as having a header")
	}
	m = send(t, m, special(tea.KeyEnter, 0))

	rs, err := m.driver.Query(context.Background(), `SELECT name FROM widgets WHERE id = 3`)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs.Rows) != 1 || rs.Rows[0][0] != "x, y\nz" {
		t.Fatalf("imported row = %v; log = %v", rs.Rows, backupLogText(m))
	}
}

func TestImportBindings(t *testing.T) {
	m := sized(120, 40)
	if !helpMentions(m, panelObjects, "import CSV into table…") {
		t.Error("? on [2] does not list the import key")
	}
	if m.keys.CancelImport.Enabled() {
		t.Error("the cancel key is enabled with nothing running")
	}
}
