package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"lazysql/internal/sqlhl"
)

// filterLine builds a line on a fixed prefix, with the clause already
// typed and the caret at its end — what `/` leaves behind after typing.
func filterLine(d sqlhl.Dialect, clause string) *filterInput {
	return newFilterInput(newStyles(), d, `SELECT * FROM "grid" WHERE `, clause, nil)
}

// The clause is SQL, so it is coloured like SQL: the same token kinds,
// the same theme styles the query editor uses.
func TestFilterInputHighlightsEveryTokenKind(t *testing.T) {
	fi := filterLine(sqlhl.SQLite, `"col" = 'a''b' AND id > 12 AND note = :name`)
	out := fi.view(120)
	s := fi.st

	for _, c := range []struct {
		what  string
		style lipgloss.Style
		text  string
	}{
		{"keyword", s.sqlKeyword, "AND"},
		{"string", s.sqlString, "'a''b'"},
		{"number", s.sqlNumber, "12"},
		{"placeholder", s.sqlPlaceholder, ":name"},
		{"quoted identifier", s.sqlQuoted, `"col"`},
	} {
		if want := c.style.Render(c.text); !strings.Contains(out, want) {
			t.Errorf("%s %q is not styled as such:\n%q", c.what, c.text, out)
		}
	}
	// The statement prefix is chrome, not clause: it stays muted, and
	// nothing in it is coloured as a keyword even though it is full of
	// them.
	if want := s.muted.Render(`SELECT * FROM "grid" WHERE `); !strings.Contains(out, want) {
		t.Errorf("the prefix lost its muted styling:\n%q", out)
	}
	if strings.Contains(out, s.sqlKeyword.Render("SELECT")) {
		t.Errorf("the prefix was highlighted as SQL:\n%q", out)
	}
}

// The colours follow the engine, not a guess: `"x"` is a quoted
// identifier in SQLite and a string literal in MySQL, and the line reads
// it the way the connection behind it would.
func TestFilterInputHighlightsInTheConnectionDialect(t *testing.T) {
	lite := filterLine(sqlhl.SQLite, `"x" = 1`)
	my := filterLine(sqlhl.MySQL, `"x" = 1`)

	if want := lite.st.sqlQuoted.Render(`"x"`); !strings.Contains(lite.view(120), want) {
		t.Errorf("sqlite: %q is not a quoted identifier:\n%q", `"x"`, lite.view(120))
	}
	if want := my.st.sqlString.Render(`"x"`); !strings.Contains(my.view(120), want) {
		t.Errorf("mysql: %q is not a string literal:\n%q", `"x"`, my.view(120))
	}
}

// Highlighting is recomputed from the value, so it follows every way the
// value can change: typing, and recalling an entry from the relation's
// history.
func TestFilterInputHighlightUpdatesWithTheValue(t *testing.T) {
	fi := newFilterInput(newStyles(), sqlhl.SQLite, `SELECT * FROM "grid" WHERE `, "", []string{"id > 100"})
	if got := fi.view(120); strings.Contains(got, fi.st.sqlNumber.Render("100")) {
		t.Fatalf("an empty clause rendered a number:\n%q", got)
	}

	fi.input, _ = fi.input.Update(press('i'))
	fi.input, _ = fi.input.Update(press('n'))
	if want := fi.st.sqlKeyword.Render("in"); !strings.Contains(fi.view(120), want) {
		t.Errorf("a keyword typed key by key is not highlighted:\n%q", fi.view(120))
	}

	fi.recall(1)
	if got, want := fi.value(), "id > 100"; got != want {
		t.Fatalf("recall gave %q, want %q", got, want)
	}
	if want := fi.st.sqlNumber.Render("100"); !strings.Contains(fi.view(120), want) {
		t.Errorf("a recalled clause is not highlighted:\n%q", fi.view(120))
	}
}

// The caret is drawn on the cell it sits on, wherever that is — over a
// token, over the space past the end of the clause — and it is drawn
// exactly once.
func TestFilterInputDrawsTheCaret(t *testing.T) {
	fi := filterLine(sqlhl.SQLite, "id > 12")
	if got := countCursors(fi.view(120)); got != 1 {
		t.Fatalf("caret cells at the end of the clause = %d, want 1", got)
	}

	fi.input.SetCursor(0)
	out := fi.view(120)
	if got := countCursors(out); got != 1 {
		t.Fatalf("caret cells on the first rune = %d, want 1", got)
	}
	if want := fi.st.editorCursor.Render("i"); !strings.Contains(out, want) {
		t.Errorf("the caret is not on the rune the cursor is at:\n%q", out)
	}
	// The rest of the clause keeps its colours behind the caret.
	if want := fi.st.sqlNumber.Render("12"); !strings.Contains(out, want) {
		t.Errorf("the caret cost the clause its highlighting:\n%q", out)
	}
}

