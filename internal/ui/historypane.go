package ui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"lazysql/internal/db"
	"lazysql/internal/history"
	"lazysql/internal/snippets"
	"lazysql/internal/sqlhl"
)

// paneSection is which half of the floating pane has the keyboard:
// the chronological history or the named snippets. `tab` toggles it.
type paneSection int

const (
	sectionHistory paneSection = iota
	sectionSnippets
	sectionCount
)

// historyModal is the floating query-history pane, opened with `H` (or
// `backspace`, the legacy alias) from the editor's normal mode. Like
// every other modal it renders from its own snapshot of the entries; a
// statement recorded while it is open shows up the next time it opens.
//
// Its keys are the keyMap's Hist* bindings — one source for dispatch,
// the footer and `?`. `enter` follows the app-wide drill-in reading and
// loads the selected statement into the editor; `r` executes it
// immediately through the same path as ctrl+r — including the
// placeholder prompt and the unguarded-write confirm. `d` deletes the
// entry, on disk too, and `s` saves it as a named snippet.
//
// `tab` switches to the Snippets section, which offers the same verbs
// over the named statements in internal/snippets.
type historyModal struct {
	entries []history.Entry
	snips   []snippets.Snippet
	dialect sqlhl.Dialect
	// keys is the shell's keyMap, carried in so the footer renders from
	// the same bindings update dispatches on — a `[keys]` override shows
	// up in both or in neither.
	keys    keyMap
	section paneSection
	// cursor and offset are per section, so switching back and forth
	// does not lose either list's position.
	cursor [sectionCount]int
	offset [sectionCount]int
}

func newHistoryModal(entries []history.Entry, snips []snippets.Snippet, d sqlhl.Dialect, k keyMap) *historyModal {
	return &historyModal{
		entries: append([]history.Entry(nil), entries...),
		snips:   append([]snippets.Snippet(nil), snips...),
		dialect: d,
		keys:    k,
	}
}

// rows is how many items the active section holds.
func (hm *historyModal) rowCount() int {
	if hm.section == sectionSnippets {
		return len(hm.snips)
	}
	return len(hm.entries)
}

// scroll is the wheel: it walks the active section's cursor, which is
// what the pane scrolls by — the offset is derived from it at render.
func (hm *historyModal) scroll(delta int) {
	cur := &hm.cursor[hm.section]
	*cur += delta
	if *cur >= hm.rowCount() {
		*cur = hm.rowCount() - 1
	}
	if *cur < 0 {
		*cur = 0
	}
}

func (hm *historyModal) update(msg tea.KeyPressMsg, m *Model) (bool, tea.Cmd) {
	cur := &hm.cursor[hm.section]
	k := m.keys
	switch {
	case msg.String() == "esc", msg.String() == "q", msg.String() == "backspace":
		return true, nil
	case key.Matches(msg, k.HistSection):
		hm.section = (hm.section + 1) % sectionCount
		return false, nil
	case key.Matches(msg, k.Down):
		if *cur < hm.rowCount()-1 {
			*cur++
		}
		return false, nil
	case key.Matches(msg, k.Up):
		if *cur > 0 {
			*cur--
		}
		return false, nil
	}
	switch msg.String() {
	case "g", "home":
		*cur = 0
		return false, nil
	case "G", "end":
		*cur = max(hm.rowCount()-1, 0)
		return false, nil
	}
	if hm.section == sectionSnippets {
		return hm.updateSnippets(msg, m)
	}
	switch {
	case key.Matches(msg, k.HistLoad):
		// enter drills in, the way it does everywhere else: the statement
		// lands in the editor, where running it is one visible key away.
		if e, ok := hm.selected(); ok {
			return true, m.loadIntoEditor(e.SQL)
		}
		return true, nil
	case key.Matches(msg, k.HistRun):
		// The immediate path, through the same guards as the editor's own
		// run: placeholder prompt, read-only vet, unguarded-write confirm.
		if e, ok := hm.selected(); ok {
			return true, m.submitQuery(e.SQL)
		}
		return true, nil
	case key.Matches(msg, k.HistSnippet):
		e, ok := hm.selected()
		if !ok {
			return false, nil
		}
		// The prompt replaces the pane: promptSaveSnippet sets the modal
		// itself, and the replacement rule keeps it there.
		return true, m.promptSaveSnippet(e.SQL)
	case key.Matches(msg, k.HistDelete):
		e, ok := hm.selected()
		if !ok {
			return false, nil
		}
		hm.entries = append(hm.entries[:*cur:*cur], hm.entries[*cur+1:]...)
		if *cur >= len(hm.entries) && *cur > 0 {
			*cur--
		}
		// The model's history is the source of truth the pane was
		// snapshotted from; the entry is matched by value because the
		// model may have recorded new statements since the snapshot.
		for i, me := range m.history {
			if me.SQL == e.SQL && me.Engine == e.Engine && me.Connection == e.Connection && me.At.Equal(e.At) {
				m.history = append(m.history[:i:i], m.history[i+1:]...)
				break
			}
		}
		return false, tea.Batch(
			saveHistoryCmd(m.history),
			logCmd("-- delete history entry: %s", truncate(flatten(e.SQL), 60)),
		)
	}
	return false, nil
}

