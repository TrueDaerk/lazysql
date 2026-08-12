package history

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func tempPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "history")
}

func entry(sql string, min int) Entry {
	return Entry{
		SQL:    sql,
		Engine: "sqlite",
		At:     time.Date(2026, 8, 9, 12, min, 0, 0, time.UTC),
	}
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

func TestAppendThenLoadIsNewestFirst(t *testing.T) {
	path := tempPath(t)
	for i, sql := range []string{"SELECT 1", "SELECT 2", "SELECT 3"} {
		if err := AppendTo(path, entry(sql, i)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"SELECT 3", "SELECT 2", "SELECT 1"}
	if len(got) != len(want) {
		t.Fatalf("loaded %d entries, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].SQL != w {
			t.Errorf("entry %d = %q, want %q", i, got[i].SQL, w)
		}
		if got[i].Engine != "sqlite" {
			t.Errorf("entry %d engine = %q, want the engine it ran on", i, got[i].Engine)
		}
		if got[i].At.IsZero() {
			t.Errorf("entry %d lost its timestamp", i)
		}
	}
}

func TestMultilineStatementSurvivesRoundTrip(t *testing.T) {
	path := tempPath(t)
	sql := "SELECT a,\n       b\n  FROM t\n WHERE a = 'x;y'"
	if err := AppendTo(path, entry(sql, 0)); err != nil {
		t.Fatal(err)
	}
	got, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SQL != sql {
		t.Fatalf("round trip = %#v, want the statement verbatim", got)
	}
}

func TestSaveRewritesOldestFirst(t *testing.T) {
	path := tempPath(t)
	// Entries are handed over newest first, the order the model keeps.
	if err := SaveTo(path, []Entry{entry("SELECT 2", 1), entry("SELECT 1", 0)}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "SELECT 1") {
		t.Fatalf("file = %q, want the oldest statement first", data)
	}
	got, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].SQL != "SELECT 2" {
		t.Fatalf("reload = %#v, want newest first", got)
	}
}

func TestSaveIsOwnerOnly(t *testing.T) {
	path := tempPath(t)
	if err := SaveTo(path, []Entry{entry("SELECT 1", 0)}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("history file mode = %o, want 600 — statements can carry data", perm)
	}
}

func TestLoadSkipsUnparsableLines(t *testing.T) {
	path := tempPath(t)
	if err := AppendTo(path, entry("SELECT 1", 0)); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	// A crash mid-write leaves a truncated last line.
	if _, err := f.WriteString(`{"sql":"SELECT 2","eng`); err != nil {
		t.Fatal(err)
	}
	f.Close()

	got, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom() error = %v, want the readable entries back", err)
	}
	if len(got) != 1 || got[0].SQL != "SELECT 1" {
		t.Fatalf("LoadFrom() = %#v, want the one intact entry", got)
	}
}

func TestLoadCompactsOverlongFile(t *testing.T) {
	path := tempPath(t)
	for i := 0; i < MaxEntries+10; i++ {
		if err := AppendTo(path, entry("SELECT "+strings.Repeat("x", 1), i)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != MaxEntries {
		t.Fatalf("loaded %d entries, want the cap of %d", len(got), MaxEntries)
	}
	// The compaction is written back, so the next load reads the cap.
	again, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != MaxEntries {
		t.Fatalf("reload has %d entries, want the file compacted to %d", len(again), MaxEntries)
	}
}

func TestLoadToleratesEntriesWithoutConnection(t *testing.T) {
	path := tempPath(t)
	// A history file written before the Connection field existed: no
	// "connection" key on the line at all.
	if err := os.WriteFile(path, []byte(`{"sql":"SELECT 1","engine":"sqlite","at":"2026-08-09T12:00:00Z"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom() error = %v, want a pre-Connection line to load fine", err)
	}
	if len(got) != 1 || got[0].SQL != "SELECT 1" || got[0].Connection != "" {
		t.Fatalf("LoadFrom() = %#v, want one entry with an empty Connection", got)
	}
}

func TestForConnectionFiltersToTheGivenConnection(t *testing.T) {
	entries := []Entry{
		{SQL: "SELECT 1", Connection: "prod"},
		{SQL: "SELECT 2", Connection: "staging"},
		{SQL: "SELECT 3", Connection: "prod"},
	}
	got := ForConnection(entries, "prod")
	if len(got) != 2 || got[0].SQL != "SELECT 1" || got[1].SQL != "SELECT 3" {
		t.Fatalf("ForConnection(prod) = %#v, want only prod's entries, order kept", got)
	}
}

func TestForConnectionKeepsLegacyEntriesForEveryConnection(t *testing.T) {
	entries := []Entry{
		{SQL: "SELECT 1", Connection: "prod"},
		{SQL: "SELECT 2", Connection: ""}, // written before Connection existed
		{SQL: "SELECT 3", Connection: "staging"},
	}
	for _, conn := range []string{"prod", "staging", "anything-else"} {
		got := ForConnection(entries, conn)
		found := false
		for _, e := range got {
			if e.SQL == "SELECT 2" {
				found = true
			}
		}
		if !found {
			t.Fatalf("ForConnection(%q) = %#v, want the legacy entry visible", conn, got)
		}
	}
	if got := ForConnection(entries, "staging"); len(got) != 2 {
		t.Fatalf("ForConnection(staging) = %#v, want the legacy entry plus staging's own, not prod's", got)
	}
}

func TestDirHonoursXDGStateHome(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/tmp/state")
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("/tmp/state", AppDir); dir != want {
		t.Fatalf("Dir() = %q, want %q", dir, want)
	}
}
