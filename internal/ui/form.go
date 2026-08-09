package ui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// formModal is the reusable multi-field popup: a vertical stack of labelled
// fields with one cursor, an inline validation error line, and the usual
// enter/esc contract. The connection editor is the first user; row insert and
// filter builders will reuse it.
//
// Fields can be hidden dynamically (`visible`), which is how the engine choice
// swaps host/port for a file path without rebuilding the modal.

type fieldKind int

const (
	fieldText fieldKind = iota
	fieldPassword
	fieldSelect
	fieldBool
)

type formField struct {
	name  string
	label string
	kind  fieldKind

	input textinput.Model // fieldText, fieldPassword

	choices []string // fieldSelect: display labels
	values  []string // fieldSelect: values behind the labels
	choice  int

	on bool // fieldBool

	help string
	// visible reports whether the field applies to the current form state.
	// nil means always visible.
	visible func(f *formModal) bool
}

func newTextField(name, label, initial, placeholder string) *formField {
	ti := textinput.New()
	ti.Placeholder = placeholder
	ti.SetValue(initial)
	ti.CursorEnd()
	ti.SetWidth(34)
	return &formField{name: name, label: label, kind: fieldText, input: ti}
}

func newPasswordField(name, label, placeholder string) *formField {
	f := newTextField(name, label, "", placeholder)
	f.kind = fieldPassword
	f.input.EchoMode = textinput.EchoPassword
	f.input.EchoCharacter = '•'
	return f
}

func newSelectField(name, label string, labels, values []string, selected string) *formField {
	f := &formField{name: name, label: label, kind: fieldSelect, choices: labels, values: values}
	for i, v := range values {
		if v == selected {
			f.choice = i
		}
	}
	return f
}

func newBoolField(name, label string, on bool) *formField {
	return &formField{name: name, label: label, kind: fieldBool, on: on}
}

func (f *formField) withHelp(h string) *formField { f.help = h; return f }

func (f *formField) withVisible(fn func(*formModal) bool) *formField { f.visible = fn; return f }

// value is the field's current content as a string. Select fields report the
// value behind the label; bool fields report "true"/"false".
func (f *formField) value() string {
	switch f.kind {
	case fieldSelect:
		if f.choice >= 0 && f.choice < len(f.values) {
			return f.values[f.choice]
		}
		return ""
	case fieldBool:
		if f.on {
			return "true"
		}
		return "false"
	case fieldPassword:
		// Never trim a secret: leading/trailing spaces can be significant.
		return f.input.Value()
	default:
		return strings.TrimSpace(f.input.Value())
	}
}

func (f *formField) display() string {
	switch f.kind {
	case fieldSelect:
		if f.choice >= 0 && f.choice < len(f.choices) {
			return "‹ " + f.choices[f.choice] + " ›"
		}
		return "‹ ›"
	case fieldBool:
		if f.on {
			return "[x] yes"
		}
		return "[ ] no"
	default:
		return f.input.View()
	}
}

// formModal is the popup itself. onSubmit returns close=false to keep the
// form open after a failed validation; set f.err first so the user sees why.
type formModal struct {
	title    string
	fields   []*formField
	cursor   int
	err      string
	footer   string
	onSubmit func(m *Model, f *formModal) (close bool, cmd tea.Cmd)
}

func newFormModal(title string, fields []*formField, onSubmit func(*Model, *formModal) (bool, tea.Cmd)) *formModal {
	f := &formModal{title: title, fields: fields, onSubmit: onSubmit}
	f.syncFocus()
	return f
}

// field returns a field by name, or nil.
func (f *formModal) field(name string) *formField {
	for _, fl := range f.fields {
		if fl.name == name {
			return fl
		}
	}
	return nil
}

// value returns a field's current value, or "" when the field is absent or
// currently hidden.
func (f *formModal) value(name string) string {
	fl := f.field(name)
	if fl == nil || !f.isVisible(fl) {
		return ""
	}
	return fl.value()
}