// The line renders exactly the width it is given, at every width, and
// keeps both the caret and the clause around it on screen — a clause
// longer than the box scrolls under it rather than running past the
// grid's border.
func TestFilterInputScrollsToTheCaret(t *testing.T) {
	clause := "id > 100 AND name = 'a rather long value that will not fit' AND note IS NULL"
	for _, w := range []int{1, 2, 3, 4, 8, 12, 20, 40, 79, 200} {
		fi := filterLine(sqlhl.SQLite, clause)
		for _, pos := range []int{len(clause), 0, 7, len(clause) / 2, len(clause)} {
			fi.input.SetCursor(pos)
			out := fi.view(w)
			if got := lipgloss.Width(out); got != w {
				t.Fatalf("w=%d pos=%d: rendered %d cells\n%q", w, pos, got, out)
			}
			if got := countCursors(out); got != 1 {
				t.Fatalf("w=%d pos=%d: %d caret cells\n%q", w, pos, got, out)
			}
		}
	}
}

// A box too narrow for the full statement falls back to `WHERE `, and
// one too narrow for that drops the label rather than the clause.
func TestFilterInputPrefixFallbackKeepsHighlighting(t *testing.T) {
	fi := filterLine(sqlhl.SQLite, "id > 12")
	out := fi.view(30)
	if strings.Contains(out, "SELECT") {
		t.Errorf("a 30-cell box kept the full statement:\n%q", out)
	}
	if want := fi.st.muted.Render(shortFilterPrefix); !strings.Contains(out, want) {
		t.Errorf("the short prefix is missing or not muted:\n%q", out)
	}
	if want := fi.st.sqlNumber.Render("12"); !strings.Contains(out, want) {
		t.Errorf("the fallback cost the clause its highlighting:\n%q", out)
	}
	if out := fi.view(8); strings.Contains(out, "WHERE") {
		t.Errorf("an 8-cell box kept the label instead of the clause:\n%q", out)
	}
}

// Multi-cell and multi-rune text is measured in cells: a clause of CJK
// still renders the width it was given, and a combining accent stays on
// the letter carrying it.
func TestFilterInputHandlesWideAndCombiningRunes(t *testing.T) {
	for _, clause := range []string{"name = '広い値の列'", "name = 'äbc'"} {
		fi := filterLine(sqlhl.SQLite, clause)
		for _, w := range []int{6, 11, 20, 60} {
			for pos := 0; pos <= len([]rune(clause)); pos++ {
				fi.input.SetCursor(pos)
				out := fi.view(w)
				if got := lipgloss.Width(out); got != w {
					t.Fatalf("%q w=%d pos=%d: rendered %d cells\n%q", clause, w, pos, got, out)
				}
			}
		}
	}
}

// The grid's cursor stays where it is while the line is open, but stops
// shouting: the idle tint is weaker than the focused one, and the row
// tint under it goes away entirely.
func TestGridCursorDimsWhileTheFilterLineIsOpen(t *testing.T) {
	m := Model{style: newStyles()}
	loudCell := m.cellStyle(false, true, false, true, false, false, rowPlain).Render("x")
	idleCell := m.cellStyle(true, true, false, true, false, false, rowPlain).Render("x")
	if loudCell == idleCell {
		t.Error("the cursor cell looks the same whether the grid has the keyboard or not")
	}
	if want := m.style.cellCursorIdle.Render("x"); idleCell != want {
		t.Errorf("idle cursor cell = %q, want the idle tint %q", idleCell, want)
	}
	if got := m.cellStyle(true, true, false, false, false, false, rowPlain).Render("x"); got != "x" {
		t.Errorf("idle row tint = %q, want the row left untinted", got)
	}
}

// The open line says so: it wears the focus bar in the panel-focus
// green, and the grid behind it drops to the idle cursor tint so the
// loud highlight is on what the keys actually reach. Closing the line
// puts both back.
func TestFilterInputShowsWhereTheKeyboardIs(t *testing.T) {
	m := dataBrowsing(t)
	s := m.style
	bar := s.filterFocus.Render(filterMarker)

	before := m.View().Content
	if strings.Contains(before, bar) {
		t.Fatal("the focus bar is drawn with no filter line open")
	}
	if m.dataCursor().idle {
		t.Fatal("the grid starts idle with no filter line open")
	}

	m = send(t, m, press('/'))
	open := m.View().Content
	if !strings.Contains(open, bar) {
		t.Error("the open filter line does not show the focus bar")
	}
	if !strings.Contains(open, "\x1b[7m") {
		t.Error("the open filter line draws no caret")
	}
	if m.filterInput.dialect != m.sqlDialect() {
		t.Errorf("line dialect = %q, want the connection's %q", m.filterInput.dialect, m.sqlDialect())
	}
	if !m.dataCursor().idle {
		t.Error("the grid still claims the keyboard while the line is open")
	}

	m = send(t, m, special(tea.KeyEscape, 0))
	after := m.View().Content
	if strings.Contains(after, bar) {
		t.Error("the focus bar outlived the filter line")
	}
	if m.dataCursor().idle {
		t.Error("the grid did not take the keyboard back after esc")
	}
	if after == open {
		t.Error("closing the line changed nothing on screen")
	}
}
