package ui

import (
	"fmt"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// Minimum usable terminal. Below this the layout math produces negative
// widths, so we render a "too small" notice instead.
const (
	minWidth  = 60
	minHeight = 18
)

// screenMode mirrors lazygit's `+`/`_` cycling: how much room the focused
// side panel gets relative to the rest of the layout.
type screenMode int

const (
	screenNormal screenMode = iota
	screenHalf
	screenFull
	screenModeCount
)

var screenModeNames = [screenModeCount]string{"normal", "half", "full"}

// ---------- messages ----------

// Panels emit domain messages instead of mutating shared state; the root
// model reduces them. Real DB work will arrive the same way, from tea.Cmds.

// commandLogMsg appends a line to the command log under the main view.
type commandLogMsg struct{ line string }

func logCmd(format string, a ...any) tea.Cmd {
	line := fmt.Sprintf(format, a...)
	return func() tea.Msg { return commandLogMsg{line: line} }
}

// focusPanelMsg moves focus to a panel.
type focusPanelMsg struct{ id panelID }

func focusCmd(id panelID) tea.Cmd {
	return func() tea.Msg { return focusPanelMsg{id: id} }
}

// historyEntryMsg records a statement in the query history panel.
type historyEntryMsg struct{ statement string }

// ---------- root model ----------

// Model is the single root model: it owns terminal size, focus, one child
// model per side panel, main view state, the open modal (nil = none) and the
// command log.
type Model struct {
	width, height int

	focus  panelID
	panels [panelCount]*sidePanel
	prev   []panelID // focus stack for `esc`

	modal  modal
	screen screenMode

	commandLog []string

	keys  keyMap
	help  help.Model
	style styles
}

// New builds the shell with placeholder content; real data arrives later via
// the driver layer.
func New() Model {
	m := Model{
		focus: panelConnections,
		keys:  newKeyMap(),
		help:  help.New(),
		style: newStyles(),
	}
	m.panels[panelConnections] = &sidePanel{id: panelConnections, items: []string{
		"local-postgres", "local-mysql", "analytics.duckdb", "notes.sqlite",
	}}
	m.panels[panelDatabases] = &sidePanel{id: panelDatabases, items: []string{
		"postgres", "app_dev", "app_test",
	}}
	m.panels[panelTables] = &sidePanel{id: panelTables, items: []string{
		"users", "accounts", "sessions", "audit_log",
	}}
	m.panels[panelHistory] = &sidePanel{id: panelHistory}
	return m
}

func (m Model) Init() tea.Cmd { return nil }

// Update routes in a fixed order: WindowSizeMsg → open modal (swallows all
// keys) → global keys → focused panel.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case commandLogMsg:
		m.commandLog = append(m.commandLog, msg.line)
		if n := len(m.commandLog); n > 200 {
			m.commandLog = m.commandLog[n-200:]
		}
		return m, nil

	case historyEntryMsg:
		h := m.panels[panelHistory]
		h.setItems(append(h.items, msg.statement))
		return m, nil

	case focusPanelMsg:
		m.setFocus(msg.id)
		return m, nil

	case tea.KeyPressMsg:
		// 1. A modal swallows every key. esc always cancels.
		if m.modal != nil {
			cur := m.modal
			shouldClose, cmd := cur.update(msg, &m)
			// Only clear if the handler didn't open a replacement modal.
			if shouldClose && m.modal == cur {
				m.modal = nil
			}
			return m, cmd
		}
		// 2. Global keys.
		if handled, mm, cmd := m.updateGlobal(msg); handled {
			return mm, cmd
		}
		// 3. Focused panel.
		return m.updateFocused(msg)
	}
	return m, nil
}

func (m Model) updateGlobal(msg tea.KeyPressMsg) (bool, tea.Model, tea.Cmd) {
	k := m.keys
	switch {
	case key.Matches(msg, k.Quit):
		return true, m, tea.Quit

	case key.Matches(msg, k.Help):
		m.modal = newHelpModal(
			"Keybindings — "+panelTitles[m.focus],
			k.helpGroups(m.focus),
		)
		return true, m, nil

	case key.Matches(msg, k.Jump):
		if n := int(msg.String()[0] - '1'); n >= 0 && n < int(panelCount) {
			m.setFocus(panelID(n))
		}
		return true, m, nil

	case key.Matches(msg, k.NextPanel):
		m.setFocus((m.focus + 1) % panelCount)
		return true, m, nil

	case key.Matches(msg, k.PrevPanel):
		m.setFocus((m.focus + panelCount - 1) % panelCount)
		return true, m, nil

	case key.Matches(msg, k.ScreenNext):
		m.screen = (m.screen + 1) % screenModeCount
		return true, m, nil

	case key.Matches(msg, k.ScreenPrev):
		m.screen = (m.screen + screenModeCount - 1) % screenModeCount
		return true, m, nil
	}
	return false, m, nil
}