// rawValue ignores visibility — used when a hidden field still carries state
// worth keeping, e.g. a stored host while a file engine is selected.
func (f *formModal) rawValue(name string) string {
	if fl := f.field(name); fl != nil {
		return fl.value()
	}
	return ""
}

func (f *formModal) isVisible(fl *formField) bool {
	return fl.visible == nil || fl.visible(f)
}

// visibleFields is the field list the cursor actually walks.
func (f *formModal) visibleFields() []*formField {
	out := make([]*formField, 0, len(f.fields))
	for _, fl := range f.fields {
		if f.isVisible(fl) {
			out = append(out, fl)
		}
	}
	return out
}

func (f *formModal) current() *formField {
	vis := f.visibleFields()
	if len(vis) == 0 {
		return nil
	}
	if f.cursor >= len(vis) {
		f.cursor = len(vis) - 1
	}
	if f.cursor < 0 {
		f.cursor = 0
	}
	return vis[f.cursor]
}

// syncFocus keeps exactly the field under the cursor focused, so only one
// text input blinks and receives runes.
func (f *formModal) syncFocus() {
	cur := f.current()
	for _, fl := range f.fields {
		if fl.kind != fieldText && fl.kind != fieldPassword {
			continue
		}
		if fl == cur {
			fl.input.Focus()
		} else {
			fl.input.Blur()
		}
	}
}

func (f *formModal) move(delta int) {
	n := len(f.visibleFields())
	if n == 0 {
		return
	}
	f.cursor = (f.cursor + delta + n) % n
	f.syncFocus()
}

func (f *formModal) update(msg tea.KeyPressMsg, m *Model) (bool, tea.Cmd) {
	cur := f.current()
	switch msg.String() {
	case "esc":
		return true, nil
	case "tab", "down":
		f.move(1)
		return false, nil
	case "shift+tab", "up":
		f.move(-1)
		return false, nil
	case "enter":
		if f.onSubmit == nil {
			return true, nil
		}
		f.err = ""
		return f.onSubmit(m, f)
	}

	if cur == nil {
		return false, nil
	}
	switch cur.kind {
	case fieldSelect:
		switch msg.String() {
		case "left", "h":
			cur.choice = (cur.choice - 1 + len(cur.choices)) % len(cur.choices)
		case "right", "l", " ", "space":
			cur.choice = (cur.choice + 1) % len(cur.choices)
		}
		// Visibility rules may now hide the field the cursor sits on.
		f.syncFocus()
		return false, nil
	case fieldBool:
		switch msg.String() {
		case " ", "space", "left", "right", "h", "l", "y", "n":
			cur.on = !cur.on
		}
		return false, nil
	}

	var cmd tea.Cmd
	cur.input, cmd = cur.input.Update(msg)
	return false, cmd
}

func (f *formModal) view(s styles, maxW, maxH int) string {
	vis := f.visibleFields()
	labelW := 0
	for _, fl := range vis {
		if w := lipgloss.Width(fl.label); w > labelW {
			labelW = w
		}
	}
	inputW := min(38, maxInt(maxW-labelW-12, 12))

	var b strings.Builder
	b.WriteString(s.modalTitle.Render(f.title) + "\n\n")
	for i, fl := range vis {
		if fl.kind == fieldText || fl.kind == fieldPassword {
			fl.input.SetWidth(inputW)
		}
		label := fl.label + strings.Repeat(" ", labelW-lipgloss.Width(fl.label))
		marker := "  "
		if i == f.cursor {
			marker = s.keyHint.Render("▸ ")
			label = s.titleFocused.Render(label)
		} else {
			label = s.muted.Render(label)
		}
		line := marker + label + "  " + fl.display()
		if fl.help != "" && i == f.cursor {
			line += "  " + s.muted.Render(fl.help)
		}
		b.WriteString(line + "\n")
	}
	if f.err != "" {
		b.WriteString("\n" + s.danger.Render("✗ "+f.err) + "\n")
	}
	footer := f.footer
	if footer == "" {
		footer = "tab/↑↓ field · ←→ change · enter save · esc cancel"
	}
	b.WriteString("\n" + s.muted.Render(footer))
	return s.modal.Render(b.String())
}
