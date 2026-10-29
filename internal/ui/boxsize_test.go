package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// assertExactSize fails unless block is exactly w cells wide on every row
// and exactly h rows tall — the size contract every box and the full frame
// keep (wiki/design/box-size-contract.md).
func assertExactSize(t *testing.T, what, block string, w, h int) {
	t.Helper()
	lines := strings.Split(block, "\n")
	if len(lines) != h || lipgloss.Height(block) != h {
		t.Errorf("%s: %d rows, want %d:\n%s", what, len(lines), h, ansi.Strip(block))
		return
	}
	for i, l := range lines {
		if got := lipgloss.Width(l); got != w {
			t.Errorf("%s: row %d is %d cells wide, want %d: %q", what, i, got, w, ansi.Strip(l))
		}
	}
}

// sizeContractBodies is content that used to push a box out of its
// rectangle one way or another.
func sizeContractBodies() map[string]string {
	s := newStyles()
	return map[string]string{
		"empty":       "",
		"overlong":    strings.Repeat("SELECT * FROM t WHERE x = 1 ", 20),
		"many rows":   strings.Repeat("row\n", 50),
		"multi-line":  "SELECT a,\n       b\nFROM t\nWHERE " + strings.Repeat("c = 1 AND ", 30),
		"CJK":         strings.Repeat("日本語テキスト", 20) + "\n" + strings.Repeat("中", 3),
		"emoji":       strings.Repeat("🚀👍🏽", 30),
		"ANSI styled": s.titleFocused.Render(strings.Repeat("x", 200)) + "\n" + s.danger.Render("err"),
		"tabs":        strings.Repeat("a\tb\t", 40),
		"carriage":    "abc\r\n" + strings.Repeat("d\r", 80),
		"wide at cut": "a" + strings.Repeat("日", 40),
	}
}

func TestFitBlockIsExactSize(t *testing.T) {
	for name, body := range sizeContractBodies() {
		for _, sz := range [][2]int{{1, 1}, {7, 3}, {20, 5}, {80, 24}} {
			assertExactSize(t, fmt.Sprintf("%s %dx%d", name, sz[0], sz[1]),
				fitBlock(body, sz[0], sz[1]), sz[0], sz[1])
		}
	}
	if got := fitBlock("anything", 0, 3); got != "" {
		t.Errorf("fitBlock at width 0 = %q, want empty", got)
	}
}

// renderTitledBox is the box layer every panel goes through: whatever the
// renderer hands it, the box is exactly the size it was allocated, with
// both corners of the top and bottom borders in place.
func TestTitledBoxIsExactSizeForAnyBody(t *testing.T) {
	s := newStyles()
	titles := []string{"", s.titleFocused.Render("[2] Objects"),
		s.title.Render("name\nwith a break " + strings.Repeat("长", 40))}
	for name, body := range sizeContractBodies() {
		for _, title := range titles {
			for _, sz := range [][2]int{{4, 3}, {24, 5}, {50, 12}, {120, 40}} {
				box := renderTitledBox(s.blurredBorder, title, body, sz[0], sz[1])
				what := fmt.Sprintf("%s %dx%d title=%q", name, sz[0], sz[1], ansi.Strip(title))
				assertExactSize(t, what, box, sz[0], sz[1])
				lines := strings.Split(ansi.Strip(box), "\n")
				first, last := lines[0], lines[len(lines)-1]
				if !strings.HasPrefix(first, "╭") || !strings.HasSuffix(first, "╮") ||
					!strings.HasPrefix(last, "╰") || !strings.HasSuffix(last, "╯") {
					t.Errorf("%s: border lost a corner:\n%s", what, ansi.Strip(box))
				}
			}
		}
	}
}

// withNoisyLog fills the command log with the entries that used to wrap
// the strip: overlong and multi-line SQL, tabs, wide runes.
func withNoisyLog(m Model) Model {
	at := time.Now()
	texts := []string{
		"SELECT " + strings.Repeat("some_long_column_name, ", 30) + "id FROM t;",
		"SELECT a,\n       b\nFROM t\nWHERE x = 1\n  AND y = 2;",
		"UPDATE t SET note = '日本語テキスト🚀' WHERE\tid = 1;",
		"-- note\twith\ttabs " + strings.Repeat("x", 300),
		"SELECT 1\nUNION ALL\nSELECT 2\nUNION ALL\nSELECT 3\nUNION ALL\nSELECT 4;",
	}
	for i, txt := range texts {
		m.commandLog = append(m.commandLog, logLine{text: txt, at: at.Add(time.Duration(i) * time.Millisecond), err: i == 1})
	}
	m.commandLog = append(m.commandLog, logLine{text: "SELECT 'newest';", at: at.Add(time.Second)})
	return m
}

