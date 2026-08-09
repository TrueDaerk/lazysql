package ui

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// press builds the KeyPressMsg for a printable rune.
func press(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

// special builds the KeyPressMsg for a non-printable key such as tab or esc.
func special(code rune, mod tea.KeyMod) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code, Mod: mod}
}

// send drives the model through a sequence of key presses, running each
// returned command synchronously so domain messages are reduced like they
// would be by the runtime.
func send(t *testing.T, m Model, msgs ...tea.Msg) Model {
	t.Helper()
	for _, msg := range msgs {
		next, cmd := m.Update(msg)
		m = next.(Model)
		for _, out := range drain(cmd) {
			next, cmd2 := m.Update(out)
			m = next.(Model)
			for _, out2 := range drain(cmd2) {
				next, _ := m.Update(out2)
				m = next.(Model)
			}
		}
	}
	return m
}

// drain flattens a command (including batches) into the messages it produces.
func drain(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	switch msg := msg.(type) {
	case nil:
		return nil
	case tea.BatchMsg:
		var out []tea.Msg
		for _, c := range msg {
			out = append(out, drain(c)...)
		}
		return out
	default:
		return []tea.Msg{msg}
	}
}

func sized(w, h int) Model {
	m := New()
	next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return next.(Model)
}

func TestNumberKeysJumpToPanel(t *testing.T) {
	m := sized(120, 40)
	for i, r := range []rune{'2', '3', '4', '1'} {
		m = send(t, m, press(r))
		want := panelID(r - '1')
		if m.focus != want {
			t.Fatalf("step %d: focus = %v, want %v", i, m.focus, want)
		}
	}
}

func TestTabCyclesPanels(t *testing.T) {
	m := sized(120, 40)
	for i := 0; i < int(panelCount); i++ {
		want := panelID((i + 1) % int(panelCount))
		m = send(t, m, special(tea.KeyTab, 0))
		if m.focus != want {
			t.Fatalf("after %d tabs: focus = %v, want %v", i+1, m.focus, want)
		}
	}
	m = send(t, m, special(tea.KeyTab, tea.ModShift))
	if m.focus != panelHistory {
		t.Fatalf("shift+tab: focus = %v, want %v", m.focus, panelHistory)
	}
}

func TestCursorMovesWithinPanel(t *testing.T) {
	m := sized(120, 40)
	m = send(t, m, press('j'), press('j'))
	if got := m.panels[panelConnections].cursor; got != 2 {
		t.Fatalf("cursor = %d, want 2", got)
	}
	m = send(t, m, press('k'))
	if got := m.panels[panelConnections].cursor; got != 1 {
		t.Fatalf("cursor = %d, want 1", got)
	}
	// Cursor is clamped at both ends.
	m = send(t, m, press('k'), press('k'), press('k'))
	if got := m.panels[panelConnections].cursor; got != 0 {
		t.Fatalf("cursor = %d, want 0", got)
	}
}

func TestModalSwallowsBackgroundKeys(t *testing.T) {
	m := sized(120, 40)
	m = send(t, m, press('?'))
	if m.modal == nil {
		t.Fatal("? did not open the help modal")
	}
	// j/2 would move the cursor / switch panels if they leaked through.
	m = send(t, m, press('j'), press('2'))
	if m.focus != panelConnections {
		t.Fatalf("focus leaked through modal: %v", m.focus)
	}
	if got := m.panels[panelConnections].cursor; got != 0 {
		t.Fatalf("cursor leaked through modal: %d", got)
	}
	m = send(t, m, special(tea.KeyEscape, 0))
	if m.modal != nil {
		t.Fatal("esc did not close the modal")
	}
}

func TestEscCancelsEveryModal(t *testing.T) {
	cases := []struct {
		name string
		open func(Model) Model
	}{
		{"help", func(m Model) Model { m.modal = newHelpModal("t", m.keys.helpGroups(m.focus)); return m }},
		{"menu", func(m Model) Model { m.modal = m.actionsMenu(); return m }},
		{"confirm", func(m Model) Model { m.modal = &confirmModal{title: "t", body: "b"}; return m }},
		{"prompt", func(m Model) Model { m.modal = newPromptModal("t", "", "", nil); return m }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.open(sized(120, 40))
			m = send(t, m, special(tea.KeyEscape, 0))
			if m.modal != nil {
				t.Fatal("esc did not cancel the modal")
			}
		})
	}
}

