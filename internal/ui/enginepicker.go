package ui

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"lazysql/internal/db"
)

// enginePickerModal is step one of the connection flow: pick the engine,
// get the form that engine actually needs. The choice is the one input
// that decides which fields exist at all, so it is asked alone, first —
// see wiki/design/connection-form-ux.md for the flow's rationale.
//
// Every engine carries a digit, so the common case is a single keystroke;
// j/k + enter work like every other list in the app.
type enginePickerModal struct {
	title   string
	choices []engineChoice
	cursor  int

	// onPick replaces this modal with the chosen engine's form.
	onPick func(m *Model, e db.Engine)
	// onBack, when set, is what esc opens instead of just closing — the
	// change-engine reopen returns to the form it came from, values
	// intact. nil means esc cancels the whole flow.
	onBack func(m *Model)
}

// engineChoice is one row of the picker.
type engineChoice struct {
	engine db.Engine
	label  string
	desc   string
}

// enginePickOrder ranks the engines the way the picker lists them: the
// server engines developers reach for daily first, the embedded files
// after. Engines registered but not named here (a future driver) are
// appended in registry order rather than dropped.
var enginePickOrder = []db.Engine{
	db.EnginePostgres, db.EngineMySQL, db.EngineMariaDB,
	db.EngineSQLite, db.EngineDuckDB,
}

// engineChoices builds the picker rows from the driver registry.
func engineChoices() []engineChoice {
	registered := map[db.Engine]bool{}
	for _, e := range db.Engines() {
		registered[e] = true
	}
	ordered := make([]db.Engine, 0, len(registered))
	for _, e := range enginePickOrder {
		if registered[e] {
			ordered = append(ordered, e)
			delete(registered, e)
		}
	}
	for _, e := range db.Engines() {
		if registered[e] {
			ordered = append(ordered, e)
		}
	}

	out := make([]engineChoice, 0, len(ordered))
	for _, e := range ordered {
		d, err := db.DialectFor(e)
		if err != nil {
			continue
		}
		out = append(out, engineChoice{engine: e, label: d.DisplayName(), desc: engineDesc(e)})
	}
	return out
}

// engineDesc is the one-line answer to "what will this form ask me for".
func engineDesc(e db.Engine) string {
	switch {
	case e == db.EngineDuckDB:
		return "local file — or in-memory"
	case db.FileBased(e):
		return "single file on disk"
	}
	if p := db.DefaultPort(e); p > 0 {
		return fmt.Sprintf("server · default port %d", p)
	}
	return "server"
}

// newEnginePicker builds the modal, with the cursor on selected when that
// engine is among the choices.
func newEnginePicker(title string, selected db.Engine, onPick func(*Model, db.Engine)) *enginePickerModal {
	p := &enginePickerModal{title: title, choices: engineChoices(), onPick: onPick}
	for i, c := range p.choices {
		if c.engine == selected {
			p.cursor = i
		}
	}
	return p
}

func (p *enginePickerModal) pick(m *Model, i int) (bool, tea.Cmd) {
	if i < 0 || i >= len(p.choices) || p.onPick == nil {
		return true, nil
	}
	p.onPick(m, p.choices[i].engine)
	return true, nil
}

func (p *enginePickerModal) update(msg tea.KeyPressMsg, m *Model) (bool, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		if p.onBack != nil {
			p.onBack(m)
		}
		return true, nil
	case "down", "j", "tab":
		if p.cursor < len(p.choices)-1 {
			p.cursor++
		}
	case "up", "k", "shift+tab":
		if p.cursor > 0 {
			p.cursor--
		}
	case "enter", "l", "right":
		return p.pick(m, p.cursor)
	default:
		// A digit is the fast path: it both selects and confirms.
		if n, err := strconv.Atoi(msg.String()); err == nil && n >= 1 && n <= len(p.choices) {
			return p.pick(m, n-1)
		}
	}
	return false, nil
}

// scroll is the wheel: it walks the cursor like j/k, clamped.
func (p *enginePickerModal) scroll(delta int) {
	p.cursor += delta
	if p.cursor > len(p.choices)-1 {
		p.cursor = len(p.choices) - 1
	}
	if p.cursor < 0 {
		p.cursor = 0
	}
}

func (p *enginePickerModal) view(s styles, maxW, maxH int) string {
	labelW := 0
	for _, c := range p.choices {
		if w := lipgloss.Width(c.label); w > labelW {
			labelW = w
		}
	}

	var b strings.Builder
	b.WriteString(s.modalTitle.Render(p.title) + "\n\n")
	b.WriteString(s.muted.Render("Pick an engine — it decides the fields that follow.") + "\n\n")
	for i, c := range p.choices {
		marker, label := "  ", c.label
		if i == p.cursor {
			marker = s.keyHint.Render("▸ ")
			label = s.titleFocused.Render(label)
		}
		pad := strings.Repeat(" ", labelW-lipgloss.Width(c.label)+2)
		line := marker + s.keyHint.Render(strconv.Itoa(i+1)) + "  " + label + pad + s.muted.Render(c.desc)
		b.WriteString(truncate(line, maxInt(maxW-8, 16)) + "\n")
	}
	back := "esc cancel"
	if p.onBack != nil {
		back = "esc back to the form"
	}
	footer := fmt.Sprintf("1-%d pick · j/k move · enter choose · %s", len(p.choices), back)
	b.WriteString("\n" + s.muted.Render(footer))
	return s.modal.Render(b.String())
}
