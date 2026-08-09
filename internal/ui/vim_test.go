package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// ---------- the pure engine ----------

// buf builds a vimBuffer over the given lines with the cursor at (row, col).
func buf(row, col int, lines ...string) vimBuffer {
	return newVimBuffer(strings.Join(lines, "\n"), row, col)
}

func wantCursor(t *testing.T, b vimBuffer, row, col int) {
	t.Helper()
	if b.row != row || b.col != col {
		t.Fatalf("cursor = (%d,%d), want (%d,%d)", b.row, b.col, row, col)
	}
}

func TestVimHorizontalMotionClampsAtLineEnds(t *testing.T) {
	b := buf(0, 0, "ab")
	b.left()
	wantCursor(t, b, 0, 0)
	b.right()
	wantCursor(t, b, 0, 1)
	// Normal mode sits on the last character, never past it.
	b.right()
	wantCursor(t, b, 0, 1)
}

func TestVimVerticalMotionRestoresTheWantedColumn(t *testing.T) {
	b := buf(0, 4, "SELECT id", "", "FROM long_table_name")
	b.down()
	wantCursor(t, b, 1, 0) // the empty line has only column 0
	b.down()
	wantCursor(t, b, 2, 4) // the wanted column survives the empty line
	b.up()
	b.up()
	wantCursor(t, b, 0, 4)
	// A horizontal motion resets what the cursor wants.
	b.left()
	b.down()
	b.down()
	wantCursor(t, b, 2, 3)
}

func TestVimVerticalMotionStopsAtBufferEdges(t *testing.T) {
	b := buf(0, 0, "one", "two")
	b.up()
	wantCursor(t, b, 0, 0)
	b.down()
	b.down()
	wantCursor(t, b, 1, 0)
}

func TestVimLineAndBufferMotions(t *testing.T) {
	b := buf(1, 2, "first", "second line", "third")
	b.lineEnd()
	wantCursor(t, b, 1, 10)
	b.lineStart()
	wantCursor(t, b, 1, 0)
	b.bottom()
	wantCursor(t, b, 2, 0)
	b.top()
	wantCursor(t, b, 0, 0)
}

func TestVimLineEndOnEmptyLineStaysAtZero(t *testing.T) {
	b := buf(1, 0, "a", "", "b")
	b.lineEnd()
	wantCursor(t, b, 1, 0)
}

func TestVimWordForward(t *testing.T) {
	b := buf(0, 0, "SELECT id, name FROM t")
	b.wordForward()
	wantCursor(t, b, 0, 7) // id
	b.wordForward()
	wantCursor(t, b, 0, 9) // the comma is its own word
	b.wordForward()
	wantCursor(t, b, 0, 11) // name
	b.wordForward()
	wantCursor(t, b, 0, 16) // FROM
}

func TestVimWordForwardCrossesLinesAndStopsOnEmptyOnes(t *testing.T) {
	b := buf(0, 4, "SELECT", "", "1")
	b.wordForward()
	wantCursor(t, b, 1, 0) // an empty line is a landing spot
	b.wordForward()
	wantCursor(t, b, 2, 0)
	// At the buffer's last word `w` has nowhere left to go.
	b.wordForward()
	wantCursor(t, b, 2, 0)
}

func TestVimWordBack(t *testing.T) {
	b := buf(0, 16, "SELECT id, name FROM t")
	b.wordBack()
	wantCursor(t, b, 0, 11) // name
	b.wordBack()
	wantCursor(t, b, 0, 9) // the comma
	b.wordBack()
	wantCursor(t, b, 0, 7) // id
	b.wordBack()
	wantCursor(t, b, 0, 0) // SELECT
	b.wordBack()
	wantCursor(t, b, 0, 0)
}

func TestVimWordBackCrossesLines(t *testing.T) {
	b := buf(2, 0, "SELECT one", "", "two")
	b.wordBack()
	wantCursor(t, b, 1, 0) // the empty line is a word, as in vim
	b.wordBack()
	wantCursor(t, b, 0, 7) // then `one`
}

func TestVimDeleteChar(t *testing.T) {
	b := buf(0, 1, "abc")
	reg, ok := b.deleteChar()
	if !ok || reg.text != "b" || reg.line {
		t.Fatalf("x yielded %+v ok=%v, want the character b", reg, ok)
	}
	if b.text() != "ac" {
		t.Fatalf("buffer = %q, want ac", b.text())
	}
	wantCursor(t, b, 0, 1)
	// Deleting the last character pulls the cursor back onto the line.
	if _, ok := b.deleteChar(); !ok {
		t.Fatal("second x failed")
	}
	if b.text() != "a" {
		t.Fatalf("buffer = %q, want a", b.text())
	}
	wantCursor(t, b, 0, 0)
}

