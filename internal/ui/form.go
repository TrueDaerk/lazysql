package ui

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// formModal is the reusable multi-field popup: a vertical stack of labelled
// fields with one cursor, optional section headers, an inline validation
// error line, a reserved status line, a cursor-following scroll window and
// the usual enter/esc contract. Its outer size is pinned for the lifetime
// of one field set, see formLayout. The connection editor is its main user.
// (The row insert form
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

	// anchor* is where the value cell of the completing field landed in the
	// box view last rendered, relative to that box's own top-left cell, plus
	// the text width the field's input occupies. The candidate list is a
	// layer composited in *screen* coordinates (see pathSuggestLayer), and
	// the centered box has no idea where it ended up — so view records the
	// offset while it still knows it. anchorOK is false whenever there is
	// nothing to anchor: no completing field, no candidates, or a scroll
	// window that clipped the field's own row away.
	anchorX, anchorY, anchorW int
	anchorOK                  bool

	// offset is the first body line the scroll window shows. A sectioned
	// form can outgrow a small terminal; view keeps the cursor's line
	// inside the window and marks clipped rows with ⋮.
	offset int

	// layout pins the box geometry, see formLayout.
	layout formLayout
}

// formLayout is the outer geometry the box is pinned to. A popup that
// changes size while the user types reads as broken, so width and the
// reserved row counts are computed once from the worst case — the widest
// line any transient content could produce, one status line, the whole
// field block — and every draw renders *into* that frame: content that is
// absent leaves blank cells instead of shrinking the box. Only a change the
// user made deliberately invalidates it: a different set of visible fields
// (the SSH section unfolding, an engine swap) or a resized terminal.
type formLayout struct {
	// key is the state the geometry was derived from; a draw whose key
	// differs recomputes, every other draw reuses.
	key       string
	contentW  int // cells every line is padded and clipped to
	bodyRows  int // rows reserved for the body block
	fieldRows int // rows reserved for the field block (the scroll window)
}

// formMsgWidth is the column reserved next to the field under the cursor for
// its help text or its inline validation message. Both are clipped to it
// rather than allowed to widen the box: the column is what a message costs,
// whatever it says. Nothing is lost that matters — a help text is a hint,
// and the message that blocks a submit shows in full in the status line.
const formMsgWidth = 32

// formBodyWidth is the width a form with a body block claims. The block is
// free-form text that tracks what is typed (the dump/restore command
// preview), so it cannot be measured once: pinning the box to its first
// draw would clip every later, longer command. It gets a generous fixed
// column instead, capped by the terminal.
const formBodyWidth = 100

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
		// While path candidates are up, the first esc only dismisses the
		// list — it is the thing on screen the user is most likely trying
		// to back out of. A second esc (list already gone) cancels the form
		// as usual.
		if sf := f.suggestField(); sf != nil && f.sugg.active() {
			f.sugg.clear()
			return false, nil
		}
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
		// While path candidates are up, ↓ walks the candidate list instead
		// of moving to the next field — the field navigation users expect
		// tab, not the arrows, to still own.
		if sf := f.suggestField(); sf != nil && f.sugg.active() {
			sf.input.SetValue(f.sugg.navigate(1))
			sf.input.CursorEnd()
			return false, nil
		}
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
		if sf := f.suggestField(); sf != nil && f.sugg.active() {
			sf.input.SetValue(f.sugg.navigate(-1))
			sf.input.CursorEnd()
			return false, nil
		}
		f.move(-1)
		return false, nil
	case msg.String() == "enter":
		// While path candidates are up, enter accepts the highlighted one
		// into the field instead of submitting the form — the form only
		// submits once the list is gone, same shape as esc above.
		if sf := f.suggestField(); sf != nil && f.sugg.active() {
			sf.input.SetValue(f.sugg.accept())
			sf.input.CursorEnd()
			return false, nil
		}
		fallthrough
	case key.Matches(msg, m.keys.AcceptChanges):
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

// valueWidth is the widest the field's value cell can get. It is a worst
// case, not a measurement of the current value: a select is as wide as its
// longest choice, so stepping through the choices cannot move the box.
func (fl *formField) valueWidth(inputW int) int {
	switch fl.kind {
	case fieldSelect:
		w := 0
		for _, c := range fl.choices {
			w = maxInt(w, lipgloss.Width(c))
		}
		return w + 4 // the ‹ › chevrons
	case fieldBool:
		return lipgloss.Width("[x] yes")
	default:
		// The textinput renders its prompt and a cursor cell outside the
		// width it was given.
		return inputW + 2
	}
}

// footerWidth is the widest footer the form can show. The completion
// contract swaps the footer while candidates are up, which is a keystroke
// away in any path field — so all three spellings count towards the width.
func (f *formModal) footerWidth() int {
	w := 0
	for _, t := range []string{
		f.footer,
		"tab/↑↓ field · ←→ change · enter/ctrl+enter save · esc cancel",
		"tab complete path · ↑↓ select · enter accept · ctrl+enter save · esc dismiss",
		"tab/shift+tab cycle path · ↑↓ select · enter accept · ctrl+enter save · esc dismiss",
	} {
		w = maxInt(w, lipgloss.Width(t))
	}
	return w
}

