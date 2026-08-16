package ui

import (
	"context"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"lazysql/internal/db"
)

func TestFormatCount(t *testing.T) {
	cases := map[int64]string{
		0:             "0",
		1:             "1",
		999:           "999",
		9999:          "9999",
		10000:         "10K",
		12345:         "12.3K",
		999_949:       "999.9K", // just under the step
		999_999:       "1M",     // rounds up into the next unit, not to 1000K
		1200000:       "1.2M",
		3450000000:    "3.5B",
		2000000000000: "2T",
	}
	for in, want := range cases {
		if got := formatCount(in); got != want {
			t.Errorf("formatCount(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatBytes(t *testing.T) {
	cases := map[int64]string{
		0:          "0 B",
		512:        "512 B",
		1024:       "1 KB",
		1536:       "1.5 KB",
		356515840:  "340 MB",
		1073741824: "1 GB",
		1048570:    "1 MB", // 1023.99 KB steps up rather than reading 1024 KB
	}
	for in, want := range cases {
		if got := formatBytes(in); got != want {
			t.Errorf("formatBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

// The annotation marks the row count as an estimate, spells a singular
// row, and leaves out whatever the engine could not answer.
func TestStatNote(t *testing.T) {
	cases := []struct {
		stat db.TableStat
		want string
	}{
		{db.TableStat{Rows: 1200000, Bytes: 356515840}, "~1.2M rows · 340 MB"},
		{db.TableStat{Rows: 1, Bytes: db.StatUnknown}, "~1 row"},
		{db.TableStat{Rows: 0, Bytes: db.StatUnknown}, "~0 rows"},
		{db.TableStat{Rows: db.StatUnknown, Bytes: 4096}, "4 KB"},
		{db.TableStat{Rows: db.StatUnknown, Bytes: db.StatUnknown}, ""},
	}
	for _, c := range cases {
		if got := statNote(c.stat); got != c.want {
			t.Errorf("statNote(%+v) = %q, want %q", c.stat, got, c.want)
		}
	}
}

// A table node wears its size once the stats reply lands; views and
// triggers never do, and a table the statistics say nothing about keeps
// the plain rendering.
func TestTableNodesShowStatsAfterLoad(t *testing.T) {
	m := sized(120, 40)
	seedTree(&m, []string{"users", "accounts"}, []string{"active_users"})
	m.applyTableStats("", []db.TableStat{
		{Table: "users", Rows: 1200000, Bytes: 356515840},
		{Table: "accounts", Rows: db.StatUnknown, Bytes: db.StatUnknown},
	})
	m.refreshTree()

	if got := noteOf(t, m, "users"); got != "~1.2M rows · 340 MB" {
		t.Errorf("users note = %q", got)
	}
	if got := noteOf(t, m, "accounts"); got != "" {
		t.Errorf("accounts note = %q, want none for an all-unknown stat", got)
	}
	// The view category is not annotated at all, even for a view whose
	// name collides with a table's stats entry.
	m.applyTableStats("", []db.TableStat{{Table: "active_users", Rows: 5, Bytes: 1024}})
	m.tree.category("", catViews).expanded = true
	m.refreshTree()
	if got := noteOf(t, m, "active_users"); got != "" {
		t.Errorf("view note = %q, want none", got)
	}
}

// A relation listing that lands after the sizes still gets them: the two
// replies are independent and either order must annotate the tree.
func TestStatsSurviveRelationReload(t *testing.T) {
	m := sized(120, 40)
	seedTree(&m, []string{"users"}, nil)
	m.applyTableStats("", []db.TableStat{{Table: "users", Rows: 42, Bytes: db.StatUnknown}})
	// applyRelations replaces the nodes wholesale, as a reload does.
	m.applyRelations("", []db.Relation{{Name: "users", Kind: db.RelationTable}})
	m.tree.category("", catTables).expanded = true
	m.refreshTree()
	if got := noteOf(t, m, "users"); got != "~42 rows" {
		t.Errorf("users note after a relation reload = %q, want ~42 rows", got)
	}
}

// Expanding the Tables category asks for the sizes in one query of its
// own, and the reply annotates the tree.
func TestExpandingTablesLoadsStats(t *testing.T) {
	m := browsing(t)
	ctx := context.Background()
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS sized_users`,
		`CREATE TABLE sized_users (id INTEGER PRIMARY KEY, name TEXT)`,
		`INSERT INTO sized_users (id, name) VALUES (1, 'a'), (2, 'b'), (3, 'c')`,
		// Without an ANALYZE SQLite has no row statistics at all.
		`ANALYZE`,
	} {
		if _, err := m.driver.Exec(ctx, stmt); err != nil {
			t.Fatalf("fixture %q: %v", stmt, err)
		}
	}
	m = send(t, m, press('2'), press('R'))
	p := m.panels[panelObjects]
	if !p.selectByName("sized_users") {
		t.Fatalf("fixture table not listed: %v", p.items)
	}
	row, ok := p.rowAt(p.cursor)
	if !ok {
		t.Fatal("the cursor row is not a tree row")
	}
	note, style := row.note()
	if style != noteStats || !strings.Contains(note, "~3 rows") {
		t.Fatalf("sized_users note = %q (style %v), want the row estimate", note, style)
	}
	if row.node.stat == nil || row.node.stat.Rows != 3 {
		t.Fatalf("sized_users node carries no row estimate: %+v", row.node.stat)
	}
}

// A failed or stale statistics reply is not an error the user sees: the
// tree keeps rendering exactly as it did without one.
func TestStatsFailureIsSilent(t *testing.T) {
	m := sized(120, 40)
	seedTree(&m, []string{"users"}, nil)
	before := m.View().Content

	m = send(t, m, tableStatsLoadedMsg{conn: m.active, database: "", err: context.DeadlineExceeded})
	if got := noteOf(t, m, "users"); got != "" {
		t.Errorf("a failed stats reply annotated the tree: %q", got)
	}
	// A reply for a connection the user has already left is dropped too.
	m = send(t, m, tableStatsLoadedMsg{
		conn:  "another-connection",
		stats: []db.TableStat{{Table: "users", Rows: 7, Bytes: db.StatUnknown}},
	})
	if got := noteOf(t, m, "users"); got != "" {
		t.Errorf("a stale stats reply annotated the tree: %q", got)
	}
	if got := m.View().Content; got != before {
		t.Error("a failed stats reply changed the rendering")
	}
}

// The annotation is what a narrow panel gives up: first its size half,
// then the whole note — never a cell of the table name.
func TestNarrowPanelTruncatesAnnotationFirst(t *testing.T) {
	for _, w := range []int{60, 34, 26, 18, 12} {
		m := sized(w+40, 40)
		seedTree(&m, []string{"users"}, nil)
		m.applyTableStats("", []db.TableStat{{Table: "users", Rows: 1200000, Bytes: 356515840}})
		m.refreshTree()

		p := m.panels[panelObjects]
		p.selectByName("Tables") // keep the highlight off the row under test
		out := ansi.Strip(p.render(m.style, false, w, 10))
		line := ""
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(l, "users") {
				line = l
			}
		}
		if line == "" {
			t.Fatalf("width %d: the table name was cut:\n%s", w, out)
		}
		if lipgloss.Width(line) > w {
			t.Fatalf("width %d: row is %d cells wide: %q", w, lipgloss.Width(line), line)
		}
		// Whatever survives of the annotation is a prefix of the full one.
		note := strings.TrimSpace(strings.SplitN(strings.TrimSpace(line), "users", 2)[1])
		if note != "" && !strings.HasPrefix("~1.2M rows · 340 MB", note) {
			t.Errorf("width %d: annotation %q is not a shortening of the full one", w, note)
		}
	}
}

func TestFitStatNote(t *testing.T) {
	const full = " ~1.2M rows · 340 MB"
	cases := []struct {
		room int
		want string
	}{
		{40, full},
		{lipgloss.Width(full), full},
		{lipgloss.Width(full) - 1, " ~1.2M rows"},
		{11, " ~1.2M rows"},
		{10, ""},
		{0, ""},
		{-5, ""},
	}
	for _, c := range cases {
		if got := fitStatNote(full, c.room); got != c.want {
			t.Errorf("fitStatNote(room=%d) = %q, want %q", c.room, got, c.want)
		}
	}
}

// noteOf returns the annotation rendered beside one tree row.
func noteOf(t *testing.T, m Model, name string) string {
	t.Helper()
	p := m.panels[panelObjects]
	for i := range p.items {
		if p.items[i] != name {
			continue
		}
		row, ok := p.rowAt(i)
		if !ok {
			t.Fatalf("row %q is not a tree row", name)
		}
		note, _ := row.note()
		return note
	}
	t.Fatalf("row %q not in the tree: %v", name, p.items)
	return ""
}
