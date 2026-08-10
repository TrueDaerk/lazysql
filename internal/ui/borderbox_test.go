package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// renderTitledBox splices the title into the top border: the top line
// keeps the corners, keeps its exact display width, and carries the title.
func TestTitledBoxTopLine(t *testing.T) {
	s := newStyles()
	box := renderTitledBox(s.focusedBorder, s.titleFocused.Render("[3] Tables"), "users\nposts", 30, 5)

	lines := strings.Split(box, "\n")
	if len(lines) != 5 {
		t.Fatalf("box has %d lines, want 5:\n%s", len(lines), box)
	}
	for i, l := range lines {
		if w := lipgloss.Width(l); w != 30 {
			t.Errorf("line %d width = %d, want 30: %q", i, w, ansi.Strip(l))
		}
	}
	top := ansi.Strip(lines[0])
	if !strings.Contains(top, "[3] Tables") {
		t.Errorf("top border does not carry the title: %q", top)
	}
	if !strings.HasPrefix(top, "╭─") || !strings.HasSuffix(top, "╮") {
		t.Errorf("top border lost its corners: %q", top)
	}
	if strings.Contains(ansi.Strip(lines[1]), "[3] Tables") {
		t.Errorf("the title also took a content row: %q", ansi.Strip(lines[1]))
	}
}

// A panel narrower than its title truncates the title instead of pushing
// the corner out: the border stays intact at the exact box width.
func TestTitledBoxTruncatesNarrowTitle(t *testing.T) {
	s := newStyles()
	title := s.titleFocused.Render("[1] Connections") + " " + s.muted.Render("‹Data|Structure›")

	for _, w := range []int{3, 4, 6, 12, 20} {
		box := renderTitledBox(s.blurredBorder, title, "row", w, 4)
		for i, l := range strings.Split(box, "\n") {
			if got := lipgloss.Width(l); got != w {
				t.Errorf("w=%d: line %d width = %d: %q", w, i, got, ansi.Strip(l))
			}
		}
		top := ansi.Strip(strings.Split(box, "\n")[0])
		if !strings.HasPrefix(top, "╭") || !strings.HasSuffix(top, "╮") {
			t.Errorf("w=%d: top border lost a corner: %q", w, top)
		}
	}
}

// The title is styled independently of the border: a green-bordered box
// with a muted title keeps both sequences, and neither bleeds past its own
// segment — the fill after the title is border-coloured again.
func TestTitledBoxKeepsTitleAndBorderStylesApart(t *testing.T) {
	s := newStyles()
	box := renderTitledBox(s.focusedBorder, s.title.Render("Command log"), "", 24, 3)
	top := strings.Split(box, "\n")[0]
	if !strings.Contains(top, "\x1b[") {
		t.Fatalf("top border carries no styling: %q", top)
	}
	// Whatever the title left set must be closed before the corner: the
	// last cell of the line is border, not title.
	if !strings.HasSuffix(ansi.Strip(top), "─╮") {
		t.Errorf("top border does not end in fill + corner: %q", ansi.Strip(top))
	}
}

// A side panel spends every content row on rows now that the title moved
// into the border: a 3-row body shows 3 items, where it used to show 2.
func TestPanelBodyGainsTheTitleRow(t *testing.T) {
	p := &sidePanel{id: panelTables}
	p.setItems([]string{"a", "b", "c", "d"})
	body := p.render(newStyles(), true, 20, 3)

	lines := strings.Split(body, "\n")
	if len(lines) != 3 {
		t.Fatalf("body has %d rows, want 3:\n%s", len(lines), body)
	}
	for i, want := range []string{"a", "b", "c"} {
		if !strings.Contains(ansi.Strip(lines[i]), want) {
			t.Errorf("row %d = %q, want %q", i, ansi.Strip(lines[i]), want)
		}
	}
	if strings.Contains(ansi.Strip(body), "Tables") {
		t.Errorf("the title is still in the body:\n%s", body)
	}
}
