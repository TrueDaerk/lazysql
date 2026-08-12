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
	p := &sidePanel{id: panelObjects}
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
	if strings.Contains(ansi.Strip(body), "Objects") {
		t.Errorf("the title is still in the body:\n%s", body)
	}
}

// truncate is handed text that is already styled — a grid row, the grid
// header, the options bar — and an escape sequence is bytes, not columns.
// Counting its `[`, its digits and its `m` as visible width cut a tinted
// row several cells short of its box and threw away the closing reset
// with the rest, so the cursor cell's tint was both too narrow and bled
// into the frame after it (issue #132).
func TestTruncateMeasuresStyledTextInCells(t *testing.T) {
	s := newStyles()
	cases := []struct {
		what string
		text string
		w    int
		want int
	}{
		{"plain, fits", "abcdef", 10, 6},
		{"plain, cut", "abcdefghij", 6, 6},
		{"styled, fits", s.titleFocused.Render("abcdef"), 10, 6},
		{"styled, cut", s.titleFocused.Render(strings.Repeat("x", 40)), 20, 20},
		{"several runs", s.title.Render("abc") + s.muted.Render(strings.Repeat("y", 40)), 12, 12},
		{"wide runes", strings.Repeat("日", 10), 9, 9},
	}
	for _, c := range cases {
		got := truncate(c.text, c.w)
		if w := lipgloss.Width(got); w != c.want {
			t.Errorf("%s: truncate(…, %d) is %d cells wide, want %d: %q",
				c.what, c.w, w, c.want, got)
		}
		// Nothing may be left open: a style the cut ran through has to be
		// closed, or it runs on into the rest of the frame.
		if strings.Contains(got, "\x1b[") && !strings.HasSuffix(got, "\x1b[m") {
			t.Errorf("%s: truncate left a style open: %q", c.what, got)
		}
	}
	// A multi-line block is cut row by row: w is a box width, not a budget
	// spent across the whole block.
	if got, want := truncate("aaaa\nbbbb\ncccc", 3), "aa…\nbb…\ncc…"; got != want {
		t.Errorf("truncate over a block = %q, want %q", got, want)
	}
}
