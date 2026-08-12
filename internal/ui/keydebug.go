package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// keyDebugModel is `lazysql --debug-keys`: a one-screen dump of what the
// terminal actually reports for every key pressed. It exists because the
// modified keys the grid binds — shift+arrows, ctrl+enter — are the ones
// a terminal is free not to report at all, and the only way to tell a
// binding that does not match from a key that never arrived is to look at
// the events. See wiki/reference/terminal-key-reporting.md.
//
// It deliberately runs on the main screen rather than the alt screen, so
// the dump stays in the scrollback after quitting and can be pasted into
// a bug report.
type keyDebugModel struct {
	lines  []string
	enh    tea.KeyboardEnhancementsMsg
	gotEnh bool
}

// keyDebugLines is how many key events the dump keeps on screen.
const keyDebugLines = 12

// NewKeyDebug builds the key-report debugger.
func NewKeyDebug() tea.Model { return keyDebugModel{} }

func (m keyDebugModel) Init() tea.Cmd { return nil }

func (m keyDebugModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyboardEnhancementsMsg:
		m.enh, m.gotEnh = msg, true
		return m, nil
	case tea.KeyPressMsg:
		// ctrl+q quits rather than `q`: `q` is itself a key worth
		// reporting, and so is every plain letter.
		if msg.String() == "ctrl+q" {
			return m, tea.Quit
		}
		// The code prints as a rune literal rather than as text: the
		// special keys sit in the private-use area, where a raw glyph is
		// a replacement character in most fonts.
		m.lines = append(m.lines, fmt.Sprintf("%-18s code=%U mod=%d text=%q",
			msg.String(), msg.Code, msg.Mod, msg.Text))
		if len(m.lines) > keyDebugLines {
			m.lines = m.lines[len(m.lines)-keyDebugLines:]
		}
		return m, nil
	}
	return m, nil
}

func (m keyDebugModel) View() tea.View {
	var b strings.Builder
	b.WriteString("lazysql key report — press keys, ctrl+q to quit\n")
	b.WriteString(m.enhancementLine() + "\n\n")
	if len(m.lines) == 0 {
		b.WriteString("(no keys yet — try shift+↑, shift+↓, shift+←, shift+→)\n")
	}
	for _, l := range m.lines {
		b.WriteString(l + "\n")
	}
	return tea.NewView(b.String())
}

// enhancementLine says whether the terminal answered the keyboard
// enhancement request, which is the single fact that decides whether
// shift+arrows can be reported at all.
func (m keyDebugModel) enhancementLine() string {
	if !m.gotEnh {
		return "keyboard enhancements: none\n" +
			"(legacy encoding — shift+arrows arrive only as CSI 1;2A-style sequences)"
	}
	return fmt.Sprintf("keyboard enhancements: supported (disambiguation on, event types=%v)",
		m.enh.SupportsEventTypes())
}
