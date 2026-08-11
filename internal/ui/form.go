package ui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// formModal is the reusable multi-field popup: a vertical stack of labelled
// fields with one cursor, an inline validation error line, and the usual
// enter/esc contract. The connection editor is its user. (The row insert form
// is its own modal: its fields are columns, and each one cycles between a
// typed value, NULL and the column's default rather than holding a string.)
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
	// suggest turns on filesystem path completion for this text field: the
	// form recomputes candidates on every edit and `tab` completes instead
	// of moving to the next field while any exist.
	suggest bool
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

// withSuggest enables path completion on a text field.
func (f *formField) withSuggest() *formField { f.suggest = true; return f }

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
	// info is the green counterpart of err: a transient status line, e.g.
	// the connection form's test result.
	info     string
	footer   string
	onSubmit func(m *Model, f *formModal) (close bool, cmd tea.Cmd)

	// onKey, when set, sees every key the form's own contract does not
	// claim (esc/tab/enter/↑↓). Returning handled=true stops the key from
	// reaching the field under the cursor — the connection form's ctrl+t
	// test hook lives here.
	onKey func(m *Model, f *formModal, key string) (handled bool, cmd tea.Cmd)

	// onCancel, when set, runs when esc closes the form without
	// submitting — cleanup for state a caller set up expecting either a
	// submit or a cancel, e.g. the restore-session password prompt
	// dropping its pending restore.
	onCancel func(m *Model)

	// body, when set, renders extra lines between the title and the
	// fields. It is called on every draw, so it can reflect what has been
	// typed — which is what makes the dump/restore preview show the
	// command the form is actually about to run.
	body func(f *formModal) []string

	// sugg holds the path candidates for the field under the cursor when
	// that field opted into completion. It is emptied whenever the cursor
	// leaves the field, and whenever the form closes or submits.
	sugg pathSuggest
}

// withBody attaches the lines rendered above the fields.
func (f *formModal) withBody(fn func(*formModal) []string) *formModal {
	f.body = fn
	return f
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
	f.sugg.clear()
	f.syncFocus()
}

// suggestField is the field under the cursor when it takes path completion,
// otherwise nil.
func (f *formModal) suggestField() *formField {
	cur := f.current()
	if cur != nil && cur.suggest && (cur.kind == fieldText || cur.kind == fieldPassword) {
		return cur
	}
	return nil
}

func (f *formModal) update(msg tea.KeyPressMsg, m *Model) (bool, tea.Cmd) {
	cur := f.current()
	switch {
	case msg.String() == "esc":
		f.sugg.clear()
		if f.onCancel != nil {
			f.onCancel(m)
		}
		return true, nil
	case msg.String() == "tab":
		// While path candidates are up, tab completes the path; ↑↓ (and
		// shift+tab) stay the way to walk fields. With no candidates tab
		// keeps its usual meaning.
		if sf := f.suggestField(); sf != nil && f.sugg.active() {
			sf.input.SetValue(f.sugg.complete(sf.input.Value()))
			sf.input.CursorEnd()
			return false, nil
		}
		f.move(1)
		return false, nil
	case msg.String() == "down":
		f.move(1)
		return false, nil
	case msg.String() == "shift+tab", msg.String() == "up":
		f.move(-1)
		return false, nil
	case msg.String() == "enter", key.Matches(msg, m.keys.AcceptChanges):
		if f.onSubmit == nil {
			f.sugg.clear()
			return true, nil
		}
		f.err, f.info = "", ""
		close, cmd := f.onSubmit(m, f)
		if close {
			f.sugg.clear()
		}
		return close, cmd
	}

	if f.onKey != nil {
		if handled, cmd := f.onKey(m, f, msg.String()); handled {
			return false, cmd
		}
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
		f.sugg.clear()
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
	if cur.suggest {
		f.sugg.refresh(cur.input.Value())
	}
	return false, cmd
}

// paste puts pasted text in the field under the cursor, when that field
// holds text at all — a select or a bool has no room for it. A pasted
// path re-runs completion, the same way typing one does.
func (f *formModal) paste(msg tea.PasteMsg, _ *Model) tea.Cmd {
	cur := f.current()
	if cur == nil || (cur.kind != fieldText && cur.kind != fieldPassword) {
		return nil
	}
	var cmd tea.Cmd
	cur.input, cmd = cur.input.Update(msg)
	if cur.suggest {
		f.sugg.refresh(cur.input.Value())
	}
	return cmd
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

	var bodyLines []string
	if f.body != nil {
		bodyLines = f.body(f)
	}

	// Path suggestions only get the rows the terminal has left over: the
	// modal is centered on the full height, so growing past it would break
	// the tiny-terminal guard. 4 covers the box border and padding.
	sugRows := 0
	if sf := f.suggestField(); sf != nil && f.sugg.active() {
		used := 4 + 2 + len(vis) + 2 // chrome, title+blank, fields, blank+footer
		if len(bodyLines) > 0 {
			used += len(bodyLines) + 1
		}
		if f.err != "" {
			used += 2
		}
		if f.info != "" {
			used += 2
		}
		sugRows = min(maxSuggestLines, maxH-used)
	}

	var b strings.Builder
	b.WriteString(s.modalTitle.Render(f.title) + "\n\n")
	for _, line := range bodyLines {
		b.WriteString(truncate(line, maxInt(maxW-6, 8)) + "\n")
	}
	if len(bodyLines) > 0 {
		b.WriteString("\n")
	}
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
		if fl.suggest && i == f.cursor {
			indent := strings.Repeat(" ", labelW+4)
			for _, sl := range f.sugg.lines(sugRows) {
				b.WriteString(indent + s.muted.Render(truncate(sl, maxInt(inputW, 8))) + "\n")
			}
		}
	}
	if f.err != "" {
		b.WriteString("\n" + s.danger.Render("✗ "+f.err) + "\n")
	}
	if f.info != "" {
		b.WriteString("\n" + s.titleFocused.Render(f.info) + "\n")
	}
	footer := f.footer
	if footer == "" {
		footer = "tab/↑↓ field · ←→ change · enter/ctrl+enter save · esc cancel"
	}
	if sugRows > 0 {
		// tab is taken by completion here, so the bar must stop advertising
		// it as the way to move between fields.
		footer = "tab complete path · ↑↓ field · enter/ctrl+enter save · esc cancel"
	}
	b.WriteString("\n" + s.muted.Render(footer))
	return s.modal.Render(b.String())
}
