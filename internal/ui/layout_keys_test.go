package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"charm.land/bubbles/v2/key"
)

// `<` / `>` are the primary main-tab bindings (a dedicated key on
// QWERTZ, shift+,/. on US/UK); `[` / `]` and `,` / `.` remain bound as
// legacy aliases. All three spellings must land on the same tab.
func TestMainTabAliasesSwitchTabs(t *testing.T) {
	for _, tc := range []struct {
		name       string
		next, prev rune
	}{
		{"primary", '>', '<'},
		{"us", ']', '['},
		{"qwertz", '.', ','},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := dataBrowsing(t)
			m = send(t, m, press(tc.next))
			if m.tab != mainTabStructure {
				t.Fatalf("tab after %q = %v, want Structure", tc.next, m.tab)
			}
			m = send(t, m, press(tc.prev))
			if m.tab != mainTabData {
				t.Fatalf("tab after %q = %v, want Data", tc.prev, m.tab)
			}
		})
	}
}

// `@` is AltGr+q on QWERTZ; `L` opens (and closes) the command log too.
func TestCommandLogAliasOpensAndCloses(t *testing.T) {
	m := browsing(t)
	m = send(t, m, press('L'))
	if _, ok := m.modal.(*commandLogModal); !ok {
		t.Fatalf("modal = %T, want *commandLogModal", m.modal)
	}
	m = send(t, m, press('L'))
	if m.modal != nil {
		t.Fatalf("modal = %T, want nil after the alias closes it", m.modal)
	}
}

// ctrl+@ is untypeable on QWERTZ (ctrl+AltGr+q) and Bubble Tea reports
// the NUL byte as ctrl+space anyway, so only ctrl+space stays bound.
func TestCompleteDropsCtrlAt(t *testing.T) {
	k := newKeyMap()
	for _, s := range k.Complete.Keys() {
		if s == "ctrl+@" {
			t.Fatalf("complete still binds ctrl+@: %v", k.Complete.Keys())
		}
	}
	nul := tea.KeyPressMsg{Code: ' ', Mod: tea.ModCtrl}
	if !key.Matches(nul, k.Complete) {
		t.Fatalf("ctrl+space no longer matches complete: %v", k.Complete.Keys())
	}
}

// Every binding must be reachable without AltGr on a German QWERTZ
// layout: the characters below are the ones AltGr produces there, and
// none of them may be the only way to an action.
func TestNoActionNeedsAltGr(t *testing.T) {
	altGr := map[string]bool{
		"[": true, "]": true, "{": true, "}": true, "\\": true,
		"@": true, "|": true, "~": true, "€": true,
	}
	k := newKeyMap()
	for _, slot := range k.slots() {
		keys := slot.ptr.Keys()
		var free bool
		for _, s := range keys {
			if !altGr[s] && !altGr[trimCtrl(s)] {
				free = true
			}
		}
		if !free {
			t.Errorf("action %q is AltGr-only on QWERTZ: %v", slot.name, keys)
		}
	}
}

// trimCtrl strips a `ctrl+` prefix so a chord over an AltGr character
// (ctrl+@) counts as AltGr-dependent too.
func trimCtrl(s string) string {
	const p = "ctrl+"
	if len(s) > len(p) && s[:len(p)] == p {
		return s[len(p):]
	}
	return s
}
