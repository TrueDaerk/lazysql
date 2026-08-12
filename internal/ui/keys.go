package ui

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/bubbles/v2/key"
)

// keyMap holds every binding the shell knows about. The options bar and the
// `?` help modal both render slices assembled from this struct — there is no
// second place where a key is described.
type keyMap struct {
	// Navigation, valid in every panel.
	Up    key.Binding
	Down  key.Binding
	Enter key.Binding
	Back  key.Binding

	// AcceptChanges is ctrl+enter / cmd+enter, matched alongside plain enter
	// everywhere enter accepts, confirms, submits or commits a change: the
	// edit cell modal, the insert row form, the generic form, confirm
	// modals, and (aliasing `c`) the commit-staged-changes action. See
	// acceptKeys below for the terminal-support caveat.
	AcceptChanges key.Binding

	// Panel switching.
	Jump      key.Binding
	NextPanel key.Binding
	PrevPanel key.Binding

	// Screen modes and app-level keys.
	ScreenNext key.Binding
	ScreenPrev key.Binding
	OpenEditor key.Binding
	// LeaveInsert ends the query editor's insert mode. It is a binding of
	// its own rather than a second meaning of Back, so `?` can name what
	// esc does while the buffer has the keyboard.
	LeaveInsert key.Binding
	// CancelQuery is only meaningful while a query runs and is enabled
	// only then, so `ctrl+c` keeps meaning quit the rest of the time.
	CancelQuery key.Binding
	CommandLog  key.Binding
	Help        key.Binding
	Quit        key.Binding

	// Context actions, keyed by panel.
	NewConnection  key.Binding
	EditConnection key.Binding
	DropConnection key.Binding
	TestConnection key.Binding
	SchemaDiff     key.Binding
	Connect        key.Binding
	Refresh        key.Binding
	Actions        key.Binding
	Filter         key.Binding

	// The [2] Objects tree. ExpandNode opens the branch under the cursor
	// (and steps into it when it is already open), CollapseNode closes it
	// (and steps out to the parent when it is already closed) — `enter`
	// toggles the same branch, so the two are the lazygit-style shortcut,
	// not the only way in.
	ExpandNode   key.Binding
	CollapseNode key.Binding

	// Query editor, panel [3]. EditQuery enters insert mode, where every
	// key that is not RunEditor, CancelQuery or Back types into the
	// buffer; RunEditor executes it from either mode. History opens the
	// floating query-history pane from the editor's normal mode.
	// SaveSnippet keeps the buffer as a named snippet. It is bound in both
	// editor modes — ctrl+s is not a character, so insert mode can spare
	// it — and reaches the same store the pane's Snippets section browses.
	EditQuery key.Binding
	RunEditor key.Binding
	// ExplainQuery asks the server for the plan of the statement under
	// the caret. Like RunEditor it is bound in both editor modes —
	// ctrl+e is not a character — and it never executes the statement.
	ExplainQuery key.Binding
	ClearQuery   key.Binding
	History      key.Binding
	SaveSnippet  key.Binding

	// The history/snippets pane. The pane is a modal, so these only act
	// while it is open — but they are keyMap bindings, not literals in its
	// update function, so `?` documents them, `[keys]` can rebind them and
	// the pane's own footer renders from the same source. HistLoad is
	// `enter` — the app-wide "drill in" reading: it loads the statement
	// into the editor, where running it is one visible ctrl+r away.
	// HistRun executes immediately, through the same submitQuery pipeline
	// as the editor (placeholder prompt, unguarded-write confirm included).
	HistLoad    key.Binding
	HistRun     key.Binding
	HistSnippet key.Binding
	HistDelete  key.Binding
	HistSection key.Binding

	// Vim normal mode in the query editor. j/k/↑/↓ reuse Up and Down.
	// The two-key commands (dd, yy, gg) are bound to their first key and
	// completed by the editor's pending-key handler; their help text
	// names the full chord.
	VimLeft       key.Binding
	VimRight      key.Binding
	VimWordFwd    key.Binding
	VimWordBack   key.Binding
	VimLineStart  key.Binding
	VimLineEnd    key.Binding
	VimTop        key.Binding
	VimBottom     key.Binding
	VimAppend     key.Binding
	VimOpenBelow  key.Binding
	VimOpenAbove  key.Binding
	VimDeleteChar key.Binding
	VimDeleteLine key.Binding
	VimYankLine   key.Binding
	VimPaste      key.Binding

	// The autocomplete popup. Complete opens it on demand; the other four
	// only act while it is open, which is why CloseCompletion can be esc
	// without costing insert mode its own esc.
	Complete         key.Binding
	CompleteNext     key.Binding
	CompletePrev     key.Binding
	AcceptCompletion key.Binding
	CloseCompletion  key.Binding

	// Data grid (the focusable main view).
	ColLeft     key.Binding
	ColRight    key.Binding
	NextPage    key.Binding
	PrevPage    key.Binding
	SortColumn  key.Binding
	WhereFilter key.Binding
	ClearFilter key.Binding
	ViewCell    key.Binding
	RowDetail   key.Binding

	// Multi-row selection. SelectRows (ctrl+v) anchors a selection at the
	// cursor row and `j`/`k` extend it; CopySelection (ctrl+c) copies what
	// is selected. CopySelection is enabled only while a selection is up,
	// the same way CancelQuery is enabled only during a run — `ctrl+c`
	// keeps meaning quit the rest of the time. ShiftUp/ShiftDown are the
	// conventional alternative: with no selection up they anchor one at the
	// cursor row and move, like ctrl+v followed by k/j; with one already up
	// they just extend or shrink it, like plain k/j do. Whether the
	// terminal reports shift+arrows at all depends on kitty keyboard
	// protocol support — the same caveat acceptKeys documents for
	// ctrl+enter — so where it is not reported these bindings simply never
	// match and plain Up/Down keep working.
	SelectRows    key.Binding
	CopySelection key.Binding
	ShiftUp       key.Binding
	ShiftDown     key.Binding

	// Foreign-key navigation. FollowFK jumps along the constraint the
	// cursor column takes part in, IncomingRefs goes the other way, and
	// BrowseBack walks the jumps back — as does `esc`, which pops the
	// jump history before it gives the focus stack a turn.
	FollowFK     key.Binding
	IncomingRefs key.Binding
	BrowseBack   key.Binding

	// Staged editing (Data tab).
	EditCell       key.Binding
	DeleteRow      key.Binding
	InsertRow      key.Binding
	DuplicateRow   key.Binding
	CommitChanges  key.Binding
	UnstageCell    key.Binding
	DiscardChanges key.Binding

	// The date picker, opened for columns db.ClassifyType calls temporal.
	// PickPrev/PickNext move sideways (a day in the calendar, a spinner in
	// the clock) and PickUp/PickDown move by the bigger unit (a week, one
	// hour/minute/second). PickRaw hands the column back to plain text
	// entry, which is where NULL and SQL expressions live; OpenPicker is
	// the way back into the calendar from a text field.
	PickPrev      key.Binding
	PickNext      key.Binding
	PickUp        key.Binding
	PickDown      key.Binding
	PickMonthPrev key.Binding
	PickMonthNext key.Binding
	PickToday     key.Binding
	PickSection   key.Binding
	PickRaw       key.Binding
	OpenPicker    key.Binding

	// Main-view tabs (Data | Structure | Indexes | DDL).
	PrevMainTab key.Binding
	NextMainTab key.Binding

	// Copy and export.
	CopyMenu     key.Binding
	ExportTable  key.Binding
	CancelExport key.Binding
	// ExportDatabaseDDL lives on the Objects panel, not the main view: it
	// exports every relation of the browsed database in one file, rather
	// than the one relation ExportTable is scoped to.
	ExportDatabaseDDL key.Binding

	// Connection form: path completion on the File field, plus the field
	// navigation tab shares with it.
	CompletePath key.Binding
	NextField    key.Binding
	PrevField    key.Binding

	// Backup opens the dump/restore menu on panels [1] and [2], and
	// CancelBackup kills the tool it started. Like CancelExport the
	// cancel key is only enabled while a job runs.
	Backup       key.Binding
	CancelBackup key.Binding
}

