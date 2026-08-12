package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
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

// ---------- the caret's rune-to-cell mapping ----------
//
// The textarea counts its cursor in runes, the terminal lays the row out
// in cells, and issue #132 was what happens when the two are mixed up.
// The tests below pin the mapping itself and then read the caret back out
// of a rendered block to check it end to end.

func TestLineCellsMapsRunesToDisplayColumns(t *testing.T) {
	cases := []struct {
		what string
		line string
		want []int
	}{
		{"ascii", "ab", []int{0, 1, 2}},
		// A CJK ideograph is two columns wide.
		{"wide", "a日b", []int{0, 1, 3, 4}},
		// The decomposed spelling of `ä` is two runes over one column, and
		// the combining accent maps onto the letter carrying it.
		{"combining", "äb", []int{0, 0, 1, 2}},
		{"empty", "", []int{0}},
	}
	for _, c := range cases {
		runes := []rune(c.line)
		got := lineCells(runes)
		if len(got) != len(c.want) {
			t.Errorf("%s: lineCells(%q) has %d entries, want %d", c.what, c.line, len(got), len(c.want))
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: lineCells(%q) = %v, want %v", c.what, c.line, got, c.want)
				break
			}
		}
		// The last entry is the line's own width, which is what decides
		// whether a trailing caret still fits on the row.
		if w := lipgloss.Width(c.line); got[len(got)-1] != w {
			t.Errorf("%s: lineCells(%q) ends at %d, but the line is %d cells wide",
				c.what, c.line, got[len(got)-1], w)
		}
	}
}

// A caret at the end of a row that is full in *cells* needs a row of its
// own. Counting runes instead said a row of CJK still had room, and the
// trailing cell landed one column past the editor's right edge — where
// the layout clipped it, taking the caret with it.
func TestCursorSegmentMeasuresAFullRowInCells(t *testing.T) {
	const width = 8
	runes := []rune(strings.Repeat("日", 4)) // 4 runes, 8 cells: exactly full
	cells := lineCells(runes)
	segs := wrapSegments(runes, width)
	if len(segs) != 1 || segs[0] != [2]int{0, 4} {
		t.Fatalf("wrapSegments = %v, want one full row", segs)
	}
	row, _, extra := cursorSegment(cells, segs, len(runes), width)
	if !extra || row != 1 {
		t.Fatalf("a caret past a full CJK row landed on row %d (extra=%v), want a row of its own",
			row, extra)
	}
	// The same line one ideograph shorter has room for the caret.
	runes = runes[:3]
	cells, segs = lineCells(runes), wrapSegments(runes, width)
	row, off, extra := cursorSegment(cells, segs, len(runes), width)
	if extra || row != 0 || off != 3 {
		t.Fatalf("cursorSegment on a row with room = (%d, %d, %v), want (0, 3, false)", row, off, extra)
	}
}

// caretText is the text of the caret cell in a rendered block, its row,
// and its display column — read the way the terminal reads them, so the
// assertion is about what is on screen rather than about the code that
// put it there.
func caretText(block string) (text string, row, col int, ok bool) {
	for i, line := range blockRows(block) {
		j := strings.Index(line, "\x1b[7m")
		if j < 0 {
			continue
		}
		cell := line[j+len("\x1b[7m"):]
		if e := strings.Index(cell, "\x1b"); e >= 0 {
			cell = cell[:e]
		}
		return cell, i, ansi.StringWidth(line[:j]), true
	}
	return "", 0, 0, false
}

