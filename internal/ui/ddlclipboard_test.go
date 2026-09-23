package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// `E` → `c` puts the very same document on the clipboard that `E` → `f`
// writes to disk: the destination menu changes where the text goes, not
// what it is.
func TestExportDatabaseDDLToClipboardMatchesTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "db-ddl.sql")

	m := ddlDatabaseBrowsing(t)
	m = send(t, m, press('E'), press('f'))
	m = typePath(t, m, path)
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(want) == 0 {
		t.Fatal("the file export produced nothing to compare against")
	}

	got := fakeClipboard(t)
	m = send(t, m, press('E'), press('c'))

	if *got != string(want) {
		t.Fatalf("clipboard and file differ:\nclipboard:\n%s\nfile:\n%s", *got, want)
	}
	if !logContains(m, "copy DDL of") || !logContains(m, "to clipboard (") {
		t.Fatalf("command log = %v", m.commandLog)
	}
	if m.dbDDLExport.running {
		t.Error("the clipboard export is still marked as running")
	}
}

// `esc` on the destination menu exports nothing at all — neither
// destination is started, and no path prompt opens behind it.
func TestExportDatabaseDDLMenuCancels(t *testing.T) {
	m := ddlDatabaseBrowsing(t)
	m = send(t, m, press('E'))
	if _, ok := m.modal.(*menuModal); !ok {
		t.Fatalf("modal after E = %T, want the destination menu", m.modal)
	}
	m = send(t, m, special(tea.KeyEsc, 0))
	if m.modal != nil {
		t.Fatalf("modal after esc = %T, want none", m.modal)
	}
	if m.dbDDLExport.running {
		t.Error("esc started an export anyway")
	}
}

// A database DDL copy with no clipboard to write to takes the same spill
// file every other copy takes, and the log names it.
func TestExportDatabaseDDLToClipboardSpills(t *testing.T) {
	noClipboard(t)
	fakeOSC52(t, false)
	spilled := fakeSpill(t)

	m := ddlDatabaseBrowsing(t)
	m = send(t, m, press('E'), press('c'))

	if !strings.Contains(*spilled, "-- table: people") {
		t.Fatalf("spill file = %q", *spilled)
	}
	if !logContains(m, "no clipboard") || !logContains(m, "/tmp/fake-spill-") {
		t.Fatalf("command log = %v", m.commandLog)
	}
}

// `y` → `d` on a relation node of [2] copies that relation's CREATE
// statement without it ever having been opened in the grid.
func TestTreeCopyMenuCopiesRelationDDL(t *testing.T) {
	got := fakeClipboard(t)

	m := ddlDatabaseBrowsing(t)
	m = treeSelect(t, send(t, m, press('2')), "people")
	if n := m.selectedNode(); n == nil || n.kind != nodeObject {
		t.Fatalf("selected node = %+v, want the people relation", n)
	}
	m = send(t, m, press('y'), press('d'))

	if !strings.Contains(*got, "CREATE TABLE") || !strings.Contains(*got, "people") {
		t.Fatalf("clipboard = %q, want people's CREATE statement", *got)
	}
	if !logContains(m, "copy DDL of people to clipboard") {
		t.Fatalf("command log = %v", m.commandLog)
	}
}

// `y` → `D` on a relation node copies the whole namespace's DDL, the
// same document `E` → `c` produces.
func TestTreeCopyMenuCopiesDatabaseDDL(t *testing.T) {
	got := fakeClipboard(t)

	m := ddlDatabaseBrowsing(t)
	m = treeSelect(t, send(t, m, press('2')), "people")
	m = send(t, m, press('y'), press('D'))

	for _, want := range []string{"lazysql DDL export of", "-- table: orders", "-- table: zebras"} {
		if !strings.Contains(*got, want) {
			t.Fatalf("clipboard is missing %q:\n%s", want, *got)
		}
	}
}

// A node that names nothing with DDL behind it — a category header —
// gets one skip line rather than an empty menu.
func TestTreeCopyMenuSkipsNonRelationNodes(t *testing.T) {
	m := ddlDatabaseBrowsing(t)
	m = treeSelect(t, send(t, m, press('2')), "Tables")
	if n := m.selectedNode(); n == nil || n.kind != nodeCategory {
		t.Fatalf("selected node = %+v, want the Tables category", n)
	}
	m = send(t, m, press('y'))

	if m.modal != nil {
		t.Fatalf("modal = %T, want no menu for a category node", m.modal)
	}
	if !logContains(m, "nothing to copy") {
		t.Fatalf("command log = %v", m.commandLog)
	}
}
