package ui

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// ANSI palette colors respect the user's terminal theme, which keeps the
// lazygit look consistent across color schemes. These are the defaults;
// applyPalette overwrites them once at startup from the resolved `[theme]`
// config (built-in preset plus any per-color overrides).
var (
	colorGreen  = lipgloss.Color("2")
	colorYellow = lipgloss.Color("3")
	colorCyan   = lipgloss.Color("6")
	colorMuted  = lipgloss.Color("8")

	colorDeleted = lipgloss.Color("1")
	colorError   = lipgloss.Color("1")

	colorSelectionBg  = lipgloss.Color("237")
	colorRowCursorBg  = lipgloss.Color("236")
	colorCellCursorBg = lipgloss.Color("240")
)

type styles struct {
	focusedBorder lipgloss.Style
	blurredBorder lipgloss.Style

	title        lipgloss.Style
	titleFocused lipgloss.Style
	selected     lipgloss.Style
	muted        lipgloss.Style
	keyHint      lipgloss.Style

	optionsBar lipgloss.Style
	modal      lipgloss.Style
	modalTitle lipgloss.Style
	danger     lipgloss.Style
	pending    lipgloss.Style

	// Data grid. The row tint is deliberately weaker than the cell tint
	// so the cell cursor stays readable inside the highlighted row.
	gridHeader       lipgloss.Style
	gridHeaderCursor lipgloss.Style
	rowCursor        lipgloss.Style
	cellCursor       lipgloss.Style
}

func newStyles() styles {
	border := lipgloss.NewStyle().Border(lipgloss.NormalBorder())
	return styles{
		focusedBorder: border.BorderForeground(colorGreen),
		blurredBorder: border.BorderForeground(colorMuted),

		title:        lipgloss.NewStyle().Foreground(colorMuted),
		titleFocused: lipgloss.NewStyle().Foreground(colorGreen).Bold(true),
		selected:     lipgloss.NewStyle().Background(colorSelectionBg).Foreground(colorCyan),
		muted:        lipgloss.NewStyle().Foreground(colorMuted),
		keyHint:      lipgloss.NewStyle().Foreground(colorCyan),

		optionsBar: lipgloss.NewStyle().Foreground(colorMuted),
		modal: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorGreen).
			Padding(0, 2),
		modalTitle: lipgloss.NewStyle().Bold(true).Foreground(colorGreen),
		danger:     lipgloss.NewStyle().Foreground(colorError),
		pending:    lipgloss.NewStyle().Foreground(colorYellow),

		gridHeader:       lipgloss.NewStyle().Bold(true),
		gridHeaderCursor: lipgloss.NewStyle().Bold(true).Foreground(colorCyan),
		rowCursor:        lipgloss.NewStyle().Background(colorRowCursorBg),
		cellCursor:       lipgloss.NewStyle().Background(colorCellCursorBg).Bold(true),
	}
}

// statusColor maps a row status to its tint. The idle status has no color of
// its own so it inherits the terminal's default foreground.
func statusColor(st itemStatus) (color.Color, bool) {
	switch st {
	case statusOK:
		return colorGreen, true
	case statusError:
		return colorError, true
	case statusPending:
		return colorYellow, true
	}
	return nil, false
}
