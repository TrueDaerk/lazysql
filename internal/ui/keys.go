package ui

import "charm.land/bubbles/v2/key"

// keyMap holds every binding the shell knows about. The options bar and the
// `?` help modal both render slices assembled from this struct — there is no
// second place where a key is described.
type keyMap struct {
	// Navigation, valid in every panel.
	Up    key.Binding
	Down  key.Binding
	Enter key.Binding
	Back  key.Binding

	// Panel switching.
	Jump      key.Binding
	NextPanel key.Binding
	PrevPanel key.Binding

	// Screen modes and app-level keys.
	ScreenNext key.Binding
	ScreenPrev key.Binding
	Help       key.Binding
	Quit       key.Binding

	// Context actions, keyed by panel.
	NewConnection  key.Binding
	EditConnection key.Binding
	DropConnection key.Binding
	TestConnection key.Binding
	Connect        key.Binding
	Refresh        key.Binding
	Actions        key.Binding
	Filter         key.Binding
	ToggleTab      key.Binding
	RunQuery       key.Binding
	ClearHistory   key.Binding
}

func newKeyMap() keyMap {
	return keyMap{
		Up:    key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		Down:  key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		Enter: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "drill in")),
		Back:  key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),

		Jump:      key.NewBinding(key.WithKeys("1", "2", "3", "4"), key.WithHelp("1-4", "jump to panel")),
		NextPanel: key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "next panel")),
		PrevPanel: key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("shift+tab", "prev panel")),

		ScreenNext: key.NewBinding(key.WithKeys("+"), key.WithHelp("+", "next screen mode")),
		ScreenPrev: key.NewBinding(key.WithKeys("_"), key.WithHelp("_", "prev screen mode")),
		Help:       key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Quit:       key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),

		NewConnection:  key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new connection")),
		EditConnection: key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "edit connection")),
		DropConnection: key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "remove connection")),
		TestConnection: key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "test connection")),
		Connect:        key.NewBinding(key.WithKeys("enter", "space"), key.WithHelp("enter", "connect")),
		Refresh:        key.NewBinding(key.WithKeys("R", "r"), key.WithHelp("R", "reload from server")),
		Actions:        key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "actions")),
		Filter:         key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "fuzzy filter")),
		ToggleTab:      key.NewBinding(key.WithKeys("[", "]"), key.WithHelp("[/]", "tables/views")),
		RunQuery:       key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "run query")),
		ClearHistory:   key.NewBinding(key.WithKeys("D"), key.WithHelp("D", "clear history")),
	}
}

// navigation returns the bindings shared by every panel.
func (k keyMap) navigation() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Enter, k.Back}
}

// global returns the bindings handled by the root model.
func (k keyMap) global() []key.Binding {
	return []key.Binding{k.Jump, k.NextPanel, k.PrevPanel, k.ScreenNext, k.ScreenPrev, k.Help, k.Quit}
}

// actionID names a context action so that a key press and the `a` actions
// menu can dispatch through the same code path.
type actionID int

const (
	actConnect actionID = iota
	actNewConnection
	actEditConnection
	actDropConnection
	actTestConnection
	actRefresh
	actFilter
	actToggleTab
	actRunQuery
	actClearHistory
)

// action pairs a dispatchable action with the binding that documents it.
type action struct {
	id      actionID
	binding key.Binding
}

// panelActions is the single source of truth for a panel's context actions:
// key handling, the options bar, the actions menu and `?` all read it.
func (k keyMap) panelActions(id panelID) []action {
	switch id {
	case panelConnections:
		return []action{
			{actConnect, k.Connect},
			{actNewConnection, k.NewConnection},
			{actEditConnection, k.EditConnection},
			{actDropConnection, k.DropConnection},
			{actTestConnection, k.TestConnection},
		}
	case panelDatabases:
		return []action{
			{actRefresh, k.Refresh},
			{actFilter, k.Filter},
		}
	case panelTables:
		return []action{
			{actRefresh, k.Refresh},
			{actFilter, k.Filter},
			{actToggleTab, k.ToggleTab},
		}
	case panelHistory:
		return []action{
			{actRunQuery, k.RunQuery},
			{actClearHistory, k.ClearHistory},
		}
	}
	return nil
}

// actions returns the context-sensitive bindings of a single panel, including
// the actions-menu key itself.
func (k keyMap) actions(id panelID) []key.Binding {
	pa := k.panelActions(id)
	out := make([]key.Binding, 0, len(pa)+1)
	for _, a := range pa {
		out = append(out, a.binding)
	}
	if len(out) > 0 {
		out = append(out, k.Actions)
	}
	return out
}

// optionsBarBindings is what the bottom bar shows for the focused panel: the
// panel's own actions first, then the most useful universal keys.
func (k keyMap) optionsBarBindings(id panelID) []key.Binding {
	out := append([]key.Binding{}, k.actions(id)...)
	return append(out, k.Jump, k.NextPanel, k.Help, k.Quit)
}

// helpGroups is the full `?` listing for the focused panel, drawn from the
// same bindings as the options bar.
func (k keyMap) helpGroups(id panelID) [][]key.Binding {
	return [][]key.Binding{
		k.actions(id),
		k.navigation(),
		k.global(),
	}
}
