package ui

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	"lazysql/internal/sqlhl"
)

// SQL in the query editor is highlighted by a renderer of lazysql's own,
// not by the textarea. Bubbles' textarea takes a plain string and styles
// it whole, so there is no hook that could colour part of a line; the
// only two ways in are to post-process its ANSI output or to keep the
// textarea purely as the input model and draw the buffer here. This is
// the second. The textarea still owns the text, the cursor and every
// editing key — Value(), Line() and Column() are read back from it — and
// this file owns the gutter, the wrapping, the colours and the cursor
// cell.
//
// The consequence to keep in mind: the textarea must not soft-wrap
// behind our backs, or its idea of "the row above" would disagree with
// what is on screen. newQueryEditor sets its width wide enough that it
// never does, so its cursor moves one *logical* line at a time and this
// renderer is free to wrap however the panel needs.

// editorWrapWidth is the width the textarea is kept at. It is the widest
// the component allows (its own MaxWidth default), which makes every
// realistic SQL line a single unwrapped row inside the model.
const editorWrapWidth = 500

// sqlDialect is the tokenizer dialect for the open connection. With
// nothing connected the shared core is used: a draft is still SQL.
func (m Model) sqlDialect() sqlhl.Dialect {
	if m.driver == nil {
		return sqlhl.Generic
	}
	return sqlhl.For(string(m.driver.Engine()))
}

// sqlStyle maps a token kind to its themed style. Plain text, bare
// identifiers and operators return the zero style, so they render with
// the terminal's own foreground and cost no escape sequences.
func (s styles) sqlStyle(k sqlhl.Kind) lipgloss.Style {
	switch k {
	case sqlhl.Keyword:
		return s.sqlKeyword
	case sqlhl.String:
		return s.sqlString
	case sqlhl.Number:
		return s.sqlNumber
	case sqlhl.Comment:
		return s.sqlComment
	case sqlhl.Placeholder:
		return s.sqlPlaceholder
	case sqlhl.QuotedIdent:
		return s.sqlQuoted
	}
	return lipgloss.NewStyle()
}

// highlightSQL renders one line of SQL. It is the entry point for the
// read-only places — the panel [5] preview, and the history pane once it
// wants the same treatment — where there is no cursor to draw.
func highlightSQL(s styles, d sqlhl.Dialect, line string) string {
	runes := []rune(line)
	return renderTokens(s, runes, sqlhl.Kinds(d, line), -1, lipgloss.NewStyle())
}

// renderTokens writes runes with one style per kind, grouping equal
// kinds so a keyword costs one escape sequence rather than one per
// letter. cursor is a rune index to draw with cursorStyle, or -1 for
// none; cursor == len(runes) draws a trailing cell, which is where the
// caret sits at the end of a line.
func renderTokens(s styles, runes []rune, kinds []sqlhl.Kind, cursor int, cursorStyle lipgloss.Style) string {
	var b strings.Builder
	for i := 0; i < len(runes); {
		if i == cursor {
			b.WriteString(cursorStyle.Render(string(runes[i])))
			i++
			continue
		}
		k := kindAt(kinds, i)
		j := i + 1
		for j < len(runes) && j != cursor && kindAt(kinds, j) == k {
			j++
		}
		b.WriteString(s.sqlStyle(k).Render(string(runes[i:j])))
		i = j
	}
	if cursor == len(runes) {
		b.WriteString(cursorStyle.Render(" "))
	}
	return b.String()
}

// kindAt is a bounds-safe lookup. The kinds slice is built from the same
// text as the runes, but a caller that slices one and not the other
// should get unstyled text, never a panic.
func kindAt(kinds []sqlhl.Kind, i int) sqlhl.Kind {
	if i < 0 || i >= len(kinds) {
		return sqlhl.Plain
	}
	return kinds[i]
}

// hlLine is one logical line of the buffer with its per-rune kinds. The
// kinds come from tokenizing the *whole* buffer, so a string literal or
// a block comment that spans lines keeps its colour on every one of them.
type hlLine struct {
	runes []rune
	kinds []sqlhl.Kind
}

func highlightLines(d sqlhl.Dialect, src string) []hlLine {
	runes := []rune(src)
	kinds := sqlhl.Kinds(d, src)
	// Kinds is one per rune by contract; padding rather than trusting it
	// keeps a tokenizer change from turning into a panic in the renderer.
	for len(kinds) < len(runes) {
		kinds = append(kinds, sqlhl.Plain)
	}
	out := []hlLine{}
	start := 0
	for i := 0; i <= len(runes); i++ {
		if i == len(runes) || runes[i] == '\n' {
			out = append(out, hlLine{runes: runes[start:i], kinds: kinds[start:i]})
			start = i + 1
		}
	}
	return out
}

// wrapSegments splits a line into display rows no wider than width, as
// rune index ranges into the line. It hard-wraps: a query is code, and
// breaking it on words would move the wrap point every time a word is
// typed, which is exactly the flicker the editor must not have.
//
// The ranges always cover the line and always contain at least one
// entry, so a caller can index segment 0 without checking.
func wrapSegments(runes []rune, width int) [][2]int {
	if width < 1 {
		width = 1
	}
	segs := [][2]int{}
	start, w := 0, 0
	for i, r := range runes {
		rw := lipgloss.Width(string(r))
		if rw < 1 {
			rw = 1
		}
		if w+rw > width && i > start {
			segs = append(segs, [2]int{start, i})
			start, w = i, 0
		}
		w += rw
	}
	segs = append(segs, [2]int{start, len(runes)})
	return segs
}

