package ui

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// pgUp/pgDown build the KeyPressMsg the terminal sends for those keys —
// see data_test.go:182 for the same construction over the data grid.
func pgDown() tea.KeyPressMsg { return special(tea.KeyPgDown, 0) }
func pgUp() tea.KeyPressMsg   { return special(tea.KeyPgUp, 0) }

// TestPageDownUpPagesObjectsPanelAndClamps drives [2] Objects with more
// rows than fit the panel: pgdown/pgup must move the cursor by exactly one
// visible page and stop at either end rather than overshooting past the
// first/last row.
func TestPageDownUpPagesObjectsPanelAndClamps(t *testing.T) {
	m := sized(80, 20)
	items := make([]string, 20)
	for i := range items {
		items[i] = fmt.Sprintf("item-%02d", i)
	}
	m.panels[panelObjects].setItems(items)
	m = send(t, m, press('2'))
	if m.focus != panelObjects {
		t.Fatalf("focus = %v, want panelObjects", m.focus)
	}

	page := m.sidePanelPageSize()
	if page <= 0 || page >= len(items) {
		t.Fatalf("page size = %d, want a partial page over %d rows", page, len(items))
	}

	m = send(t, m, pgDown())
	if got := m.panels[panelObjects].cursor; got != page {
		t.Fatalf("cursor after pgdown = %d, want %d", got, page)
	}
	// offset is derived from the cursor at render time (sidePanel.visible),
	// the same as a plain j/k move — render once to make it follow, then
	// check the cursor row still falls inside the visible window.
	m.View()
	p := m.panels[panelObjects]
	if p.cursor < p.offset || p.cursor >= p.offset+page {
		t.Fatalf("cursor %d not inside the visible window [%d, %d)", p.cursor, p.offset, p.offset+page)
	}

	// Enough pgdowns to run past the end must clamp on the last row, not
	// walk off the slice.
	for i := 0; i < len(items); i++ {
		m = send(t, m, pgDown())
	}
	if got := m.panels[panelObjects].cursor; got != len(items)-1 {
		t.Fatalf("cursor = %d, want clamped to the last row %d", got, len(items)-1)
	}

	m = send(t, m, pgUp())
	if want := len(items) - 1 - page; m.panels[panelObjects].cursor != want {
		t.Fatalf("cursor after pgup = %d, want %d", m.panels[panelObjects].cursor, want)
	}

	// ctrl+b is the pgup alias; enough of them clamp at the first row.
	for i := 0; i < len(items); i++ {
		m = send(t, m, ctrl('b'))
	}
	if got := m.panels[panelObjects].cursor; got != 0 {
		t.Fatalf("ctrl+b did not clamp at the first row: cursor = %d", got)
	}
	m.View()
	if got := m.panels[panelObjects].offset; got != 0 {
		t.Fatalf("offset did not follow the cursor back to the top: offset = %d", got)
	}
}

// TestPageDownUpPagesConnectionsPanel confirms panel [1] pages the same
// way as [2] — the issue asks for it in every side panel, not just Objects.
func TestPageDownUpPagesConnectionsPanel(t *testing.T) {
	m := sized(80, 20)
	names := make([]string, 15)
	for i := range names {
		names[i] = fmt.Sprintf("conn-%02d", i)
	}
	m.panels[panelConnections].setItems(names)

	page := m.sidePanelPageSize()
	m = send(t, m, pgDown())
	if got := m.panels[panelConnections].cursor; got != page {
		t.Fatalf("cursor after pgdown = %d, want %d", got, page)
	}
	m = send(t, m, ctrl('f'))
	if got := m.panels[panelConnections].cursor; got != 2*page {
		t.Fatalf("cursor after ctrl+f = %d, want %d", got, 2*page)
	}
}

// TestPageDownUpDuringFilterPagesFilteredRowsWithoutDisturbingTyping covers
// the acceptance criterion that an active `/` filter keeps paging over the
// narrowed rows and pgup/pgdown never lands in the pattern text.
func TestPageDownUpDuringFilterPagesFilteredRowsWithoutDisturbingTyping(t *testing.T) {
	m := sized(80, 20)
	items := make([]string, 20)
	for i := range items {
		items[i] = fmt.Sprintf("row-%02d", i)
	}
	m.panels[panelObjects].setItems(items)
	m = send(t, m, press('2'), press('/'))
	if !m.panels[panelObjects].filtering {
		t.Fatal("`/` did not open the filter input")
	}

	page := m.sidePanelPageSize()
	m = send(t, m, pgDown())
	if !m.panels[panelObjects].filtering {
		t.Fatal("pgdown closed the filter input")
	}
	if got := m.panels[panelObjects].cursor; got != page {
		t.Fatalf("cursor after pgdown while filtering = %d, want %d", got, page)
	}

	m = send(t, m, press('r'), press('o'))
	if got := m.panels[panelObjects].filter; got != "ro" {
		t.Fatalf("filter = %q, want typing to still reach the pattern", got)
	}
}