// computeLayout derives the pinned geometry for the current field set.
// fieldRows is the number of rows the unclipped field block occupies.
func (f *formModal) computeLayout(key string, vis []*formField, labelW, inputW, indent, bodyRows, fieldLines, maxW, maxH int) formLayout {
	// Border and padding cost 6 cells; nothing may render wider than that.
	lineW := maxInt(maxW-6, 16)

	w := maxInt(lipgloss.Width(f.title), f.footerWidth())
	for _, fl := range vis {
		w = maxInt(w, indent+2+labelW+2+fl.valueWidth(inputW)+2+formMsgWidth)
	}
	if bodyRows > 0 {
		w = maxInt(w, formBodyWidth)
	}
	w = min(w, lineW)

	// Rows the frame spends outside the field block: two border rows, the
	// title and its blank line, the body block and its blank line, the
	// reserved status line with its blank line, and the footer with its own.
	budget := maxH - 8 - bodyRows
	if bodyRows > 0 {
		budget--
	}
	if budget < 3 {
		budget = 3
	}
	return formLayout{
		key:       key,
		contentW:  w,
		bodyRows:  bodyRows,
		fieldRows: min(fieldLines, budget),
	}
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

	// Path candidates float over the box instead of being rows in it (see
	// pathsuggest.go), so nothing here reserves height for them — the form's
	// layout is the same whether or not the list is up.
	suggesting := false
	if sf := f.suggestField(); sf != nil && f.sugg.active() {
		suggesting = true
	}

	// A header underlines itself across the field block, so the group
	// boundary is a rule, not just a word.
	blockW := min(len(indent)+2+labelW+2+inputW, maxInt(maxW-8, 20))

	// The field block: headers and field lines. cursorLine remembers where
	// the cursor's field landed so the scroll window can follow it — and,
	// once the window is known, so the candidate list can be anchored to it.
	var lines []string
	cursorLine := 0
	prev := ""
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
	}

	// The pinned geometry: recomputed only when the visible field set or
	// the terminal changed, so nothing typed into a field can move the box.
	key := layoutKey(f.title, f.footer, vis, maxW, maxH)
	if f.layout.key != key {
		f.layout = f.computeLayout(key, vis, labelW, inputW, lipgloss.Width(indent),
			len(bodyLines), len(lines), maxW, maxH)
	}

	// Scroll window: a sectioned form with SSH open outgrows a small
	// terminal, so the block is clipped to what fits and the window
	// follows the cursor. Clipped edges show ⋮ in place of their first or
	// last row.
	budget := f.layout.fieldRows
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

	// Anchor the candidate list on the value cell of the field the cursor is
	// on, in coordinates relative to the box this call returns: the modal
	// frame, the title and its blank line, the optional body block, then the
	// field's row inside the scroll window. A field scrolled out of the
	// window has no cell to point at, so the overlay is dropped instead of
	// floating over an unrelated row.
	f.anchorOK = false
	if suggesting {
		lead := 2 + len(bodyLines) // title + blank
		if len(bodyLines) > 0 {
			lead++ // the blank line under the body block
		}
		row := cursorLine - f.offset
		if row >= 0 && row < len(lines) {
			f.anchorX = s.modal.GetBorderLeftSize() + s.modal.GetPaddingLeft() +
				lipgloss.Width(indent) + 2 + labelW + 2
			f.anchorY = s.modal.GetBorderTopSize() + s.modal.GetPaddingTop() + lead + row
			f.anchorW = inputW
			f.anchorOK = true
		}
	}

	// Every line is clipped to the pinned width and padded back out to it,
	// so the box measures the same however much (or little) each line
	// carries — a long help text cannot widen it, an empty one cannot
	// narrow it.
	lineW := f.layout.contentW
	fit := func(line string) string { return pad(truncate(line, lineW), lineW) }
	var b strings.Builder
	b.WriteString(fit(s.modalTitle.Render(f.title)) + "\n\n")
	// The body block keeps its reserved rows whatever it returns today:
	// short of them it is padded out, past them it is cut.
	for i := 0; i < f.layout.bodyRows; i++ {
		line := ""
		if i < len(bodyLines) {
			line = bodyLines[i]
		}
		b.WriteString(fit(line) + "\n")
	}
	if f.layout.bodyRows > 0 {
		b.WriteString("\n")
	}
	for i := 0; i < f.layout.fieldRows; i++ {
		line := ""
		if i < len(lines) {
			line = lines[i]
		}
		b.WriteString(fit(line) + "\n")
	}
	// One status line, always reserved: an error, otherwise an info
	// message, otherwise blank. Showing one must not push the footer down.
	status := ""
	switch {
	case f.err != "":
		status = s.danger.Render("✗ " + f.err)
	case f.info != "":
		status = s.titleFocused.Render(f.info)
	}
	b.WriteString("\n" + fit(status) + "\n")
	footer := f.footer
	if footer == "" {
		footer = "tab/↑↓ field · ←→ change · enter/ctrl+enter save · esc cancel"
	}
	if suggesting {
		// tab and ↑↓ are taken by completion here, so the bar must stop
		// advertising them as the way to move between fields — and enter/esc
		// act on the list first, so their usual save/cancel meaning gets a
		// ctrl+enter mention instead.
		footer = "tab complete path · ↑↓ select · enter accept · ctrl+enter save · esc dismiss"
		if f.sugg.cycling {
			footer = "tab/shift+tab cycle path · ↑↓ select · enter accept · ctrl+enter save · esc dismiss"
		}
	}
	b.WriteString("\n" + fit(s.muted.Render(footer)))
	return s.modal.Render(b.String())
}

// layoutKey is the state the pinned geometry depends on: the terminal, the
// title and footer, and which fields are visible with which labels. Nothing
// in it changes from typing — engine-dependent fields appearing or
// disappearing is a deliberate content change and may resize the box.
func layoutKey(title, footer string, vis []*formField, maxW, maxH int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%dx%d|%s|%s|", maxW, maxH, title, footer)
	for _, fl := range vis {
		b.WriteString(fl.name + ":" + fl.label + ",")
	}
	return b.String()
}
