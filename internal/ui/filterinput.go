package ui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"lazysql/internal/db"
	"lazysql/internal/sqlhl"
)

// filterInput is the data grid's `/`: one line pinned to the bottom of
// the grid where the WHERE clause is typed directly, instead of the
// popup the filter used to open.
//
// The line is pre-labeled with the statement the clause goes into —
// `SELECT * FROM "orders" WHERE ` — and that label is not part of the
// value, so it cannot be edited, selected or backspaced into: what the
// user types is the WHERE clause and nothing else. The prefix is
// rendered by the dialect (db.FilterPrefixSQL), so the relation named on
// screen is quoted exactly as the statement behind it will be.
//
// The clause is drawn by this file rather than by textinput.View(): the
// component styles its value whole, and the clause is SQL that has to be
// highlighted token by token, like the query editor's buffer. The
// textinput stays the model — value, cursor, editing keys — and view()
// below owns the prefix, the colours, the caret cell and the horizontal
// scroll.
//
// The clause itself stays user-authored SQL by design — db.ParseFilter
// binds what it can take apart and flags the rest as verbatim, the same
// as before. See wiki/design/inline-where-filter.md.
type filterInput struct {
	input  textinput.Model
	prefix string

	// st and dialect are what the clause is drawn with. Both are fixed
	// for the life of the line: it belongs to one relation of one
	// connection, and every change of either closes it.
	st      styles
	dialect sqlhl.Dialect

	// offset is the rune index the visible slice of the clause starts at
	// — the horizontal scroll, which is ours now that the line renders
	// itself.
	offset int

	// hist are the filters recorded for this relation, newest first, and
	// idx walks them: -1 is the line being typed, which draft holds while
	// a recalled entry sits on screen. Recalling is therefore
	// non-destructive — walking back past the newest entry restores what
	// was half-typed.
	hist  []string
	idx   int
	draft string
}

// shortFilterPrefix stands in for the full statement in a box too narrow
// to hold it. The clause being typed is what must stay readable; the
// label is context, and `WHERE` is the part of it that carries meaning.
const shortFilterPrefix = "WHERE "

// minFilterClauseWidth is how many cells the clause needs before the
// prefix is allowed to take the rest.
const minFilterClauseWidth = 16

// newFilterInput builds the line for one relation. initial is the filter
// already running, so `/` opens on what the grid is showing rather than
// on an empty clause, and hist is that relation's recall list.
func newFilterInput(s styles, d sqlhl.Dialect, prefix, initial string, hist []string) *filterInput {
	ti := textinput.New()
	// Its own prompt and width stay unset: nothing textinput would print
	// is ever printed, so anything it computed from them — its prompt
	// style, its blinking cursor, its own scroll window — would only be
	// a second, disagreeing answer to a question view() already answers.
	ti.SetValue(initial)
	ti.CursorEnd()
	ti.Focus()
	return &filterInput{input: ti, prefix: prefix, st: s, dialect: d, hist: hist, idx: -1}
}

// value is the clause as typed, trimmed.
func (fi *filterInput) value() string { return strings.TrimSpace(fi.input.Value()) }

// recall walks the relation's filter history: delta +1 goes back in
// time, -1 forward. Stepping forward past the newest entry restores the
// draft, which is what makes the whole walk undoable.
func (fi *filterInput) recall(delta int) {
	next := fi.idx + delta
	if next < -1 || next >= len(fi.hist) {
		return
	}
	if fi.idx == -1 {
		fi.draft = fi.input.Value()
	}
	fi.idx = next
	if next == -1 {
		fi.input.SetValue(fi.draft)
	} else {
		fi.input.SetValue(fi.hist[next])
	}
	fi.input.CursorEnd()
}

// filterMarker is the bar drawn at the head of the line while it owns
// the keyboard. It wears the green the focused panel borders wear, so
// "the keyboard is here" reads the same on the line as it does on a
// panel, and it goes away with the line.
const filterMarker = "▌"

// view renders the line into a w-cell box: the focus bar, the muted
// statement prefix, and the clause highlighted as SQL with the caret
// drawn on it. The prefix gives way to shortFilterPrefix when the box
// cannot hold both it and a usable clause: truncating the line instead
// would cut off the end the caret is on.
func (fi *filterInput) view(w int) string {
	if w <= 0 {
		return ""
	}
	marker, rest := "", w
	// A box too narrow for both keeps the clause: the caret sitting in
	// highlighted text is a focus cue of its own, the bar is the louder
	// one, and neither is worth a cell taken off the clause here.
	if w >= 4 {
		marker, rest = fi.st.filterFocus.Render(filterMarker), w-lipgloss.Width(filterMarker)
	}
	prefix, clauseW := fitFilterPrefix(fi.prefix, rest)
	return marker + fi.st.muted.Render(prefix) + fi.clauseView(clauseW)
}

// fitFilterPrefix picks the prefix a w-cell box can afford and gives the
// clause the rest.
func fitFilterPrefix(prefix string, w int) (string, int) {
	if w-lipgloss.Width(prefix) < minFilterClauseWidth {
		prefix = shortFilterPrefix
	}
	// A box that cannot hold even the short label drops it: the caret has
	// to stay on screen, and the label is only context.
	if w-lipgloss.Width(prefix) < 2 {
		prefix = ""
	}
	return prefix, maxInt(w-lipgloss.Width(prefix), 1)
}

