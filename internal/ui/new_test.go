package ui

import (
	"os"
	"strings"
	"testing"

	"lazysql/internal/config"
)

// writeConfig drops a config.toml at the test's XDG_CONFIG_HOME (set up by
// TestMain) and removes it once the test ends, so New() in later tests sees
// a clean slate again.
func writeConfig(t *testing.T, cfg *config.Config) {
	t.Helper()
	path, err := config.Path()
	if err != nil {
		t.Fatalf("config.Path: %v", err)
	}
	if err := cfg.SaveTo(path); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	t.Cleanup(func() { os.Remove(path) })
}

// An invalid [keys]/[theme] section must fail New() with a descriptive
// error, not panic or silently start with a broken keymap — unlike a
// broken connections list, which New() tolerates (see model.go's New doc).
func TestNewFailsOnUnknownKeyAction(t *testing.T) {
	writeConfig(t, &config.Config{Keys: map[string]string{"quiet": "x"}})
	_, err := New()
	if err == nil {
		t.Fatal("expected New() to fail on an unknown key action")
	}
	if !strings.Contains(err.Error(), "quiet") {
		t.Errorf("error %q does not name the offending action", err)
	}
}

func TestNewFailsOnInvalidThemeColor(t *testing.T) {
	writeConfig(t, &config.Config{Theme: map[string]string{"staged": "not-a-color"}})
	_, err := New()
	if err == nil {
		t.Fatal("expected New() to fail on an invalid theme color")
	}
	if !strings.Contains(err.Error(), "staged") {
		t.Errorf("error %q does not name the offending color", err)
	}
}

func TestNewSucceedsWithValidOverrides(t *testing.T) {
	resetPalette(t)
	writeConfig(t, &config.Config{
		Keys:  map[string]string{"quit": "x"},
		Theme: map[string]string{"theme": "light"},
	})
	m, err := New()
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	if got := m.keys.Quit.Keys(); len(got) != 1 || got[0] != "x" {
		t.Errorf("Quit.Keys() = %v, want [x]", got)
	}
}
