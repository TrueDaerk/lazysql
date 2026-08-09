package ui

import (
	"strings"
	"unicode"
)

// The query editor's normal mode speaks a small vim dialect: motions,
// insert entry points, and whole-line delete/yank/paste. The layer lives
// here as a pure buffer type — lines and a cursor, no textarea, no tea —
// so every motion and edit is testable as a plain function. query.go
// reads the textarea into a vimBuffer, applies one command, and writes
// the result back.
//
// Scope is the issue's minimum set: no visual mode, no registers beyond
// the one implicit register, no counts, no `:` commands. Undo is omitted
// because the v2 textarea keeps no edit history to call into — see the
// wiki concept design/vim-mode-query-editor.

// vimRegister is the single yank/delete register. line records whether
// the text is a whole line, which decides how `p` pastes it.
type vimRegister struct {
	text string
	line bool
}

// vimBuffer is the editor's text as normal mode sees it: lines, a cursor
// clamped to the last character of its line (vim style, one left of
// where insert mode may sit), and the column the cursor wants when a
// vertical motion passes through a shorter line.
type vimBuffer struct {
	lines []string
	row   int
	col   int
	// want survives j/k through short and empty lines: moving down a
	// column-40 cursor over an empty line and back out lands on column
	// 40 again, not 0.
	want int
}

func newVimBuffer(text string, row, col int) vimBuffer {
	b := vimBuffer{lines: strings.Split(text, "\n"), row: row, col: col}
	b.clampRow()
	b.clampCol()
	b.want = b.col
	return b
}

func (b vimBuffer) text() string { return strings.Join(b.lines, "\n") }

func (b vimBuffer) line() []rune { return []rune(b.lines[b.row]) }

// maxCol is the rightmost normal-mode column of the current line: the
// last character, or 0 on an empty line.
func (b vimBuffer) maxCol() int {
	if n := len(b.line()); n > 0 {
		return n - 1
	}
	return 0
}

func (b *vimBuffer) clampRow() {
	if b.row >= len(b.lines) {
		b.row = len(b.lines) - 1
	}
	if b.row < 0 {
		b.row = 0
	}
}

func (b *vimBuffer) clampCol() {
	if b.col > b.maxCol() {
		b.col = b.maxCol()
	}
	if b.col < 0 {
		b.col = 0
	}
}

// setCol is every horizontal motion's exit: the landing column is also
// the new desired column.
func (b *vimBuffer) setCol(col int) {
	b.col = col
	b.clampCol()
	b.want = b.col
}

// ---------- motions ----------

func (b *vimBuffer) left()  { b.setCol(b.col - 1) }
func (b *vimBuffer) right() { b.setCol(b.col + 1) }

// down and up restore the wanted column instead of keeping the clamp a
// short line forced.
func (b *vimBuffer) down() {
	if b.row < len(b.lines)-1 {
		b.row++
	}
	b.col = b.want
	b.clampCol()
}

func (b *vimBuffer) up() {
	if b.row > 0 {
		b.row--
	}
	b.col = b.want
	b.clampCol()
}

func (b *vimBuffer) lineStart() { b.setCol(0) }
func (b *vimBuffer) lineEnd()   { b.setCol(b.maxCol()) }

func (b *vimBuffer) top() {
	b.row = 0
	b.setCol(0)
}

func (b *vimBuffer) bottom() {
	b.row = len(b.lines) - 1
	b.setCol(0)
}

// charClass groups runes the way vim's `w`/`b` do: whitespace, word
// characters (letters, digits, underscore), and everything else. A word
// boundary is any change of class that is not into whitespace.
func charClass(r rune) int {
	switch {
	case unicode.IsSpace(r):
		return 0
	case r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r):
		return 1
	default:
		return 2
	}
}

