package ui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"lazysql/internal/db"
)

// filterInput is the data grid's `/`: one line pinned to the bottom of
// the grid where the WHERE clause is typed directly, instead of the
// popup the filter used to open.
//
// The line is pre-labeled with the statement the clause goes into —
// `SELECT * FROM "orders" WHERE ` — and that label is the textinput's
// prompt, so it cannot be edited, selected or backspaced into: what the
// user types is the WHERE clause and nothing else. The prefix is
// rendered by the dialect (db.FilterPrefixSQL), so the relation named on
// screen is quoted exactly as the statement behind it will be.
//
// The clause itself stays user-authored SQL by design — db.ParseFilter
// binds what it can take apart and flags the rest as verbatim, the same
// as before. See wiki/design/inline-where-filter.md.
type filterInput struct {
	input  textinput.Model
	prefix string

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
func newFilterInput(s styles, prefix, initial string, hist []string) *filterInput {
	ti := textinput.New()
	ti.Prompt = prefix
	ti.SetValue(initial)
	ti.CursorEnd()
	ti.Focus()
	// The prompt is not editable text, so it is styled like the muted
	// chrome it is rather than like the clause next to it. The caret does
	// not blink: it sits in the grid's own box, which repaints on every
	// key anyway, and a blink is a timer per keystroke for a cursor the
	// editor already renders static elsewhere in the app.
	st := ti.Styles()
	st.Focused.Prompt = s.muted
	st.Blurred.Prompt = s.muted
	st.Cursor.Blink = false
	ti.SetStyles(st)
	return &filterInput{input: ti, prefix: prefix, hist: hist, idx: -1}
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

// view renders the line into a w-cell box. The prefix gives way to
// shortFilterPrefix when the box cannot hold both it and a usable
// clause: truncating the line instead would cut off the end the caret is
// on.
func (fi *filterInput) view(w int) string {
	if w <= 0 {
		return ""
	}
	fi.resize(w)
	return fi.input.View()
}

// resize fits the line to a w-cell box: it picks the prefix the box can
// afford and gives the clause the rest.
func (fi *filterInput) resize(w int) {
	prefix := fi.prefix
	if w-lipgloss.Width(prefix) < minFilterClauseWidth {
		prefix = shortFilterPrefix
	}
	// A box that cannot hold even the short label drops it: the caret has
	// to stay on screen, and the label is only context.
	if w-lipgloss.Width(prefix) < 2 {
		prefix = ""
	}
	width := maxInt(w-lipgloss.Width(prefix)-1, 1)
	if prefix == fi.input.Prompt && width == fi.input.Width() {
		return
	}
	fi.input.Prompt = prefix
	fi.input.SetWidth(width)
	// textinput recomputes which slice of a too-long clause is on screen
	// when the value changes, not when the width does. Re-setting the
	// value is what makes a resize — or the very first frame, before a
	// key has landed on a line opened with a filter already in it — scroll
	// to the caret instead of drawing past the box.
	fi.input.SetValue(fi.input.Value())
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
