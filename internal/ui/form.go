package ui

import (
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// formModal is the reusable multi-field popup: a vertical stack of labelled
// fields with one cursor, optional section headers, an inline validation
// error line, a cursor-following scroll window and the usual enter/esc
// contract. The connection editor is its main user. (The row insert form
// is its own modal: its fields are columns, and each one cycles between a
// typed value, NULL and the column's default rather than holding a string.)
//
// Fields can be hidden dynamically (`visible`), which is how the SSH
// section unfolds under its toggle without rebuilding the modal.

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

	// section names the header this field renders under. Consecutive
	// fields sharing a section form one visually separated group; ""
	// keeps the flat list every other form uses.
	section string

	help string
	// suggest turns on filesystem path completion for this text field: the
	// form recomputes candidates on every edit and `tab` completes instead
	// of moving to the next field while any exist.
	suggest bool
	// visible reports whether the field applies to the current form state.
	// nil means always visible.
	visible func(f *formModal) bool
	// validate reports why the field's current value cannot be saved, or
	// "". It runs on every draw, so the message tracks what is typed; a
	// failed required-field check only shows after the first submit
	// attempt (see errorFor), so an empty form does not open covered in
	// red. Submitting is blocked while any visible field validates false.
	validate func(f *formModal, value string) string
}

func newTextField(name, label, initial, placeholder string) *formField {
	ti := textinput.New()
	ti.Placeholder = placeholder
	ti.SetValue(initial)
	ti.CursorEnd()
	ti.SetWidth(34)
	return &formField{name: name, label: label, kind: fieldText, input: ti}
}

func newPasswordField(name, label, initial, placeholder string) *formField {
	f := newTextField(name, label, initial, placeholder)
	f.kind = fieldPassword
	f.input.EchoMode = textinput.EchoPassword
	f.input.EchoCharacter = '•'
	return f
}

