package ui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

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

// ---------- issue #208: a descending sort must cost what an ascending
// one costs ----------

// sortPerfRows is the table size the issue was reported on: small enough
// that the server cannot be the bottleneck.
const sortPerfRows = 714

// bigNoteFrom is the first id whose `note` carries a large payload. The
// rows behind it are the ones a descending sort by the primary key puts
// on page one and an ascending sort never reaches — the asymmetry the
// report was actually made of.
const bigNoteFrom = sortPerfRows - dataPageSize

// sortPerfModel browses a 714-row table whose newest rows carry a big
// text payload and whose oldest ones do not — a shape real tables grow
// into, and the one that made `s`-twice feel slow while `s`-once did not.
func sortPerfModel(t *testing.T) Model {
	t.Helper()
	m := browsing(t)
	ctx := context.Background()
	if _, err := m.driver.Exec(ctx,
		`CREATE TABLE sortperf (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, note TEXT)`); err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("a long note that no column is ever wide enough to show. ", 1200)
	for i := 0; i < sortPerfRows; i++ {
		note := "short"
		if i >= bigNoteFrom {
			note = big
		}
		if _, err := m.driver.Exec(ctx, `INSERT INTO sortperf (name, note) VALUES (?, ?)`,
			fmt.Sprintf("row %d", i), note); err != nil {
			t.Fatal(err)
		}
	}
	m = send(t, m, press('R'))
	m = treeSelect(t, m, "sortperf")
	m = send(t, m, special(tea.KeyEnter, 0))
	if len(m.data.rows) != dataPageSize {
		t.Fatalf("page holds %d rows, want %d", len(m.data.rows), dataPageSize)
	}
	return m
}

// navCost is what one navigation key costs in the state the model is in:
// the Update plus the View build Bubble Tea does per queued message, over
// a j/k round trip so the page ends where it started. It also reports how
// many statements the driver ran while the keys were pressed — which must
// be none, whatever the sort is.
func navCost(t *testing.T, m Model, keys int) (Model, time.Duration, int) {
	t.Helper()
	// An untimed pass first: the first frames of a new page warm the
	// allocator, and that is not what is being compared.
	for i := 0; i < 10; i++ {
		m = pumpKey(m, press('j'))
		m = pumpKey(m, press('k'))
	}
	before := len(m.driver.Logger().Entries())
	best := time.Duration(0)
	for round := 0; round < 3; round++ {
		start := time.Now()
		for i := 0; i < keys; i++ {
			m = pumpKey(m, press('j'))
			m = pumpKey(m, press('k'))
		}
		if took := time.Since(start) / time.Duration(2*keys); best == 0 || took < best {
			best = took
		}
	}
	return m, best, len(m.driver.Logger().Entries()) - before
}

// Navigating a descending-sorted page must be the same local state change
// as navigating an ascending one: no round trip per keystroke, and no
// per-frame work that scales with the values the page happens to hold.
// The second half was what issue #208 was about — buildGrid flattened,
// UTF-8 checked, measured and truncated every cell of the page in full on
// every frame, so a descending sort that put the table's big rows on page
// one cost an order of magnitude more per keystroke than the ascending
// sort of the same table. See cellScanBytes.
func TestDescendingSortNavigatesLikeAscending(t *testing.T) {
	m := sortPerfModel(t)

	m = send(t, m, press('s')) // ascending
	if m.data.sort == nil || m.data.sort.Desc {
		t.Fatalf("first s left sort = %+v, want ascending", m.data.sort)
	}
	m, asc, ascStmts := navCost(t, m, 40)

	m = send(t, m, press('s')) // descending
	if m.data.sort == nil || !m.data.sort.Desc {
		t.Fatalf("second s left sort = %+v, want descending", m.data.sort)
	}
	m, desc, descStmts := navCost(t, m, 40)

	if ascStmts != 0 || descStmts != 0 {
		t.Errorf("navigation ran %d statements ascending and %d descending, want none",
			ascStmts, descStmts)
	}
	// Same order of magnitude, with room for the noise a wall-clock
	// measurement carries on a shared CI runner. The regression this
	// guards against was far larger than the factor.
	if desc > 4*asc {
		t.Errorf("descending navigation costs %v per key, ascending %v — more than 4x", desc, asc)
	}
	t.Logf("per key: ascending %v, descending %v", asc, desc)
}

// benchWideCellGrid is sortPerfModel's descending page without a database
// behind it: a full page whose cells are far wider than any column can
// draw.
func benchWideCellGrid(b *testing.B) Model {
	b.Helper()
	m, err := New(true)
	if err != nil {
		b.Fatalf("New: %v", err)
	}
	m.width, m.height = 160, 48
	cols := []db.Column{
		{Name: "id", DataType: "integer"},
		{Name: "name", DataType: "text"},
		{Name: "note", DataType: "text"},
	}
	big := strings.Repeat("a long note that no column is ever wide enough to show. ", 1200)
	rows := make([][]any, dataPageSize)
	for r := range rows {
		rows[r] = []any{int64(r), fmt.Sprintf("row %d", r), big}
	}
	m.data = dataView{conn: "bench", database: "d", table: "t", cols: cols, rows: rows}
	m.table = "t"
	m.setFocus(panelMain)
	return m
}

func BenchmarkNavKeyDataGridWideCells(b *testing.B) {
	m := benchWideCellGrid(b)
	down := press('j')
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m = pumpKey(m, down)
	}
}