// The strip never wraps: each entry is one row, cut to the box, the
// newest entries are the ones shown, and the border stays intact.
func TestCommandLogStripNeverWraps(t *testing.T) {
	m := withNoisyLog(dataBrowsing(t))
	for _, sz := range [][2]int{{40, 5}, {60, 7}, {120, 10}} {
		w, h := sz[0], sz[1]
		strip := m.renderCommandLog(w, h)
		assertExactSize(t, fmt.Sprintf("strip %dx%d", w, h), strip, w, h)

		lines := strings.Split(ansi.Strip(strip), "\n")
		rows := lines[1 : len(lines)-1]
		for i, r := range rows {
			// One entry per row: every row opens with its own timestamp.
			if body := strings.TrimPrefix(r, "│"); len(body) < 8 || body[2] != ':' || body[5] != ':' {
				t.Errorf("strip %dx%d: row %d is not an entry of its own: %q", w, h, i, r)
			}
		}
		if !strings.Contains(rows[len(rows)-1], "newest") {
			t.Errorf("strip %dx%d: newest entry not on the last row:\n%s", w, h, ansi.Strip(strip))
		}
	}
}

// The expanded log keeps a statement's line breaks, so it scrolls by
// display line — counting a multi-line entry as one row grew the box past
// the terminal.
func TestCommandLogModalFitsTheScreen(t *testing.T) {
	m := withNoisyLog(dataBrowsing(t))
	for _, sz := range [][2]int{{minWidth, minHeight}, {80, 24}} {
		m = send(t, m, tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
		m = send(t, m, press('@'))
		box := m.modal.view(m.style, m.width, m.height)
		if h := lipgloss.Height(box); h > m.height {
			t.Errorf("%dx%d: log modal is %d rows tall", sz[0], sz[1], h)
		}
		if !strings.Contains(ansi.Strip(box), "newest") {
			t.Errorf("%dx%d: log modal does not open on the newest line:\n%s", sz[0], sz[1], ansi.Strip(box))
		}
		m = send(t, m, special(tea.KeyEscape, 0))
	}
}

// The whole frame is exactly the terminal at every size above the
// tiny-terminal guard, in every screen mode, with the log open or
// collapsed, and with a modal up.
func TestViewIsExactlyTheTerminal(t *testing.T) {
	base := withNoisyLog(dataBrowsing(t))
	sizes := [][2]int{
		{minWidth, minHeight}, {minWidth + 1, minHeight + 1}, {61, 19}, {70, 20},
		{80, 24}, {100, 30}, {120, 40}, {203, 57},
	}
	for _, sz := range sizes {
		m := send(t, base, tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
		check := func(state string) {
			t.Helper()
			assertExactSize(t, fmt.Sprintf("%dx%d %s", sz[0], sz[1], state), m.View().Content, sz[0], sz[1])
		}
		for i := 0; i < 3; i++ { // normal → half → full
			check(fmt.Sprintf("screen=%d", m.screen))
			for f := 0; f < 4; f++ {
				m = send(t, m, special(tea.KeyTab, 0))
				check(fmt.Sprintf("screen=%d focus=%d", m.screen, m.focus))
			}
			m = send(t, m, press('+'))
		}
		m = send(t, m, press('T'))
		check("log collapsed")
		m = send(t, m, press('T'))
		m = send(t, m, press('@'))
		check("log modal")
		m = send(t, m, special(tea.KeyEscape, 0))
		m = send(t, m, press('?'))
		check("help modal")
	}
}

// Shrinking the terminal after a long query ran keeps the frame exact:
// nothing rendered for the old size survives into the new one.
func TestViewStaysExactAcrossResize(t *testing.T) {
	m := withNoisyLog(dataBrowsing(t))
	for _, sz := range [][2]int{{200, 60}, {minWidth, minHeight}, {120, 40}, {61, 19}, {80, 24}} {
		m = send(t, m, tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
		assertExactSize(t, fmt.Sprintf("after resize to %dx%d", sz[0], sz[1]), m.View().Content, sz[0], sz[1])
	}
}

// The root layout's pieces sum to the terminal on their own, before the
// frame's final clip: side panels stack to the body height, the main
// column and the log strip fill theirs, and the bar is one row.
func TestLayoutPiecesSumToTheTerminal(t *testing.T) {
	m := withNoisyLog(dataBrowsing(t))
	for _, sz := range [][2]int{{minWidth, minHeight}, {80, 24}, {120, 40}} {
		m = send(t, m, tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
		bodyH := m.height - 1
		sideW := m.sideWidth()
		heights := m.panelHeights(bodyH)
		sum := 0
		for id := panelID(0); id < panelCount; id++ {
			sum += heights[id]
			assertExactSize(t, fmt.Sprintf("%dx%d panel %d", sz[0], sz[1], id),
				m.renderPanel(id, sideW, heights[id]), sideW, heights[id])
		}
		if sum != bodyH {
			t.Errorf("%dx%d: side panels sum to %d rows, want %d", sz[0], sz[1], sum, bodyH)
		}
		assertExactSize(t, fmt.Sprintf("%dx%d main column", sz[0], sz[1]),
			m.renderMainColumn(m.width-sideW, bodyH), m.width-sideW, bodyH)
		assertExactSize(t, fmt.Sprintf("%dx%d options bar", sz[0], sz[1]),
			m.renderOptionsBar(), m.width, 1)
	}
}
