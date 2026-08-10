package ui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// The trigger view: `enter` on a trigger in the [2] Objects tree opens
// its definition in the main view. It is read-only, like the DDL tab —
// there is no row to browse and nothing to stage — so the only keys it
// answers to are the two that scroll it and the one that leaves.

// triggerView is one trigger's definition on screen.
type triggerView struct {
	database string
	name     string
	// table is the relation the trigger fires on, empty when the engine
	// does not report one.
	table string
	// req drops a reply for a trigger that is no longer open, the same
	// way dataView.req drops a stale page.
	req     int
	loading bool
	ddl     string
	err     string
	// offset is the first rendered line of the definition.
	offset int
}

// openTrigger is `enter` on a trigger node: it puts the definition on the
// main view and starts the read. Like every other catalog read it runs in
// a tea.Cmd, so the tree stays responsive while the server answers.
func (m *Model) openTrigger(n *treeNode) tea.Cmd {
	if n == nil {
		return nil
	}
	if m.driver == nil {
		return logCmd("-- open trigger %s skipped: not connected", n.name)
	}
	m.triggerReq++
	m.trigger = &triggerView{
		database: n.database,
		name:     n.name,
		table:    n.table,
		req:      m.triggerReq,
		loading:  true,
	}
	return loadTriggerDDLCmd(m.active, m.driver, n.database, n.name, m.triggerReq)
}

// applyTriggerDDL reduces the definition reply onto the open view.
func (m *Model) applyTriggerDDL(msg triggerDDLMsg) tea.Cmd {
	if m.trigger == nil || msg.conn != m.active ||
		msg.req != m.trigger.req || msg.name != m.trigger.name {
		return nil
	}
	v := *m.trigger
	v.loading = false
	if msg.err != nil {
		v.err = msg.err.Error()
		m.trigger = &v
		return logCmd("-- definition of trigger %s FAILED: %v", msg.name, msg.err)
	}
	v.ddl, v.err = msg.ddl, ""
	m.trigger = &v
	return nil
}

// updateTriggerKeys is the main view's key handler while a trigger is
// open. It runs instead of the grid's: none of the grid's actions have
// anything to act on here.
func (m Model) updateTriggerKeys(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := m.keys
	switch {
	case key.Matches(msg, k.Down):
		cmd := m.wheelAt(scrollTarget{zone: zoneMain}, 1)
		return m, cmd
	case key.Matches(msg, k.Up):
		cmd := m.wheelAt(scrollTarget{zone: zoneMain}, -1)
		return m, cmd
	case key.Matches(msg, k.Back):
		m.trigger = nil
		m.focusBack()
		return m, nil
	}
	return m, nil
}

// scrollTrigger moves the definition's window. Like the grid it has no
// scrollbar of its own: the offset is clamped when it is rendered.
func (m *Model) scrollTrigger(delta int) {
	if m.trigger == nil {
		return
	}
	v := *m.trigger
	v.offset += delta
	if v.offset < 0 {
		v.offset = 0
	}
	m.trigger = &v
}

// triggerTitle names the trigger in the main view's top border.
func (m Model) triggerTitle() string {
	v := m.trigger
	titleStyle := m.style.title
	if m.mainFocused() {
		titleStyle = m.style.titleFocused
	}
	line := titleStyle.Render("Trigger") +
		m.style.muted.Render(" "+displayDatabase(v.database)+".") + v.name
	if v.table != "" {
		line += m.style.muted.Render(" on " + v.table)
	}
	if v.loading {
		line += " " + m.style.pending.Render("loading…")
	}
	return line
}

// triggerContent renders the definition into the main view's content box.
func (m Model) triggerContent(w, h int) string {
	v := m.trigger
	switch {
	case v.err != "":
		return joinTruncated([]string{"", m.style.danger.Render(v.err)}, w, h)
	case v.loading:
		return joinTruncated(
			[]string{"", m.style.pending.Render("reading the trigger definition…")}, w, h)
	case strings.TrimSpace(v.ddl) == "":
		return joinTruncated(
			[]string{"", m.style.muted.Render("the engine reports no definition")}, w, h)
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimRight(v.ddl, "\n"), "\n") {
		lines = append(lines, strings.ReplaceAll(l, "\t", "    "))
	}
	out := scrollLines(lines, v.offset, w, maxInt(h-1, 0))
	out = append(out, m.style.keyHint.Render(truncate(
		"read-only · j/k scrolls · esc back", w)))
	return strings.Join(out, "\n")
}
