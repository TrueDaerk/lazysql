package ui

import (
	"fmt"
	"image/color"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
)

// palette names every themeable color slot. The `[theme]` config section
// (see internal/config) maps these kebab-case names to a color, so this
// struct is the single place that defines what "a color" in the app means.
type palette struct {
	BorderFocused color.Color
	BorderBlurred color.Color
	Accent        color.Color
	Staged        color.Color
	Deleted       color.Color
	Error         color.Color
	SelectionBg   color.Color
	RowCursorBg   color.Color
	CellCursorBg  color.Color
	// FocusInputBg tints the box of a single-line input that currently
	// holds the keyboard (the quick filter, prompt and form modals), so
	// "typing lands here" reads at a glance instead of only from a dim
	// cursor. It has to stay a green dark/light enough that default text
	// drawn over it — in either theme — is still legible.
	FocusInputBg color.Color

	// SQL syntax highlighting in the query editor. Identifiers and
	// operators have no slot of their own: they keep the terminal's
	// foreground, which is what makes the coloured tokens stand out.
	SQLKeyword     color.Color
	SQLString      color.Color
	SQLNumber      color.Color
	SQLComment     color.Color
	SQLPlaceholder color.Color
}

// presets are the built-in themes selectable via `theme = "<name>"` in the
// `[theme]` section. default uses the low ANSI indexes so it tracks whatever
// scheme the user's terminal already has; light hardcodes concrete colors
// tuned for a white background, since those low indexes cannot be trusted to
// stay readable there.
var presets = map[string]palette{
	"default": {
		BorderFocused: lipgloss.Color("2"),
		BorderBlurred: lipgloss.Color("8"),
		Accent:        lipgloss.Color("6"),
		Staged:        lipgloss.Color("3"),
		Deleted:       lipgloss.Color("1"),
		Error:         lipgloss.Color("1"),
		SelectionBg:   lipgloss.Color("237"),
		RowCursorBg:   lipgloss.Color("236"),
		CellCursorBg:  lipgloss.Color("240"),
		FocusInputBg:  lipgloss.Color("22"),

		SQLKeyword:     lipgloss.Color("5"),
		SQLString:      lipgloss.Color("2"),
		SQLNumber:      lipgloss.Color("6"),
		SQLComment:     lipgloss.Color("8"),
		SQLPlaceholder: lipgloss.Color("3"),
	},
	"light": {
		BorderFocused: lipgloss.Color("#1a7f37"),
		BorderBlurred: lipgloss.Color("#6e7781"),
		Accent:        lipgloss.Color("#036672"),
		Staged:        lipgloss.Color("#9a6700"),
		Deleted:       lipgloss.Color("#cf222e"),
		Error:         lipgloss.Color("#a40e26"),
		SelectionBg:   lipgloss.Color("253"),
		RowCursorBg:   lipgloss.Color("253"),
		CellCursorBg:  lipgloss.Color("245"),
		FocusInputBg:  lipgloss.Color("#c3ecc9"),

		SQLKeyword:     lipgloss.Color("#8250df"),
		SQLString:      lipgloss.Color("#0a3069"),
		SQLNumber:      lipgloss.Color("#0550ae"),
		SQLComment:     lipgloss.Color("#6e7781"),
		SQLPlaceholder: lipgloss.Color("#9a6700"),
	},
}

type paletteSlot struct {
	name string
	ptr  *color.Color
}

// slots is the single source both resolvePalette and its "unknown color
// name" error message read: every config-facing name paired with the
// palette field it sets.
func (p *palette) slots() []paletteSlot {
	return []paletteSlot{
		{"border-focused", &p.BorderFocused},
		{"border-blurred", &p.BorderBlurred},
		{"accent", &p.Accent},
		{"staged", &p.Staged},
		{"deleted", &p.Deleted},
		{"error", &p.Error},
		{"selection-bg", &p.SelectionBg},
		{"row-cursor-bg", &p.RowCursorBg},
		{"cell-cursor-bg", &p.CellCursorBg},
		{"focus-input-bg", &p.FocusInputBg},
		{"sql-keyword", &p.SQLKeyword},
		{"sql-string", &p.SQLString},
		{"sql-number", &p.SQLNumber},
		{"sql-comment", &p.SQLComment},
		{"sql-placeholder", &p.SQLPlaceholder},
	}
}

