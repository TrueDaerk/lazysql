package ui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// numsBrowsing opens a small table with a clean integer column, a REAL
// column holding a NULL, an INTEGER column holding a NULL and a text
// column, cursor on the top-left cell.
func numsBrowsing(t *testing.T) Model {
	t.Helper()
	m := browsing(t)
	ctx := context.Background()
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS nums`,
		`CREATE TABLE nums (id INTEGER PRIMARY KEY, amount REAL, qty INTEGER, label TEXT)`,
		`INSERT INTO nums VALUES (1, 1.5, 10, 'a'), (2, 2.5, 20, 'b'), (3, NULL, 30, 'c'), (4, 4, NULL, 'd')`,
	} {
		if _, err := m.driver.Exec(ctx, stmt); err != nil {
			t.Fatalf("fixture %q: %v", stmt, err)
		}
	}
	m = send(t, m, press('2'), press('R'))
	if !m.panels[panelObjects].selectByName("nums") {
		t.Fatalf("fixture table not listed: %v", m.panels[panelObjects].items)
	}
	return send(t, m, special(tea.KeyEnter, 0))
}

func plainStatus(m Model, w int) string { return ansi.Strip(m.dataStatusFit(w)) }

// A numeric block reports count, sum, avg, min and max, and says the
// figures are the current page's.
func TestSelectionAggregateNumericBlock(t *testing.T) {
	m := send(t, numsBrowsing(t), shiftRight(), shiftRight(), shiftDown())
	got := plainStatus(m, 0)
	for _, want := range []string{
		"2 rows × 3 columns selected",
		"this page: count 6  sum 37  avg 6.1667  min 1  max 20",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("status = %q, want %q", got, want)
		}
	}
	if strings.Contains(got, "skipped") {
		t.Fatalf("status = %q, want no skipped cells in a clean block", got)
	}
}

// NULL cells stay out of the figures and are counted as skipped.
func TestSelectionAggregateSkipsNulls(t *testing.T) {
	m := send(t, numsBrowsing(t), press('l'), press('C'), shiftDown(), shiftDown(), shiftDown())
	got := plainStatus(m, 0)
	want := "this page: count 3  sum 8  avg 2.6667  min 1.5  max 4 · 1 skipped"
	if !strings.Contains(got, want) {
		t.Fatalf("status = %q, want %q", got, want)
	}
}

// A block of text says nothing numeric was selected rather than zero.
func TestSelectionAggregateAllText(t *testing.T) {
	m := send(t, numsBrowsing(t), press('l'), press('l'), press('l'), press('C'), shiftDown())
	got := plainStatus(m, 0)
	if !strings.Contains(got, "2 rows × 1 columns selected · nothing numeric selected") {
		t.Fatalf("status = %q, want the nothing-numeric note", got)
	}
	if strings.Contains(got, "sum") {
		t.Fatalf("status = %q, want no sum for a text block", got)
	}
}

// The figures follow the column span as it narrows.
func TestSelectionAggregateFollowsColumnNarrowing(t *testing.T) {
	m := send(t, numsBrowsing(t), shiftRight(), shiftRight(), shiftDown(), shiftLeft())
	got := plainStatus(m, 0)
	if !strings.Contains(got, "count 4  sum 7  avg 1.75  min 1  max 2.5") {
		t.Fatalf("status = %q, want the id/amount block only", got)
	}
}

// A whole-row selection is not aggregated, and neither is a grid with no
// selection at all.
func TestSelectionAggregateOnlyForBlocks(t *testing.T) {
	m := numsBrowsing(t)
	if got := plainStatus(m, 0); strings.Contains(got, "page:") {
		t.Fatalf("status = %q, want no aggregate without a selection", got)
	}
	m = send(t, m, ctrl('v'), shiftDown())
	if got := plainStatus(m, 0); strings.Contains(got, "sum") || strings.Contains(got, "numeric") {
		t.Fatalf("status = %q, want no aggregate for whole rows", got)
	}
}

// At narrow widths the aggregate shortens, then drops out, rather than
// pushing the sort marker off the line — and every form that shows a
// figure still says it is the page's.
func TestSelectionAggregateDegradesWithWidth(t *testing.T) {
	m := send(t, numsBrowsing(t), press('l'), press('C'), shiftDown(), shiftDown(), shiftDown())
	full := lipgloss.Width(m.dataStatusFit(0))
	for w := full; w >= 20; w-- {
		got := m.dataStatusFit(w)
		plain := ansi.Strip(got)
		if strings.Contains(plain, "sum") {
			if lipgloss.Width(got) > w {
				t.Fatalf("width %d: status %q is %d wide", w, plain, lipgloss.Width(got))
			}
			if !strings.Contains(plain, "page") || !strings.Contains(plain, "skipped") {
				t.Fatalf("width %d: status %q lost the page or skipped note", w, plain)
			}
		}
	}
	if got := plainStatus(m, 40); strings.Contains(got, "sum") {
		t.Fatalf("status = %q, want the aggregate dropped at 40 cells", got)
	}
}

// Past the cell bound the aggregate says so instead of walking the block.
func TestSelectionAggregateTooLarge(t *testing.T) {
	a := selectionAggregate{cells: aggregateCellLimit + 1, tooLarge: true}
	if v := a.variants(); !strings.Contains(v[0], "too many cells") {
		t.Fatalf("variants = %q", v)
	}
}

func TestNumericValue(t *testing.T) {
	cases := []struct {
		v       any
		decimal bool
		want    float64
		ok      bool
	}{
		{int64(3), false, 3, true},
		{2.5, false, 2.5, true},
		{nil, false, 0, false},
		{"12", false, 0, false},
		{"12.50", true, 12.5, true},
		{"n/a", true, 0, false},
		{true, false, 0, false},
	}
	for _, c := range cases {
		got, ok := numericValue(c.v, c.decimal)
		if got != c.want || ok != c.ok {
			t.Errorf("numericValue(%v, %v) = %v, %v; want %v, %v", c.v, c.decimal, got, ok, c.want, c.ok)
		}
	}
}
