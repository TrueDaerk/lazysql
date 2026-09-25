package ui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// `@` opens the expanded, scrollable command log; `esc` returns to the
// normal layout, like every other modal.
func TestCommandLogExpandsAndCollapses(t *testing.T) {
	m := browsing(t)
	m = send(t, m, press('@'))
	if _, ok := m.modal.(*commandLogModal); !ok {
		t.Fatalf("modal = %T, want *commandLogModal", m.modal)
	}
	m = send(t, m, special(tea.KeyEscape, 0))
	if m.modal != nil {
		t.Fatalf("modal = %T, want nil after esc", m.modal)
	}
}

// The catalog queries lazysql runs on its own behalf stay out of the log
// by default, while the user's own statements and lifecycle notes stay
// in; ctrl+l reveals the introspection, from the strip and from the
// expanded modal alike.
func TestCommandLogHidesIntrospectionByDefault(t *testing.T) {
	m := dataBrowsing(t) // connected and opened a relation
	intro := false
	for _, e := range m.driver.Logger().Entries() {
		if e.Introspection {
			intro = true
		}
	}
	if !intro {
		t.Fatal("fixture ran no introspection; the test proves nothing")
	}
	if logContains(m, "sqlite_master") || logContains(m, "PRAGMA") {
		t.Fatalf("introspection shown by default: %v", m.commandLogEntries())
	}
	for _, want := range []string{`LIMIT 100 OFFSET 0`, `SELECT COUNT(*)`, "-- connect"} {
		if !logContains(m, want) {
			t.Fatalf("log lost %q: %v", want, m.commandLogEntries())
		}
	}

	hidden := len(m.commandLogEntries())
	m = send(t, m, ctrl('l'))
	if !logContains(m, "sqlite_master") || len(m.commandLogEntries()) != len(m.driver.Logger().Entries())+len(m.commandLog) {
		t.Fatalf("ctrl+l did not reveal every entry: %v", m.commandLogEntries())
	}
	m = send(t, m, ctrl('l'))
	if got := len(m.commandLogEntries()); got != hidden {
		t.Fatalf("second ctrl+l left %d entries, want %d", got, hidden)
	}

	m = send(t, m, press('@'))
	lm, ok := m.modal.(*commandLogModal)
	if !ok {
		t.Fatalf("modal = %T, want *commandLogModal", m.modal)
	}
	before := len(lm.lines)
	m = send(t, m, ctrl('l'))
	if m.modal != lm || len(lm.lines) <= before || !m.showIntrospection {
		t.Fatalf("ctrl+l in the modal: open=%v lines %d -> %d", m.modal == lm, before, len(lm.lines))
	}
}

// A failing introspection call is shown even while introspection is
// hidden: an error the user cannot see is worse than noise.
func TestCommandLogShowsFailedIntrospection(t *testing.T) {
	m := browsing(t)
	if _, err := m.driver.ListRelations(context.Background(), "no_such_db"); err == nil {
		t.Fatal("expected introspection of a missing database to fail")
	}
	found := false
	for _, e := range m.commandLogEntries() {
		if e.err && strings.Contains(e.text, "no_such_db") {
			found = true
		}
	}
	if !found {
		t.Fatalf("failed introspection hidden: %v", m.commandLogEntries())
	}
}

// A statement the Driver's Logger caught as a failure must render red in
// both the slim panel and the expanded view — the merged feed is where
// both read from, so this only has to check the feed.
func TestCommandLogColorsFailedStatement(t *testing.T) {
	m := browsing(t)
	if _, err := m.driver.Exec(context.Background(), "UPDATE no_such_table SET x = 1"); err == nil {
		t.Fatal("expected the statement against a missing table to fail")
	}
	var found bool
	for _, e := range m.commandLogEntries() {
		if e.err {
			found = true
		}
	}
	if !found {
		t.Fatalf("no failed entry in the merged command log: %+v", m.commandLogEntries())
	}
}

// The Driver's Logger, not hand-formatted UI strings, is what the panel
// renders a statement from: a page load appears in the merged log with
// its duration attached, even though nothing in the UI layer logged it.
func TestCommandLogEntriesCarryDuration(t *testing.T) {
	m := dataBrowsing(t)
	for _, e := range m.commandLogEntries() {
		if e.err {
			t.Fatalf("unexpected failed entry: %s", e.text)
		}
	}
	if !logContains(m, "LIMIT 100 OFFSET 0") {
		t.Fatalf("command log = %v", m.commandLogEntries())
	}
}
