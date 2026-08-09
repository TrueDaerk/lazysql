package snippets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func tempPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "snippets")
}

func snip(name, sql string) Snippet {
	return Snippet{
		Name:      name,
		SQL:       sql,
		Engine:    "sqlite",
		CreatedAt: time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC),
	}
}

func names(list []Snippet) []string {
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = s.Name
	}
	return out
}

func TestLoadMissingFileIsEmpty(t *testing.T) {
	got, err := LoadFrom(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("LoadFrom() error = %v, want nil for a missing file", err)
	}
	if len(got) != 0 {
		t.Fatalf("LoadFrom() = %v, want empty", got)
	}
}

func TestSaveThenLoadRoundTrips(t *testing.T) {
	path := tempPath(t)
	list, _ := Put(nil, snip("recent orders", "SELECT *\nFROM orders\nWHERE id = :id"))
	if err := SaveTo(path, list); err != nil {
		t.Fatal(err)
	}
	got, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("loaded %d snippets, want 1", len(got))
	}
	if got[0].Name != "recent orders" {
		t.Errorf("name = %q, want %q", got[0].Name, "recent orders")
	}
	if got[0].SQL != "SELECT *\nFROM orders\nWHERE id = :id" {
		t.Errorf("a multi-line statement did not survive the round trip: %q", got[0].SQL)
	}
	if got[0].Engine != "sqlite" {
		t.Errorf("engine = %q, want the engine it was saved from", got[0].Engine)
	}
	if got[0].CreatedAt.IsZero() {
		t.Error("the snippet lost its timestamp")
	}
}

func TestSaveKeepsOneLinePerSnippet(t *testing.T) {
	path := tempPath(t)
	list, _ := Put(nil, snip("a", "SELECT 1\nUNION\nSELECT 2"))
	list, _ = Put(list, snip("b", "SELECT 3"))
	if err := SaveTo(path, list); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(strings.Split(strings.TrimSpace(string(raw)), "\n")); n != 2 {
		t.Fatalf("file has %d lines, want one per snippet:\n%s", n, raw)
	}
}

func TestSaveIsOwnerOnly(t *testing.T) {
	path := tempPath(t)
	if err := SaveTo(path, []Snippet{snip("a", "SELECT 1")}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 600: a snippet can name schemas and values", perm)
	}
}

func TestPutOverwritesTheSameName(t *testing.T) {
	list, replaced := Put(nil, snip("orders", "SELECT 1"))
	if replaced {
		t.Error("Put() into an empty list reported an overwrite")
	}
	list, replaced = Put(list, snip("orders", "SELECT 2"))
	if !replaced {
		t.Error("Put() over an existing name did not report an overwrite")
	}
	if len(list) != 1 {
		t.Fatalf("list = %v, want the name to hold one snippet", names(list))
	}
	if list[0].SQL != "SELECT 2" {
		t.Errorf("SQL = %q, want the newer statement", list[0].SQL)
	}
}

func TestPutMatchesNamesCaseInsensitively(t *testing.T) {
	list, _ := Put(nil, snip("Orders", "SELECT 1"))
	list, replaced := Put(list, snip("orders", "SELECT 2"))
	if !replaced || len(list) != 1 {
		t.Fatalf("list = %v, want %q to overwrite %q", names(list), "orders", "Orders")
	}
}

func TestPutDoesNotMutateTheInput(t *testing.T) {
	original, _ := Put(nil, snip("a", "SELECT 1"))
	_, _ = Put(original, snip("a", "SELECT 2"))
	if original[0].SQL != "SELECT 1" {
		t.Errorf("Put() mutated the list the model still holds: %q", original[0].SQL)
	}
}

func TestPutSortsByName(t *testing.T) {
	var list []Snippet
	for _, n := range []string{"zulu", "alpha", "Mike"} {
		list, _ = Put(list, snip(n, "SELECT 1"))
	}
	want := []string{"alpha", "Mike", "zulu"}
	for i, w := range want {
		if list[i].Name != w {
			t.Fatalf("order = %v, want %v", names(list), want)
		}
	}
}

func TestDeleteRemovesByName(t *testing.T) {
	list, _ := Put(nil, snip("a", "SELECT 1"))
	list, _ = Put(list, snip("b", "SELECT 2"))

	list, deleted := Delete(list, "A")
	if !deleted {
		t.Error("Delete() did not report the removal")
	}
	if len(list) != 1 || list[0].Name != "b" {
		t.Fatalf("list = %v, want only %q", names(list), "b")
	}
	if _, deleted := Delete(list, "nope"); deleted {
		t.Error("Delete() of an unknown name reported a removal")
	}
}

func TestDeleteSurvivesAReload(t *testing.T) {
	path := tempPath(t)
	list, _ := Put(nil, snip("a", "SELECT 1"))
	list, _ = Put(list, snip("b", "SELECT 2"))
	if err := SaveTo(path, list); err != nil {
		t.Fatal(err)
	}
	list, _ = Delete(list, "a")
	if err := SaveTo(path, list); err != nil {
		t.Fatal(err)
	}
	got, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "b" {
		t.Fatalf("after a reload: %v, want only %q", names(got), "b")
	}
}

func TestLoadSkipsBrokenAndEmptyLines(t *testing.T) {
	path := tempPath(t)
	body := `{"name":"good","sql":"SELECT 1","created_at":"2026-08-09T12:00:00Z"}

{"name":"","sql":"SELECT 2","created_at":"2026-08-09T12:00:00Z"}
{"name":"nosql","sql":"   ","created_at":"2026-08-09T12:00:00Z"}
{"name":"truncated","sql":"SEL`
	if err := os.WriteFile(path, []byte(body+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom() error = %v, want a partial load", err)
	}
	if len(got) != 1 || got[0].Name != "good" {
		t.Fatalf("loaded %v, want only the parsable named snippet", names(got))
	}
}

func TestLoadKeepsTheLastOfADuplicateName(t *testing.T) {
	path := tempPath(t)
	body := `{"name":"dup","sql":"SELECT 1","created_at":"2026-08-09T12:00:00Z"}
{"name":"dup","sql":"SELECT 2","created_at":"2026-08-09T12:00:00Z"}`
	if err := os.WriteFile(path, []byte(body+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SQL != "SELECT 2" {
		t.Fatalf("loaded %d snippets (%q), want the last line to win", len(got), got[0].SQL)
	}
}

func TestFind(t *testing.T) {
	list, _ := Put(nil, snip("Recent Orders", "SELECT 1"))
	if _, ok := Find(list, "recent orders"); !ok {
		t.Error("Find() missed a name differing only in case")
	}
	if _, ok := Find(list, "other"); ok {
		t.Error("Find() invented a snippet")
	}
}

func TestDirHonoursXDGStateHome(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/tmp/state")
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	wantDir := filepath.Join("/tmp/state", AppDir)
	if dir != wantDir {
		t.Errorf("Dir() = %q, want %q", dir, wantDir)
	}
	path, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if wantPath := filepath.Join(wantDir, FileName); path != wantPath {
		t.Errorf("Path() = %q, want %q", path, wantPath)
	}
}