// acceptKeys are the ctrl+enter / cmd+enter ("super+enter") key strings
// Bubble Tea v2 reports on terminals that support the kitty keyboard
// protocol's key-disambiguation extension. Bubble Tea always asks the
// terminal for it, but not every terminal answers — the rest keep sending
// plain \r for ctrl+enter, indistinguishable from enter, so every binding
// built from acceptKeys must leave plain enter working on its own.
var acceptKeys = []string{"ctrl+enter", "super+enter"}

func newKeyMap() keyMap {
	return keyMap{
		Up:    key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		Down:  key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		Enter: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "drill in")),
		Back:  key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),

		AcceptChanges: key.NewBinding(
			key.WithKeys(acceptKeys...),
			key.WithHelp("ctrl+enter", "accept (alias for enter; needs terminal support)")),

		Jump:      key.NewBinding(key.WithKeys("1", "2", "3"), key.WithHelp("1-3", "jump to panel")),
		NextPanel: key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "next panel")),
		PrevPanel: key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("shift+tab", "prev panel")),

		ScreenNext: key.NewBinding(key.WithKeys("+"), key.WithHelp("+", "next screen mode")),
		ScreenPrev: key.NewBinding(key.WithKeys("_"), key.WithHelp("_", "prev screen mode")),
		OpenEditor: key.NewBinding(key.WithKeys(":"), key.WithHelp(":", "query editor")),
		// esc leaves insert mode, so it is the editor's own back key too.
		LeaveInsert: key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "normal mode")),
		CancelQuery: key.NewBinding(
			key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "cancel query"), key.WithDisabled()),
		// `@` is AltGr+q on QWERTZ/AZERTY and often never reaches the
		// program, so `L` (for log) is bound next to it as the
		// layout-neutral alias. It is free in every panel and in the
		// editor's vim mode, and global keys are matched before either.
		CommandLog: key.NewBinding(key.WithKeys("@", "L"), key.WithHelp("@/L", "expand command log")),
		Help:       key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Quit:       key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),

		NewConnection:  key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new connection")),
		EditConnection: key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "edit connection")),
		DropConnection: key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "remove connection")),
		TestConnection: key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "test connection")),
		SchemaDiff:     key.NewBinding(key.WithKeys("D"), key.WithHelp("D", "schema diff vs…")),
		Connect:        key.NewBinding(key.WithKeys("enter", "space"), key.WithHelp("enter", "connect")),
		Refresh:        key.NewBinding(key.WithKeys("R", "r"), key.WithHelp("R", "reload from server")),
		Actions:        key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "actions")),
		Filter:         key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "fuzzy filter")),

		ExpandNode: key.NewBinding(key.WithKeys("l", "right"), key.WithHelp("l/→", "expand")),
		CollapseNode: key.NewBinding(
			key.WithKeys("h", "left"), key.WithHelp("h/←", "collapse")),

		EditQuery: key.NewBinding(key.WithKeys("i", "enter"), key.WithHelp("i/enter", "edit (insert)")),
		// ctrl+enter/cmd+enter are the acceptKeys alias: where the terminal
		// reports them (see acceptKeys above), they mean run, like ctrl+r.
		RunEditor: key.NewBinding(
			key.WithKeys(append([]string{"ctrl+r"}, acceptKeys...)...), key.WithHelp("ctrl+r", "run")),
		ExplainQuery: key.NewBinding(
			key.WithKeys("ctrl+e"), key.WithHelp("ctrl+e", "explain")),
		ClearQuery: key.NewBinding(key.WithKeys("D"), key.WithHelp("D", "clear buffer")),
		// backspace was the pane's original opener; it stays as an alias
		// for the muscle memory, H is the discoverable, mnemonic key.
		History: key.NewBinding(
			key.WithKeys("H", "backspace"), key.WithHelp("H", "history & snippets")),
		SaveSnippet: key.NewBinding(
			key.WithKeys("ctrl+s"), key.WithHelp("ctrl+s", "save snippet")),

		HistLoad: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "load into editor")),
		HistRun:  key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "run now")),
		HistSnippet: key.NewBinding(
			key.WithKeys("s"), key.WithHelp("s", "save as snippet")),
		HistDelete: key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete entry")),
		HistSection: key.NewBinding(
			key.WithKeys("tab", "shift+tab"), key.WithHelp("tab", "switch section")),

		VimLeft:       key.NewBinding(key.WithKeys("h", "left"), key.WithHelp("h/←", "left")),
		VimRight:      key.NewBinding(key.WithKeys("l", "right"), key.WithHelp("l/→", "right")),
		VimWordFwd:    key.NewBinding(key.WithKeys("w"), key.WithHelp("w", "next word")),
		VimWordBack:   key.NewBinding(key.WithKeys("b"), key.WithHelp("b", "prev word")),
		VimLineStart:  key.NewBinding(key.WithKeys("0"), key.WithHelp("0", "line start")),
		VimLineEnd:    key.NewBinding(key.WithKeys("$"), key.WithHelp("$", "line end")),
		VimTop:        key.NewBinding(key.WithKeys("g"), key.WithHelp("gg", "buffer start")),
		VimBottom:     key.NewBinding(key.WithKeys("G"), key.WithHelp("G", "buffer end")),
		VimAppend:     key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "append (insert)")),
		VimOpenBelow:  key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "open line below")),
		VimOpenAbove:  key.NewBinding(key.WithKeys("O"), key.WithHelp("O", "open line above")),
		VimDeleteChar: key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "delete char")),
		VimDeleteLine: key.NewBinding(key.WithKeys("d"), key.WithHelp("dd", "delete line")),
		VimYankLine:   key.NewBinding(key.WithKeys("y"), key.WithHelp("yy", "yank line")),
		VimPaste:      key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "paste")),

		// `ctrl+@` used to be bound as the second spelling terminals use
		// for ctrl+space. It is dropped: Bubble Tea v2 normalises that
		// byte to ctrl+space, and on QWERTZ/AZERTY the literal chord
		// (ctrl+AltGr+q) cannot be typed at all.
		Complete: key.NewBinding(
			key.WithKeys("ctrl+space", "tab"),
			key.WithHelp("ctrl+space/tab", "complete")),
		CompleteNext: key.NewBinding(
			key.WithKeys("down", "ctrl+n"), key.WithHelp("↓/ctrl+n", "next suggestion")),
		CompletePrev: key.NewBinding(
			key.WithKeys("up", "ctrl+p"), key.WithHelp("↑/ctrl+p", "prev suggestion")),
		AcceptCompletion: key.NewBinding(
			key.WithKeys("enter", "tab"), key.WithHelp("enter/tab", "accept suggestion")),
		CloseCompletion: key.NewBinding(
			key.WithKeys("esc"), key.WithHelp("esc", "close the popup")),

		CompletePath: key.NewBinding(
			key.WithKeys("tab"), key.WithHelp("tab", "complete path")),
		NextField: key.NewBinding(
			key.WithKeys("down", "tab"), key.WithHelp("↓/tab", "next field")),
		PrevField: key.NewBinding(
			key.WithKeys("up", "shift+tab"), key.WithHelp("↑/shift+tab", "prev field")),

		ColLeft:    key.NewBinding(key.WithKeys("h", "left"), key.WithHelp("h/←", "prev column")),
		ColRight:   key.NewBinding(key.WithKeys("l", "right"), key.WithHelp("l/→", "next column")),
		NextPage:   key.NewBinding(key.WithKeys("ctrl+f", "pgdown"), key.WithHelp("ctrl+f", "next page")),
		PrevPage:   key.NewBinding(key.WithKeys("ctrl+b", "pgup"), key.WithHelp("ctrl+b", "prev page")),
		SortColumn: key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "sort column")),
		// `/` is the filter key everywhere else in the shell, so the grid
		// answers to it too; `f` stays bound for the muscle memory the
		// prompt-only filter built up.
		WhereFilter: key.NewBinding(key.WithKeys("/", "f"), key.WithHelp("/", "filter rows")),
		ClearFilter: key.NewBinding(key.WithKeys("F"), key.WithHelp("F", "clear filter")),
		ViewCell:    key.NewBinding(key.WithKeys("v"), key.WithHelp("v", "view cell")),
		RowDetail:   key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "row detail")),

		// ctrl+v is vim's visual-block key, and the grid is the one place
		// it is free: bracketed paste arrives as tea.PasteMsg rather than
		// as a key, so nothing in the app reads ctrl+v as "paste".
		SelectRows: key.NewBinding(
			key.WithKeys("ctrl+v"), key.WithHelp("ctrl+v", "select rows")),
		// Disabled until a selection exists — see the field comment.
		CopySelection: key.NewBinding(
			key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "copy selection…"), key.WithDisabled()),
		ShiftUp: key.NewBinding(
			key.WithKeys("shift+up"), key.WithHelp("shift+↑", "extend selection up")),
		ShiftDown: key.NewBinding(
			key.WithKeys("shift+down"), key.WithHelp("shift+↓", "extend selection down")),

		FollowFK: key.NewBinding(key.WithKeys("g"), key.WithHelp("g", "follow foreign key")),
		IncomingRefs: key.NewBinding(
			key.WithKeys("G"), key.WithHelp("G", "rows referencing this one")),
		BrowseBack: key.NewBinding(
			key.WithKeys("ctrl+o"), key.WithHelp("ctrl+o", "back to previous table")),

		EditCell:     key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "edit cell")),
		DeleteRow:    key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "stage row delete")),
		InsertRow:    key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "insert row")),
		DuplicateRow: key.NewBinding(key.WithKeys("D"), key.WithHelp("D", "duplicate row")),
		// ctrl+enter/cmd+enter alias `c` here too, through the same
		// acceptKeys as everywhere else — opening the confirm modal, never
		// bypassing it.
		CommitChanges: key.NewBinding(
			key.WithKeys(append([]string{"c"}, acceptKeys...)...),
			key.WithHelp("c", "commit staged changes")),
		UnstageCell:    key.NewBinding(key.WithKeys("u"), key.WithHelp("u", "unstage")),
		DiscardChanges: key.NewBinding(key.WithKeys("U"), key.WithHelp("U", "discard staged changes")),

		PickPrev: key.NewBinding(
			key.WithKeys("h", "left"), key.WithHelp("h/←", "prev day / time field")),
		PickNext: key.NewBinding(
			key.WithKeys("l", "right"), key.WithHelp("l/→", "next day / time field")),
		PickUp: key.NewBinding(
			key.WithKeys("k", "up"), key.WithHelp("k/↑", "prev week / +1 unit")),
		PickDown: key.NewBinding(
			key.WithKeys("j", "down"), key.WithHelp("j/↓", "next week / -1 unit")),
		// `[` / `]` are hard to type on QWERTZ/AZERTY (see PrevMainTab), so
		// `H` / `L` are bound as the layout-neutral alias the issue asked
		// for — free inside the picker, which claims every key it is open
		// for.
		PickMonthPrev: key.NewBinding(
			key.WithKeys("[", "H", ","), key.WithHelp("[/H", "prev month")),
		PickMonthNext: key.NewBinding(
			key.WithKeys("]", "L", "."), key.WithHelp("]/L", "next month")),
		PickToday: key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "jump to now")),
		PickSection: key.NewBinding(
			key.WithKeys("tab", "shift+tab"), key.WithHelp("tab", "date / time half")),
		PickRaw: key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "raw text (NULL, now())")),
		OpenPicker: key.NewBinding(
			key.WithKeys("ctrl+t"), key.WithHelp("ctrl+t", "date picker")),

		// `[` / `]` are AltGr+8 / AltGr+9 on QWERTZ (and AZERTY), which
		// terminals may deliver as alt-chords or swallow outright. `,`
		// and `.` sit on the same physical keys on both layouts and are
		// free in the main view, so they are the portable alias.
		PrevMainTab: key.NewBinding(key.WithKeys("[", ","), key.WithHelp("[/,", "prev tab")),
		NextMainTab: key.NewBinding(key.WithKeys("]", "."), key.WithHelp("]/.", "next tab")),

		CopyMenu:    key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "copy…")),
		ExportTable: key.NewBinding(key.WithKeys("E"), key.WithHelp("E", "export to file")),
		// Only meaningful while an export is running, and shown only
		// then: the options bar and `?` both skip disabled bindings.
		CancelExport: key.NewBinding(
			key.WithKeys("X"), key.WithHelp("X", "cancel export"), key.WithDisabled()),
		ExportDatabaseDDL: key.NewBinding(
			key.WithKeys("E"), key.WithHelp("E", "export database DDL")),

		Backup: key.NewBinding(key.WithKeys("B"), key.WithHelp("B", "dump / restore…")),
		CancelBackup: key.NewBinding(
			key.WithKeys("X"), key.WithHelp("X", "cancel dump/restore"), key.WithDisabled()),
	}
}

