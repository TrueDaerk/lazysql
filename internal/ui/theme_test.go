package ui

import (
	"image/color"
	"strings"
	"testing"
)

// resetPalette restores the default theme after a test that calls
// applyPalette, since the color vars it sets are package-global.
func resetPalette(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		applyPalette(presets["default"])
	})
}

func TestResolveColorFormats(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"ansi name", "green", false},
		{"ansi name uppercase", "Green", false},
		{"bright ansi name", "bright-red", false},
		{"gray alias", "gray", false},
		{"256 index", "237", false},
		{"256 index boundary", "255", false},
		{"hex long", "#1a7f37", false},
		{"hex short", "#fff", false},
		{"empty", "", true},
		{"out of range index", "256", true},
		{"negative index", "-1", true},
		{"bad hex", "#zzzzzz", true},
		{"unknown name", "chartreuse", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := resolveColor(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("resolveColor(%q) error = %v, wantErr %v", tc.in, err, tc.wantErr)
			}
		})
	}
}

func TestResolvePaletteDefaultsWithNoTheme(t *testing.T) {
	p, err := resolvePalette(nil)
	if err != nil {
		t.Fatalf("resolvePalette(nil): %v", err)
	}
	if p != presets["default"] {
		t.Fatalf("resolvePalette(nil) = %+v, want the default preset", p)
	}
}

func TestResolvePaletteLightPreset(t *testing.T) {
	p, err := resolvePalette(map[string]string{"theme": "light"})
	if err != nil {
		t.Fatalf("resolvePalette(light): %v", err)
	}
	if p != presets["light"] {
		t.Fatalf("resolvePalette(light) = %+v, want the light preset", p)
	}
	if p == presets["default"] {
		t.Fatal("light preset resolved to the same colors as default")
	}
}

func TestResolvePaletteOverridesOneColor(t *testing.T) {
	p, err := resolvePalette(map[string]string{"border-focused": "#00ff00"})
	if err != nil {
		t.Fatalf("resolvePalette: %v", err)
	}
	if p.BorderFocused != resolveMust(t, "#00ff00") {
		t.Errorf("BorderFocused not overridden: %v", p.BorderFocused)
	}
	if p.BorderBlurred != presets["default"].BorderBlurred {
		t.Errorf("BorderBlurred changed unexpectedly: %v", p.BorderBlurred)
	}
}

func TestResolvePaletteUnknownTheme(t *testing.T) {
	_, err := resolvePalette(map[string]string{"theme": "dracula"})
	if err == nil {
		t.Fatal("expected an error for an unknown theme preset")
	}
	if !strings.Contains(err.Error(), "default") || !strings.Contains(err.Error(), "light") {
		t.Errorf("error %q does not list the valid presets", err)
	}
}

func TestResolvePaletteUnknownColorName(t *testing.T) {
	_, err := resolvePalette(map[string]string{"borde-focused": "green"})
	if err == nil {
		t.Fatal("expected an error for an unknown color name")
	}
	if !strings.Contains(err.Error(), "border-focused") {
		t.Errorf("error %q does not list valid color names", err)
	}
}

func TestResolvePaletteInvalidColorValue(t *testing.T) {
	_, err := resolvePalette(map[string]string{"staged": "not-a-color"})
	if err == nil {
		t.Fatal("expected an error for an invalid color value")
	}
	if !strings.Contains(err.Error(), "staged") {
		t.Errorf("error %q does not name the offending key", err)
	}
}

func TestApplyPaletteSetsPackageColors(t *testing.T) {
	resetPalette(t)
	applyPalette(presets["light"])
	if colorGreen != presets["light"].BorderFocused {
		t.Errorf("colorGreen = %v, want the light preset's BorderFocused", colorGreen)
	}
	if colorDeleted != presets["light"].Deleted {
		t.Errorf("colorDeleted = %v, want the light preset's Deleted", colorDeleted)
	}
}

func resolveMust(t *testing.T, s string) color.Color {
	t.Helper()
	c, err := resolveColor(s)
	if err != nil {
		t.Fatalf("resolveColor(%q): %v", s, err)
	}
	return c
}
