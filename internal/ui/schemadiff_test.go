package ui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/config"
	"lazysql/internal/db"
)

// seedSQLiteFile creates a SQLite file with the given schema.
func seedSQLiteFile(t *testing.T, path string, stmts []string) {
	t.Helper()
	drv, err := db.Open(db.EngineSQLite)
	if err != nil {
		t.Fatal(err)
	}
	dsn, err := db.BuildDSN(db.EngineSQLite, db.ConnParams{File: path})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := drv.Connect(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	defer drv.Close()
	for _, s := range stmts {
		if _, err := drv.Exec(ctx, s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}

// diffModel is a model whose panel [1] holds two SQLite fixtures with
// deliberately different schemas, cursor on the first.
func diffModel(t *testing.T) Model {
	t.Helper()
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.db"), filepath.Join(dir, "b.db")
	seedSQLiteFile(t, a, []string{
		`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL, age INT)`,
		`CREATE TABLE legacy (id INTEGER PRIMARY KEY)`,
	})
	seedSQLiteFile(t, b, []string{
		// age INTEGER vs INT above: same type, must not show up.
		`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, age INTEGER, email TEXT)`,
	})

	m := sized(120, 40)
	m.cfg = &config.Config{Connections: []config.Connection{
		{Name: "diff-a", Engine: db.EngineSQLite, File: a},
		{Name: "diff-b", Engine: db.EngineSQLite, File: b},
	}}
	m.refreshConnections("diff-a")
	return m
}

// runDiff presses D and submits the form as it opens: A is the selected
// connection, B the form's default other side.
func runDiff(t *testing.T, m Model) Model {
	t.Helper()
	m = send(t, m, press('D'))
	if _, ok := m.modal.(*formModal); !ok {
		t.Fatalf("modal = %T, want the schema diff form", m.modal)
	}
	return send(t, m, special(tea.KeyEnter, 0))
}

func TestSchemaDiffProducesReport(t *testing.T) {
	m := runDiff(t, diffModel(t))
	if m.diff == nil || m.diff.report == nil {
		t.Fatalf("diff = %+v, want a finished report", m.diff)
	}
	if m.diff.running {
		t.Error("diff still marked running after the report landed")
	}
	text := m.diff.report.RenderText()
	for _, want := range []string{"- legacy", "+ column email", "~ column name nullable"} {
		if !strings.Contains(text, want) {
			t.Errorf("report missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "~ column age") {
		t.Errorf("INT vs INTEGER flagged in same-engine diff:\n%s", text)
	}
	if !logContains(m, "schema diff diff-a vs diff-b") {
		t.Errorf("command log missing the diff line: %v", m.commandLog)
	}
	// The report owns the main view while panel [1] is focused, and
	// names itself in the box's top border.
	if view := m.mainContent(100, 30); !strings.Contains(view, "Schema diff") {
		t.Errorf("main view does not show the report:\n%s", view)
	}
	if title := m.mainTitle(100); !strings.Contains(title, "Schema diff") {
		t.Errorf("main view is not titled for the report: %q", title)
	}
}

func TestSchemaDiffEscClosesReport(t *testing.T) {
	m := runDiff(t, diffModel(t))
	m = send(t, m, special(tea.KeyEscape, 0))
	if m.diff != nil {
		t.Fatalf("diff = %+v after esc, want nil", m.diff)
	}
	if view := m.mainContent(100, 30); strings.Contains(view, "Schema diff") {
		t.Error("report still on screen after esc")
	}
}

func TestSchemaDiffIdenticalSaysNoDifferences(t *testing.T) {
	m := diffModel(t)
	// Compare diff-a with itself: the form's select field is moved off
	// its default onto A's own name.
	m = send(t, m, press('D'))
	f, ok := m.modal.(*formModal)
	if !ok {
		t.Fatalf("modal = %T, want the schema diff form", m.modal)
	}
	other := f.field("other")
	for i, v := range other.values {
		if v == "diff-a" {
			other.choice = i
		}
	}
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.diff == nil || m.diff.report == nil {
		t.Fatalf("diff = %+v, want a finished report", m.diff)
	}
	if !m.diff.report.Empty() {
		t.Fatalf("self-diff not empty: %+v", m.diff.report)
	}
	if text := m.diff.report.RenderText(); !strings.Contains(text, "no differences") {
		t.Errorf("report must say no differences:\n%s", text)
	}
}

func TestSchemaDiffErrorSurfacesCleanly(t *testing.T) {
	m := diffModel(t)
	// Point B at a file whose directory does not exist: dialing it fails.
	m.cfg.Connections[1].File = filepath.Join(t.TempDir(), "missing", "nope.db")
	m = runDiff(t, m)
	if m.diff == nil {
		t.Fatal("diff state gone after a failed run")
	}
	if m.diff.err == "" {
		t.Fatalf("diff = %+v, want an error", m.diff)
	}
	if !strings.Contains(m.diff.err, "diff-b") {
		t.Errorf("error does not name the failing side: %q", m.diff.err)
	}
	if !logContains(m, "schema diff FAILED") {
		t.Errorf("command log missing the failure: %v", m.commandLog)
	}
	// esc dismisses the failed run like a finished one.
	m = send(t, m, special(tea.KeyEscape, 0))
	if m.diff != nil {
		t.Error("failed diff not dismissed by esc")
	}
}

func TestSchemaDiffCopyAndExport(t *testing.T) {
	var copied string
	clipboardWrite = func(text string) error { copied = text; return nil }
	defer func() { clipboardWrite = writeClipboard }()

	m := runDiff(t, diffModel(t))
	m = send(t, m, press('y'))
	if !strings.Contains(copied, "Schema diff") || !strings.Contains(copied, "+ column email") {
		t.Errorf("clipboard = %q, want the report", copied)
	}

	path := filepath.Join(t.TempDir(), "report.txt")
	m = send(t, m, press('E'))
	m = typePath(t, m, path)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "- table legacy") && !strings.Contains(string(data), "- legacy") {
		t.Errorf("exported report = %q", data)
	}
	if !logContains(m, "export schema diff wrote") {
		t.Errorf("command log missing the export line: %v", m.commandLog)
	}
}

func TestSchemaDiffScrollKeys(t *testing.T) {
	m := runDiff(t, diffModel(t))
	if m.diff.offset != 0 {
		t.Fatalf("offset = %d at open", m.diff.offset)
	}
	m = send(t, m, press('j'), press('j'), press('k'))
	if m.diff.offset != 1 {
		t.Errorf("offset = %d after jjk, want 1", m.diff.offset)
	}
	m = send(t, m, press('G'))
	if m.diff.offset != len(m.diff.lines) {
		t.Errorf("offset = %d after G, want %d", m.diff.offset, len(m.diff.lines))
	}
	m = send(t, m, press('g'))
	if m.diff.offset != 0 {
		t.Errorf("offset = %d after g, want 0", m.diff.offset)
	}
}