// wordForward is `w`: the start of the next word, crossing line ends.
// An empty line counts as a word, as it does in vim.
func (b *vimBuffer) wordForward() {
	row, col := b.row, b.col
	line := b.line()
	cls := 0
	if col < len(line) {
		cls = charClass(line[col])
	}
	// Step off the current word first, then skip whitespace to the next.
	inWord := cls != 0
	for {
		col++
		if col >= len(line) {
			if row >= len(b.lines)-1 {
				b.row, b.col = row, col
				b.clampCol()
				b.want = b.col
				return
			}
			row++
			col = 0
			line = []rune(b.lines[row])
			if len(line) == 0 {
				// An empty line is a landing spot of its own.
				b.row = row
				b.setCol(0)
				return
			}
			inWord = false
		}
		c := charClass(line[col])
		if c == 0 {
			inWord = false
			continue
		}
		if !inWord || c != cls {
			b.row = row
			b.setCol(col)
			return
		}
	}
}

// wordBack is `b`: the start of the current word, or of the previous one
// when already at a start.
func (b *vimBuffer) wordBack() {
	row, col := b.row, b.col
	line := b.line()
	for {
		col--
		if col < 0 {
			if row == 0 {
				b.row = 0
				b.setCol(0)
				return
			}
			row--
			line = []rune(b.lines[row])
			col = len(line) - 1
			if col < 0 {
				b.row = row
				b.setCol(0)
				return
			}
		}
		if charClass(line[col]) == 0 {
			continue
		}
		// col is on a word; walk to that word's first character.
		cls := charClass(line[col])
		for col > 0 && charClass(line[col-1]) == cls {
			col--
		}
		b.row = row
		b.setCol(col)
		return
	}
}

// ---------- insert entry points ----------

// appendCol is `a`: the insert column one past the cursor. It may equal
// the line length, which is legal in insert mode but not here — the
// caller hands it straight to the textarea.
func (b vimBuffer) appendCol() int {
	if len(b.line()) == 0 {
		return 0
	}
	return b.col + 1
}

// openBelow is `o`: a new empty line under the cursor, ready to type on.
func (b *vimBuffer) openBelow() {
	b.lines = append(b.lines[:b.row+1], append([]string{""}, b.lines[b.row+1:]...)...)
	b.row++
	b.setCol(0)
}

// openAbove is `O`: a new empty line over the cursor.
func (b *vimBuffer) openAbove() {
	b.lines = append(b.lines[:b.row], append([]string{""}, b.lines[b.row:]...)...)
	b.setCol(0)
}

// ---------- edits ----------

// deleteChar is `x`: remove the character under the cursor into the
// register. On an empty line it is a no-op, like vim's.
func (b *vimBuffer) deleteChar() (vimRegister, bool) {
	line := b.line()
	if len(line) == 0 {
		return vimRegister{}, false
	}
	reg := vimRegister{text: string(line[b.col])}
	b.lines[b.row] = string(line[:b.col]) + string(line[b.col+1:])
	b.clampCol()
	b.want = b.col
	return reg, true
}

// deleteLine is `dd`: remove the whole line into the register. Deleting
// the only line leaves one empty line — a buffer always has at least one.
func (b *vimBuffer) deleteLine() vimRegister {
	reg := vimRegister{text: b.lines[b.row], line: true}
	if len(b.lines) == 1 {
		b.lines[0] = ""
	} else {
		b.lines = append(b.lines[:b.row], b.lines[b.row+1:]...)
	}
	b.clampRow()
	b.setCol(0)
	return reg
}

// yankLine is `yy`.
func (b vimBuffer) yankLine() vimRegister {
	return vimRegister{text: b.lines[b.row], line: true}
}

// paste is `p`: a line register pastes as a new line below the cursor, a
// character register after the cursor, both leaving the cursor on the
// pasted text.
func (b *vimBuffer) paste(reg vimRegister) bool {
	if reg.text == "" && !reg.line {
		return false
	}
	if reg.line {
		b.lines = append(b.lines[:b.row+1], append([]string{reg.text}, b.lines[b.row+1:]...)...)
		b.row++
		b.setCol(0)
		return true
	}
	line := b.line()
	at := b.col
	if len(line) > 0 {
		at++
	}
	b.lines[b.row] = string(line[:at]) + reg.text + string(line[at:])
	b.setCol(at + len([]rune(reg.text)) - 1)
	return true
}
