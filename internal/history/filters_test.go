package history

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func filterEntry(key, where string, min int) Entry {
	return Entry{
		SQL:    where,
		Engine: "sqlite",
		At:     time.Date(2026, 8, 12, 9, min, 0, 0, time.UTC),
		Key:    key,
	}
}

// A scope names one relation of one connection, and nothing about the
// parts can spell another scope's key.
func TestScopeSeparatesItsParts(t *testing.T) {
	a := Scope("prod", "shop", "orders")
	b := Scope("prod", "shop", "order_items")
	if a == b {
		t.Fatalf("two relations share the scope %q", a)
	}
	// A name that contains the separator of a naive "conn/db/table" key
	// must not be able to impersonate another scope.
	if Scope("prod/shop", "", "orders") == Scope("prod", "shop", "orders") {
		t.Fatal("a name containing a path separator collides with another scope")
	}
	if Scope("prod", "", "") == Scope("prod", "", "orders") {
		t.Fatal("the connection scope collides with one of its relations")
	}
}

// The scope survives the JSON Lines round trip, and entries come back
// newest first like every other history read.
func TestFilterEntriesRoundTripWithTheirScope(t *testing.T) {
	path := filepath.Join(t.TempDir(), FilterFileName)
	orders := Scope("prod", "shop", "orders")
	items := Scope("prod", "shop", "order_items")
	for i, e := range []Entry{
		filterEntry(orders, "id > 100", 0),
		filterEntry(items, "qty = 0", 1),
		filterEntry(orders, "status = 'open'", 2),
	} {
		e.At = e.At.Add(time.Duration(i) * time.Second)
		if err := AppendTo(path, e); err != nil {
			t.Fatal(err)
		}
	}

	got, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("loaded %d entries, want 3", len(got))
	}
	mine := InScope(got, orders)
	if len(mine) != 2 {
		t.Fatalf("scope has %d entries, want the 2 recorded on it: %#v", len(mine), mine)
	}
	if mine[0].SQL != "status = 'open'" || mine[1].SQL != "id > 100" {
		t.Fatalf("scope = %#v, want newest first", mine)
	}
	if mine[0].Engine != "sqlite" {
		t.Fatalf("entry lost its engine: %#v", mine[0])
	}
	if n := len(InScope(got, items)); n != 1 {
		t.Fatalf("the other relation has %d entries, want its own 1", n)
	}
	if InScope(got, "") != nil {
		t.Fatal("an empty scope matched entries")
	}
}

// An entry without a scope — the app-wide statement history — is not
// picked up by any scope, and the field stays out of its JSON.
func TestUnscopedEntriesStayOutOfEveryScope(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := AppendTo(path, entry("SELECT 1", 0)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"key"`) {
		t.Fatalf("an unscoped entry wrote a key field: %s", data)
	}
	got, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(InScope(got, Scope("prod", "shop", "orders"))); n != 0 {
		t.Fatalf("scope picked up %d unscoped entries", n)
	}
}

// A scope is capped on its own, without touching what other scopes
// recorded.
func TestTrimScopeCapsOneScopeOnly(t *testing.T) {
	orders := Scope("prod", "shop", "orders")
	other := Scope("prod", "shop", "customers")
	var entries []Entry
	for i := 0; i < 5; i++ {
		entries = append(entries, filterEntry(orders, "id > "+string(rune('0'+i)), i))
	}
	entries = append(entries, filterEntry(other, "name IS NULL", 9))

	got := TrimScope(entries, orders, 3)
	if n := len(InScope(got, orders)); n != 3 {
		t.Fatalf("scope kept %d entries, want the cap of 3", n)
	}
	if got[0].SQL != "id > 0" || InScope(got, orders)[2].SQL != "id > 2" {
		t.Fatalf("trim dropped from the wrong end: %#v", InScope(got, orders))
	}
	if n := len(InScope(got, other)); n != 1 {
		t.Fatalf("the other scope lost entries: %#v", InScope(got, other))
	}
	// Under the cap nothing is copied or reordered.
	if same := TrimScope(entries, orders, 10); len(same) != len(entries) {
		t.Fatalf("trim under the cap changed the list: %#v", same)
	}
}

// The filter file is the state directory's, next to the statement
// history, and is written owner-only for the same reason: a clause can
// name data.
func TestFilterPathAndModeMatchTheStatementHistory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)

	path, err := FilterPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, AppDir, FilterFileName); path != want {
		t.Fatalf("FilterPath() = %q, want %q", path, want)
	}
	if err := SaveFilters([]Entry{filterEntry(Scope("c", "", "t"), "id = 1", 0)}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("filter file mode = %o, want 600 — a clause can carry data", perm)
	}
	got, err := LoadFilters()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SQL != "id = 1" {
		t.Fatalf("LoadFilters() = %#v, want the saved clause", got)
	}
}