func TestVimDeleteCharOnEmptyLineIsANoOp(t *testing.T) {
	b := buf(1, 0, "a", "", "b")
	if _, ok := b.deleteChar(); ok {
		t.Fatal("x on an empty line claimed to delete")
	}
	if b.text() != "a\n\nb" {
		t.Fatalf("buffer changed to %q", b.text())
	}
}

func TestVimDeleteLine(t *testing.T) {
	b := buf(1, 3, "one", "two", "three")
	reg := b.deleteLine()
	if reg.text != "two" || !reg.line {
		t.Fatalf("dd yielded %+v, want the line two", reg)
	}
	if b.text() != "one\nthree" {
		t.Fatalf("buffer = %q", b.text())
	}
	wantCursor(t, b, 1, 0)
}

func TestVimDeleteLastLineMovesUp(t *testing.T) {
	b := buf(1, 0, "one", "two")
	b.deleteLine()
	if b.text() != "one" {
		t.Fatalf("buffer = %q", b.text())
	}
	wantCursor(t, b, 0, 0)
}

func TestVimDeleteOnlyLineLeavesAnEmptyBuffer(t *testing.T) {
	b := buf(0, 2, "only")
	b.deleteLine()
	if b.text() != "" {
		t.Fatalf("buffer = %q, want empty", b.text())
	}
	wantCursor(t, b, 0, 0)
}

func TestVimYankAndPasteLine(t *testing.T) {
	b := buf(0, 2, "one", "two")
	reg := b.yankLine()
	if reg.text != "one" || !reg.line {
		t.Fatalf("yy yielded %+v", reg)
	}
	if !b.paste(reg) {
		t.Fatal("p refused a full register")
	}
	if b.text() != "one\none\ntwo" {
		t.Fatalf("buffer = %q", b.text())
	}
	wantCursor(t, b, 1, 0) // on the pasted line
}

func TestVimPasteCharwise(t *testing.T) {
	b := buf(0, 0, "ac")
	reg, _ := b.deleteChar() // register holds "a", cursor on "c"
	if b.paste(reg); b.text() != "ca" {
		t.Fatalf("buffer = %q, want ca", b.text())
	}
	wantCursor(t, b, 0, 1)
}

func TestVimPasteEmptyRegisterIsANoOp(t *testing.T) {
	b := buf(0, 0, "a")
	if b.paste(vimRegister{}) {
		t.Fatal("p acted on an empty register")
	}
}

func TestVimOpenBelowAndAbove(t *testing.T) {
	b := buf(0, 2, "one", "two")
	b.openBelow()
	if b.text() != "one\n\ntwo" {
		t.Fatalf("o produced %q", b.text())
	}
	wantCursor(t, b, 1, 0)

	b = buf(1, 1, "one", "two")
	b.openAbove()
	if b.text() != "one\n\ntwo" {
		t.Fatalf("O produced %q", b.text())
	}
	wantCursor(t, b, 1, 0)
}

func TestVimAppendColumn(t *testing.T) {
	b := buf(0, 2, "abc")
	if got := b.appendCol(); got != 3 {
		t.Fatalf("a at line end = column %d, want 3", got)
	}
	b = buf(1, 0, "x", "")
	if got := b.appendCol(); got != 0 {
		t.Fatalf("a on an empty line = column %d, want 0", got)
	}
}

// ---------- the mode layer over the panel ----------

// editorAt focuses panel [4] in normal mode with the given script and the
// cursor driven to the top.
func editorAt(t *testing.T, script string) Model {
	t.Helper()
	m := sized(120, 40)
	m.setScript(script)
	m = send(t, m, press('4'))
	if m.focus != panelQuery {
		t.Fatalf("focus = %v, want the query panel", m.focus)
	}
	if m.editor.editing {
		t.Fatal("panel [4] gained focus in insert mode, want normal")
	}
	return send(t, m, press('g'), press('g'))
}

func editorCursor(m Model) (int, int) {
	return m.editor.area.Line(), m.editor.area.Column()
}

func TestNormalModeTypingNeverInsertsText(t *testing.T) {
	m := editorAt(t, "SELECT 1")
	for _, r := range "wbhjklxonce" { // motions, edits, and unbound letters
		if r == 'x' || r == 'o' {
			continue // these are meant to change the buffer
		}
		m = send(t, m, press(r))
	}
	m = send(t, m, press('g'), press('j')) // a cancelled chord acts as itself
	if m.script() != "SELECT 1" {
		t.Fatalf("normal-mode keys changed the buffer to %q", m.script())
	}
	if m.editor.editing {
		t.Fatal("normal-mode keys entered insert mode")
	}
}

