package ui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"lazysql/internal/db"
)

// modal is the interface every popup implements. While a modal is open the
// root model routes all key input here and nothing reaches the panels.
//
// update reports whether the modal should close; any resulting work is
// returned as a tea.Cmd so the root reduces it like any other message.
type modal interface {
	update(msg tea.KeyPressMsg, m *Model) (close bool, cmd tea.Cmd)
	view(s styles, maxW, maxH int) string
}

// ---------- confirm ----------

type confirmModal struct {
	title     string
	body      string
	danger    bool
	onConfirm func(m *Model) tea.Cmd
}

func (c *confirmModal) update(msg tea.KeyPressMsg, m *Model) (bool, tea.Cmd) {
	switch msg.String() {
	case "enter", "y":
		if c.onConfirm == nil {
			return true, nil
		}
		return true, c.onConfirm(m)
	case "esc", "n", "q":
		return true, nil
	}
	return false, nil
}

func (c *confirmModal) view(s styles, maxW, maxH int) string {
	box := s.modal
	if c.danger {
		box = box.BorderForeground(colorDeleted)
	}
	body := lipgloss.NewStyle().Width(min(maxW-6, 60)).Render(c.body)
	return box.Render(lipgloss.JoinVertical(lipgloss.Left,
		s.modalTitle.Render(c.title),
		"",
		body,
		"",
		s.muted.Render("enter confirm · esc cancel"),
	))
}

// ---------- menu ----------

type menuEntry struct {
	key    string
	label  string
	action func(m *Model) tea.Cmd
}

type menuModal struct {
	title   string
	entries []menuEntry
	cursor  int
	offset  int
}

func (mm *menuModal) update(msg tea.KeyPressMsg, m *Model) (bool, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		return true, nil
	case "down", "j":
		if mm.cursor < len(mm.entries)-1 {
			mm.cursor++
		}
	case "up", "k":
		if mm.cursor > 0 {
			mm.cursor--
		}
	case "enter":
		if mm.cursor < 0 || mm.cursor >= len(mm.entries) {
			return true, nil
		}
		e := mm.entries[mm.cursor]
		if e.action == nil {
			return true, nil
		}
		return true, e.action(m)
	default:
		for _, e := range mm.entries {
			if e.key != "" && e.key == msg.String() && e.action != nil {
				return true, e.action(m)
			}
		}
	}
	return false, nil
}

func (mm *menuModal) view(s styles, maxW, maxH int) string {
	rows := maxH - 6 // title, blank, footer, borders
	if rows < 1 {
		rows = 1
	}
	if rows > len(mm.entries) {
		rows = len(mm.entries)
	}
	if mm.cursor < mm.offset {
		mm.offset = mm.cursor
	}
	if mm.cursor >= mm.offset+rows {
		mm.offset = mm.cursor - rows + 1
	}
	if maxOff := len(mm.entries) - rows; mm.offset > maxOff {
		mm.offset = maxOff
	}
	if mm.offset < 0 {
		mm.offset = 0
	}

	width := lipgloss.Width(mm.title)
	for _, e := range mm.entries {
		if w := lipgloss.Width(e.key) + 2 + lipgloss.Width(e.label); w > width {
			width = w
		}
	}
	width = min(width, maxW-6)

	var b strings.Builder
	b.WriteString(s.modalTitle.Render(mm.title) + "\n\n")
	for i := mm.offset; i < mm.offset+rows && i < len(mm.entries); i++ {
		e := mm.entries[i]
		label := truncate(e.label, width-5)
		line := s.keyHint.Render(fmt.Sprintf("%-4s", e.key)) + " " + label
		if i == mm.cursor {
			line = s.selected.Width(width).Render(fmt.Sprintf("%-4s %s", e.key, label))
		}
		b.WriteString(line + "\n")
	}
	if len(mm.entries) > rows {
		b.WriteString(s.muted.Render(fmt.Sprintf("… %d more\n", len(mm.entries)-rows)))
	}
	b.WriteString("\n" + s.muted.Render("enter select · esc cancel"))
	return s.modal.Render(b.String())
}

// ---------- prompt ----------

type promptModal struct {
	title    string
	input    textinput.Model
	onSubmit func(m *Model, value string) tea.Cmd
}

