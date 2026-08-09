package ui

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// ANSI palette colors respect the user's terminal theme, which keeps the
// lazygit look consistent across color schemes.
var (
	colorGreen  = lipgloss.Color("2")
	colorYellow = lipgloss.Color("3")
	colorRed    = lipgloss.Color("1")
	colorCyan   = lipgloss.Color("6")
	colorMuted  = lipgloss.Color("8")
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
}

func newStyles() styles {
	border := lipgloss.NewStyle().Border(lipgloss.NormalBorder())
	return styles{
		focusedBorder: border.BorderForeground(colorGreen),
		blurredBorder: border.BorderForeground(colorMuted),

		title:        lipgloss.NewStyle().Foreground(colorMuted),
		titleFocused: lipgloss.NewStyle().Foreground(colorGreen).Bold(true),
		selected:     lipgloss.NewStyle().Background(lipgloss.Color("237")).Foreground(colorCyan),
		muted:        lipgloss.NewStyle().Foreground(colorMuted),
		keyHint:      lipgloss.NewStyle().Foreground(colorCyan),

		optionsBar: lipgloss.NewStyle().Foreground(colorMuted),
		modal: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorGreen).
			Padding(0, 2),
		modalTitle: lipgloss.NewStyle().Bold(true).Foreground(colorGreen),
		danger:     lipgloss.NewStyle().Foreground(colorRed),
		pending:    lipgloss.NewStyle().Foreground(colorYellow),
	}
}

// statusColor maps a row status to its tint. The idle status has no color of
// its own so it inherits the terminal's default foreground.
func statusColor(st itemStatus) (color.Color, bool) {
	switch st {
	case statusOK:
		return colorGreen, true
	case statusError:
		return colorRed, true
	case statusPending:
		return colorYellow, true
	}
	return nil, false
}