// navigation returns the bindings shared by every panel.
func (k keyMap) navigation() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Enter, k.Back}
}

// navigationFor is navigation() as the named panel means it. The query
// editor is the one panel where `enter` is not "drill in" — it starts
// insert mode, and says so in its own action list — so it drops out here
// instead of being described twice.
func (k keyMap) navigationFor(id panelID) []key.Binding {
	if id == panelQuery {
		return []key.Binding{k.Up, k.Down, k.Back}
	}
	return k.navigation()
}

// editorInsert are the keys that still act while the query editor is in
// insert mode. Every other key types into the buffer, which is why this
// slice — and not the panel's action list — is what the options bar and
// `?` show there.
func (k keyMap) editorInsert() []key.Binding {
	return []key.Binding{
		k.Complete, k.RunEditor, k.ExplainQuery, k.SaveSnippet, k.CancelQuery, k.LeaveInsert}
}

// editorNormal are the vim keys of the query editor's normal mode. They
// are dispatched by updateQuery, not through panelActions — a motion is
// not a menu entry — so this slice is what documents them in `?`.
func (k keyMap) editorNormal() []key.Binding {
	return []key.Binding{
		k.VimLeft, k.VimRight, k.VimWordFwd, k.VimWordBack,
		k.VimLineStart, k.VimLineEnd, k.VimTop, k.VimBottom,
		k.VimAppend, k.VimOpenBelow, k.VimOpenAbove,
		k.VimDeleteChar, k.VimDeleteLine, k.VimYankLine, k.VimPaste,
	}
}

