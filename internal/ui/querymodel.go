package ui

// queryModel is panel [3] as a sub-model: the editor buffer and its mode,
// the script currently executing, the editor's transaction, the
// parameter memory, the autocomplete popup with the schema cache behind
// it, and the highlight cache. Every open connection owns one (see
// sessions.go) and the root routes to the one on screen; like gridModel
// it is not a tea.Model, because its keys are reduced by the root the
// same way the side panels' are. The history and the snippets behind the
// `H` pane are shared by every connection and live in recallStore. See
// wiki/design/tui-shell-architecture.md.
type queryModel struct {
	// editor is panel [3] — the buffer and its mode, which outlive every
	// focus change — and run is the script currently executing, if any.
	editor queryEditor
	run    queryRun
	// tx is the editor's explicit transaction, nil when none is open.
	// Every run started while it is set executes inside it. See tx.go.
	tx *editorTx

	// params remembers the values last bound to each statement's
	// placeholders, so re-running a query or a snippet opens the prompt
	// pre-filled. It is a pointer so every copied Model shares one store,
	// the way changes shares one changeset, and it is deliberately
	// session-scoped: parameter values are often exactly the data that
	// must not survive on disk. See params.go.
	params *paramMemory

	// completion is the editor's autocomplete popup, and schema the
	// column cache behind it. The cache keys itself on connection +
	// database and drops itself when either changes.
	completion completion
	schema     schemaCache

	// hl caches the query editor's tokenization and wrap geometry, so a
	// pure cursor move re-styles only the visible rows instead of
	// re-highlighting the whole buffer. See highlight.go.
	hl *editorCache
}

// newQueryModel builds an empty editor with a fresh parameter memory and
// highlight cache.
func newQueryModel() queryModel {
	return queryModel{
		editor: newQueryEditor(),
		params: newParamMemory(),
		hl:     &editorCache{},
	}
}
