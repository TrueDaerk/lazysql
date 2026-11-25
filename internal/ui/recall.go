package ui

import (
	"lazysql/internal/history"
	"lazysql/internal/snippets"
)

// recallStore holds the lists every open connection shares because they
// mirror one file each on disk: the query history and the named snippets
// behind the `H` pane, and the filter history behind the grid's `/` line.
// They live on the root Model rather than in a connection's session, so a
// statement recorded by a background connection lands in the same list
// the pane reads.
type recallStore struct {
	// history is the persistent query history behind panel [3], newest
	// first.
	history []history.Entry

	// snippets are the named statements behind the pane's Snippets
	// section, sorted by name. They are the deliberate half of the recall
	// story the history is the automatic half of.
	snippets []snippets.Snippet

	// filters is the per-relation filter history behind the recall keys
	// of the grid's `/` line, newest first, across every scope.
	filters []history.Entry
}

// ---------- history ----------

// pushHistory adds e to the front of the history and caps its length.
// Re-recording the newest entry — same statement, engine and connection
// — is a no-op and reports false: `enter` in the history pane would
// otherwise grow the list by one on every replay, and the timestamp is
// the only thing that would differ.
func (r *recallStore) pushHistory(e history.Entry) bool {
	if len(r.history) > 0 {
		newest := r.history[0]
		if newest.SQL == e.SQL && newest.Engine == e.Engine && newest.Connection == e.Connection {
			return false
		}
	}
	r.history = append([]history.Entry{e}, r.history...)
	if len(r.history) > history.MaxEntries {
		r.history = r.history[:history.MaxEntries]
	}
	return true
}

// dropHistory removes the entry equal to e — matched by value, because
// the history pane's snapshot may be older than the list — and reports
// whether one was found.
func (r *recallStore) dropHistory(e history.Entry) bool {
	for i, me := range r.history {
		if me.SQL == e.SQL && me.Engine == e.Engine && me.Connection == e.Connection && me.At.Equal(e.At) {
			r.history = append(r.history[:i:i], r.history[i+1:]...)
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
func (r *recallStore) putSnippet(s snippets.Snippet) (replaced bool) {
	if old, ok := snippets.Find(r.snippets, s.Name); ok && !old.CreatedAt.IsZero() {
		s.CreatedAt = old.CreatedAt
	}
	r.snippets, replaced = snippets.Put(r.snippets, s)
	return replaced
}

// removeSnippet drops the snippet called name and reports whether there
// was one.
func (r *recallStore) removeSnippet(name string) bool {
	list, deleted := snippets.Delete(r.snippets, name)
	if deleted {
		r.snippets = list
	}
	return deleted
}