// editorCompletion are the keys the autocomplete popup owns while it is
// open. Like editorInsert they are dispatched by updateEditor rather than
// through panelActions — they only mean anything inside the editor — so
// this slice is what the options bar shows and what `?` lists for them.
func (k keyMap) editorCompletion() []key.Binding {
	return []key.Binding{
		k.CompleteNext, k.CompletePrev, k.AcceptCompletion, k.CloseCompletion,
	}
}

// historyPane are the keys the floating history/snippets pane owns while
// it is open. Dispatched inside the pane — a modal swallows every key —
// so this slice is what documents them in `?` and what the pane's footer
// renders.
func (k keyMap) historyPane() []key.Binding {
	return []key.Binding{
		k.HistLoad, k.HistRun, k.HistSnippet, k.HistDelete, k.HistSection,
	}
}

// queryResultKeys are the data-grid keys that actually survive the vim
// layer while a result sits under the buffer: everything else the grid
// binds (h/l, x, y, s, g, G, d…) is claimed by a motion or a chord
// first and never falls through. Focusing the grid itself (tab) is the
// way to the full key set.
func (k keyMap) queryResultKeys() []key.Binding {
	return []key.Binding{k.NextPage, k.PrevPage, k.ViewCell}
}

// formPathComplete are the keys the connection form's File field owns while
// path suggestions are up. Like editorCompletion they are dispatched inside
// the owning component — formModal.update, since an open modal swallows every
// key — so this slice is what documents them in `?`.
func (k keyMap) formPathComplete() []key.Binding {
	return []key.Binding{k.CompletePath, k.NextField, k.PrevField}
}

