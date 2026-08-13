package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/config"
	"lazysql/internal/db"
)

// `n` opens the engine picker, not a form: the engine is the one choice
// that decides which fields exist, so it is asked alone, first.
func TestNewConnectionOpensEnginePicker(t *testing.T) {
	m := send(t, sized(120, 40), press('1'), press('n'))
	p, ok := m.modal.(*enginePickerModal)
	if !ok {
		t.Fatalf("n opened %T, want the engine picker", m.modal)
	}
	if len(p.choices) < 5 {
		t.Fatalf("picker offers %d engines, want all registered ones", len(p.choices))
	}
	// esc from step one cancels the whole flow.
	m = send(t, m, special(tea.KeyEscape, 0))
	if m.modal != nil {
		t.Fatalf("esc left %T open, want no modal", m.modal)
	}
}

// A digit picks the engine and lands in that engine's form, with the
// placeholders describing the chosen engine — no cycling through a select.
func TestPickingPostgresBuildsItsForm(t *testing.T) {
	m := send(t, sized(120, 40), press('1'), press('n'))
	m = send(t, m, pickEngineKey(t, m, db.EnginePostgres))
	f, ok := m.modal.(*formModal)
	if !ok {
		t.Fatalf("picking postgres opened %T, want *formModal", m.modal)
	}
	if got := db.Engine(f.rawValue("engine")); got != db.EnginePostgres {
		t.Fatalf("engine = %q, want postgres", got)
	}
	if got := f.field("port").input.Placeholder; got != "5432" {
		t.Fatalf("port placeholder = %q, want 5432", got)
	}
	// The cursor opens on Name — the first thing to type, since the host
	// is already prefilled for the local-dev case.
	if cur := f.current(); cur == nil || cur.name != "name" {
		t.Fatalf("form opens on %v, want the name field", cur)
	}
	if got := f.field("host").value(); got != "localhost" {
		t.Fatalf("host prefill = %q, want localhost", got)
	}
	if !strings.Contains(f.title, "PostgreSQL") {
		t.Fatalf("title = %q, want the engine named", f.title)
	}
}

// The minimal create flow for a local dev database: n, one digit, a name,
// enter. Host is prefilled, the port and user defaults are the engine's.
func TestLocalPostgresIsFourStepsPlusName(t *testing.T) {
	m := send(t, sized(120, 40), press('1'), press('n'))
	m = send(t, m, pickEngineKey(t, m, db.EnginePostgres))
	for _, r := range "dev" {
		m = send(t, m, press(r))
	}
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.modal != nil {
		t.Fatalf("form still open: %v", m.modal.(*formModal).err)
	}
	c, ok := m.cfg.Find("dev")
	if !ok {
		t.Fatal("profile was not saved")
	}
	if c.Engine != db.EnginePostgres || c.Host != "localhost" || c.Port != 5432 {
		t.Fatalf("profile = %+v, want postgres@localhost:5432", c)
	}
}

// The minimal SQLite flow: the cursor opens on the file path, and the
// profile name derives from the file when left empty.
func TestSQLiteNameDerivesFromFile(t *testing.T) {
	m := send(t, sized(120, 40), press('1'), press('n'))
	m = send(t, m, pickEngineKey(t, m, db.EngineSQLite))
	f := m.modal.(*formModal)
	if cur := f.current(); cur == nil || cur.name != "file" {
		t.Fatalf("SQLite form opens on %v, want the file field", cur)
	}
	f.field("file").input.SetValue("/tmp/app.db")
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.modal != nil {
		t.Fatalf("form still open: %v", f.err)
	}
	c, ok := m.cfg.Find("app")
	if !ok || c.File != "/tmp/app.db" {
		t.Fatalf("profile = %+v (found %v), want name derived from the file", c, ok)
	}
}

// Submitting an incomplete form must not close it: the first failed
// validator blocks the submit, its message lands in the error line, and the
// cursor jumps to the offending field.
func TestConnectionFormSubmitBlockedInline(t *testing.T) {
	m := send(t, sized(120, 40), press('1'), press('n'))
	m = send(t, m, pickEngineKey(t, m, db.EnginePostgres))
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
}