func TestConfirmModalRunsActionOnEnter(t *testing.T) {
	m := sized(120, 40)
	before := len(m.panels[panelConnections].items)
	m = send(t, m, press('d')) // remove connection
	if m.modal == nil {
		t.Fatal("d did not open the confirm modal")
	}
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.modal != nil {
		t.Fatal("confirm modal stayed open")
	}
	if got := len(m.panels[panelConnections].items); got != before-1 {
		t.Fatalf("items = %d, want %d", got, before-1)
	}
	if len(m.commandLog) == 0 {
		t.Fatal("action was not written to the command log")
	}
}

func TestPromptModalAppendsConnection(t *testing.T) {
	m := sized(120, 40)
	before := len(m.panels[panelConnections].items)
	m = send(t, m, press('n'))
	if m.modal == nil {
		t.Fatal("n did not open the prompt modal")
	}
	m = send(t, m, press('d'), press('b'), special(tea.KeyEnter, 0))
	if got := m.panels[panelConnections].items; len(got) != before+1 || got[len(got)-1] != "db" {
		t.Fatalf("items = %v, want trailing %q", got, "db")
	}
}

func TestActionsMenuDispatchesSameActionAsKey(t *testing.T) {
	m := sized(120, 40)
	m = send(t, m, press('a'))
	mm, ok := m.modal.(*menuModal)
	if !ok {
		t.Fatalf("a opened %T, want *menuModal", m.modal)
	}
	// First entry on Connections is "connect".
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.modal != nil {
		t.Fatal("menu stayed open after enter")
	}
	if m.focus != panelDatabases {
		t.Fatalf("focus = %v, want %v (connect should drill in)", m.focus, panelDatabases)
	}
	if len(mm.entries) < 2 {
		t.Fatalf("menu had %d entries, want the panel's actions", len(mm.entries))
	}
}

func TestDrillInLogsAndRecordsHistory(t *testing.T) {
	m := sized(120, 40)
	m = send(t, m,
		special(tea.KeyEnter, 0), // connection -> databases
		special(tea.KeyEnter, 0), // database -> tables
		special(tea.KeyEnter, 0), // table -> select
	)
	if m.focus != panelTables {
		t.Fatalf("focus = %v, want %v", m.focus, panelTables)
	}
	hist := m.panels[panelHistory].items
	if len(hist) != 1 || !strings.HasPrefix(hist[0], "SELECT * FROM ") {
		t.Fatalf("history = %v, want one SELECT entry", hist)
	}
	if len(m.commandLog) != 3 {
		t.Fatalf("command log = %v, want 3 entries", m.commandLog)
	}
}

func TestEscBacksOutOfFocusStack(t *testing.T) {
	m := sized(120, 40)
	m = send(t, m, press('3'), special(tea.KeyEscape, 0))
	if m.focus != panelConnections {
		t.Fatalf("focus = %v, want %v", m.focus, panelConnections)
	}
}

func TestScreenModeCycles(t *testing.T) {
	m := sized(120, 40)
	for _, want := range []screenMode{screenHalf, screenFull, screenNormal} {
		m = send(t, m, press('+'))
		if m.screen != want {
			t.Fatalf("screen = %v, want %v", m.screen, want)
		}
	}
	m = send(t, m, press('_'))
	if m.screen != screenFull {
		t.Fatalf("screen = %v, want %v", m.screen, screenFull)
	}
}

func TestPanelHeightsFillBody(t *testing.T) {
	m := sized(120, 40)
	for _, mode := range []screenMode{screenNormal, screenHalf} {
		m.screen = mode
		hs := m.panelHeights(39)
		sum := 0
		for _, h := range hs {
			if h < 3 {
				t.Fatalf("mode %v: panel height %d too small", mode, h)
			}
			sum += h
		}
		if sum != 39 {
			t.Fatalf("mode %v: heights sum to %d, want 39", mode, sum)
		}
	}
}

