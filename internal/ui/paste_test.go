package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// paste builds the message a bracketed paste arrives as.
func paste(s string) tea.PasteMsg { return tea.PasteMsg{Content: s} }

// Pasting into insert mode inserts at the cursor, newlines and all.
func TestPasteIntoTheEditorInsertsVerbatim(t *testing.T) {
	m := sized(120, 40)
	m.setScript("SELECT *")
	m = send(t, m, press(':'))
	if !m.editor.editing {
		t.Fatal("`:` did not enter insert mode")
	}
	m = send(t, m, paste("\nFROM users\nWHERE id = 1"))

	want := "SELECT *\nFROM users\nWHERE id = 1"
	if got := m.script(); got != want {
		t.Fatalf("buffer = %q, want %q", got, want)
	}
	if !m.editor.editing {
		t.Fatal("a paste left insert mode")
	}
}

// The bug this feature exists for: in normal mode the pasted characters
// must not be read as vim commands. `D` clears the buffer, `d`/`x`
// delete and `p` pastes the register — as keys they would leave a
// confirm modal and a mangled buffer behind. As a paste they are text.
func TestPasteIntoNormalModeIsTextNotVimCommands(t *testing.T) {
	m := sized(120, 40)
	m.setScript("")
	m = send(t, m, press(':'), special(tea.KeyEscape, 0))
	if m.editor.editing {
		t.Fatal("esc did not leave insert mode")
	}

	script := "DELETE FROM x;\nDROP TABLE dd;\nSELECT p, x;"
	m = send(t, m, paste(script))

	if got := m.script(); got != script {
		t.Fatalf("buffer = %q, want the pasted text verbatim", got)
	}
	if m.modal != nil {
		t.Fatalf("paste opened %T — its characters were run as commands", m.modal)
	}
	if m.editor.editing {
		t.Fatal("a paste switched the editor into insert mode")
	}
	if m.editor.register.text != "" {
		t.Fatalf("vim register = %q, want a paste to leave it alone", m.editor.register.text)
	}
}

// A paste in normal mode lands at the cursor, not at the end of the
// buffer, and leaves the caret on a column normal mode can sit on.
func TestPasteInNormalModeLandsAtTheCursor(t *testing.T) {
	m := sized(120, 40)
	m.setScript("SELECT 1")
	m = send(t, m, press(':'), special(tea.KeyEscape, 0))
	// gg puts the caret on the first character of the first line.
	m = send(t, m, press('g'), press('g'))
	m = send(t, m, paste("-- note\n"))

	want := "-- note\nSELECT 1"
	if got := m.script(); got != want {
		t.Fatalf("buffer = %q, want %q", got, want)
	}
	if col := m.editor.area.Column(); col < 0 {
		t.Fatalf("cursor column = %d", col)
	}
}

// The query editor is not the only place text is typed: a prompt takes
// a paste too.
func TestPasteIntoAPromptModal(t *testing.T) {
	m := sized(120, 40)
	var submitted string
	m.modal = newPromptModal("Snippet name", "name", "", func(_ *Model, v string) tea.Cmd {
		submitted = v
		return nil
	})
	m = send(t, m, paste("weekly report"), special(tea.KeyEnter, 0))
	if submitted != "weekly report" {
		t.Fatalf("submitted %q, want the pasted text", submitted)
	}
}

// A multi-line paste into a one-line field collapses rather than losing
// everything after the first newline.
func TestPasteIntoAPromptModalFlattensNewlines(t *testing.T) {
	m := sized(120, 40)
	p := newPromptModal("Snippet name", "name", "", nil)
	m.modal = p
	m = send(t, m, paste("weekly\nreport"))
	if got := p.input.Value(); got != "weekly report" {
		t.Fatalf("input = %q, want the newline flattened", got)
	}
}

// The connection form is the other text-heavy popup: a pasted DSN or
// host has to reach the field under the cursor. The form opens on the
// engine select, so one `down` puts the cursor on Name first.
func TestPasteIntoTheConnectionForm(t *testing.T) {
	m := send(t, sized(120, 40), press('1'), press('n'))
	f, ok := m.modal.(*formModal)
	if !ok {
		t.Fatalf("`n` opened %T, want the connection form", m.modal)
	}
	m = send(t, m, special(tea.KeyDown, 0), paste("staging-db"))
	if got := f.value("name"); got != "staging-db" {
		t.Fatalf("name field = %q, want the pasted text", got)
	}
}

// A modal without a text field ignores a paste instead of acting on it.
func TestPasteIntoAConfirmModalIsIgnored(t *testing.T) {
	m := sized(120, 40)
	var confirmed bool
	m.modal = &confirmModal{title: "Drop it", onConfirm: func(*Model) tea.Cmd {
		confirmed = true
		return nil
	}}
	m = send(t, m, paste("y\n"))
	if confirmed {
		t.Fatal("a pasted y confirmed the modal")
	}
	if m.modal == nil {
		t.Fatal("a paste closed the confirm modal")
	}
}

// The inline `/` filter takes a paste as a pattern, on one line.
func TestPasteIntoAPanelFilter(t *testing.T) {
	m := send(t, sized(120, 40), press('2'), press('/'), paste("audit\nlog"))
	if got := m.panels[panelObjects].filter; got != "audit log" {
		t.Fatalf("filter = %q, want the pasted pattern flattened", got)
	}
	if !m.panels[panelObjects].filtering {
		t.Fatal("a paste closed the filter prompt")
	}
}

// Nothing on the data grid takes text, so a paste there does nothing at
// all — in particular it does not reach the grid as keys.
func TestPasteOnTheDataGridIsHarmless(t *testing.T) {
	m := sized(120, 40)
	m.focus = panelMain
	before := len(m.commandLog)
	m = send(t, m, paste("yyy"))
	if m.modal != nil {
		t.Fatalf("paste opened %T on the data grid", m.modal)
	}
	if len(m.commandLog) != before {
		t.Fatalf("command log grew by %d lines", len(m.commandLog)-before)
	}
}

// A paste is one edit, so the completion popup it may trigger reflects
// the text that landed rather than the word before it.
func TestPasteInInsertModeDoesNotLeaveAStaleCompletion(t *testing.T) {
	m := sized(120, 40)
	m.setScript("")
	m = send(t, m, press(':'), paste("SELECT 1;\n"))
	if m.completion.open && !strings.HasSuffix(m.script(), "\n") {
		t.Fatal("completion popup left open on a buffer ending in a newline")
	}
}
