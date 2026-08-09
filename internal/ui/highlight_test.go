package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// editorRows splits a rendered editor block into its rows.
func blockRows(block string) []string { return strings.Split(block, "\n") }

// countCursors counts the cursor cells in a rendered block. The insert
// cursor is the only thing the editor renders reversed, and reverse
// video is SGR 7 whatever the terminal's colours are.
func countCursors(block string) int { return strings.Count(block, "\x1b[7m") }

// focusedEditor is a model with the query panel focused, in insert mode,
// holding script.
func focusedEditor(t *testing.T, script string) Model {
	t.Helper()
	m := sized(120, 40)
	m = send(t, m, press(':'))
	m.setScript(script)
	return m
}

func TestEditorHighlightsEveryTokenKind(t *testing.T) {
	m := focusedEditor(t, "SELECT 'a''b', 12 -- WHERE\nFROM t WHERE id = :name")
	out := m.editorBlock(60, 6)
	s := m.style

	cases := []struct {
		what  string
		style lipgloss.Style
		text  string
	}{
		{"keyword", s.sqlKeyword, "SELECT"},
		{"string", s.sqlString, "'a''b'"},
		{"number", s.sqlNumber, "12"},
		{"comment", s.sqlComment, "-- WHERE"},
		{"placeholder", s.sqlPlaceholder, ":name"},
	}
	for _, c := range cases {
		if want := c.style.Render(c.text); !strings.Contains(out, want) {
			t.Errorf("%s %q is not styled as such:\n%q", c.what, c.text, out)
		}
	}
	// The five kinds have to differ from one another, or "visually
	// distinct" is only true on paper.
	seen := map[string]string{}
	for _, c := range cases {
		code := c.style.Render("x")
		if prev, dup := seen[code]; dup {
			t.Errorf("%s renders exactly like %s", c.what, prev)
		}
		seen[code] = c.what
	}
	// A keyword inside a comment stays comment-coloured: the tokenizer's
	// rule has to survive the trip through the renderer. The comment is
	// the whole of row 1; the WHERE on row 2 is a real keyword.
	if row := blockRows(out)[0]; strings.Contains(row, s.sqlKeyword.Render("WHERE")) {
		t.Errorf("the WHERE inside the line comment was highlighted as a keyword:\n%q", row)
	}
}

// Identifiers and operators keep the terminal's own foreground, which is
// what lets the coloured tokens stand out at all.
func TestEditorLeavesIdentifiersUnstyled(t *testing.T) {
	m := focusedEditor(t, "SELECT id FROM t")
	out := m.editorBlock(60, 3)
	if !strings.Contains(out, " id ") {
		t.Errorf("the identifier `id` picked up styling:\n%q", out)
	}
}

func TestEditorCursorSitsOnTheCharacterUnderIt(t *testing.T) {
	m := focusedEditor(t, "SELECT 1")
	m.editor.area.MoveToBegin()
	out := m.editorBlock(60, 3)
	if want := m.style.editorCursor.Render("S"); !strings.Contains(out, want) {
		t.Fatalf("the cursor is not on the first character:\n%q", out)
	}

	// Two columns in, it is on the L — and nowhere else.
	m.editor.area.SetCursorColumn(2)
	out = m.editorBlock(60, 3)
	if want := m.style.editorCursor.Render("L"); !strings.Contains(out, want) {
		t.Fatalf("the cursor is not on column 2:\n%q", out)
	}
	if n := countCursors(out); n != 1 {
		t.Fatalf("%d cursor cells were drawn, want 1:\n%q", n, out)
	}

	// At the end of the buffer it becomes a trailing cell rather than
	// disappearing.
	m.editor.area.MoveToEnd()
	out = m.editorBlock(60, 3)
	if want := m.style.editorCursor.Render(" "); !strings.Contains(out, want) {
		t.Fatalf("no cursor at the end of the buffer:\n%q", out)
	}
}

// The cursor follows the buffer onto the second line, not just the
// second column.
func TestEditorCursorOnALaterLine(t *testing.T) {
	m := focusedEditor(t, "SELECT 1\nFROM t")
	m.editor.area.MoveToEnd()
	rows := blockRows(m.editorBlock(60, 4))
	if !strings.Contains(rows[1], m.style.editorCursor.Render(" ")) {
		t.Fatalf("the cursor is not on row 2:\n%q", rows)
	}
	if countCursors(rows[0]) != 0 {
		t.Fatalf("a cursor was drawn on row 1 too:\n%q", rows)
	}
}

// A blurred editor draws no cursor: one on an unfocused panel reads as
// "typing here works".
func TestEditorDrawsNoCursorWhileUnfocused(t *testing.T) {
	m := focusedEditor(t, "SELECT 1")
	m = send(t, m, special('\x1b', 0), special('\x1b', 0)) // esc out of insert, then out of the panel
	if m.focus == panelQuery {
		t.Fatal("the test did not leave the query panel")
	}
	out := m.editorBlock(60, 3)
	if countCursors(out) != 0 {
		t.Fatalf("an unfocused editor drew a cursor:\n%q", out)
	}
}