func newPromptModal(title, placeholder, initial string, onSubmit func(*Model, string) tea.Cmd) *promptModal {
	ti := textinput.New()
	ti.Placeholder = placeholder
	ti.SetValue(initial)
	ti.CursorEnd()
	ti.Focus()
	ti.SetWidth(40)
	return &promptModal{title: title, input: ti, onSubmit: onSubmit}
}

func (p *promptModal) update(msg tea.KeyPressMsg, m *Model) (bool, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return true, nil
	case "enter":
		if p.onSubmit == nil {
			return true, nil
		}
		return true, p.onSubmit(m, strings.TrimSpace(p.input.Value()))
	}
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	return false, cmd
}

func (p *promptModal) view(s styles, maxW, maxH int) string {
	p.input.SetWidth(min(50, maxW-8))
	return s.modal.Render(lipgloss.JoinVertical(lipgloss.Left,
		s.modalTitle.Render(p.title),
		"",
		p.input.View(),
		"",
		s.muted.Render("enter submit · esc cancel"),
	))
}

// ---------- cell ----------

// cellModal is `v`: the full, scrollable value of the cell under the
// cursor, which the grid can only show truncated at maxColWidth. Content
// decides its own rendering — classifyCell picks pretty-printed JSON, a
// hex dump for invalid UTF-8 (a BLOB), or the raw text otherwise — but
// `y` always copies the untransformed value, never the rendered form.
type cellModal struct {
	title  string
	lines  []string
	offset int

	rawText      string // exactly what `y` copies
	copySubject  string
	copyFilename string
}

// newCellModal builds the popup for one cell. subject is the table (or
// "query") and column names it, the way the copy menu's log lines do;
// colType is the column's declared SQL type and falls back to the
// detected content kind when the driver did not report one (a free-form
// query result can leave DatabaseTypeName empty).
func newCellModal(subject, column string, colType string, value any) *cellModal {
	label := column
	if subject != "" && column != "" {
		label = subject + "." + column
	} else if subject != "" {
		label = subject
	}
	if label == "" {
		label = "cell"
	}
	copySubject := "cell " + label
	copyFilename := label + ".txt"

	if value == nil {
		return &cellModal{
			title:        label + " — NULL",
			lines:        []string{nullText},
			copySubject:  copySubject + " (NULL → empty)",
			copyFilename: copyFilename,
		}
	}

	raw := db.FormatValue(value, nullText)
	var text, kindLabel string
	switch classifyCell(raw) {
	case cellJSON:
		text, kindLabel = prettyJSON(raw), "json"
	case cellBinary:
		text, kindLabel = strings.Join(hexDumpLines(raw), "\n"), "binary"
	default:
		text, kindLabel = raw, "text"
	}
	typ := colType
	if typ == "" {
		typ = kindLabel
	}
	title := fmt.Sprintf("%s — %s, %s", label, typ, byteCount(len(raw)))
	// The declared column type (e.g. "text", "blob") does not always say
	// what the bytes actually hold; the detected kind is worth a second
	// word whenever it adds information the type did not already give.
	if kindLabel == "json" || kindLabel == "binary" {
		title += " (" + kindLabel + ")"
	}

	return &cellModal{
		title:        title,
		lines:        strings.Split(text, "\n"),
		rawText:      raw,
		copySubject:  copySubject,
		copyFilename: copyFilename,
	}
}

func (c *cellModal) update(msg tea.KeyPressMsg, m *Model) (bool, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", "enter", "v":
		return true, nil
	case "y":
		return false, copyTextCmd(c.copySubject, c.copyFilename, c.rawText)
	case "down", "j":
		c.offset++
	case "up", "k":
		c.offset--
	case "ctrl+d", "pgdown", "ctrl+f":
		c.offset += 10
	case "ctrl+u", "pgup", "ctrl+b":
		c.offset -= 10
	case "g", "home":
		c.offset = 0
	case "G", "end":
		c.offset = len(c.lines)
	}
	return false, nil
}