func (hm *historyModal) selected() (history.Entry, bool) {
	c := hm.cursor[sectionHistory]
	if c < 0 || c >= len(hm.entries) {
		return history.Entry{}, false
	}
	return hm.entries[c], true
}

// ---------- the snippets section ----------

// updateSnippets is the same verbs as the history section over the
// named statements: load, run, delete — plus the confirm a delete of kept
// work deserves and a history entry's does not.
func (hm *historyModal) updateSnippets(msg tea.KeyPressMsg, m *Model) (bool, tea.Cmd) {
	sn, ok := hm.selectedSnippet()
	if !ok {
		return false, nil
	}
	k := m.keys
	switch {
	case key.Matches(msg, k.HistLoad):
		return true, m.loadIntoEditor(sn.SQL)
	case key.Matches(msg, k.HistRun):
		// The same path as ctrl+r, so a snippet with `?`/`:name`
		// placeholders prompts for its values and runs prepared.
		return true, m.submitQuery(sn.SQL)
	case key.Matches(msg, k.HistDelete):
		// The confirm replaces the pane; reopening it after the answer
		// would cost the snapshot, so the pane closes either way.
		m.modal = &confirmModal{
			title:  "Delete snippet",
			body:   fmt.Sprintf("Delete the snippet %q? Its statement is not kept anywhere else.", sn.Name),
			danger: true,
			onConfirm: func(mm *Model) tea.Cmd {
				return mm.deleteSnippet(sn.Name)
			},
		}
		return true, nil
	}
	return false, nil
}

func (hm *historyModal) selectedSnippet() (snippets.Snippet, bool) {
	c := hm.cursor[sectionSnippets]
	if c < 0 || c >= len(hm.snips) {
		return snippets.Snippet{}, false
	}
	return hm.snips[c], true
}

// ---------- rendering ----------

func (hm *historyModal) view(s styles, maxW, maxH int) string {
	width := min(maxW-8, 100)
	if width < 20 {
		width = 20
	}

	var b strings.Builder
	b.WriteString(hm.tabs(s) + "\n\n")

	if hm.rowCount() == 0 {
		empty := "no history yet — run a query first"
		if hm.section == sectionSnippets {
			empty = "no snippets yet — save one with ctrl+s in the editor"
		}
		b.WriteString(s.muted.Render(empty) + "\n")
		b.WriteString("\n" + s.muted.Render("tab switch section · esc close"))
		return s.modal.Render(b.String())
	}

	// The pane splits its rows between the list and the detail of the
	// selected entry: the list truncates every statement to one line, the
	// detail shows the selected one in full (clipped, not wrapped).
	content := maxH - 6 // borders, title, blank, footer
	detail := min(9, content/2)
	rows := content - detail
	if rows < 1 {
		rows = 1
	}
	if rows > hm.rowCount() {
		rows = hm.rowCount()
	}
	cursor, offset := hm.cursor[hm.section], hm.offset[hm.section]
	if cursor < offset {
		offset = cursor
	}
	if cursor >= offset+rows {
		offset = cursor - rows + 1
	}
	if maxOff := hm.rowCount() - rows; offset > maxOff {
		offset = maxOff
	}
	if offset < 0 {
		offset = 0
	}
	hm.offset[hm.section] = offset

	for i := offset; i < offset+rows; i++ {
		prefix, sql := hm.row(i)
		line := truncate(flatten(sql), width-lipgloss.Width(prefix))
		if i == cursor {
			b.WriteString(s.selected.Width(width).Render(prefix+line) + "\n")
			continue
		}
		// Truncate first, then highlight: styling then cutting would
		// slice an escape sequence in half.
		b.WriteString(s.muted.Render(prefix) + highlightSQL(s, hm.dialect, line) + "\n")
	}
	if hm.rowCount() > rows {
		b.WriteString(s.muted.Render(fmt.Sprintf("… %d more", hm.rowCount()-rows)) + "\n")
	}

	if detail > 2 {
		b.WriteString(hm.detail(s, width, detail))
	}

	b.WriteString("\n" + s.muted.Render(hm.footer()))
	return s.modal.Render(b.String())
}