func TestInsertRoundTripAndBackingOut(t *testing.T) {
	m := editorAt(t, "")
	m = send(t, m, press('i'))
	if !m.editor.editing {
		t.Fatal("i did not enter insert mode")
	}
	m = send(t, m, press('S'), press('E'), press('L'))
	// Typing SEL opens the completion popup, whose own esc closes it
	// first; the next esc is the one that ends insert mode.
	if m = send(t, m, special(tea.KeyEscape, 0)); m.completion.open {
		t.Fatal("esc did not close the completion popup")
	}
	if m.editor.editing {
		m = send(t, m, special(tea.KeyEscape, 0))
	}
	if m.editor.editing {
		t.Fatal("esc did not leave insert mode")
	}
	if m.script() != "SEL" {
		t.Fatalf("buffer = %q, want SEL", m.script())
	}
	m = send(t, m, special(tea.KeyEscape, 0))
	if m.focus == panelQuery {
		t.Fatal("esc in normal mode did not back out of the panel")
	}
}

func TestNormalModeMotionsMoveTheTextareaCursor(t *testing.T) {
	m := editorAt(t, "SELECT id\nFROM t")
	m = send(t, m, press('j'))
	if row, _ := editorCursor(m); row != 1 {
		t.Fatalf("j left the cursor on row %d", row)
	}
	m = send(t, m, press('$'))
	if _, col := editorCursor(m); col != 5 {
		t.Fatalf("$ left the cursor on column %d, want 5", col)
	}
	m = send(t, m, press('0'))
	if _, col := editorCursor(m); col != 0 {
		t.Fatalf("0 left the cursor on column %d", col)
	}
	m = send(t, m, press('w'))
	if _, col := editorCursor(m); col != 5 {
		t.Fatalf("w left the cursor on column %d, want 5", col)
	}
	m = send(t, m, press('G'))
	if row, _ := editorCursor(m); row != 1 {
		t.Fatalf("G left the cursor on row %d", row)
	}
	m = send(t, m, press('g'), press('g'))
	if row, _ := editorCursor(m); row != 0 {
		t.Fatalf("gg left the cursor on row %d", row)
	}
}

func TestDeleteYankPasteLineFlow(t *testing.T) {
	m := editorAt(t, "one\ntwo\nthree")
	m = send(t, m, press('j'), press('d'), press('d'))
	if m.script() != "one\nthree" {
		t.Fatalf("dd left %q", m.script())
	}
	// dd leaves the cursor on the line that moved up, so p pastes the
	// register below it — vim's behavior exactly.
	m = send(t, m, press('p'))
	if m.script() != "one\nthree\ntwo" {
		t.Fatalf("p after dd left %q", m.script())
	}
	m = send(t, m, press('g'), press('g'), press('y'), press('y'), press('p'))
	if m.script() != "one\none\nthree\ntwo" {
		t.Fatalf("yy+p left %q", m.script())
	}
}

func TestDeleteCharUnderCursor(t *testing.T) {
	m := editorAt(t, "abc")
	m = send(t, m, press('l'), press('x'))
	if m.script() != "ac" {
		t.Fatalf("x left %q, want ac", m.script())
	}
}

func TestPendingChordDoesNotSurviveLeavingThePanel(t *testing.T) {
	m := editorAt(t, "one\ntwo")
	m = send(t, m, press('d'), press('1'), press('4'), press('d'))
	if m.script() != "one\ntwo" {
		t.Fatalf("a chord split across a panel detour deleted: %q", m.script())
	}
}

func TestAppendAndOpenEnterInsertWithPlacement(t *testing.T) {
	m := editorAt(t, "ab")
	m = send(t, m, press('a'))
	if !m.editor.editing {
		t.Fatal("a did not enter insert mode")
	}
	m = send(t, m, press('X'))
	if m.script() != "aXb" {
		t.Fatalf("a placed the caret wrong: %q", m.script())
	}

	m = editorAt(t, "one\ntwo")
	m = send(t, m, press('o'), press('X'))
	if m.script() != "one\nX\ntwo" {
		t.Fatalf("o left %q", m.script())
	}
	m = send(t, m, special(tea.KeyEscape, 0), press('O'), press('Y'))
	if m.script() != "one\nY\nX\ntwo" {
		t.Fatalf("O left %q", m.script())
	}
}

func TestNormalModeHelpListsTheVimKeys(t *testing.T) {
	k := newKeyMap()
	var flat []string
	for _, group := range k.helpGroups(panelQuery) {
		for _, b := range group {
			flat = append(flat, b.Help().Key)
		}
	}
	joined := strings.Join(flat, " ")
	for _, want := range []string{"h/←", "w", "b", "0", "$", "gg", "G", "a", "o", "O", "x", "dd", "yy", "p"} {
		found := false
		for _, got := range flat {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("`?` for panel [4] is missing %q (has: %s)", want, joined)
		}
	}
}