func (c *cellModal) view(s styles, maxW, maxH int) string {
	width := min(maxW-8, 100)
	if width < 8 {
		width = 8
	}
	rows := maxH - 6
	if rows < 1 {
		rows = 1
	}
	if rows > len(c.lines) {
		rows = len(c.lines)
	}
	if maxOff := len(c.lines) - rows; c.offset > maxOff {
		c.offset = maxOff
	}
	if c.offset < 0 {
		c.offset = 0
	}

	var b strings.Builder
	b.WriteString(s.modalTitle.Render(truncate(c.title, width)) + "\n\n")
	for _, line := range c.lines[c.offset : c.offset+rows] {
		b.WriteString(truncate(line, width) + "\n")
	}
	footer := "y copy · esc close"
	if len(c.lines) > rows {
		footer = fmt.Sprintf("lines %d–%d of %d · ↑/↓ scroll · y copy · esc close",
			c.offset+1, c.offset+rows, len(c.lines))
	}
	b.WriteString("\n" + s.muted.Render(footer))
	return s.modal.Render(b.String())
}

// ---------- help ----------

// helpModal renders the focused panel's bindings — the same slices the
// options bar is built from.
type helpModal struct {
	title  string
	groups [][]key.Binding
	help   help.Model
}

func newHelpModal(title string, groups [][]key.Binding) *helpModal {
	h := help.New()
	h.ShowAll = true
	return &helpModal{title: title, groups: groups, help: h}
}

func (hm *helpModal) update(msg tea.KeyPressMsg, m *Model) (bool, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", "?", "enter":
		return true, nil
	}
	return false, nil
}

func (hm *helpModal) view(s styles, maxW, maxH int) string {
	hm.help.SetWidth(maxW - 6)
	groups := make([][]key.Binding, 0, len(hm.groups))
	for _, g := range hm.groups {
		if len(g) > 0 {
			groups = append(groups, g)
		}
	}
	return s.modal.Render(lipgloss.JoinVertical(lipgloss.Left,
		s.modalTitle.Render(hm.title),
		"",
		hm.help.FullHelpView(groups),
		"",
		s.muted.Render("esc close"),
	))
}

// ---------- command log ----------

// commandLogModal is `@`'s expanded, scrollable view of the command log:
// a snapshot of every entry taken when it opened, full untruncated SQL
// text and all — the slim strip under the main view truncates to fit,
// this does not. Like every other modal it renders from its own state,
// so a statement that lands while it is open shows up the next time `@`
// reopens it, not live.
type commandLogModal struct {
	lines  []logLine
	offset int
}

func newCommandLogModal(lines []logLine) *commandLogModal {
	return &commandLogModal{lines: lines, offset: max(len(lines)-1, 0)}
}

func (c *commandLogModal) update(msg tea.KeyPressMsg, m *Model) (bool, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", "@":
		return true, nil
	case "down", "j":
		c.offset++
	case "up", "k":
		c.offset--
	case "pgdown", "ctrl+f":
		c.offset += 10
	case "pgup", "ctrl+b":
		c.offset -= 10
	case "g", "home":
		c.offset = 0
	case "G", "end":
		c.offset = len(c.lines)
	}
	return false, nil
}

func (c *commandLogModal) view(s styles, maxW, maxH int) string {
	width := min(maxW-8, 160)
	if width < 20 {
		width = 20
	}
	rows := maxH - 6
	if rows < 1 {
		rows = 1
	}
	if rows > len(c.lines) {
		rows = len(c.lines)
	}
	if maxOff := len(c.lines) - rows; c.offset > maxOff {
		c.offset = maxOff
	}
	if c.offset < 0 {
		c.offset = 0
	}

	var b strings.Builder
	b.WriteString(s.modalTitle.Render("Command log") + "\n\n")
	for _, line := range c.lines[c.offset : c.offset+rows] {
		text := truncate(line.render(), width)
		if line.err {
			b.WriteString(s.danger.Render(text) + "\n")
		} else {
			b.WriteString(text + "\n")
		}
	}
	footer := "esc close"
	if len(c.lines) > rows {
		footer = fmt.Sprintf("lines %d–%d of %d · ↑/↓ scroll · esc close",
			c.offset+1, c.offset+rows, len(c.lines))
	}
	b.WriteString("\n" + s.muted.Render(footer))
	return s.modal.Render(b.String())
}
