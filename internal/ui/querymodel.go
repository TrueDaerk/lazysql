package ui

import (
	"lazysql/internal/history"
	"lazysql/internal/snippets"
)

// queryModel is panel [3] as a sub-model: the editor buffer and its mode,
// the script currently executing, the persistent history and the named
// snippets behind the `H` pane, the session-scoped parameter memory, the
// autocomplete popup with the schema cache behind it, and the highlight
// cache. The root Model owns one and routes to it; like gridModel it is
// not a tea.Model, because its keys are reduced by the root the same way
// the side panels' are. See wiki/design/tui-shell-architecture.md.
type queryModel struct {
	// editor is panel [3] — the buffer and its mode, which outlive every
	// focus change — and run is the script currently executing, if any.
	editor queryEditor
	run    queryRun

	// history is the persistent query history behind panel [3], newest
	// first.
	history []history.Entry

	// snippets are the named statements behind the pane's Snippets
	// section, sorted by name. They are the deliberate half of the recall
	// story the history is the automatic half of.
	snippets []snippets.Snippet

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

// ---------- history ----------

// pushHistory adds e to the front of the history and caps its length.
// Re-recording the newest entry — same statement, engine and connection
// — is a no-op and reports false: `enter` in the history pane would
// otherwise grow the list by one on every replay, and the timestamp is
// the only thing that would differ.
func (q *queryModel) pushHistory(e history.Entry) bool {
	if len(q.history) > 0 {
		newest := q.history[0]
		if newest.SQL == e.SQL && newest.Engine == e.Engine && newest.Connection == e.Connection {
			return false
		}
	}
	q.history = append([]history.Entry{e}, q.history...)
	if len(q.history) > history.MaxEntries {
		q.history = q.history[:history.MaxEntries]
	}
	return true
}

// dropHistory removes the entry equal to e — matched by value, because
// the history pane's snapshot may be older than the list — and reports
// whether one was found.
func (q *queryModel) dropHistory(e history.Entry) bool {
	for i, me := range q.history {
		if me.SQL == e.SQL && me.Engine == e.Engine && me.Connection == e.Connection && me.At.Equal(e.At) {
			q.history = append(q.history[:i:i], q.history[i+1:]...)
			return true
		}
	}
	return false
}

// ---------- snippets ----------

// putSnippet stores s under its name, keeping the list sorted, and
// reports whether it replaced an existing snippet. An overwrite keeps
// the original creation time: the name is the identity, and the date
// says how long it has been in use.
func (q *queryModel) putSnippet(s snippets.Snippet) (replaced bool) {
	if old, ok := snippets.Find(q.snippets, s.Name); ok && !old.CreatedAt.IsZero() {
		s.CreatedAt = old.CreatedAt
	}
	q.snippets, replaced = snippets.Put(q.snippets, s)
	return replaced
}

// removeSnippet drops the snippet called name and reports whether there
// was one.
func (q *queryModel) removeSnippet(name string) bool {
	list, deleted := snippets.Delete(q.snippets, name)
	if deleted {
		q.snippets = list
	}
	return deleted
}
