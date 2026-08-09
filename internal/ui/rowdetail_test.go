package ui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// `x` opens the cursor row as a name/type/value list: every column of
// the fixture, NULL rendered like the grid.
func TestRowDetailListsColumnsWithNullAndValue(t *testing.T) {
	m := dataBrowsing(t)
	m = send(t, m, press('x'))
	rd, ok := m.modal.(*rowDetailModal)
	if !ok {
		t.Fatalf("x opened %T, want the row detail modal", m.modal)
	}
	if len(rd.fields) != len(m.data.cols) {
		t.Fatalf("fields = %d, want %d columns", len(rd.fields), len(m.data.cols))
	}
	if rd.fields[0].name != "id" || rd.fields[0].text != "1" {
		t.Fatalf("id field = %+v, want text \"1\"", rd.fields[0])
	}
	if rd.fields[2].name != "note" || !rd.fields[2].isNull || rd.fields[2].text != nullText {
		t.Fatalf("note field = %+v, want a NULL field", rd.fields[2])
	}
	out := rd.view(m.style, m.width, m.height)
	for _, want := range []string{"id", "name", "note", "payload", nullText} {
		if !strings.Contains(out, want) {
			t.Errorf("view is missing %q:\n%s", want, out)
		}
	}
}

// A staged cell edit shows its pending value with the staged tint, the
// same rule the grid follows.
func TestRowDetailShowsStagedEdit(t *testing.T) {
	m := dataBrowsing(t)
	m = send(t, m, press('l')) // cursor onto `name`
	m = stageEdit(t, m, "renamed")
	m = send(t, m, press('x'))
	rd, ok := m.modal.(*rowDetailModal)
	if !ok {
		t.Fatalf("x opened %T, want the row detail modal", m.modal)
	}
	if !rd.fields[1].isStaged || rd.fields[1].text != "renamed" {
		t.Fatalf("name field = %+v, want the staged value", rd.fields[1])
	}
}

// A row staged for deletion opens with the same struck-through status the
// grid renders it with.
func TestRowDetailShowsStagedDelete(t *testing.T) {
	m := dataBrowsing(t)
	m = send(t, m, press('d'), press('x'))
	rd, ok := m.modal.(*rowDetailModal)
	if !ok {
		t.Fatalf("x opened %T, want the row detail modal", m.modal)
	}
	if rd.status != rowDeleted {
		t.Fatalf("status = %v, want rowDeleted", rd.status)
	}
	out := rd.view(m.style, m.width, m.height)
	if !strings.Contains(out, "staged for deletion") {
		t.Errorf("view = %q, want it to mention the staged deletion", out)
	}
}

// The phantom row of a staged INSERT shows DEFAULT for the column it left
// unbound and the typed value for the rest.
func TestRowDetailShowsStagedInsert(t *testing.T) {
	m := dataBrowsing(t)
	m, f := insertForm(t, m, 'n')
	setField(f, "name", "phantom")
	m = send(t, m, special(tea.KeyEnter, 0))

	m.data.row = len(m.data.rows)
	m.clampCursor()
	m = send(t, m, press('x'))
	rd, ok := m.modal.(*rowDetailModal)
	if !ok {
		t.Fatalf("x opened %T, want the row detail modal", m.modal)
	}
	if rd.status != rowInserted {
		t.Fatalf("status = %v, want rowInserted", rd.status)
	}
	if !rd.fields[0].isDefault || rd.fields[0].text != defaultText {
		t.Fatalf("id field = %+v, want DEFAULT", rd.fields[0])
	}
	if rd.fields[1].text != "phantom" {
		t.Fatalf("name field = %+v, want the staged insert value", rd.fields[1])
	}
}

// j/k move the field cursor and scroll once the row has more fields than
// the modal has room for.
func TestRowDetailScrolls(t *testing.T) {
	m := dataBrowsing(t)
	ctx := context.Background()
	cols := make([]string, 0, 30)
	for i := 0; i < 30; i++ {
		cols = append(cols, fmt.Sprintf("c%d", i))
	}
	create := "CREATE TABLE wide (" + strings.Join(colDefs(cols), ", ") + ")"
	if _, err := m.driver.Exec(ctx, create); err != nil {
		t.Fatal(err)
	}
	insert := "INSERT INTO wide VALUES (" + strings.Repeat("1,", 29) + "1)"
	if _, err := m.driver.Exec(ctx, insert); err != nil {
		t.Fatal(err)
	}
	m = send(t, m, press('3'), press('R'))
	if !m.panels[panelTables].selectByName("wide") {
		t.Fatalf("fixture table not listed: %v", m.panels[panelTables].items)
	}
	m = send(t, m, special(tea.KeyEnter, 0))
	m = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 20})
	m = send(t, m, press('x'))
	rd, ok := m.modal.(*rowDetailModal)
	if !ok {
		t.Fatalf("x opened %T, want the row detail modal", m.modal)
	}
	if len(rd.fields) != 30 {
		t.Fatalf("fields = %d, want 30", len(rd.fields))
	}
	for i := 0; i < 29; i++ {
		m = send(t, m, press('j'))
	}
	rd = m.modal.(*rowDetailModal)
	if rd.cursor != 29 {
		t.Fatalf("cursor = %d, want the last field", rd.cursor)
	}
	// offset is derived at render time, not on every cursor move.
	_ = m.View()
	if rd.offset == 0 {
		t.Fatal("offset did not advance to keep the cursor on screen")
	}
}

func colDefs(names []string) []string {
	defs := make([]string, len(names))
	for i, n := range names {
		defs[i] = n + " INTEGER"
	}
	return defs
}

// esc closes the row detail back to the grid without moving the cell
// cursor.
func TestRowDetailEscPreservesCursor(t *testing.T) {
	m := dataBrowsing(t)
	m = send(t, m, press('j'), press('l'))
	row, col := m.data.row, m.data.col
	m = send(t, m, press('x'), special(tea.KeyEscape, 0))
	if m.modal != nil {
		t.Fatal("esc did not close the row detail modal")
	}
	if m.data.row != row || m.data.col != col {
		t.Fatalf("cursor = (%d,%d), want (%d,%d) preserved", m.data.row, m.data.col, row, col)
	}
}

// `v` on a field opens the same cell detail popup `v` opens from the
// grid, pretty-printing JSON the same way.
func TestRowDetailViewFieldOpensCellModal(t *testing.T) {
	m := dataBrowsing(t)
	m = send(t, m, press('x'))
	rd := m.modal.(*rowDetailModal)
	rd.cursor = 3 // payload column
	m = send(t, m, press('v'))
	c, ok := m.modal.(*cellModal)
	if !ok {
		t.Fatalf("v opened %T, want the cell modal", m.modal)
	}
	if !strings.Contains(c.title, "json") {
		t.Errorf("title = %q, want it to mention json", c.title)
	}
}

// The binding is documented in `?` and shown for the panel like every
// other data-grid action.
func TestRowDetailKeyIsDocumented(t *testing.T) {
	m := dataBrowsing(t)
	m = send(t, m, press('?'))
	if !strings.Contains(m.View().Content, "row detail") {
		t.Error("help is missing the row detail binding")
	}
}
