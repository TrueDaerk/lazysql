package ui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"lazysql/internal/db"
	"lazysql/internal/history"
	"lazysql/internal/sqlhl"
)

// historyModal is the floating query-history pane, opened with
// `backspace` from the editor's normal mode. Like every other modal it
// renders from its own snapshot of the entries; a statement recorded
// while it is open shows up the next time it opens.
//
// `enter` executes the selected entry through the same path as ctrl+r in
// the editor — including the placeholder prompt and the unguarded-write
// confirm — and `e` loads it into the editor instead. `d` deletes the
// entry, on disk too.
type historyModal struct {
	entries []history.Entry
	dialect sqlhl.Dialect
	cursor  int
	offset  int
}

func newHistoryModal(entries []history.Entry, d sqlhl.Dialect) *historyModal {
	return &historyModal{entries: append([]history.Entry(nil), entries...), dialect: d}
}

func (hm *historyModal) update(msg tea.KeyPressMsg, m *Model) (bool, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", "backspace":
		return true, nil
	case "down", "j":
		if hm.cursor < len(hm.entries)-1 {
			hm.cursor++
		}
	case "up", "k":
		if hm.cursor > 0 {
			hm.cursor--
		}
	case "g", "home":
		hm.cursor = 0
	case "G", "end":
		hm.cursor = max(len(hm.entries)-1, 0)
	case "enter":
		if e, ok := hm.selected(); ok {
			return true, m.submitQuery(e.SQL)
		}
		return true, nil
	case "e":
		if e, ok := hm.selected(); ok {
			return true, m.loadIntoEditor(e.SQL)
		}
		return true, nil
	case "d":
		e, ok := hm.selected()
		if !ok {
			return false, nil
		}
		hm.entries = append(hm.entries[:hm.cursor:hm.cursor], hm.entries[hm.cursor+1:]...)
		if hm.cursor >= len(hm.entries) && hm.cursor > 0 {
			hm.cursor--
		}
		// The model's history is the source of truth the pane was
		// snapshotted from; the entry is matched by value because the
		// model may have recorded new statements since the snapshot.
		for i, me := range m.history {
			if me.SQL == e.SQL && me.Engine == e.Engine && me.At.Equal(e.At) {
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
	if hm.cursor < 0 || hm.cursor >= len(hm.entries) {
		return history.Entry{}, false
	}
	return hm.entries[hm.cursor], true
}

func (hm *historyModal) view(s styles, maxW, maxH int) string {
	width := min(maxW-8, 100)
	if width < 20 {
		width = 20
	}

	var b strings.Builder
	b.WriteString(s.modalTitle.Render("Query history") + "\n\n")

	if len(hm.entries) == 0 {
		b.WriteString(s.muted.Render("no history yet — run a query first") + "\n")
		b.WriteString("\n" + s.muted.Render("esc close"))
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
	if rows > len(hm.entries) {
		rows = len(hm.entries)
	}
	if hm.cursor < hm.offset {
		hm.offset = hm.cursor
	}
	if hm.cursor >= hm.offset+rows {
		hm.offset = hm.cursor - rows + 1
	}
	if maxOff := len(hm.entries) - rows; hm.offset > maxOff {
		hm.offset = maxOff
	}
	if hm.offset < 0 {
		hm.offset = 0
	}

	for i := hm.offset; i < hm.offset+rows; i++ {
		e := hm.entries[i]
		stamp := e.At.Local().Format("15:04") + "  "
		line := truncate(flatten(e.SQL), width-lipgloss.Width(stamp))
		if i == hm.cursor {
			b.WriteString(s.selected.Width(width).Render(stamp+line) + "\n")
			continue
		}
		// Truncate first, then highlight: styling then cutting would
		// slice an escape sequence in half.
		b.WriteString(s.muted.Render(stamp) + highlightSQL(s, hm.dialect, line) + "\n")
	}
	if len(hm.entries) > rows {
		b.WriteString(s.muted.Render(fmt.Sprintf("… %d more", len(hm.entries)-rows)) + "\n")
	}

	if e, ok := hm.selected(); ok && detail > 2 {
		engine := e.Engine
		if engine == "" {
			engine = "(unknown)"
		}
		b.WriteString("\n" + s.muted.Render(truncate(
			engine+" · "+e.At.Local().Format(time.RFC1123), width)) + "\n")
		lines := strings.Split(e.SQL, "\n")
		shown := min(len(lines), detail-2)
		for _, l := range lines[:shown] {
			b.WriteString(highlightSQL(s, hm.dialect, truncate(l, width)) + "\n")
		}
		if shown < len(lines) {
			b.WriteString(s.muted.Render(fmt.Sprintf("… %d more lines", len(lines)-shown)) + "\n")
		}
	}

	b.WriteString("\n" + s.muted.Render("enter run · e load into editor · d delete · esc close"))
	return s.modal.Render(b.String())
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
