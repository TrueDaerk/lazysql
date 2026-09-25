package ui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"lazysql/internal/config"
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

// `T` collapses the command log strip, handing its rows to the main view
// box instead of leaving an empty strip or a stray border behind; a
// second press brings it back. `@`/`L` still opens the full log modal
// either way.
func TestToggleCommandLogCollapsesTheStrip(t *testing.T) {
	m := dataBrowsing(t)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	before := m.commandLogHeight(m.height - 1)
	if before <= 0 {
		t.Fatalf("commandLogHeight = %d before collapsing, want > 0", before)
	}
	mainBefore := lipgloss.Height(m.renderMainColumn(m.width, m.height-1))

	m = send(t, m, press('T'))
	if !m.logCollapsed {
		t.Fatal("logCollapsed = false after pressing T, want true")
	}
	if got := m.commandLogHeight(m.height - 1); got != 0 {
		t.Fatalf("commandLogHeight = %d while collapsed, want 0", got)
	}
	mainAfter := lipgloss.Height(m.renderMainColumn(m.width, m.height-1))
	if mainAfter != mainBefore {
		t.Fatalf("renderMainColumn rendered %d lines collapsed, want %d (full main-column height)",
			mainAfter, mainBefore)
	}
	if got := lipgloss.Height(m.View().Content); got != m.height {
		t.Fatalf("collapsed frame is %d lines tall, want %d", got, m.height)
	}

	if _, ok := m.modal.(*commandLogModal); ok {
		t.Fatal("T opened the log modal, want only the strip to toggle")
	}
	m = send(t, m, press('@'))
	if _, ok := m.modal.(*commandLogModal); !ok {
		t.Fatalf("modal = %T, want *commandLogModal while the strip is collapsed", m.modal)
	}
	m = send(t, m, special(tea.KeyEscape, 0))

	m = send(t, m, press('T'))
	if m.logCollapsed {
		t.Fatal("logCollapsed = true after a second press, want false")
	}
	if got := lipgloss.Height(m.View().Content); got != m.height {
		t.Fatalf("re-expanded frame is %d lines tall, want %d", got, m.height)
	}
}

// The collapsed state round-trips through config.State the same way the
// screen mode does, so it survives a restart.
func TestCommandLogCollapsedRoundTripsThroughState(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/state.toml"

	st := &config.State{LogCollapsed: true}
	if err := st.SaveTo(path); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	back := config.LoadStateFrom(path)
	if !back.LogCollapsed {
		t.Fatal("LogCollapsed = false after round trip, want true")
	}
}