func (m Model) updateFocused(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := m.keys
	p := m.panels[m.focus]

	switch {
	case key.Matches(msg, k.Down):
		p.move(1)
		return m, nil
	case key.Matches(msg, k.Up):
		p.move(-1)
		return m, nil

	case key.Matches(msg, k.Enter):
		return m, m.drillIn()

	case key.Matches(msg, k.Back):
		if n := len(m.prev); n > 0 {
			back := m.prev[n-1]
			m.prev = m.prev[:n-1]
			m.focus = back
		}
		return m, nil

	case key.Matches(msg, k.Actions):
		if len(k.panelActions(m.focus)) > 0 {
			m.modal = m.actionsMenu()
		}
		return m, nil
	}

	// Panel-specific actions, dispatched through the same table the options
	// bar and the actions menu are built from.
	for _, a := range k.panelActions(m.focus) {
		if key.Matches(msg, a.binding) {
			return m.runAction(a.id)
		}
	}
	return m, nil
}

// runAction performs a context action. Both a key press and an entry in the
// `a` actions menu reach the panel behaviour through here.
func (m Model) runAction(id actionID) (Model, tea.Cmd) {
	switch id {
	case actConnect:
		if sel := m.panels[panelConnections].selected(); sel != "" {
			return m, tea.Batch(logCmd("-- connect %s", sel), focusCmd(panelDatabases))
		}

	case actNewConnection:
		m.modal = newPromptModal("New connection", "postgres://user@host/db", "",
			func(m *Model, v string) tea.Cmd {
				if v == "" {
					return nil
				}
				p := m.panels[panelConnections]
				p.setItems(append(p.items, v))
				return logCmd("-- add connection %s", v)
			})

	case actEditConnection:
		if sel := m.panels[panelConnections].selected(); sel != "" {
			m.modal = newPromptModal("Edit connection", "", sel,
				func(m *Model, v string) tea.Cmd {
					if v == "" {
						return nil
					}
					p := m.panels[panelConnections]
					p.items[p.cursor] = v
					return logCmd("-- rename connection %s -> %s", sel, v)
				})
		}

	case actDropConnection:
		if sel := m.panels[panelConnections].selected(); sel != "" {
			m.modal = &confirmModal{
				title:  "Remove connection",
				body:   fmt.Sprintf("Remove %q from the connection list?", sel),
				danger: true,
				onConfirm: func(m *Model) tea.Cmd {
					p := m.panels[panelConnections]
					kept := append([]string{}, p.items[:p.cursor]...)
					p.setItems(append(kept, p.items[p.cursor+1:]...))
					return logCmd("-- remove connection %s", sel)
				},
			}
		}

	case actRefresh:
		return m, logCmd("-- refresh %s", panelTitles[m.focus])

	case actFilter:
		focus := m.focus
		m.modal = newPromptModal("Filter "+panelTitles[focus], "substring", "",
			func(m *Model, v string) tea.Cmd {
				return logCmd("-- filter %s: %q", panelTitles[focus], v)
			})

	case actRunQuery:
		if sel := m.panels[panelHistory].selected(); sel != "" {
			return m, logCmd("%s", sel)
		}

	case actClearHistory:
		m.modal = &confirmModal{
			title:  "Clear history",
			body:   "Discard every entry in the query history?",
			danger: true,
			onConfirm: func(m *Model) tea.Cmd {
				m.panels[panelHistory].setItems(nil)
				return logCmd("-- clear query history")
			},
		}
	}
	return m, nil
}

// drillIn is `enter`: move one step deeper in the connection → database →
// table chain, and record the resulting statement in the history panel.
func (m Model) drillIn() tea.Cmd {
	sel := m.panels[m.focus].selected()
	if sel == "" {
		return nil
	}
	switch m.focus {
	case panelConnections:
		return tea.Batch(logCmd("-- connect %s", sel), focusCmd(panelDatabases))
	case panelDatabases:
		return tea.Batch(logCmd("USE %s;", sel), focusCmd(panelTables))
	case panelTables:
		stmt := fmt.Sprintf("SELECT * FROM %s LIMIT 100;", sel)
		return tea.Batch(logCmd("%s", stmt), func() tea.Msg { return historyEntryMsg{statement: stmt} })
	case panelHistory:
		return logCmd("%s", sel)
	}
	return nil
}

func (m *Model) setFocus(id panelID) {
	if id == m.focus {
		return
	}
	m.prev = append(m.prev, m.focus)
	if len(m.prev) > 16 {
		m.prev = m.prev[len(m.prev)-16:]
	}
	m.focus = id
}

// actionsMenu is the `a` popup: one scrollable entry per binding of the
// focused panel, driven by the same key.Binding slices as the options bar.
func (m Model) actionsMenu() modal {
	var entries []menuEntry
	for _, a := range m.keys.panelActions(m.focus) {
		if !a.binding.Enabled() {
			continue
		}
		id := a.id
		entries = append(entries, menuEntry{
			key:   a.binding.Help().Key,
			label: a.binding.Help().Desc,
			action: func(m *Model) tea.Cmd {
				next, cmd := m.runAction(id)
				*m = next
				return cmd
			},
		})
	}
	entries = append(entries, menuEntry{key: "esc", label: "cancel"})
	return &menuModal{title: "Actions — " + panelTitles[m.focus], entries: entries}
}
