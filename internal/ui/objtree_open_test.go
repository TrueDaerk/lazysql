package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"lazysql/internal/db"
)

// openTree opens table via the same drillIn path a real enter keypress
// drives, and hands focus back to [2] so the rest of the test can choose
// where the cursor sits. openObject only marks the tree once m.driver is
// set — with it nil, openTable treats the model as disconnected and clears
// m.grid.data instead — so a driver that is never dialed stands in, the way
// serverModel does for the activity report.
func openTree(t *testing.T, m Model, table string) Model {
	t.Helper()
	if m.driver == nil {
		drv, err := db.OpenOpts(db.EngineSQLite, db.Options{})
		if err != nil {
			t.Fatal(err)
		}
		m.driver = drv
		t.Cleanup(func() { drv.Close() })
	}
	m = treeSelect(t, send(t, m, press('2')), table)
	m = send(t, m, special(tea.KeyEnter, 0))
	return send(t, m, press('2'))
}

func TestIsOpenNodeTracksOpenRelation(t *testing.T) {
	m := sized(80, 20)
	tables := m.tree.category("", catTables)
	var users, accounts *treeNode
	for _, c := range tables.children {
		switch c.name {
		case "users":
			users = c
		case "accounts":
			accounts = c
		}
	}
	if users == nil || accounts == nil {
		t.Fatal("fixture tree is missing users/accounts")
	}

	if m.isOpenNode(users) || m.isOpenNode(accounts) {
		t.Fatal("nothing is open yet, but isOpenNode already matched a row")
	}

	m = openTree(t, m, "users")
	if !m.isOpenNode(users) {
		t.Error("opening users did not mark its node")
	}
	if m.isOpenNode(accounts) {
		t.Error("opening users marked an unrelated node")
	}

	// Opening another table moves the mark.
	m = openTree(t, m, "accounts")
	if m.isOpenNode(users) {
		t.Error("the mark stayed on users after accounts was opened")
	}
	if !m.isOpenNode(accounts) {
		t.Error("opening accounts did not mark its node")
	}

	// Disconnecting (or switching database) clears m.grid.data, which clears it.
	m.grid.data = dataView{}
	if m.isOpenNode(accounts) {
		t.Error("clearing m.grid.data left the mark in place")
	}
}

// The two background tints the tree's rows can wear, isolated from the
// rest of the SGR sequence lipgloss wraps a rendered cell in — the exact
// values presets["default"] resolves SelectionBg/RowCursorBg to.
const (
	selectedSGR = "48;5;237"
	openRowSGR  = "48;5;236"
)

// styledLineFor renders the [2] panel and returns the raw (ANSI-carrying)
// line whose stripped text contains name, so a test can tell which
// background style a row actually got.
func styledLineFor(m Model, focused bool, name string) string {
	p := m.panels[panelObjects]
	raw := p.render(m.style, focused, 40, 10, m.isOpenNode)
	for _, l := range strings.Split(raw, "\n") {
		if strings.Contains(ansi.Strip(l), name) {
			return l
		}
	}
	return ""
}

func TestObjectsPanelMarksOpenRowUnfocused(t *testing.T) {
	m := sized(80, 20)
	m = openTree(t, m, "users")
	m = send(t, m, press('3')) // move focus off [2]

	line := styledLineFor(m, false, "users")
	if line == "" {
		t.Fatal("users row not found")
	}
	if !strings.Contains(line, openRowSGR) {
		t.Errorf("unfocused open row missing the open tint: %q", line)
	}

	other := styledLineFor(m, false, "accounts")
	if strings.Contains(other, openRowSGR) {
		t.Errorf("a row that is not open got the open tint: %q", other)
	}
}

func TestObjectsPanelMarksOpenRowFocusedCursorElsewhere(t *testing.T) {
	m := sized(80, 20)
	m = openTree(t, m, "users")
	m = treeSelect(t, m, "accounts") // cursor elsewhere, [2] still focused

	line := styledLineFor(m, true, "users")
	if !strings.Contains(line, openRowSGR) {
		t.Errorf("open row lost its tint once the cursor moved away: %q", line)
	}
	if strings.Contains(line, selectedSGR) {
		t.Errorf("the open row (not under the cursor) got the cursor style: %q", line)
	}

	cursorLine := styledLineFor(m, true, "accounts")
	if !strings.Contains(cursorLine, selectedSGR) {
		t.Errorf("cursor row lost its selection style: %q", cursorLine)
	}
}

func TestObjectsPanelCursorOnOpenRowStillReadsSelected(t *testing.T) {
	m := sized(80, 20)
	m = openTree(t, m, "users")
	m = treeSelect(t, m, "users")

	line := styledLineFor(m, true, "users")
	if !strings.Contains(line, selectedSGR) {
		t.Errorf("cursor-on-open row did not use the cursor style: %q", line)
	}
}