// The one invariant the whole area exists for: whatever column the
// textarea reports, the cell drawn as the caret is the character under
// it, at the column that character occupies, inside the box.
func TestEditorCaretSitsOnTheCharacterUnderTheCursor(t *testing.T) {
	scripts := []string{
		"SELECT " + strings.Repeat("ab", 20),
		"SELECT '" + strings.Repeat("日", 20) + "' FROM t",
		"SELECT " + strings.Repeat("日a", 15) + " FROM t",
		// The decomposed umlauts a macOS paste hands over.
		"SELECT 'Käse' FROM straße",
		"SELECT " + strings.Repeat("ä", 25) + " FROM t",
		"SELECT a\nFROM 日本語\nWHERE x = 'ö'",
	}
	for _, script := range scripts {
		m := focusedEditor(t, script)
		lines := m.editorLines()
		for _, w := range []int{12, 17, 23, 40, 60} {
			gutterW, contentW := editorGutterWidth(len(lines), w)
			for row := range lines {
				cells := lineCells(lines[row].runes)
				for col := 0; col <= len(lines[row].runes); col++ {
					m.editor.area.MoveToBegin()
					for i := 0; i < row; i++ {
						m.editor.area.CursorDown()
					}
					m.editor.area.SetCursorColumn(col)
					// Every render is asked of a fresh cache: the memo is
					// keyed on the caret, and reusing one would test the
					// key rather than the rendering.
					m.hl = &editorCache{}
					block := m.editorBlock(w, 40)

					got, _, x, ok := caretText(block)
					if !ok {
						t.Fatalf("%q at w=%d (%d,%d): no caret was drawn", script, w, row, col)
					}
					// The caret's text is the grapheme cluster the cursor
					// points at — or a blank cell past the last one.
					want := " "
					if start := clusterStart(cells, col); start < len(lines[row].runes) {
						want = string(lines[row].runes[start : start+clusterRunes(lines[row].runes[start:])])
					}
					if got != want {
						t.Fatalf("%q at w=%d (%d,%d): the caret reads %q, but the cursor is on %q",
							script, w, row, col, got, want)
					}
					// It sits inside the text area, never on the gutter and
					// never past the box.
					if x < gutterW || x >= gutterW+contentW {
						t.Fatalf("%q at w=%d (%d,%d): the caret is at column %d, outside [%d,%d)",
							script, w, row, col, x, gutterW, gutterW+contentW)
					}
					for _, l := range blockRows(block) {
						if lw := lipgloss.Width(l); lw > w {
							t.Fatalf("%q at w=%d (%d,%d): a row is %d cells wide:\n%q",
								script, w, row, col, lw, l)
						}
					}
				}
			}
		}
	}
}

// Syntax highlighting must not move the caret: the escape sequences a
// keyword or a string costs are bytes, not columns.
func TestEditorCaretIgnoresHighlightingEscapes(t *testing.T) {
	m := focusedEditor(t, "SELECT 'lit' FROM t -- note")
	runes := []rune(m.script())
	for col := range runes {
		m.editor.area.SetCursorColumn(col)
		m.hl = &editorCache{}
		got, _, x, ok := caretText(m.editorBlock(60, 4))
		if !ok {
			t.Fatalf("column %d: no caret", col)
		}
		gutterW, _ := editorGutterWidth(1, 60)
		if want := gutterW + col; x != want {
			t.Errorf("column %d: caret at %d, want %d (the highlighting shifted it)", col, x, want)
		}
		if want := string(runes[col]); got != want {
			t.Errorf("column %d: caret reads %q, want %q", col, got, want)
		}
	}
}

// A tab typed into the editor reaches the buffer as spaces — the
// textarea sanitizes it on the way in — so the caret keeps landing on the
// character under it either way.
func TestEditorCaretAfterATab(t *testing.T) {
	m := focusedEditor(t, "")
	m.editor.area.InsertString("SELECT\tid")
	if strings.Contains(m.script(), "\t") {
		t.Fatalf("the textarea kept a raw tab in the buffer: %q", m.script())
	}
	runes := []rune(m.script())
	m.editor.area.SetCursorColumn(len(runes) - 1)
	got, _, x, ok := caretText(m.editorBlock(60, 4))
	if !ok {
		t.Fatal("no caret after a tab")
	}
	gutterW, _ := editorGutterWidth(1, 60)
	if want := gutterW + len(runes) - 1; x != want {
		t.Errorf("caret at %d, want %d", x, want)
	}
	if got != "d" {
		t.Errorf("caret reads %q, want %q", got, "d")
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
