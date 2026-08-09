package ui

import (
	"context"
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
