package ui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
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

	colorSQLKeyword     = lipgloss.Color("5")
	colorSQLString      = lipgloss.Color("2")
	colorSQLNumber      = lipgloss.Color("6")
	colorSQLComment     = lipgloss.Color("8")
	colorSQLPlaceholder = lipgloss.Color("3")
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

	// The schema diff report: added green, removed red, changed yellow
	// — the staged-changes color language, applied to schema objects.
	// plain is the unstyled fallback for context lines.
	plain      lipgloss.Style
	diffAdd    lipgloss.Style
	diffDel    lipgloss.Style
	diffChange lipgloss.Style

	// The completion popup. It borrows the accent for its border rather
	// than the modal's green: it is not a modal — it claims five keys and
	// lets everything else keep typing — and a second green box on screen
	// would read as one.
	popup         lipgloss.Style
	popupSelected lipgloss.Style
	popupTag      lipgloss.Style

	// Data grid. The row tint is deliberately weaker than the cell tint
	// so the cell cursor stays readable inside the highlighted row.
	gridHeader       lipgloss.Style
	gridHeaderCursor lipgloss.Style
	gridSeparator    lipgloss.Style
	rowCursor        lipgloss.Style
	cellCursor       lipgloss.Style

	// SQL syntax highlighting. sqlQuoted covers delimited identifiers,
	// which are neither plain text nor literals; it borrows the accent so
	// the theme keeps one knob for "this is a name the user quoted".
	sqlKeyword     lipgloss.Style
	sqlString      lipgloss.Style
	sqlNumber      lipgloss.Style
	sqlComment     lipgloss.Style
	sqlPlaceholder lipgloss.Style
	sqlQuoted      lipgloss.Style

	// The editor's own cursor. Insert mode reverses the cell it sits on,
	// the way a terminal block cursor does, so it stays legible over any
	// token colour; normal mode only tints it, since nothing typed there
	// lands in the buffer.
	editorCursor     lipgloss.Style
	editorCursorIdle lipgloss.Style
	editorGutter     lipgloss.Style
}

func newStyles() styles {
	border := lipgloss.NewStyle().Border(lipgloss.RoundedBorder())
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

		plain:      lipgloss.NewStyle(),
		diffAdd:    lipgloss.NewStyle().Foreground(colorGreen),
		diffDel:    lipgloss.NewStyle().Foreground(colorDeleted),
		diffChange: lipgloss.NewStyle().Foreground(colorYellow),

		popup: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorCyan),
		popupSelected: lipgloss.NewStyle().Background(colorSelectionBg).Foreground(colorCyan),
		popupTag:      lipgloss.NewStyle().Foreground(colorMuted),

		gridHeader:       lipgloss.NewStyle().Bold(true),
		gridHeaderCursor: lipgloss.NewStyle().Bold(true).Foreground(colorCyan),
		gridSeparator:    lipgloss.NewStyle().Foreground(colorMuted),
		rowCursor:        lipgloss.NewStyle().Background(colorRowCursorBg),
		cellCursor:       lipgloss.NewStyle().Background(colorCellCursorBg).Bold(true),

		sqlKeyword:     lipgloss.NewStyle().Foreground(colorSQLKeyword).Bold(true),
		sqlString:      lipgloss.NewStyle().Foreground(colorSQLString),
		sqlNumber:      lipgloss.NewStyle().Foreground(colorSQLNumber),
		sqlComment:     lipgloss.NewStyle().Foreground(colorSQLComment).Italic(true),
		sqlPlaceholder: lipgloss.NewStyle().Foreground(colorSQLPlaceholder).Bold(true),
		sqlQuoted:      lipgloss.NewStyle().Foreground(colorCyan),

		editorCursor:     lipgloss.NewStyle().Reverse(true),
		editorCursorIdle: lipgloss.NewStyle().Background(colorCellCursorBg),
		editorGutter:     lipgloss.NewStyle().Foreground(colorMuted),
	}
}

// titleBorderPad is how many border runes sit between a corner and the
// embedded title, matching lazygit's `╭─[3]─Local branches─╮`.
const titleBorderPad = 1

// renderTitledBox draws body inside a w x h box and splices the title into
// the top border line, lazygit style, instead of spending a content row on
// it. Lipgloss has no border-title support, so the top line is rebuilt from
// the style's own border runes and top foreground: the corners and the fill
// keep the border colour, the title keeps whatever styling it arrived with.
//
// title is expected to be pre-styled (it mixes colours — number, name,
// sub-tabs, loading marker), so truncation has to be ANSI-aware; a byte or
// rune cut would slice an escape sequence and bleed colour into the border.
func renderTitledBox(border lipgloss.Style, title, body string, w, h int) string {
	box := border.Width(w).Height(h).Render(body)
	if title == "" || !border.GetBorderTop() {
		return box
	}
	b := border.GetBorderStyle()
	if b.Top == "" {
		return box
	}
	lines := strings.Split(box, "\n")
	if len(lines) == 0 {
		return box
	}

	// What is left of the top line once the corners and the padding runes
	// on either side of the title are accounted for.
	room := w - lipgloss.Width(b.TopLeft) - lipgloss.Width(b.TopRight) - 2*titleBorderPad*lipgloss.Width(b.Top)
	if room < 1 {
		return box
	}
	t := ansi.Truncate(title, room, "…")
	fill := room - lipgloss.Width(t)
	if fill < 0 {
		fill = 0
	}

	ink := lipgloss.NewStyle()
	if fg := border.GetBorderTopForeground(); fg != nil {
		ink = ink.Foreground(fg)
	}
	if bg := border.GetBorderTopBackground(); bg != nil {
		ink = ink.Background(bg)
	}
	lines[0] = ink.Render(b.TopLeft+strings.Repeat(b.Top, titleBorderPad)) +
		t +
		ink.Render(strings.Repeat(b.Top, fill+titleBorderPad)+b.TopRight)
	return strings.Join(lines, "\n")
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
