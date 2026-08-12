package history

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func filterEntry(conn, database, table, where string, min int) Entry {
	return Entry{
		SQL:        where,
		Connection: conn,
		Engine:     "sqlite",
		At:         time.Date(2026, 8, 12, 9, min, 0, 0, time.UTC),
		Database:   database,
		Table:      table,
	}
}

// The relation an entry belongs to survives the JSON Lines round trip,
// and entries come back newest first like every other history read.
func TestFilterEntriesRoundTripWithTheirRelation(t *testing.T) {
	path := filepath.Join(t.TempDir(), FilterFileName)
	for i, e := range []Entry{
		filterEntry("prod", "shop", "orders", "id > 100", 0),
		filterEntry("prod", "shop", "order_items", "qty = 0", 1),
		filterEntry("prod", "shop", "orders", "status = 'open'", 2),
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
	mine := InRelation(got, "prod", "shop", "orders")
	if len(mine) != 2 {
		t.Fatalf("relation has %d entries, want the 2 recorded on it: %#v", len(mine), mine)
	}
	if mine[0].SQL != "status = 'open'" || mine[1].SQL != "id > 100" {
		t.Fatalf("relation = %#v, want newest first", mine)
	}
	if mine[0].Engine != "sqlite" {
		t.Fatalf("entry lost its engine: %#v", mine[0])
	}
	if n := len(InRelation(got, "prod", "shop", "order_items")); n != 1 {
		t.Fatalf("the other relation has %d entries, want its own 1", n)
	}
}

// Every part of the scope has to match: a relation of the same name
// under another connection or another database is another relation.
func TestInRelationMatchesEveryPartOfTheScope(t *testing.T) {
	entries := []Entry{
		filterEntry("prod", "shop", "orders", "a", 0),
		filterEntry("staging", "shop", "orders", "b", 1),
		filterEntry("prod", "archive", "orders", "c", 2),
		filterEntry("prod", "shop", "customers", "d", 3),
		// A file engine browses under the pseudo-namespace: an empty
		// database is a value, not a missing one.
		filterEntry("local", "", "orders", "e", 4),
	}
	for _, c := range []struct {
		conn, database, table, want string
	}{
		{"prod", "shop", "orders", "a"},
		{"staging", "shop", "orders", "b"},
		{"prod", "archive", "orders", "c"},
		{"prod", "shop", "customers", "d"},
		{"local", "", "orders", "e"},
	} {
		got := InRelation(entries, c.conn, c.database, c.table)
		if len(got) != 1 || got[0].SQL != c.want {
			t.Errorf("InRelation(%q, %q, %q) = %#v, want only %q",
				c.conn, c.database, c.table, got, c.want)
		}
	}
	if got := InRelation(entries, "", "shop", "orders"); got != nil {
		t.Errorf("an unnamed connection matched %#v", got)
	}
	if got := InRelation(entries, "prod", "shop", ""); got != nil {
		t.Errorf("an unnamed relation matched %#v", got)
	}
}

// A statement — scoped by connection alone — belongs to no relation, and
// writes neither of the two narrowing fields.
func TestStatementEntriesBelongToNoRelation(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	e := entry("SELECT 1", 0)
	e.Connection = "prod"
	if err := AppendTo(path, e); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"database"`) || strings.Contains(string(data), `"table"`) {
		t.Fatalf("a statement wrote relation fields: %s", data)
	}
	got, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(InRelation(got, "prod", "", "orders")); n != 0 {
		t.Fatalf("a relation picked up %d statements", n)
	}
	// The connection-wide filter still sees it, which is what the query
	// editor's history reads.
	if n := len(ForConnection(got, "prod")); n != 1 {
		t.Fatalf("ForConnection = %d entries, want the statement", n)
	}
}

// A relation is capped on its own, without touching what other
// relations recorded.
func TestTrimRelationCapsOneRelationOnly(t *testing.T) {
	var entries []Entry
	for i := 0; i < 5; i++ {
		entries = append(entries, filterEntry("prod", "shop", "orders", fmt.Sprintf("id > %d", i), i))
	}
	entries = append(entries, filterEntry("prod", "shop", "customers", "name IS NULL", 9))

	got := TrimRelation(entries, "prod", "shop", "orders", 3)
	mine := InRelation(got, "prod", "shop", "orders")
	if len(mine) != 3 {
		t.Fatalf("relation kept %d entries, want the cap of 3", len(mine))
	}
	if mine[0].SQL != "id > 0" || mine[2].SQL != "id > 2" {
		t.Fatalf("trim dropped from the wrong end: %#v", mine)
	}
	if n := len(InRelation(got, "prod", "shop", "customers")); n != 1 {
		t.Fatalf("the other relation lost entries: %#v", got)
	}
	// Under the cap nothing is copied or reordered.
	if same := TrimRelation(entries, "prod", "shop", "orders", 10); len(same) != len(entries) {
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
	if err := SaveFilters([]Entry{filterEntry("c", "", "t", "id = 1", 0)}); err != nil {
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