// clauseView draws the clause into exactly w cells: tokenized in the
// connection's dialect, coloured with the same sqlStyle palette the
// query editor uses, and with the caret reversed over whatever token it
// landed on. The result is padded to w, so the line covers the status
// line it stands in for.
func (fi *filterInput) clauseView(w int) string {
	value := fi.input.Value()
	runes := []rune(value)
	kinds := sqlhl.Kinds(fi.dialect, value)
	cells := lineCells(runes)
	pos := min(maxInt(fi.input.Position(), 0), len(runes))

	start, end := fi.window(cells, len(runes), pos, w)
	body := renderTokens(fi.st, runes[start:end], kindRange(kinds, start, end), pos-start, fi.st.editorCursor)

	drawn := cells[end] - cells[start]
	if pos == end {
		// renderTokens drew the caret on its own cell past the last rune.
		drawn++
	}
	if pad := w - drawn; pad > 0 {
		body += strings.Repeat(" ", pad)
	}
	return body
}

// window is the slice of the clause a w-cell box shows, as rune indexes
// into it. It scrolls only as far as it must to keep the caret — the
// empty cell past the end of the clause included — inside the box, and
// never further right than the point where the tail fills the box, so
// backspacing at the end pulls the text back into view instead of
// leaving a gap. Cluster boundaries hold on both ends: a combining
// accent may not be scrolled away from the letter carrying it.
func (fi *filterInput) window(cells []int, n, pos, w int) (start, end int) {
	if w < 1 || n == 0 {
		fi.offset = 0
		return 0, 0
	}
	total := cells[n]
	// The rightmost start that still fills the box, measured with the
	// trailing caret cell the end of the clause needs.
	maxStart := 0
	for maxStart < n && total+1-cells[maxStart] > w {
		maxStart = clusterEnd(cells, maxStart)
	}

	start = clusterStart(cells, min(maxInt(fi.offset, 0), n))
	if start > maxStart {
		start = maxStart
	}
	if pos < start {
		start = clusterStart(cells, pos)
	}
	caretEnd := total + 1
	if pos < n {
		caretEnd = cells[clusterEnd(cells, pos)]
	}
	for start < n && caretEnd-cells[start] > w {
		start = clusterEnd(cells, start)
	}
	fi.offset = start

	end = start
	for end < n && cells[clusterEnd(cells, end)]-cells[start] <= w {
		end = clusterEnd(cells, end)
	}
	return start, end
}

// kindRange is renderTokens' kinds argument for a rune range, read
// through the bounds-safe lookup: one kind per rune is the tokenizer's
// contract, and a renderer should not panic if that ever slips.
func kindRange(kinds []sqlhl.Kind, start, end int) []sqlhl.Kind {
	out := make([]sqlhl.Kind, end-start)
	for i := range out {
		out[i] = kindAt(kinds, start+i)
	}
	return out
}

// ---------- model wiring ----------

// openFilterInput is `/` on the data grid: it opens the inline WHERE
// line for the relation on screen, pre-filled with the active filter and
// loaded with that relation's recall list.
func (m *Model) openFilterInput() tea.Cmd {
	if !m.data.browsing() || m.driver == nil {
		return logCmd("-- filter skipped: no relation open")
	}
	raw := ""
	if m.data.filter != nil {
		raw = m.data.filter.Raw
	}
	m.filterInput = newFilterInput(
		m.style,
		m.sqlDialect(),
		db.FilterPrefixSQL(m.driver.Dialect(), m.data.database, m.data.table),
		raw,
		m.filterHistory(),
	)
	return nil
}

// closeFilterInput drops the line if one is open. Every change of what
// the grid is showing goes through it: the prefix names one relation of
// one connection, so a line that outlived either would be labelled with
// a statement it no longer belongs to.
func (m *Model) closeFilterInput() { m.filterInput = nil }

// filterInputOpen reports whether the inline WHERE line owns the
// keyboard. It is only live on the focused grid — the same line is drawn
// under the query editor's result, where the grid's keys are not routed.
func (m Model) filterInputOpen() bool {
	return m.filterInput != nil && m.focus == panelMain
}

// updateFilterInput is the key handler of the open line. It runs ahead
// of the global keys, so `q`, the digits and `:` type into the clause
// instead of quitting, jumping or opening the editor — the same deal the
// side panels' `/` filter gets.
func (m Model) updateFilterInput(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	fi := m.filterInput
	k := m.keys
	switch {
	case key.Matches(msg, k.CancelFilter):
		// esc changes nothing: the grid keeps whatever it was showing,
		// which is what the popup's esc did too.
		m.closeFilterInput()
		return m, nil
	case key.Matches(msg, k.ApplyFilter):
		where := fi.value()
		m.closeFilterInput()
		cmd := m.applyFilter(where)
		return m, cmd
	case key.Matches(msg, k.FilterHistPrev):
		fi.recall(1)
		return m, nil
	case key.Matches(msg, k.FilterHistNext):
		fi.recall(-1)
		return m, nil
	}
	var cmd tea.Cmd
	fi.input, cmd = fi.input.Update(msg)
	return m, cmd
}

// pasteIntoFilterInput puts pasted text in the clause. The line is one
// line, so a multi-line paste collapses rather than losing everything
// after the first newline.
func (m Model) pasteIntoFilterInput(msg tea.PasteMsg) (tea.Model, tea.Cmd) {
	flat := tea.PasteMsg{Content: flattenPaste(msg.Content)}
	var cmd tea.Cmd
	m.filterInput.input, cmd = m.filterInput.input.Update(flat)
	return m, cmd
}

// applyFilter runs the typed clause: an empty one clears the filter, and
// anything else reloads the first page behind it and is recorded in the
// relation's recall list. The statement itself reaches the command log
// through reloadPage, like every other page query.
func (m *Model) applyFilter(where string) tea.Cmd {
	if where == "" {
		return m.clearFilter()
	}
	// The filter is recorded before it runs, not after: a fragment the
	// engine rejects is exactly the one worth recalling and fixing.
	return tea.Batch(m.recordFilter(where), m.setDataFilter(where))
}
