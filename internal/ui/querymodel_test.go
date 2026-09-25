package ui

import (
	"testing"
	"time"

	"lazysql/internal/history"
	"lazysql/internal/snippets"
)

// These tests exercise the query sub-model on its own: no root Model, no
// driver, no rendered frame. Before the query state was extracted from
// Model (issue #229) the history and snippet bookkeeping could only be
// reached by running a statement through a full shell.

func TestQueryPushHistoryDedupesNewestAndCaps(t *testing.T) {
	q := newQueryModel()
	e := history.Entry{SQL: "SELECT 1", Connection: "c", Engine: "sqlite", At: time.Now()}
	if !q.pushHistory(e) {
		t.Fatal("first push was reported as a duplicate")
	}
	// The same statement on the same connection replayed: not recorded
	// twice, whatever its timestamp.
	e2 := e
	e2.At = e.At.Add(time.Minute)
	if q.pushHistory(e2) {
		t.Error("re-recording the newest entry was not deduplicated")
	}
	if len(q.history) != 1 {
		t.Fatalf("history has %d entries, want 1", len(q.history))
	}
	// The same statement on another connection is a different entry.
	e3 := e
	e3.Connection = "other"
	if !q.pushHistory(e3) || len(q.history) != 2 || q.history[0].Connection != "other" {
		t.Fatalf("history after a push from another connection = %#v", q.history)
	}

	for i := 0; i < history.MaxEntries+10; i++ {
		q.pushHistory(history.Entry{SQL: "SELECT " + string(rune('a'+i%26)) + string(rune('a'+i/26)), Connection: "c"})
	}
	if n := len(q.history); n != history.MaxEntries {
		t.Errorf("history holds %d entries, want the cap %d", n, history.MaxEntries)
	}
}

func TestQueryDropHistoryMatchesByValue(t *testing.T) {
	q := newQueryModel()
	at := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	a := history.Entry{SQL: "SELECT a", Connection: "c", At: at}
	b := history.Entry{SQL: "SELECT b", Connection: "c", At: at.Add(time.Second)}
	q.pushHistory(a)
	q.pushHistory(b)

	// A stale snapshot of a: same value, different slice.
	if !q.dropHistory(history.Entry{SQL: "SELECT a", Connection: "c", At: at}) {
		t.Fatal("dropHistory did not find an entry equal by value")
	}
	if len(q.history) != 1 || q.history[0].SQL != "SELECT b" {
		t.Errorf("history after drop = %#v, want only SELECT b", q.history)
	}
	if q.dropHistory(a) {
		t.Error("dropHistory reported a second removal of the same entry")
	}
}

func TestQuerySnippetsKeepCreationTimeOnOverwrite(t *testing.T) {
	q := newQueryModel()
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if q.putSnippet(snippets.Snippet{Name: "top", SQL: "SELECT 1", CreatedAt: created}) {
		t.Fatal("the first save was reported as an overwrite")
	}
	if !q.putSnippet(snippets.Snippet{Name: "top", SQL: "SELECT 2", CreatedAt: created.Add(time.Hour)}) {
		t.Fatal("saving under an existing name was not reported as an overwrite")
	}
	got, ok := snippets.Find(q.snippets, "top")
	if !ok || got.SQL != "SELECT 2" || !got.CreatedAt.Equal(created) {
		t.Errorf("overwritten snippet = %#v, want SELECT 2 with the original creation time", got)
	}

	if q.removeSnippet("missing") {
		t.Error("removing an unknown snippet reported success")
	}
	if !q.removeSnippet("top") || len(q.snippets) != 0 {
		t.Errorf("removeSnippet left %#v", q.snippets)
	}
}