// tabs is the pane's header: both section names, the active one titled.
func (hm *historyModal) tabs(s styles) string {
	names := [sectionCount]string{"Query history", "Snippets"}
	out := make([]string, 0, sectionCount)
	for i, n := range names {
		if paneSection(i) == hm.section {
			out = append(out, s.modalTitle.Render(n))
			continue
		}
		out = append(out, s.muted.Render(n))
	}
	return strings.Join(out, s.muted.Render("  ·  "))
}

// row is one list line of the active section: its fixed-width prefix
// (the time, or the snippet name) and the statement to render after it.
func (hm *historyModal) row(i int) (prefix, sql string) {
	if hm.section == sectionSnippets {
		sn := hm.snips[i]
		// The name column is fixed so the statements line up; padding is
		// by display width, not bytes, for a name with wide runes in it.
		name := truncate(sn.Name, 24)
		return name + strings.Repeat(" ", max(26-lipgloss.Width(name), 1)), sn.SQL
	}
	e := hm.entries[i]
	return e.At.Local().Format("15:04") + "  ", e.SQL
}

// detail renders the selected item below the list: where it came from and
// when, then its statement clipped to the remaining rows.
func (hm *historyModal) detail(s styles, width, height int) string {
	var meta, sql string
	if hm.section == sectionSnippets {
		sn, ok := hm.selectedSnippet()
		if !ok {
			return ""
		}
		engine := sn.Engine
		if engine == "" {
			engine = "(any engine)"
		}
		meta, sql = sn.Name+" · "+engine+" · saved "+sn.CreatedAt.Local().Format(time.RFC1123), sn.SQL
	} else {
		e, ok := hm.selected()
		if !ok {
			return ""
		}
		engine := e.Engine
		if engine == "" {
			engine = "(unknown)"
		}
		meta, sql = engine+" · "+e.At.Local().Format(time.RFC1123), e.SQL
	}

	var b strings.Builder
	b.WriteString("\n" + s.muted.Render(truncate(meta, width)) + "\n")
	lines := strings.Split(sql, "\n")
	shown := min(len(lines), height-2)
	for _, l := range lines[:shown] {
		b.WriteString(highlightSQL(s, hm.dialect, truncate(l, width)) + "\n")
	}
	if shown < len(lines) {
		b.WriteString(s.muted.Render(fmt.Sprintf("… %d more lines", len(lines)-shown)) + "\n")
	}
	return b.String()
}

func (hm *historyModal) footer() string {
	k := hm.keys
	pair := func(b key.Binding, verb string) string { return b.Help().Key + " " + verb }
	parts := []string{
		pair(k.HistLoad, "load into editor"),
		pair(k.HistRun, "run now"),
	}
	if hm.section != sectionSnippets {
		parts = append(parts, pair(k.HistSnippet, "save as snippet"))
	}
	other := "snippets"
	if hm.section == sectionSnippets {
		other = "history"
	}
	parts = append(parts,
		pair(k.HistDelete, "delete"),
		pair(k.HistSection, other),
		"esc close")
	return strings.Join(parts, " · ")
}