// newHiddenField is a never-visible carrier for one fixed value. The
// engine-specific connection form stores its engine here, so every
// predicate and reader that asks rawValue("engine") keeps working without
// the engine being a walkable field.
func newHiddenField(name, value string) *formField {
	f := &formField{name: name, kind: fieldSelect, choices: []string{value}, values: []string{value}}
	f.visible = func(*formModal) bool { return false }
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

// withSection puts the field under a named group header.
func (f *formField) withSection(s string) *formField { f.section = s; return f }

func (f *formField) withVisible(fn func(*formModal) bool) *formField { f.visible = fn; return f }

// withSuggest enables path completion on a text field.
func (f *formField) withSuggest() *formField { f.suggest = true; return f }

func (f *formField) withValidate(fn func(*formModal, string) string) *formField {
	f.validate = fn
	return f
}

// requiredField is the validator for fields that cannot stay empty.
func requiredField(what string) func(*formModal, string) string {
	return func(_ *formModal, v string) string {
		if v == "" {
			return what + " is required"
		}
		return ""
	}
}

// validPort accepts an empty value (the caller supplies the default) or a
// number in port range.
func validPort(_ *formModal, v string) string {
	if v == "" {
		return ""
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return "port must be a number"
	}
	if n < 1 || n > 65535 {
		return "port out of range 1-65535"
	}
	return ""
}

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

// display renders the field's value cell. Selects wear their ‹ › chevrons
// in the accent while focused — the visual cue that ←/→ acts here — and a
// switched-on toggle is green, so state reads at a glance.
func (f *formField) display(s styles, focused bool) string {
	switch f.kind {
	case fieldSelect:
		v := ""
		if f.choice >= 0 && f.choice < len(f.choices) {
			v = f.choices[f.choice]
		}
		if focused {
			return s.keyHint.Render("‹ ") + v + s.keyHint.Render(" ›")
		}
		return s.muted.Render("‹ ") + v + s.muted.Render(" ›")
	case fieldBool:
		if f.on {
			return s.toggleOn.Render("[x] yes")
		}
		return s.muted.Render("[ ]") + " no"
	default:
		return f.input.View()
	}
}

// formModal is the popup itself. onSubmit returns close=false to keep the
// form open after a failed validation; set f.err first so the user sees why.
type formModal struct {
	title  string
	fields []*formField
	cursor int
	err    string
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

	// bar, when set, supplies the bottom options bar's bindings while the
	// form is open (the connection form adds ctrl+t there). nil falls back
	// to keyMap.formKeys.
	bar func(k keyMap) []key.Binding

	// submitted flips on the first enter, and from then on required-field
	// errors show inline even on still-empty fields — before that, only
	// fields with content are judged, so a fresh form starts calm.
	submitted bool

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

	// offset is the first body line the scroll window shows. A sectioned
	// form can outgrow a small terminal; view keeps the cursor's line
	// inside the window and marks clipped rows with ⋮.
	offset int
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

// focusField puts the cursor on the named field, when it is visible — the
// connection form opens file engines on the path, the one field they need.
func (f *formModal) focusField(name string) {
	for i, fl := range f.visibleFields() {
		if fl.name == name {
			f.cursor = i
			f.syncFocus()
			return
		}
	}
}

// scroll is the wheel: like the menu modal it walks the cursor, clamped
// rather than wrapping — a wheel that jumps from the last field to the
// first reads as a glitch, not as navigation.
func (f *formModal) scroll(delta int) {
	n := len(f.visibleFields())
	if n == 0 {
		return
	}
	f.cursor += delta
	if f.cursor > n-1 {
		f.cursor = n - 1
	}
	if f.cursor < 0 {
		f.cursor = 0
	}
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

// errorFor is fl's inline validation message, or "" while the message
// should stay hidden: an empty field is only judged once a submit was
// attempted, so required-field errors do not litter a fresh form.
func (f *formModal) errorFor(fl *formField) string {
	if fl.validate == nil {
		return ""
	}
	v := fl.value()
	if v == "" && !f.submitted {
		return ""
	}
	return fl.validate(f, v)
}

// firstInvalid is the visible-field index and message of the first field
// whose validator objects, or -1. Validator messages name their subject
// ("host is required"), so the message stands on its own in the error line.
func (f *formModal) firstInvalid() (int, string) {
	for i, fl := range f.visibleFields() {
		if fl.validate == nil {
			continue
		}
		if msg := fl.validate(f, fl.value()); msg != "" {
			return i, msg
		}
	}
	return -1, ""
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
		// While path candidates are up, tab completes the path — first to
		// the longest shared prefix, then (once that stops changing
		// anything) cycling through the candidates one at a time. ↑↓ stay
		// the way to walk fields regardless. With no candidates tab keeps
		// its usual meaning.
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
	case msg.String() == "shift+tab":
		// shift+tab reverses an in-progress tab-cycle; otherwise it keeps
		// its usual meaning of walking to the previous field.
		if sf := f.suggestField(); sf != nil && f.sugg.cycling {
			sf.input.SetValue(f.sugg.completeBack())
			sf.input.CursorEnd()
			return false, nil
		}
		f.move(-1)
		return false, nil
	case msg.String() == "up":
		f.move(-1)
		return false, nil
	case msg.String() == "enter", key.Matches(msg, m.keys.AcceptChanges):
		if f.onSubmit == nil {
			f.sugg.clear()
			return true, nil
		}
		f.err, f.info = "", ""
		f.submitted = true
		// Field validators gate the submit: the cursor jumps to the first
		// offender so the fix is one keystroke away, and every invalid
		// field is now marked inline (submitted covers the empty ones).
		if i, msg := f.firstInvalid(); i >= 0 {
			f.cursor = i
			f.sugg.clear()
			f.syncFocus()
			f.err = msg
			return false, nil
		}
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
	sectioned := false
	for _, fl := range vis {
		if w := lipgloss.Width(fl.label); w > labelW {
			labelW = w
		}
		if fl.section != "" {
			sectioned = true
		}
	}
	// The 16 covers the modal chrome, the cursor marker, the label gap and
	// the textinput's own prompt and cursor cell, which render outside its
	// width.
	inputW := min(38, maxInt(maxW-labelW-16, 12))
	// Sectioned forms indent their fields two cells under the headers, so
	// the grouping reads from the left edge alone.
	indent := ""
	if sectioned {
		indent = "  "
	}

	var bodyLines []string
	if f.body != nil {
		bodyLines = f.body(f)
	}

	// Count the headers before rendering: the suggestion budget needs the
	// field block's full height, and each header costs its own line plus
	// the blank one above it.
	headerRows := 0
	prev := ""
	for _, fl := range vis {
		if fl.section != "" && fl.section != prev {
			headerRows += 2
			prev = fl.section
		}
	}
	if headerRows > 0 {
		headerRows-- // the first header has no blank line above it
	}

	// Path suggestions only get the rows the terminal has left over: the
	// modal is centered on the full height, so growing past it would break
	// the tiny-terminal guard. 4 covers the box border and padding.
	sugRows := 0
	if sf := f.suggestField(); sf != nil && f.sugg.active() {
		used := 4 + 2 + len(vis) + headerRows + 2 // chrome, title+blank, block, blank+footer
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

	// A header underlines itself across the field block, so the group
	// boundary is a rule, not just a word.
	blockW := min(len(indent)+2+labelW+2+inputW, maxInt(maxW-8, 20))

	// The field block: headers, field lines, suggestion lines. cursorLine
	// remembers where the cursor's field landed so the scroll window can
	// follow it.
	var lines []string
	cursorLine := 0
	prev = ""
	for i, fl := range vis {
		if fl.section != "" && fl.section != prev {
			if len(lines) > 0 {
				lines = append(lines, "")
			}
			fill := maxInt(blockW-lipgloss.Width(fl.section)-1, 0)
			lines = append(lines, s.formSection.Render(fl.section)+" "+
				s.muted.Render(strings.Repeat("─", fill)))
			prev = fl.section
		}
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
		line := indent + marker + label + "  " + fl.display(s, i == f.cursor)
		// An invalid field is marked in place; under the cursor the mark
		// carries the message (displacing the help — the field needs
		// fixing before it needs explaining), elsewhere it is just ✗ so
		// the eye can find every offender at a glance.
		if msg := f.errorFor(fl); msg != "" {
			if i == f.cursor {
				line += "  " + s.danger.Render("✗ "+msg)
			} else {
				line += "  " + s.danger.Render("✗")
			}
		} else if fl.help != "" && i == f.cursor {
			line += "  " + s.muted.Render(fl.help)
		}
		if i == f.cursor {
			cursorLine = len(lines)
		}
		lines = append(lines, line)
		if fl.suggest && i == f.cursor {
			sindent := indent + strings.Repeat(" ", labelW+4)
			for _, sl := range f.sugg.lines(sugRows) {
				lines = append(lines, sindent+s.muted.Render(truncate(sl, maxInt(inputW, 8))))
			}
		}
	}

	// Scroll window: a sectioned form with SSH open outgrows a small
	// terminal, so the block is clipped to what fits and the window
	// follows the cursor. Clipped edges show ⋮ in place of their first or
	// last row.
	budget := maxH - 6 - len(bodyLines)
	if len(bodyLines) > 0 {
		budget--
	}
	if f.err != "" {
		budget -= 2
	}
	if f.info != "" {
		budget -= 2
	}
	if budget < 3 {
		budget = 3
	}
	if total := len(lines); total > budget {
		if f.offset > total-budget {
			f.offset = total - budget
		}
		if f.offset < 0 {
			f.offset = 0
		}
		if cursorLine <= f.offset {
			f.offset = maxInt(cursorLine-1, 0)
		}
		if cursorLine >= f.offset+budget-1 {
			f.offset = min(cursorLine-budget+2, total-budget)
		}
		win := append([]string(nil), lines[f.offset:f.offset+budget]...)
		if f.offset > 0 {
			win[0] = s.muted.Render(indent + "⋮")
		}
		if f.offset+budget < total {
			win[len(win)-1] = s.muted.Render(indent + "⋮")
		}
		lines = win
	} else {
		f.offset = 0
	}

	// Every line is clipped to what the modal can spend horizontally
	// (border and padding cost 6 cells), or a long help text or footer
	// would widen the box past a narrow terminal.
	lineW := maxInt(maxW-6, 16)
	var b strings.Builder
	b.WriteString(s.modalTitle.Render(truncate(f.title, lineW)) + "\n\n")
	for _, line := range bodyLines {
		b.WriteString(truncate(line, lineW) + "\n")
	}
	if len(bodyLines) > 0 {
		b.WriteString("\n")
	}
	for _, line := range lines {
		b.WriteString(truncate(line, lineW) + "\n")
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
		if f.sugg.cycling {
			footer = "tab/shift+tab cycle path · ↑↓ field · enter/ctrl+enter save · esc cancel"
		}
	}
	b.WriteString("\n" + s.muted.Render(truncate(footer, lineW)))
	return s.modal.Render(b.String())
}
