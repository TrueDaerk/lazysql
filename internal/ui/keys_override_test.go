package ui

import (
	"strings"
	"testing"
)

func TestApplyKeyOverridesRebindsAction(t *testing.T) {
	km := newKeyMap()
	if err := applyKeyOverrides(&km, map[string]string{"quit": "x"}); err != nil {
		t.Fatalf("applyKeyOverrides: %v", err)
	}
	if got := km.Quit.Keys(); len(got) != 1 || got[0] != "x" {
		t.Fatalf("Quit.Keys() = %v, want [x]", got)
	}
	if got := km.Quit.Help().Key; got != "x" {
		t.Errorf("Quit.Help().Key = %q, want %q", got, "x")
	}
	if got := km.Quit.Help().Desc; got != "quit" {
		t.Errorf("Quit.Help().Desc = %q, want the original description preserved", got)
	}
}

func TestApplyKeyOverridesAcceptsMultipleKeys(t *testing.T) {
	km := newKeyMap()
	if err := applyKeyOverrides(&km, map[string]string{"down": "j, down, ctrl+n"}); err != nil {
		t.Fatalf("applyKeyOverrides: %v", err)
	}
	want := []string{"j", "down", "ctrl+n"}
	got := km.Down.Keys()
	if len(got) != len(want) {
		t.Fatalf("Down.Keys() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Down.Keys() = %v, want %v", got, want)
		}
	}
}

func TestApplyKeyOverridesUnknownAction(t *testing.T) {
	km := newKeyMap()
	err := applyKeyOverrides(&km, map[string]string{"quiet": "x"})
	if err == nil {
		t.Fatal("expected an error for an unknown action name")
	}
	if want := `unknown key action "quiet"`; !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not identify the bad action", err)
	}
	if !strings.Contains(err.Error(), "quit") {
		t.Errorf("error %q does not list valid action names", err)
	}
}

func TestApplyKeyOverridesEmptyValue(t *testing.T) {
	km := newKeyMap()
	err := applyKeyOverrides(&km, map[string]string{"quit": "  , ,"})
	if err == nil {
		t.Fatal("expected an error for an empty key value")
	}
}

// TestKeyOverridePropagatesToOptionsBarAndHelp is the acceptance criterion
// from issue #13: overriding a key must change the options bar and `?` help
// consistently, not just key dispatch. Both already read the same keyMap
// struct (see wiki/design/keybindings-single-source.md), so an override
// applied in place is enough.
func TestKeyOverridePropagatesToOptionsBarAndHelp(t *testing.T) {
	km := newKeyMap()
	if err := applyKeyOverrides(&km, map[string]string{"quit": "x"}); err != nil {
		t.Fatalf("applyKeyOverrides: %v", err)
	}

	for _, b := range km.optionsBarBindings(panelConnections) {
		if b.Help().Desc == "quit" && b.Help().Key != "x" {
			t.Errorf("options bar shows quit as %q, want %q", b.Help().Key, "x")
		}
	}

	found := false
	for _, group := range km.helpGroups(panelConnections) {
		for _, b := range group.bindings {
			if b.Help().Desc == "quit" {
				found = true
				if b.Help().Key != "x" {
					t.Errorf("help shows quit as %q, want %q", b.Help().Key, "x")
				}
			}
		}
	}
	if !found {
		t.Fatal("quit missing from help groups after override")
	}
}