// A malformed value is flagged while typing, before any submit attempt.
func TestConnectionFormLiveInlineValidation(t *testing.T) {
	m := send(t, sized(120, 40), press('1'), press('n'))
	m = send(t, m, pickEngineKey(t, m, db.EnginePostgres))
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

// While creating, esc from the form retreats to the engine picker with the
// draft kept; esc from the picker cancels. Editing has no step one, so esc
// closes the form outright.
func TestEscBacksOutStepByStep(t *testing.T) {
	m := send(t, sized(120, 40), press('1'), press('n'))
	m = send(t, m, pickEngineKey(t, m, db.EnginePostgres))
	f := m.modal.(*formModal)
	f.field("name").input.SetValue("half-typed")

	m = send(t, m, special(tea.KeyEscape, 0))
	p, ok := m.modal.(*enginePickerModal)
	if !ok {
		t.Fatalf("esc from the create form left %T, want the picker", m.modal)
	}
	// Re-entering the same engine restores the draft.
	m = send(t, m, special(tea.KeyEnter, 0))
	f, ok = m.modal.(*formModal)
	if !ok {
		t.Fatalf("enter on the picker left %T, want the form", m.modal)
	}
	if got := f.field("name").value(); got != "half-typed" {
		t.Fatalf("name = %q after the round trip, want the draft kept", got)
	}
	_ = p

	// Edit mode: esc closes.
	m = send(t, m, special(tea.KeyEscape, 0), special(tea.KeyEscape, 0)) // back to picker, cancel
	if m.modal != nil {
		t.Fatalf("create flow did not cancel: %T", m.modal)
	}
	m = send(t, m, press('e'))
	if _, ok := m.modal.(*formModal); !ok {
		t.Fatalf("e opened %T, want the form directly", m.modal)
	}
	m = send(t, m, special(tea.KeyEscape, 0))
	if m.modal != nil {
		t.Fatalf("esc from the edit form left %T open", m.modal)
	}
}

// The sectioned view: group headers render with their rules, and the
// engine no longer appears as a walkable field.
func TestConnectionFormRendersSections(t *testing.T) {
	m := send(t, sized(120, 40), press('1'), press('n'))
	m = send(t, m, pickEngineKey(t, m, db.EngineMySQL))
	f := m.modal.(*formModal)
	view := f.view(m.style, m.width, m.height)
	for _, want := range []string{"Profile", "Server", "Credentials", "SSH tunnel", "Advanced"} {
		if !strings.Contains(view, want) {
			t.Errorf("form view missing the %q section:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Engine") {
		t.Errorf("form view still shows an engine field:\n%s", view)
	}
	for _, fl := range f.visibleFields() {
		if fl.name == "engine" {
			t.Error("the engine is a walkable field")
		}
	}
}

// A small terminal gets a scroll window, not an overflowing modal: the
// rendered form never exceeds the terminal height, and moving the cursor
// to the last field keeps it visible.
func TestConnectionFormScrollsOnSmallTerminals(t *testing.T) {
	m := send(t, sized(80, 20), press('1'), press('n'))
	m = send(t, m, pickEngineKey(t, m, db.EngineMySQL))
	f := m.modal.(*formModal)
	f.field("ssh").on = true // the tallest the form gets

	for range f.visibleFields() {
		view := f.view(m.style, m.width, m.height)
		if got := strings.Count(view, "\n") + 1; got > m.height {
			t.Fatalf("form view is %d rows on a %d-row terminal", got, m.height)
		}
		f.move(1)
	}
	// The last field must actually be on screen when the cursor sits on it.
	f.focusField("read_only")
	view := f.view(m.style, m.width, m.height)
	if !strings.Contains(view, "Read-only") {
		t.Fatalf("cursor field scrolled out of view:\n%s", view)
	}
}

// While a form is open the bottom options bar advertises the form contract,
// not the panel verbs the modal would swallow — and the picker its own.
// (At narrow widths the help view elides trailing entries with "…", so the
// full set is asserted on a wide terminal.)
func TestOptionsBarShowsFormKeysWhileFormOpen(t *testing.T) {
	m := send(t, sized(190, 40), press('1'), press('n'))
	bar := m.renderOptionsBar()
	for _, want := range []string{"pick engine", "choose"} {
		if !strings.Contains(bar, want) {
			t.Fatalf("options bar %q misses %q while the engine picker is open", bar, want)
		}
	}
	m = send(t, m, pickEngineKey(t, m, db.EnginePostgres))
	bar = m.renderOptionsBar()
	for _, want := range []string{"ctrl+t", "ctrl+e", "save"} {
		if !strings.Contains(bar, want) {
			t.Fatalf("options bar %q misses %q while the connection form is open", bar, want)
		}
	}
}

// Edit mode prefills the engine-specific form from the stored profile for
// every engine family.
func TestEditPrefillsPerEngine(t *testing.T) {
	server := config.Connection{
		Name: "prod", Engine: db.EngineMariaDB, Host: "db.internal", Port: 3307,
		User: "app", Database: "orders",
	}
	f := newConnectionForm("Edit", server, server.Name)
	if got := f.field("host").value(); got != "db.internal" {
		t.Fatalf("host = %q", got)
	}
	if got := f.field("port").value(); got != "3307" {
		t.Fatalf("port = %q", got)
	}
	if got := f.field("password").input.Placeholder; got != "unchanged" {
		t.Fatalf("password placeholder = %q, want the unchanged marker", got)
	}
	if hasVisibleField(f, "file") {
		t.Fatal("server form shows a file field")
	}

	file := config.Connection{Name: "cache", Engine: db.EngineSQLite, File: "/data/cache.db"}
	f = newConnectionForm("Edit", file, file.Name)
	if got := f.field("file").value(); got != "/data/cache.db" {
		t.Fatalf("file = %q", got)
	}
	if hasVisibleField(f, "host") || hasVisibleField(f, "password") {
		t.Fatal("file form shows server fields")
	}
	// Edit opens on the name, not the file: the path exists already.
	if cur := f.current(); cur == nil || cur.name != "name" {
		t.Fatalf("edit form opens on %v, want name", cur)
	}
}
