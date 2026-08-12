package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/db"
)

// The engine select is the form's first field, and its choice retargets the
// placeholders below it: the port hint always shows the number the chosen
// engine defaults to.
func TestConnectionFormPortHintFollowsEngine(t *testing.T) {
	m := send(t, sized(120, 40), press('1'), press('n'))
	f := m.modal.(*formModal)

	if f.current() != f.field("engine") {
		t.Fatalf("form opens on %q, want the engine select", f.current().name)
	}

	// Walk the select until PostgreSQL is chosen, via the same key the
	// user would press, so the onChange hook actually fires.
	for i := 0; i < len(f.field("engine").values); i++ {
		if db.Engine(f.rawValue("engine")) == db.EnginePostgres {
			break
		}
		m = send(t, m, special(tea.KeyRight, 0))
	}
	if got := db.Engine(f.rawValue("engine")); got != db.EnginePostgres {
		t.Fatalf("engine = %q after cycling, want postgres", got)
	}
	if got := f.field("port").input.Placeholder; got != "5432" {
		t.Fatalf("port placeholder = %q, want 5432", got)
	}
}

// Submitting an incomplete form must not close it: the first failed
// validator blocks the submit, its message lands in the error line, and the
// cursor jumps to the offending field.
func TestConnectionFormSubmitBlockedInline(t *testing.T) {
	m := send(t, sized(120, 40), press('1'), press('n'))
	f := m.modal.(*formModal)

	m = send(t, m, special(tea.KeyEnter, 0))
	if m.modal == nil {
		t.Fatal("form closed despite an empty name")
	}
	if !strings.Contains(f.err, "name is required") {
		t.Fatalf("err = %q, want the name complaint", f.err)
	}
	if f.current() != f.field("name") {
		t.Fatalf("cursor on %q after failed submit, want name", f.current().name)
	}
	// After the submit attempt the empty required fields are marked
	// inline too, not only the one under the cursor.
	if got := f.errorFor(f.field("host")); !strings.Contains(got, "required") {
		t.Fatalf("host inline error = %q, want a required mark", got)
	}
}

// A malformed value is flagged while typing, before any submit attempt.
func TestConnectionFormLiveInlineValidation(t *testing.T) {
	m := send(t, sized(120, 40), press('1'), press('n'))
	f := m.modal.(*formModal)

	f.field("port").input.SetValue("nope")
	if got := f.errorFor(f.field("port")); !strings.Contains(got, "number") {
		t.Fatalf("port inline error = %q, want the not-a-number complaint", got)
	}
	// An untouched empty field stays calm until a submit is attempted.
	if got := f.errorFor(f.field("name")); got != "" {
		t.Fatalf("name inline error = %q before submit, want none", got)
	}
}

// While a form is open the bottom options bar advertises the form contract,
// not the panel verbs the modal would swallow.
// (At narrow widths the help view elides trailing entries with "…", so the
// full set is asserted on a wide terminal.)
func TestOptionsBarShowsFormKeysWhileFormOpen(t *testing.T) {
	m := send(t, sized(190, 40), press('1'), press('n'))
	bar := m.renderOptionsBar()
	for _, want := range []string{"ctrl+t", "save"} {
		if !strings.Contains(bar, want) {
			t.Fatalf("options bar %q misses %q while the connection form is open", bar, want)
		}
	}
}
