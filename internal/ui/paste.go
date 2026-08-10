package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// Bracketed paste. The terminal brackets pasted text in ESC[200~ …
// ESC[201~ (Bubble Tea turns the mode on for every view whose
// DisableBracketedPasteMode is false, which is lazysql's), and delivers
// the whole block as one tea.PasteMsg instead of as the key presses it
// is made of.
//
// That distinction is the whole feature here. Without it a pasted
// SELECT reaches the query editor as a few hundred separate keys, and
// normal mode reads them as vim commands: `d`, `d`, `x`, `p` — the
// paste edits the buffer instead of entering it. So a paste is routed
// on its own, in the same order Update routes a key, and every step
// that can hold text takes it verbatim.
//
// A paste never changes mode and never runs anything: it is text
// arriving, not a command. In the editor's normal mode it inserts at
// the cursor and leaves the editor in normal mode — vim's own `p`
// (the internal yank register) keeps its meaning, and the two do not
// share a register. See wiki/design/clipboard-strategy.md.

// pasteHandler is the optional half of the modal contract: a popup with
// a text field implements it so pasted text reaches the field. A modal
// that does not (a confirm, a menu, the help) ignores a paste, exactly
// as it ignores the keys the paste is made of.
type pasteHandler interface {
	paste(msg tea.PasteMsg, m *Model) tea.Cmd
}

// updatePaste routes one bracketed paste.
func (m Model) updatePaste(msg tea.PasteMsg) (tea.Model, tea.Cmd) {
	if msg.Content == "" {
		return m, nil
	}
	// 1. A modal swallows a paste like it swallows every key — but only
	// the ones with somewhere to put it do anything with it.
	if m.modal != nil {
		if h, ok := m.modal.(pasteHandler); ok {
			return m, h.paste(msg, &m)
		}
		return m, nil
	}
	// 2. An open `/` filter is a one-line pattern: a multi-line paste
	// collapses rather than losing everything after the first newline.
	if m.focus < panelCount && m.panels[m.focus].filtering {
		p := m.panels[m.focus]
		p.setFilter(p.filter + flattenPaste(msg.Content))
		return m, nil
	}
	// 3. The query editor, in either mode.
	if m.focus == panelQuery {
		return m.pasteIntoEditor(msg)
	}
	// The data grid and the side panels have no text to paste into. A
	// paste there is dropped in silence: an accidental middle click is
	// not worth a log line.
	return m, nil
}

// pasteIntoEditor inserts pasted text in the query buffer at the
// cursor. The textarea already knows how to take a tea.PasteMsg —
// newlines become rows, the cursor lands after the last character — so
// this only has to get the message to it.
func (m Model) pasteIntoEditor(msg tea.PasteMsg) (tea.Model, tea.Cmd) {
	if m.editor.editing {
		var cmd tea.Cmd
		m.editor.area, cmd = m.editor.area.Update(msg)
		// The completion popup follows the buffer like it does after any
		// other insertion.
		return m, tea.Batch(cmd, m.refreshCompletion(false))
	}
	// Normal mode: the textarea is blurred, and a blurred textarea drops
	// every message. It is focused for the insertion alone — the editor
	// stays in normal mode afterwards, because a paste is text arriving,
	// not a request to start typing.
	m.editor.area.Focus()
	m.editor.area, _ = m.editor.area.Update(msg)
	m.editor.area.Blur()
	// An unfinished dd/yy chord cannot survive text landing between its
	// two keys.
	m.editor.pending = 0
	// The vertical-motion column follows the paste: a want remembered
	// from before it would aim j/k at a column the text has moved.
	m.editor.want = -1
	// Insertion can leave the caret one past the last character, which
	// is an insert-mode position; normal mode sits on the character.
	m.applyVim(m.vimBuffer(), false)
	return m, nil
}

// flattenPaste squeezes a paste onto one line, for the fields that only
// have one: newlines and tabs become spaces and the run collapses, so
// pasting a wrapped table name still yields something to match on.
func flattenPaste(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