// cursorSegment finds the display row a cursor column falls on, and its
// offset within it. A cursor one past the end of a full row belongs to a
// new empty row below — the same place the next typed character goes.
func cursorSegment(segs [][2]int, col, width int) (row, offset int, extra bool) {
	for i, seg := range segs {
		if col >= seg[0] && col < seg[1] {
			return i, col - seg[0], false
		}
	}
	last := len(segs) - 1
	offset = col - segs[last][0]
	if offset >= width {
		return last + 1, 0, true
	}
	return last, offset, false
}

// editorRows is how many display rows the buffer needs at width w. The
// caller uses it to decide how much of the main view the editor gets.
func (m Model) editorRows(w int) int {
	_, contentW := editorGutterWidth(m.editor.area.LineCount(), w)
	n := 0
	for _, l := range highlightLines(m.sqlDialect(), m.script()) {
		n += len(wrapSegments(l.runes, contentW))
	}
	return maxInt(n, 1)
}

// editorGutterWidth splits the box between the line-number gutter and
// the text. A box too narrow to carry both drops the gutter rather than
// the text: numbers are navigation, the SQL is the work.
func editorGutterWidth(lines, w int) (gutter, content int) {
	digits := len(strconv.Itoa(maxInt(lines, 1)))
	gutter = digits + 2
	if w-gutter < 8 {
		return 0, maxInt(w, 1)
	}
	return gutter, w - gutter
}

// editorBlock renders the buffer: line numbers, highlighted SQL, and the
// cursor drawn over it. The result is exactly h rows of at most w cells,
// so the main view can stack it without measuring.
func (m Model) editorBlock(w, h int) string {
	s := m.style
	if w < 1 || h < 1 {
		return ""
	}
	lines := highlightLines(m.sqlDialect(), m.script())
	gutterW, contentW := editorGutterWidth(len(lines), w)

	// Where the caret is, and how it should look. It is only drawn while
	// the panel has the keyboard: a cursor on an unfocused panel reads as
	// "typing here works", which it does not.
	cursorRow, cursorCol := m.editor.area.Line(), m.editor.area.Column()
	cursorStyle := s.editorCursor
	showCursor := m.focus == panelQuery
	if showCursor && !m.editor.editing {
		cursorStyle = s.editorCursorIdle
	}
	if cursorRow < 0 || cursorRow >= len(lines) {
		showCursor = false
	}

	// An untouched buffer shows the placeholder instead of an empty
	// grid, the way the textarea did.
	if m.script() == "" {
		row := s.muted.Render(truncate("SELECT * FROM …", contentW))
		if showCursor {
			row = cursorStyle.Render("S") + s.muted.Render(truncate("ELECT * FROM …", contentW-1))
		}
		return padRows([]string{gutter(s, gutterW, 1) + row}, h)
	}

	var rows []string
	cursorAt := -1 // index into rows, for the scroll window
	for i, line := range lines {
		segs := wrapSegments(line.runes, contentW)
		curSeg, curOff, extra := -1, 0, false
		if showCursor && i == cursorRow {
			curSeg, curOff, extra = cursorSegment(segs, cursorCol, contentW)
		}
		for si, seg := range segs {
			num := 0
			if si == 0 {
				num = i + 1
			}
			at := -1
			if si == curSeg && !extra {
				at = curOff
				cursorAt = len(rows)
			}
			rows = append(rows, gutter(s, gutterW, num)+
				renderTokens(s, line.runes[seg[0]:seg[1]], line.kinds[seg[0]:seg[1]], at, cursorStyle))
		}
		// A cursor past the end of a full row gets a row of its own.
		if extra {
			cursorAt = len(rows)
			rows = append(rows, gutter(s, gutterW, 0)+cursorStyle.Render(" "))
		}
	}

	// The window scrolls only as far as it must to keep the caret in
	// view, which makes the offset a function of the state alone — no
	// stored scroll position to drift out of sync with the buffer.
	start := 0
	if cursorAt >= h {
		start = cursorAt - h + 1
	}
	if start > len(rows)-h {
		start = maxInt(len(rows)-h, 0)
	}
	end := min(start+h, len(rows))
	return padRows(rows[start:end], h)
}

// gutter renders one line-number cell. num <= 0 is a continuation row of
// a wrapped line, which gets the space but not a number.
func gutter(s styles, w, num int) string {
	if w <= 0 {
		return ""
	}
	text := strings.Repeat(" ", w)
	if num > 0 {
		text = fmt.Sprintf(" %*d ", w-2, num)
	}
	return s.editorGutter.Render(text)
}

// padRows pads a block out to exactly h rows so the editor keeps its
// height as the buffer grows and shrinks.
func padRows(rows []string, h int) string {
	for len(rows) < h {
		rows = append(rows, "")
	}
	return strings.Join(rows[:h], "\n")
}
