package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// The key report prints what the terminal sent, including the modified
// arrows the grid's selection binds — that is the whole point of it.
func TestKeyDebugReportsModifiedArrows(t *testing.T) {
	var m tea.Model = NewKeyDebug()
	for _, msg := range []tea.KeyPressMsg{
		special(tea.KeyUp, tea.ModShift),
		special(tea.KeyRight, tea.ModShift),
	} {
		m, _ = m.Update(msg)
	}
	view := m.View().Content
	for _, want := range []string{"shift+up", "shift+right"} {
		if !strings.Contains(view, want) {
			t.Errorf("key report does not show %q:\n%s", want, view)
		}
	}
	if !strings.Contains(view, "keyboard enhancements: none") {
		t.Errorf("key report does not say the terminal answered nothing:\n%s", view)
	}
}

// It quits on ctrl+q rather than `q`, because a plain letter is itself
// a key worth reporting.
func TestKeyDebugQuitsOnlyOnCtrlQ(t *testing.T) {
	var m tea.Model = NewKeyDebug()
	if _, cmd := m.Update(press('q')); cmd != nil {
		t.Fatal("plain q quit the key report instead of being reported")
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("ctrl+q did not quit the key report")
	}
}