// datePicker are the keys the date/time picker owns while it is open, plus
// the key that opens it from a text field. Like historyPane they are
// dispatched inside the modal — an open modal swallows every key — so this
// slice is what documents them in `?` and what the picker's own footer and
// the options bar render.
func (k keyMap) datePicker() []key.Binding {
	return []key.Binding{
		k.OpenPicker, k.PickPrev, k.PickNext, k.PickUp, k.PickDown,
		k.PickMonthPrev, k.PickMonthNext, k.PickToday, k.PickSection, k.PickRaw,
		k.Enter, k.Back,
	}
}

// global returns the bindings handled by the root model.
func (k keyMap) global() []key.Binding {
	return []key.Binding{
		k.Jump, k.NextPanel, k.PrevPanel, k.ScreenNext, k.ScreenPrev,
		k.OpenEditor, k.CancelQuery, k.CommandLog, k.Help, k.Quit,
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
	actSchemaDiff
	actRefresh
	actFilter
	actExpandNode
	actCollapseNode
	actEditQuery
	actRunEditor
	actExplainQuery
	actClearQuery
	actHistory
	actSaveSnippet
	actColLeft
	actColRight
	actNextPage
	actPrevPage
	actSortColumn
	actSelectRows
	actExtendSelectionUp
	actExtendSelectionDown
	actWhereFilter
	actClearFilter
	actViewCell
	actRowDetail
	actFollowFK
	actIncomingRefs
	actBrowseBack
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
	actCopyPageCSV
	actCopyPageJSON
	actCopySelectionMenu
	actCopySelectionCSV
	actCopySelectionJSON
	actCopySelectionInsert
	actCopySelectionColumn
	actExportTable
	actExportDDL
	actCancelExport
	actExportDatabaseDDL

	// Dump and restore through the engine's own external tool. actBackup
	// opens the menu; the two directions are its entries, dispatched
	// through runAction like every other menu.
	actBackup
	actDumpDatabase
	actRestoreDump
	actCancelBackup
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
			{actSchemaDiff, k.SchemaDiff},
			{actBackup, k.Backup},
			{actCancelBackup, k.CancelBackup},
		}
	case panelObjects:
		return []action{
			{actExpandNode, k.ExpandNode},
			{actCollapseNode, k.CollapseNode},
			{actRefresh, k.Refresh},
			{actFilter, k.Filter},
			{actExportDatabaseDDL, k.ExportDatabaseDDL},
			{actBackup, k.Backup},
			{actCancelBackup, k.CancelBackup},
		}
	case panelQuery:
		return []action{
			{actEditQuery, k.EditQuery},
			{actRunEditor, k.RunEditor},
			{actExplainQuery, k.ExplainQuery},
			{actClearQuery, k.ClearQuery},
			{actHistory, k.History},
			{actSaveSnippet, k.SaveSnippet},
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
			{actSelectRows, k.SelectRows},
			{actExtendSelectionUp, k.ShiftUp},
			{actExtendSelectionDown, k.ShiftDown},
			{actCopySelectionMenu, k.CopySelection},
			{actWhereFilter, k.WhereFilter},
			{actClearFilter, k.ClearFilter},
			{actViewCell, k.ViewCell},
			{actRowDetail, k.RowDetail},
			{actFollowFK, k.FollowFK},
			{actIncomingRefs, k.IncomingRefs},
			{actBrowseBack, k.BrowseBack},
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

// writeBindings are the keys that stage or apply a change. A read-only
// connection drops them from the options bar — they would only ever
// answer with "connection is read-only" — while `?` keeps listing them,
// so every binding is still documented in exactly one place.
func (k keyMap) writeBindings() []key.Binding {
	return []key.Binding{k.EditCell, k.DeleteRow, k.InsertRow, k.DuplicateRow, k.CommitChanges}
}

// withoutBindings drops every binding of hide from all, matching on the
// help text — the only comparable part of a key.Binding.
func withoutBindings(all, hide []key.Binding) []key.Binding {
	drop := make(map[key.Help]bool, len(hide))
	for _, b := range hide {
		drop[b.Help()] = true
	}
	out := make([]key.Binding, 0, len(all))
	for _, b := range all {
		if !drop[b.Help()] {
			out = append(out, b)
		}
	}
	return out
}

// helpGroup is one labeled column of the `?` listing. The label names the
// sub-context the bindings belong to, so a key that means one thing while
// browsing and another inside a modal (h/← , e, tab…) reads as scoped
// instead of duplicated or conflicting — see issue #106.
type helpGroup struct {
	label    string
	bindings []key.Binding
}

// helpGroups is the full `?` listing for the focused panel, drawn from the
// same bindings as the options bar. Every group beyond the first names the
// sub-context its keys only apply in, since those keys are not live while
// browsing the panel itself.
func (k keyMap) helpGroups(id panelID) []helpGroup {
	groups := []helpGroup{{"", k.actions(id)}}
	// The editor's insert mode swallows almost every key, so its own
	// short list is documented next to the panel's normal-mode actions
	// rather than being reachable only from a mode `?` cannot open.
	if id == panelQuery {
		groups = append(groups,
			helpGroup{"In vim normal mode:", k.editorNormal()},
			helpGroup{"In insert mode:", k.editorInsert()},
			helpGroup{"In the completion popup:", k.editorCompletion()},
			helpGroup{"In history & snippets:", k.historyPane()},
			helpGroup{"Result grid (under the editor):", k.queryResultKeys()},
		)
	}
	// The cell editor and the insert form both open from the data grid,
	// and so does the date picker they hand temporal columns to.
	if id == panelMain {
		groups = append(groups, helpGroup{"In the date picker:", k.datePicker()})
	}
	// The connection form is opened from the Connections panel, so its
	// path-completion keys are documented there.
	if id == panelConnections {
		groups = append(groups, helpGroup{"In forms:", k.formPathComplete()})
	}
	return append(groups,
		helpGroup{"", k.navigationFor(id)},
		helpGroup{"", k.global()},
	)
}

// bindingSlot pairs a `[keys]` config action name with the keyMap field it
// overrides.
type bindingSlot struct {
	name string
	ptr  *key.Binding
}

// slots is the single source both applyKeyOverrides and its "unknown
// action" error message read: every config-facing action name paired with
// the binding it controls. The name is the field name in kebab-case.
func (k *keyMap) slots() []bindingSlot {
	return []bindingSlot{
		{"up", &k.Up}, {"down", &k.Down}, {"enter", &k.Enter}, {"back", &k.Back},
		{"accept-changes", &k.AcceptChanges},

		{"jump", &k.Jump}, {"next-panel", &k.NextPanel}, {"prev-panel", &k.PrevPanel},

		{"screen-next", &k.ScreenNext}, {"screen-prev", &k.ScreenPrev}, {"open-editor", &k.OpenEditor},
		{"leave-insert", &k.LeaveInsert}, {"cancel-query", &k.CancelQuery}, {"command-log", &k.CommandLog},
		{"help", &k.Help}, {"quit", &k.Quit},

		{"new-connection", &k.NewConnection}, {"edit-connection", &k.EditConnection},
		{"drop-connection", &k.DropConnection}, {"test-connection", &k.TestConnection},
		{"schema-diff", &k.SchemaDiff},
		{"connect", &k.Connect}, {"refresh", &k.Refresh}, {"actions", &k.Actions}, {"filter", &k.Filter},
		{"expand-node", &k.ExpandNode}, {"collapse-node", &k.CollapseNode},

		{"edit-query", &k.EditQuery}, {"run-editor", &k.RunEditor},
		{"explain-query", &k.ExplainQuery}, {"clear-query", &k.ClearQuery},
		{"history", &k.History}, {"save-snippet", &k.SaveSnippet},
		{"hist-load", &k.HistLoad}, {"hist-run", &k.HistRun},
		{"hist-snippet", &k.HistSnippet}, {"hist-delete", &k.HistDelete},
		{"hist-section", &k.HistSection},

		{"vim-left", &k.VimLeft}, {"vim-right", &k.VimRight},
		{"vim-word-fwd", &k.VimWordFwd}, {"vim-word-back", &k.VimWordBack},
		{"vim-line-start", &k.VimLineStart}, {"vim-line-end", &k.VimLineEnd},
		{"vim-top", &k.VimTop}, {"vim-bottom", &k.VimBottom},
		{"vim-append", &k.VimAppend}, {"vim-open-below", &k.VimOpenBelow},
		{"vim-open-above", &k.VimOpenAbove}, {"vim-delete-char", &k.VimDeleteChar},
		{"vim-delete-line", &k.VimDeleteLine}, {"vim-yank-line", &k.VimYankLine},
		{"vim-paste", &k.VimPaste},

		{"complete", &k.Complete}, {"complete-next", &k.CompleteNext},
		{"complete-prev", &k.CompletePrev}, {"accept-completion", &k.AcceptCompletion},
		{"close-completion", &k.CloseCompletion},

		{"col-left", &k.ColLeft}, {"col-right", &k.ColRight}, {"next-page", &k.NextPage},
		{"prev-page", &k.PrevPage}, {"sort-column", &k.SortColumn}, {"where-filter", &k.WhereFilter},
		{"clear-filter", &k.ClearFilter}, {"view-cell", &k.ViewCell},
		{"row-detail", &k.RowDetail},
		{"select-rows", &k.SelectRows}, {"copy-selection", &k.CopySelection},
		{"shift-up", &k.ShiftUp}, {"shift-down", &k.ShiftDown},
		{"follow-fk", &k.FollowFK}, {"incoming-refs", &k.IncomingRefs},
		{"browse-back", &k.BrowseBack},

		{"edit-cell", &k.EditCell}, {"delete-row", &k.DeleteRow}, {"insert-row", &k.InsertRow},
		{"duplicate-row", &k.DuplicateRow}, {"commit-changes", &k.CommitChanges},
		{"unstage-cell", &k.UnstageCell}, {"discard-changes", &k.DiscardChanges},

		{"pick-prev", &k.PickPrev}, {"pick-next", &k.PickNext},
		{"pick-up", &k.PickUp}, {"pick-down", &k.PickDown},
		{"pick-month-prev", &k.PickMonthPrev}, {"pick-month-next", &k.PickMonthNext},
		{"pick-today", &k.PickToday}, {"pick-section", &k.PickSection},
		{"pick-raw", &k.PickRaw}, {"open-picker", &k.OpenPicker},

		{"prev-main-tab", &k.PrevMainTab}, {"next-main-tab", &k.NextMainTab},

		{"copy-menu", &k.CopyMenu}, {"export-table", &k.ExportTable}, {"cancel-export", &k.CancelExport},
		{"export-database-ddl", &k.ExportDatabaseDDL},
		{"backup", &k.Backup}, {"cancel-backup", &k.CancelBackup},
	}
}

// actionNames lists every valid `[keys]` action name, sorted, for use in
// error messages.
func actionNames() []string {
	var k keyMap
	names := make([]string, 0, len(k.slots()))
	for _, s := range k.slots() {
		names = append(names, s.name)
	}
	sort.Strings(names)
	return names
}

// applyKeyOverrides applies the `[keys]` config section on top of km's
// defaults: each entry rebinds one action to a new comma-separated list of
// keys, keeping its existing help description. An unknown action name or an
// empty key list is a config error, not a panic — the caller is expected to
// fail startup with it.
func applyKeyOverrides(km *keyMap, overrides map[string]string) error {
	lookup := map[string]*key.Binding{}
	for _, s := range km.slots() {
		lookup[s.name] = s.ptr
	}

	names := make([]string, 0, len(overrides))
	for name := range overrides {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		binding, ok := lookup[name]
		if !ok {
			return fmt.Errorf(
				"unknown key action %q (valid actions: %s)", name, strings.Join(actionNames(), ", "))
		}
		var keys []string
		for _, part := range strings.Split(overrides[name], ",") {
			if part = strings.TrimSpace(part); part != "" {
				keys = append(keys, part)
			}
		}
		if len(keys) == 0 {
			return fmt.Errorf("key action %q: empty key value", name)
		}
		binding.SetKeys(keys...)
		binding.SetHelp(strings.Join(keys, "/"), binding.Help().Desc)
	}
	return nil
}
