package ui

import (
	"testing"

	"lazysql/internal/db"
)

// These tests exercise the grid sub-model on its own: no root Model, no
// driver, no rendered frame. Before the grid state was extracted from
// Model (issue #229) the jump history and the FK cache could only be
// reached through a full shell.

func TestGridPushBrowseRecordsOnlyBrowsingPages(t *testing.T) {
	g := newGridModel(50)

	// An empty grid — nothing open — is nothing to come back to.
	g.pushBrowse(mainTabData)
	if len(g.browseStack) != 0 {
		t.Fatalf("pushBrowse on an empty grid recorded %d entries, want 0", len(g.browseStack))
	}

	g.data = dataView{conn: "c", database: "db", table: "users", pageSize: 50}
	g.pushBrowse(mainTabStructure)
	if len(g.browseStack) != 1 {
		t.Fatalf("pushBrowse recorded %d entries, want 1", len(g.browseStack))
	}
	st, ok := g.popBrowse()
	if !ok {
		t.Fatal("popBrowse found nothing after a push")
	}
	if st.data.table != "users" || st.tab != mainTabStructure {
		t.Errorf("popped %q on tab %v, want users on Structure", st.data.table, st.tab)
	}
	if _, ok := g.popBrowse(); ok {
		t.Error("popBrowse returned an entry from an empty history")
	}
}

func TestGridBrowseStackIsCapped(t *testing.T) {
	g := newGridModel(50)
	for i := 0; i < browseStackMax+5; i++ {
		g.data = dataView{conn: "c", database: "db", table: "t", page: i}
		g.pushBrowse(mainTabData)
	}
	if n := len(g.browseStack); n != browseStackMax {
		t.Fatalf("browse stack holds %d entries, want the cap %d", n, browseStackMax)
	}
	// The oldest entries are the ones dropped: the newest page is on top.
	st, _ := g.popBrowse()
	if st.data.page != browseStackMax+4 {
		t.Errorf("top of the stack is page %d, want %d", st.data.page, browseStackMax+4)
	}
	g.clearBrowse()
	if len(g.browseStack) != 0 || g.fkAfter != actNone {
		t.Error("clearBrowse left the history or a pending FK action behind")
	}
}

func TestGridCacheFKsDistinguishesNoneFromUnknown(t *testing.T) {
	var g gridModel // built by hand: the cache map is lazily created
	k := fkKey{conn: "c", database: "db", table: "orders"}

	g.cacheFKs(fkKey{conn: "c", database: "db"}, nil)
	if len(g.fkCache) != 0 {
		t.Fatal("a key without a table was cached")
	}

	g.cacheFKs(k, nil)
	fks, ok := g.fkCache[k]
	if !ok || fks == nil || len(fks) != 0 {
		t.Fatalf("a relation without FKs cached as %#v, want an empty non-nil slice", fks)
	}

	g.cacheFKs(k, []db.ForeignKey{{Name: "fk_customer"}})
	if got := g.fkCache[k]; len(got) != 1 || got[0].Name != "fk_customer" {
		t.Errorf("cached FKs = %#v, want the one recorded", got)
	}
}