func TestEditorWrapsLinesWiderThanThePanel(t *testing.T) {
	const w = 24
	long := "SELECT " + strings.Repeat("a", 60) + " FROM t"
	m := focusedEditor(t, long)
	m.editor.area.MoveToEnd()

	rows := blockRows(m.editorBlock(w, 10))
	filled := 0
	for _, row := range rows {
		if lipgloss.Width(row) > w {
			t.Fatalf("row %q is %d cells wide, want at most %d", row, lipgloss.Width(row), w)
		}
		if strings.TrimSpace(row) != "" {
			filled++
		}
	}
	if filled < 3 {
		t.Fatalf("a %d-cell line produced %d rows at width %d:\n%q", len(long), filled, w, rows)
	}
	// The line number is on the first row of the wrapped line only.
	if strings.Count(strings.Join(rows, "\n"), " 1 ") != 1 {
		t.Fatalf("the continuation rows repeated the line number:\n%q", rows)
	}
	// The cursor is still drawn exactly once, on a continuation row.
	if n := countCursors(strings.Join(rows, "\n")); n != 1 {
		t.Fatalf("the wrapped line drew %d cursors, want 1:\n%q", n, rows)
	}
}

// The block is always exactly the height it was asked for, so the main
// view can stack the result under it without measuring.
func TestEditorBlockKeepsItsHeight(t *testing.T) {
	for _, script := range []string{"", "SELECT 1", strings.Repeat("SELECT 1\n", 40)} {
		m := focusedEditor(t, script)
		for _, h := range []int{1, 3, 8} {
			if got := len(blockRows(m.editorBlock(40, h))); got != h {
				t.Errorf("script %q at height %d rendered %d rows", script, h, got)
			}
		}
	}
}

// A buffer taller than the box scrolls just far enough to keep the caret
// on screen, and no further.
func TestEditorScrollsToKeepTheCursorVisible(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 30; i++ {
		b.WriteString("SELECT " + strings.Repeat("x", i%5) + "\n")
	}
	m := focusedEditor(t, b.String())
	m.editor.area.MoveToEnd()
	out := m.editorBlock(40, 5)
	if !strings.Contains(out, m.style.editorCursor.Render(" ")) {
		t.Fatalf("the cursor scrolled out of view:\n%q", out)
	}
	// Back at the top, the window is back at line 1.
	m.editor.area.MoveToBegin()
	out = m.editorBlock(40, 5)
	if !strings.Contains(blockRows(out)[0], " 1 ") {
		t.Fatalf("the window did not scroll back to the first line:\n%q", out)
	}
}

func TestEmptyEditorShowsThePlaceholder(t *testing.T) {
	m := focusedEditor(t, "")
	out := m.editorBlock(40, 3)
	if !strings.Contains(out, "FROM") {
		t.Fatalf("the empty buffer lost its placeholder:\n%q", out)
	}
	if !strings.Contains(out, m.style.editorCursor.Render("S")) {
		t.Fatalf("the placeholder swallowed the cursor:\n%q", out)
	}
}

// A narrow box drops the gutter rather than the SQL.
func TestEditorDropsTheGutterWhenTooNarrow(t *testing.T) {
	m := focusedEditor(t, "SELECT 1")
	if g, c := editorGutterWidth(1, 9); g != 0 || c != 9 {
		t.Fatalf("editorGutterWidth(1, 9) = (%d, %d), want the gutter dropped", g, c)
	}
	rows := blockRows(m.editorBlock(9, 2))
	for _, row := range rows {
		if lipgloss.Width(row) > 9 {
			t.Fatalf("row %q overflows a 9-cell box", row)
		}
	}
}

// The side column previews the buffer with the same colours.
func TestQueryPanelPreviewIsHighlighted(t *testing.T) {
	m := focusedEditor(t, "SELECT 1")
	body := m.queryPanelBody(30, 5)
	if !strings.Contains(body, m.style.sqlKeyword.Render("SELECT")) {
		t.Fatalf("the panel preview is not highlighted:\n%q", body)
	}
}

// The colours come from the theme, so an override reaches the editor.
func TestEditorHighlightingFollowsTheTheme(t *testing.T) {
	resetPalette(t)
	// The model is built first: New() resolves and applies the configured
	// palette itself, so an override has to land after it.
	m := focusedEditor(t, "SELECT 1")
	p, err := resolvePalette(map[string]string{"sql-keyword": "red"})
	if err != nil {
		t.Fatalf("resolvePalette: %v", err)
	}
	applyPalette(p)
	m.style = newStyles()

	out := m.editorBlock(40, 2)
	if want := lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true).Render("SELECT"); !strings.Contains(out, want) {
		t.Fatalf("the keyword did not pick up the overridden colour:\n%q", out)
	}
}

// Every SQL slot is nameable in the config, which is what makes the
// previous test's override possible for a user too.
func TestSQLColorsAreThemeSlots(t *testing.T) {
	names := strings.Join(paletteNames(), " ")
	for _, want := range []string{"sql-keyword", "sql-string", "sql-number", "sql-comment", "sql-placeholder"} {
		if !strings.Contains(names, want) {
			t.Errorf("%s is not a theme slot (have: %s)", want, names)
		}
	}
}

// The renderer must survive anything the tokenizer can produce, on any
// box the layout can hand it.
func TestEditorRendersWithoutPanicking(t *testing.T) {
	scripts := []string{
		"", "\n", "\n\n\n", "SELECT '", "/* /* nested", "ünïcode ölümü",
		"SELECT '日本語のテキスト' FROM t", strings.Repeat("x", 500),
	}
	for _, script := range scripts {
		m := focusedEditor(t, script)
		for _, w := range []int{0, 1, 4, 12, 80} {
			for _, h := range []int{0, 1, 5} {
				m.editorBlock(w, h)
				m.editorRows(w)
			}
		}
	}
}
