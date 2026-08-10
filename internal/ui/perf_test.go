package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/db"
)

// The benchmarks below model the message pump the way bubbletea v2 runs
// it: every queued input message costs one Update *and* one View build
// (tea.go's eventLoop calls p.render(model) per message; only the flush
// is frame-rate limited). Issue #78's backlog is that product, so the
// numbers to watch are the per-(Update+View) cost of a navigation key in
// each of the three hot places: the query editor, the data grid, and a
// long side panel list.

// benchQueryModel is a shell with a several-hundred-line script open in
// the query editor's normal mode — the issue's editor scenario.
func benchQueryModel(b *testing.B, lines int) Model {
	b.Helper()
	m, err := New(true)
	if err != nil {
		b.Fatalf("New: %v", err)
	}
	m.width, m.height = 160, 48
	var sb strings.Builder
	for i := 0; i < lines; i++ {
		fmt.Fprintf(&sb, "SELECT id, name, created_at FROM orders_%d WHERE status = 'open' AND total > %d.5 -- line %d\n", i, i, i)
	}
	m.setScript(sb.String())
	m.setFocus(panelQuery)
	m.setEditing(false)
	m.editor.area.MoveToBegin()
	return m
}

// benchGridModel is a shell with a full 100-row page open in the grid.
func benchGridModel(b *testing.B) Model {
	b.Helper()
	m, err := New(true)
	if err != nil {
		b.Fatalf("New: %v", err)
	}
	m.width, m.height = 160, 48
	cols := make([]db.Column, 20)
	for i := range cols {
		cols[i] = db.Column{Name: fmt.Sprintf("column_%d", i), DataType: "text"}
	}
	rows := make([][]any, dataPageSize)
	for r := range rows {
		row := make([]any, len(cols))
		for c := range row {
			row[c] = fmt.Sprintf("value %d/%d with some width to it", r, c)
		}
		rows[r] = row
	}
	m.data = dataView{conn: "bench", database: "d", table: "t", cols: cols, rows: rows}
	m.table = "t"
	m.setFocus(panelMain)
	return m
}

// benchPanelModel is a shell with a long relation list in panel [3].
func benchPanelModel(b *testing.B) Model {
	b.Helper()
	m, err := New(true)
	if err != nil {
		b.Fatalf("New: %v", err)
	}
	m.width, m.height = 160, 48
	items := make([]string, 5000)
	for i := range items {
		items[i] = fmt.Sprintf("table_%04d", i)
	}
	m.panels[panelObjects].setItems(items)
	m.setFocus(panelObjects)
	return m
}

// pumpKey is one queued input event's full cost: Update plus the View
// build the event loop does before moving to the next message.
func pumpKey(m Model, msg tea.KeyPressMsg) Model {
	next, _ := m.Update(msg)
	mm := next.(Model)
	_ = mm.View()
	return mm
}

func BenchmarkNavKeyQueryEditor(b *testing.B) {
	m := benchQueryModel(b, 400)
	down := press('j')
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m = pumpKey(m, down)
	}
}

func BenchmarkNavKeyDataGrid(b *testing.B) {
	m := benchGridModel(b)
	down := press('j')
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m = pumpKey(m, down)
	}
}

func BenchmarkNavKeySidePanel(b *testing.B) {
	m := benchPanelModel(b)
	down := press('j')
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m = pumpKey(m, down)
	}
}

func BenchmarkViewQueryEditor400Lines(b *testing.B) {
	m := benchQueryModel(b, 400)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.View()
	}
}