// ---------- placeholder prompt ----------

// paramsModal asks for one value per placeholder of a statement about to
// run: positional `?`s in order, each repeated `:name` once. On submit
// the values are handed back as strings and bound as parameters — they
// never enter the statement text.
type paramsModal struct {
	sql      string
	dialect  sqlhl.Dialect
	labels   []string
	inputs   []textinput.Model
	focus    int
	onSubmit func(m *Model, values []string) tea.Cmd
}

func newParamsModal(sql string, d sqlhl.Dialect, phs []db.Placeholder,
	onSubmit func(*Model, []string) tea.Cmd) *paramsModal {
	p := &paramsModal{sql: sql, dialect: d, onSubmit: onSubmit}
	for i, ph := range phs {
		ti := textinput.New()
		ti.SetWidth(40)
		if i == 0 {
			ti.Focus()
		}
		p.labels = append(p.labels, ph.Label)
		p.inputs = append(p.inputs, ti)
	}
	return p
}

// setFocus moves the input focus, blurring the rest so exactly one caret
// blinks.
func (p *paramsModal) setFocus(i int) {
	n := len(p.inputs)
	p.focus = ((i % n) + n) % n
	for j := range p.inputs {
		if j == p.focus {
			p.inputs[j].Focus()
		} else {
			p.inputs[j].Blur()
		}
	}
}

func (p *paramsModal) update(msg tea.KeyPressMsg, m *Model) (bool, tea.Cmd) {
	switch msg.String() {
	case "esc":
		// Cancel executes nothing.
		return true, nil
	case "tab", "down":
		p.setFocus(p.focus + 1)
		return false, nil
	case "shift+tab", "up":
		p.setFocus(p.focus - 1)
		return false, nil
	case "enter":
		// enter walks the inputs and submits from the last one, so a
		// single-placeholder prompt is type-and-enter.
		if p.focus < len(p.inputs)-1 {
			p.setFocus(p.focus + 1)
			return false, nil
		}
		if p.onSubmit == nil {
			return true, nil
		}
		values := make([]string, len(p.inputs))
		for i := range p.inputs {
			values[i] = p.inputs[i].Value()
		}
		return true, p.onSubmit(m, values)
	}
	var cmd tea.Cmd
	p.inputs[p.focus], cmd = p.inputs[p.focus].Update(msg)
	return false, cmd
}

// paste puts pasted text in the placeholder under the cursor — a value
// copied out of another tool is exactly what these fields are for.
func (p *paramsModal) paste(msg tea.PasteMsg, _ *Model) tea.Cmd {
	if p.focus < 0 || p.focus >= len(p.inputs) {
		return nil
	}
	var cmd tea.Cmd
	p.inputs[p.focus], cmd = p.inputs[p.focus].Update(msg)
	return cmd
}

func (p *paramsModal) view(s styles, maxW, maxH int) string {
	width := min(maxW-8, 70)
	if width < 20 {
		width = 20
	}
	var b strings.Builder
	b.WriteString(s.modalTitle.Render("Query parameters") + "\n\n")
	lines := strings.Split(p.sql, "\n")
	shown := min(len(lines), 5)
	for _, l := range lines[:shown] {
		b.WriteString(highlightSQL(s, p.dialect, truncate(l, width)) + "\n")
	}
	if shown < len(lines) {
		b.WriteString(s.muted.Render(fmt.Sprintf("… %d more lines", len(lines)-shown)) + "\n")
	}
	b.WriteString("\n")

	labelW := 0
	for _, l := range p.labels {
		if w := lipgloss.Width(l); w > labelW {
			labelW = w
		}
	}
	for i := range p.inputs {
		p.inputs[i].SetWidth(max(width-labelW-3, 10))
		label := fmt.Sprintf("%-*s", labelW, p.labels[i])
		if i == p.focus {
			label = s.keyHint.Render(label)
		} else {
			label = s.muted.Render(label)
		}
		b.WriteString(label + "  " + p.inputs[i].View() + "\n")
	}
	b.WriteString("\n" + s.muted.Render("enter next/run · tab next field · esc cancel"))
	return s.modal.Render(b.String())
}