// lipgloss v2 treats Style.Width/Height as the total block size (borders
// included) and never pads beyond the content, so layout math must ask for
// the full box. Guard against the side column drifting out of alignment.
func TestRenderedBlocksMatchRequestedSize(t *testing.T) {
	m := sized(120, 34)
	bodyH := 33
	heights := m.panelHeights(bodyH)
	total := 0
	for id := panelID(0); id < panelCount; id++ {
		block := m.renderPanel(id, 40, heights[id])
		if got := lipgloss.Height(block); got != heights[id] {
			t.Errorf("panel %v height = %d, want %d", panelTitles[id], got, heights[id])
		}
		if got := lipgloss.Width(block); got != 40 {
			t.Errorf("panel %v width = %d, want 40", panelTitles[id], got)
		}
		total += lipgloss.Height(block)
	}
	if total != bodyH {
		t.Errorf("side column height = %d, want %d", total, bodyH)
	}
	if got := lipgloss.Height(m.renderMainColumn(80, bodyH)); got != bodyH {
		t.Errorf("main column height = %d, want %d", got, bodyH)
	}
}

func TestTinyTerminalDoesNotPanic(t *testing.T) {
	for _, size := range [][2]int{{0, 0}, {1, 1}, {20, 5}, {59, 17}} {
		m := sized(size[0], size[1])
		v := m.View()
		if size[0] >= minWidth && size[1] >= minHeight {
			continue
		}
		if size[0] > 0 && !strings.Contains(v.Content, "too small") {
			t.Fatalf("%dx%d: expected the too-small view", size[0], size[1])
		}
	}
}

func TestRendersAtManySizes(t *testing.T) {
	m := sized(120, 40)
	for _, size := range [][2]int{{60, 18}, {80, 24}, {200, 60}, {61, 19}} {
		for _, mode := range []screenMode{screenNormal, screenHalf, screenFull} {
			next, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			m = next.(Model)
			m.screen = mode
			if out := m.View().Content; out == "" {
				t.Fatalf("%dx%d mode %v rendered nothing", size[0], size[1], mode)
			}
		}
	}
}

// The options bar and `?` must describe the same bindings, and every one of
// them must actually be handled.
func TestOptionsBarAndHelpShareOneSource(t *testing.T) {
	k := newKeyMap()
	for id := panelID(0); id < panelCount; id++ {
		helpKeys := map[string]bool{}
		for _, group := range k.helpGroups(id) {
			for _, b := range group {
				helpKeys[b.Help().Key] = true
			}
		}
		for _, b := range k.optionsBarBindings(id) {
			if !helpKeys[b.Help().Key] {
				t.Errorf("panel %v: options bar shows %q which `?` omits", panelTitles[id], b.Help().Key)
			}
		}
	}
}

func TestEveryDocumentedKeyIsBound(t *testing.T) {
	k := newKeyMap()
	global := map[string]bool{}
	for _, b := range append(k.global(), k.navigation()...) {
		for _, ks := range b.Keys() {
			global[ks] = true
		}
	}
	for id := panelID(0); id < panelCount; id++ {
		actions := map[string]bool{}
		for _, a := range k.panelActions(id) {
			for _, ks := range a.binding.Keys() {
				actions[ks] = true
			}
		}
		actions[k.Actions.Keys()[0]] = true

		for _, group := range k.helpGroups(id) {
			for _, b := range group {
				for _, ks := range b.Keys() {
					if !global[ks] && !actions[ks] {
						t.Errorf("panel %v: `?` documents %q but nothing binds it", panelTitles[id], ks)
					}
				}
			}
		}
	}
}

// Panel action keys must not shadow the global keys handled before them.
func TestPanelActionsDoNotShadowGlobalKeys(t *testing.T) {
	k := newKeyMap()
	globals := append(k.global(), k.navigation()...)
	for id := panelID(0); id < panelCount; id++ {
		for _, a := range k.panelActions(id) {
			for _, g := range globals {
				for _, gk := range g.Keys() {
					if key.Matches(tea.KeyPressMsg{Code: rune(gk[0]), Text: gk}, a.binding) && len(gk) == 1 {
						t.Errorf("panel %v: action %q shadows global %q", panelTitles[id], a.binding.Help().Key, gk)
					}
				}
			}
		}
	}
}