func paletteNames() []string {
	var p palette
	names := make([]string, 0, len(p.slots()))
	for _, s := range p.slots() {
		names = append(names, s.name)
	}
	sort.Strings(names)
	return names
}

func presetNames() []string {
	names := make([]string, 0, len(presets))
	for name := range presets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// resolvePalette builds the active palette from the `[theme]` config
// section: the "theme" key (if present) selects the preset base, and every
// other key overrides one named color on top of it. A nil/empty map yields
// the default preset unchanged.
func resolvePalette(theme map[string]string) (palette, error) {
	presetName := theme["theme"]
	if presetName == "" {
		presetName = "default"
	}
	p, ok := presets[presetName]
	if !ok {
		return palette{}, fmt.Errorf(
			"unknown theme %q (valid presets: %s)", presetName, strings.Join(presetNames(), ", "))
	}

	lookup := map[string]*color.Color{}
	for _, s := range p.slots() {
		lookup[s.name] = s.ptr
	}

	names := make([]string, 0, len(theme))
	for name := range theme {
		if name != "theme" {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	for _, name := range names {
		ptr, ok := lookup[name]
		if !ok {
			return palette{}, fmt.Errorf(
				"unknown theme color %q (valid names: %s)", name, strings.Join(paletteNames(), ", "))
		}
		c, err := resolveColor(theme[name])
		if err != nil {
			return palette{}, fmt.Errorf("theme color %q: %w", name, err)
		}
		*ptr = c
	}
	return p, nil
}

var (
	hexColorRe = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

	// ansiColorNames are the standard 16-color terminal names. gray/grey
	// alias bright-black, the common name for the "dim" tone in that slot.
	ansiColorNames = map[string]string{
		"black": "0", "red": "1", "green": "2", "yellow": "3",
		"blue": "4", "magenta": "5", "cyan": "6", "white": "7",
		"bright-black": "8", "gray": "8", "grey": "8",
		"bright-red": "9", "bright-green": "10", "bright-yellow": "11",
		"bright-blue": "12", "bright-magenta": "13", "bright-cyan": "14", "bright-white": "15",
	}
)

// resolveColor accepts an ANSI color name, a 256-color index (0-255), or a
// #rrggbb / #rgb hex value — the three formats the README documents for
// `[theme]`.
func resolveColor(s string) (color.Color, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return nil, fmt.Errorf("empty color value")
	case hexColorRe.MatchString(s):
		return lipgloss.Color(s), nil
	}
	if idx, err := strconv.Atoi(s); err == nil {
		if idx < 0 || idx > 255 {
			return nil, fmt.Errorf("256-color index %d out of range 0-255", idx)
		}
		return lipgloss.Color(s), nil
	}
	if code, ok := ansiColorNames[strings.ToLower(s)]; ok {
		return lipgloss.Color(code), nil
	}
	return nil, fmt.Errorf(
		"invalid color %q: expected an ANSI name, a 256-color index (0-255), or a #rrggbb hex value", s)
}

// applyPalette makes p the active theme: every style newStyles builds, plus
// the few call sites that reach for a color directly (data grid row tints,
// the danger modal border), read these package vars.
func applyPalette(p palette) {
	colorGreen = p.BorderFocused
	colorMuted = p.BorderBlurred
	colorCyan = p.Accent
	colorYellow = p.Staged
	colorDeleted = p.Deleted
	colorError = p.Error
	colorSelectionBg = p.SelectionBg
	colorRowCursorBg = p.RowCursorBg
	colorCellCursorBg = p.CellCursorBg
	colorFocusInputBg = p.FocusInputBg

	colorSQLKeyword = p.SQLKeyword
	colorSQLString = p.SQLString
	colorSQLNumber = p.SQLNumber
	colorSQLComment = p.SQLComment
	colorSQLPlaceholder = p.SQLPlaceholder
}
