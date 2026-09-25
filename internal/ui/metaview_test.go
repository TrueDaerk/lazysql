package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// At a common 80-column terminal the relation name must stay fully visible
// in the main title, even if the tab strip has to give way for it — the
// strip is redundant once shortened because the focused tab is still
// highlighted (issue #217).
func TestMainTitleKeepsRelationNameAt80Columns(t *testing.T) {
	m := dataBrowsing(t)
	m.width, m.height = 80, 24
	cw := maxInt(m.width-m.sideWidth()-2, 1)
	title := m.mainTitle(cw)
	if !strings.Contains(title, m.grid.data.table) {
		t.Fatalf("main title = %q, want it to contain the relation name %q", title, m.grid.data.table)
	}
}

// At the minimum supported width the title must still fit inside the box:
// renderTitledBox truncates blindly at whatever mainTitle returns, so a
// title wider than the room it truncates to would overflow the border.
func TestMainTitleFitsAtMinimumWidth(t *testing.T) {
	m := dataBrowsing(t)
	m.width, m.height = minWidth, minHeight
	cw := maxInt(m.width-m.sideWidth()-2, 1)
	title := m.mainTitle(cw)
	room := maxInt(cw-2, 0)
	if w := lipgloss.Width(title); w > room {
		t.Fatalf("main title width = %d, want <= %d (the room renderTitledBox truncates to)", w, room)
	}
}

// Wide terminals are unchanged: the full tab strip and the full relation
// name both fit, so nothing should be shortened.
func TestMainTitleShowsFullStripAndNameWhenWide(t *testing.T) {
	m := dataBrowsing(t)
	cw := 200
	title := m.mainTabBar(cw)
	for _, want := range mainTabNames {
		if !strings.Contains(title, want) {
			t.Fatalf("tab bar = %q, want it to contain %q", title, want)
		}
	}
	if !strings.Contains(title, m.grid.data.table) {
		t.Fatalf("tab bar = %q, want it to contain the relation name %q", title, m.grid.data.table)
	}
}
