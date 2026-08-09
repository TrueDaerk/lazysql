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
	OpenEditor key.Binding
	// CancelQuery is only meaningful while a query runs and is enabled
	// only then, so `ctrl+c` keeps meaning quit the rest of the time.
	CancelQuery key.Binding
	Help        key.Binding
	Quit        key.Binding

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
	LoadQuery      key.Binding
	RunQuery       key.Binding
	DeleteHistory  key.Binding
	ClearHistory   key.Binding

	// Data grid (the focusable main view).
	ColLeft     key.Binding
	ColRight    key.Binding
	NextPage    key.Binding
	PrevPage    key.Binding
	SortColumn  key.Binding
	WhereFilter key.Binding
	ViewCell    key.Binding

	// Staged editing (Data tab).
	EditCell       key.Binding
	DeleteRow      key.Binding
	InsertRow      key.Binding
	DuplicateRow   key.Binding
	CommitChanges  key.Binding
	UnstageCell    key.Binding
	DiscardChanges key.Binding

	// Main-view tabs (Data | Structure | Indexes | DDL).
	PrevMainTab key.Binding
	NextMainTab key.Binding

	// Copy and export.
	CopyMenu     key.Binding
	ExportTable  key.Binding
	CancelExport key.Binding
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
		OpenEditor: key.NewBinding(key.WithKeys(":"), key.WithHelp(":", "query editor")),
		CancelQuery: key.NewBinding(
			key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "cancel query"), key.WithDisabled()),
		Help: key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Quit: key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),

		NewConnection:  key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new connection")),
		EditConnection: key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "edit connection")),
		DropConnection: key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "remove connection")),
		TestConnection: key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "test connection")),
		Connect:        key.NewBinding(key.WithKeys("enter", "space"), key.WithHelp("enter", "connect")),
		Refresh:        key.NewBinding(key.WithKeys("R", "r"), key.WithHelp("R", "reload from server")),
		Actions:        key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "actions")),
		Filter:         key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "fuzzy filter")),
		ToggleTab:      key.NewBinding(key.WithKeys("[", "]"), key.WithHelp("[/]", "tables/views")),
		LoadQuery:      key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "load into editor")),
		RunQuery:       key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "run query")),
		DeleteHistory:  key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete entry")),
		ClearHistory:   key.NewBinding(key.WithKeys("D"), key.WithHelp("D", "clear history")),

		ColLeft:     key.NewBinding(key.WithKeys("h", "left"), key.WithHelp("h/←", "prev column")),
		ColRight:    key.NewBinding(key.WithKeys("l", "right"), key.WithHelp("l/→", "next column")),
		NextPage:    key.NewBinding(key.WithKeys("ctrl+f", "pgdown"), key.WithHelp("ctrl+f", "next page")),
		PrevPage:    key.NewBinding(key.WithKeys("ctrl+b", "pgup"), key.WithHelp("ctrl+b", "prev page")),
		SortColumn:  key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "sort column")),
		WhereFilter: key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "where filter")),
		ViewCell:    key.NewBinding(key.WithKeys("v"), key.WithHelp("v", "view cell")),

		EditCell:       key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "edit cell")),
		DeleteRow:      key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "stage row delete")),
		InsertRow:      key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "insert row")),
		DuplicateRow:   key.NewBinding(key.WithKeys("D"), key.WithHelp("D", "duplicate row")),
		CommitChanges:  key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "commit staged changes")),
		UnstageCell:    key.NewBinding(key.WithKeys("u"), key.WithHelp("u", "unstage")),
		DiscardChanges: key.NewBinding(key.WithKeys("U"), key.WithHelp("U", "discard staged changes")),

		PrevMainTab: key.NewBinding(key.WithKeys("["), key.WithHelp("[", "prev tab")),
		NextMainTab: key.NewBinding(key.WithKeys("]"), key.WithHelp("]", "next tab")),

		CopyMenu:    key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "copy…")),
		ExportTable: key.NewBinding(key.WithKeys("E"), key.WithHelp("E", "export to file")),
		// Only meaningful while an export is running, and shown only
		// then: the options bar and `?` both skip disabled bindings.
		CancelExport: key.NewBinding(
			key.WithKeys("X"), key.WithHelp("X", "cancel export"), key.WithDisabled()),
	}
}

// navigation returns the bindings shared by every panel.
func (k keyMap) navigation() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Enter, k.Back}
}

// global returns the bindings handled by the root model.
func (k keyMap) global() []key.Binding {
	return []key.Binding{
		k.Jump, k.NextPanel, k.PrevPanel, k.ScreenNext, k.ScreenPrev,
		k.OpenEditor, k.CancelQuery, k.Help, k.Quit,
	}
}

// actionID names a context action so that a key press and the `a` actions
// menu can dispatch through the same code path.
type actionID int

const (
	// actNone is the zero value, so a struct field can mean "no action
	// pending" without a second bool next to it.
	actNone actionID = iota
	actConnect
	actNewConnection
	actEditConnection
	actDropConnection
	actTestConnection
	actRefresh
	actFilter
	actToggleTab
	actLoadQuery
	actRunQuery
	actDeleteHistory
	actClearHistory
	actColLeft
	actColRight
	actNextPage
	actPrevPage
	actSortColumn
	actWhereFilter
	actViewCell
	actEditCell
	actDeleteRow
	actInsertRow
	actDuplicateRow
	actCommitChanges
	actUnstageCell
	actDiscardChanges
	actPrevMainTab
	actNextMainTab
	actCopyDDL

	// Copy and export. Only actCopyMenu, actExportTable and
	// actCancelExport are bound to keys; the rest are the entries of the
	// `y` menu, dispatched through runAction like everything else.
	actCopyMenu
	actCopyCell
	actCopyRowCSV
	actCopyRowJSON
	actCopyRowInsert
	actCopyTableCSV
	actCopyTableJSON
	actCopyTableInsert
	actCopyTableSchema
	actExportTable
	actCancelExport
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
			{actLoadQuery, k.LoadQuery},
			{actRunQuery, k.RunQuery},
			{actDeleteHistory, k.DeleteHistory},
			{actClearHistory, k.ClearHistory},
			{actFilter, k.Filter},
		}
	case panelMain:
		return []action{
			{actPrevMainTab, k.PrevMainTab},
			{actNextMainTab, k.NextMainTab},
			{actColLeft, k.ColLeft},
			{actColRight, k.ColRight},
			{actNextPage, k.NextPage},
			{actPrevPage, k.PrevPage},
			{actSortColumn, k.SortColumn},
			{actWhereFilter, k.WhereFilter},
			{actViewCell, k.ViewCell},
			{actEditCell, k.EditCell},
			{actDeleteRow, k.DeleteRow},
			{actInsertRow, k.InsertRow},
			{actDuplicateRow, k.DuplicateRow},
			{actCommitChanges, k.CommitChanges},
			{actUnstageCell, k.UnstageCell},
			{actDiscardChanges, k.DiscardChanges},
			{actCopyMenu, k.CopyMenu},
			{actExportTable, k.ExportTable},
			{actCancelExport, k.CancelExport},
			{actRefresh, k.Refresh},
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
	return append(out, k.Jump, k.NextPanel, k.OpenEditor, k.CancelQuery, k.Help, k.Quit)
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
