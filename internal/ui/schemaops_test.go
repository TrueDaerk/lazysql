package ui

import (
	"context"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"lazysql/internal/db"
)

// schemaMenu presses `S` and returns the menu it opened.
func schemaMenu(t *testing.T, m Model) (Model, *menuModal) {
	t.Helper()
	m = send(t, m, press('S'))
	mm, ok := m.modal.(*menuModal)
	if !ok {
		t.Fatalf("S opened %T, want the schema menu", m.modal)
	}
	return m, mm
}

// menuLabel returns the label of the entry bound to key k.
func menuLabel(mm *menuModal, k string) (string, bool) {
	for _, e := range mm.entries {
		if e.key == k {
			return e.label, true
		}
	}
	return "", false
}

// formOf returns the open form modal.
func formOf(t *testing.T, m Model) *formModal {
	t.Helper()
	f, ok := m.modal.(*formModal)
	if !ok {
		t.Fatalf("modal = %T, want a form", m.modal)
	}
	return f
}

// fill sets one field of a form: text is typed, "true"/"false" toggles a
// bool, and a select picks the choice with that value.
func fill(t *testing.T, f *formModal, name, value string) {
	t.Helper()
	fl := f.field(name)
	if fl == nil {
		t.Fatalf("form has no field %q", name)
	}
	switch fl.kind {
	case fieldBool:
		fl.on = value == "true"
	case fieldSelect:
		i := slices.Index(fl.values, value)
		if i < 0 {
			t.Fatalf("field %q has no choice %q (%v)", name, value, fl.values)
		}
		fl.choice = i
	default:
		fl.input.SetValue(value)
	}
}

func gridColumns(t *testing.T, m Model) []string {
	t.Helper()
	cols, err := m.driver.TableColumns(context.Background(), "", "grid")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, c := range cols {
		out = append(out, c.Name)
	}
	return out
}

// commitAll opens the commit modal and confirms it, returning the modal
// body the user was shown.
func commitAll(t *testing.T, m Model) (Model, string) {
	t.Helper()
	m = send(t, m, press('c'))
	cm, ok := m.modal.(*confirmModal)
	if !ok {
		t.Fatalf("c opened %T, want the commit modal", m.modal)
	}
	body := cm.body
	return send(t, m, special(tea.KeyEnter, 0)), body
}

// Adding a column stages it — nothing runs, the Structure tab lists it as
// staged — and the commit runs the exact statement the preview showed,
// after which the metadata is re-read.
func TestStageAddColumnAndCommit(t *testing.T) {
	m := dataBrowsing(t)
	m = send(t, m, press('>')) // Structure
	m, mm := schemaMenu(t, m)
	if l, _ := menuLabel(mm, "a"); l != "add column…" {
		t.Fatalf("add column entry = %q", l)
	}
	m = send(t, m, press('a'))
	f := formOf(t, m)
	fill(t, f, "name", "score")
	fill(t, f, "type", "integer")
	fill(t, f, "notnull", "true")
	fill(t, f, "default", "number")
	fill(t, f, "value", "0")
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.modal != nil {
		t.Fatalf("form stayed open: %q", formOf(t, m).err)
	}

	if n := len(m.grid.changes.SchemaChanges()); n != 1 {
		t.Fatalf("staged schema changes = %d, want 1", n)
	}
	if slices.Contains(gridColumns(t, m), "score") {
		t.Fatal("ADD COLUMN executed before the commit")
	}
	want := `ALTER TABLE "grid" ADD COLUMN "score" integer NOT NULL DEFAULT 0`
	if !logContains(m, "-- stage: "+want) {
		t.Fatalf("stage line missing from the command log: %v", m.commandLog)
	}
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "add column grid.score integer") {
		t.Fatalf("the Structure tab does not show the staged change:\n%s", view)
	}

	m, body := commitAll(t, m)
	if !strings.Contains(body, want+";") {
		t.Fatalf("commit preview = %q, want the exact DDL", body)
	}
	if !strings.Contains(body, "one transaction") {
		t.Fatalf("commit preview = %q, want the transaction note", body)
	}
	if !slices.Contains(gridColumns(t, m), "score") {
		t.Fatal("the commit did not add the column")
	}
	if m.grid.changes.Len() != 0 {
		t.Fatal("the changeset survived a successful commit")
	}
	found := false
	for _, c := range m.meta.cols {
		found = found || c.Name == "score"
	}
	if !found {
		t.Fatal("the Structure tab still shows the columns from before the commit")
	}
}

// esc cancels at every step: the menu, and the form it opened.
func TestSchemaMenuEscCancels(t *testing.T) {
	m := dataBrowsing(t)
	m, _ = schemaMenu(t, m)
	m = send(t, m, special(tea.KeyEscape, 0))
	if m.modal != nil {
		t.Fatalf("esc left %T open", m.modal)
	}
	m, _ = schemaMenu(t, m)
	m = send(t, m, press('i'))
	formOf(t, m)
	m = send(t, m, special(tea.KeyEscape, 0))
	if m.modal != nil || m.grid.changes.Len() != 0 {
		t.Fatalf("esc on the form: modal %T, %d staged", m.modal, m.grid.changes.Len())
	}
}

// An operation the engine cannot do is labelled so in the menu, and
// choosing it explains instead of staging.
func TestSchemaMenuSaysWhatSQLiteCannotDo(t *testing.T) {
	m := dataBrowsing(t)
	m = send(t, m, press('2'))
	if !m.panels[panelObjects].selectByName("grid") {
		t.Fatal("grid not in [2]")
	}
	m, mm := schemaMenu(t, m)
	label, _ := menuLabel(mm, "t")
	if !strings.Contains(label, "not supported by SQLite") {
		t.Fatalf("truncate entry = %q, want it marked unsupported", label)
	}
	m = send(t, m, press('t'))
	cm, ok := m.modal.(*confirmModal)
	if !ok || !strings.Contains(cm.body, "TRUNCATE") {
		t.Fatalf("choosing it opened %T %+v, want the explanation", m.modal, m.modal)
	}
	if m.grid.changes.Len() != 0 {
		t.Fatal("an unsupported operation was staged")
	}

	// The alter form on SQLite is the rename alone, and says why.
	m = send(t, m, special(tea.KeyEscape, 0), special(tea.KeyEnter, 0), press('>'))
	m, _ = schemaMenu(t, m)
	m = send(t, m, press('e'))
	f := formOf(t, m)
	if f.field("type") != nil || len(f.fields) != 1 {
		t.Fatalf("SQLite alter form has %d fields, want the name alone", len(f.fields))
	}
	if body := strings.Join(f.body(f), " "); !strings.Contains(body, "only rename") {
		t.Fatalf("alter form body = %q, want the reason", body)
	}
	fill(t, f, "name", "ident")
	m = send(t, m, special(tea.KeyEnter, 0))
	m, _ = commitAll(t, m)
	if cols := gridColumns(t, m); cols[0] != "ident" {
		t.Fatalf("columns after the rename = %v", cols)
	}
}

// Dropping a relation gets its own confirm on top of staging, marks the
// node in [2] until commit, and after the commit the relation is gone
// from [2] and from the main view.
func TestDropTableStagesThenDisappears(t *testing.T) {
	m := dataBrowsing(t)
	m = send(t, m, press('2'))
	if !m.panels[panelObjects].selectByName("grid") {
		t.Fatal("grid not in [2]")
	}
	m, _ = schemaMenu(t, m)
	m = send(t, m, press('d'))
	cm, ok := m.modal.(*confirmModal)
	if !ok || !cm.danger || !strings.Contains(cm.body, `DROP TABLE "grid"`) {
		t.Fatalf("d opened %T, want a danger confirm naming the DROP", m.modal)
	}
	if m.grid.changes.Len() != 0 {
		t.Fatal("staged before the confirm")
	}
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.grid.changes.Len() != 1 {
		t.Fatalf("changeset = %d after the confirm, want the drop staged", m.grid.changes.Len())
	}
	if n := m.tree.category("", catTables); n == nil || !treeHasNote(n, "grid", "staged: drop") {
		t.Fatal("the [2] node does not show the staged drop")
	}
	if rels, _ := m.driver.ListTables(context.Background(), ""); !slices.Contains(rels, "grid") {
		t.Fatal("DROP TABLE executed before the commit")
	}

	m, _ = commitAll(t, m)
	if rels, _ := m.driver.ListTables(context.Background(), ""); slices.Contains(rels, "grid") {
		t.Fatal("the commit did not drop the table")
	}
	for _, it := range m.panels[panelObjects].items {
		if strings.TrimSpace(it) == "grid" {
			t.Fatalf("the dropped table lingers in [2]: %v", m.panels[panelObjects].items)
		}
	}
	if m.grid.data.browsing() {
		t.Fatal("the main view still shows the dropped table")
	}
}

func treeHasNote(cat *treeNode, name, note string) bool {
	for _, c := range cat.children {
		if c.name == name {
			got, _ := treeRow{node: c}.note()
			return got == note
		}
	}
	return false
}

// Renaming the open table from [2] reopens it under its new name after
// the commit.
func TestRenameTableReopens(t *testing.T) {
	m := dataBrowsing(t)
	m = send(t, m, press('2'))
	m.panels[panelObjects].selectByName("grid")
	m, _ = schemaMenu(t, m)
	m = send(t, m, press('r'))
	p, ok := m.modal.(*promptModal)
	if !ok {
		t.Fatalf("r opened %T, want the rename prompt", m.modal)
	}
	p.input.SetValue("grid2")
	m = send(t, m, special(tea.KeyEnter, 0))
	m, _ = commitAll(t, m)
	if m.grid.data.table != "grid2" {
		t.Fatalf("open table = %q, want the renamed one", m.grid.data.table)
	}
	if !m.panels[panelObjects].selectByName("grid2") {
		t.Fatalf("[2] does not list the renamed table: %v", m.panels[panelObjects].items)
	}
	// Leave the fixture as the other tests expect it.
	if _, err := m.driver.Exec(context.Background(), `ALTER TABLE grid2 RENAME TO grid`); err != nil {
		t.Fatal(err)
	}
}

// Creating and dropping an index from the main view.
func TestCreateAndDropIndex(t *testing.T) {
	m := dataBrowsing(t)
	m = send(t, m, press('>'), press('>')) // Indexes
	m, _ = schemaMenu(t, m)
	m = send(t, m, press('i'))
	f := formOf(t, m)
	fill(t, f, "columns", "name, nope")
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.modal == nil || m.grid.changes.Len() != 0 {
		t.Fatal("an index over a column the table lacks was staged")
	}
	fill(t, f, "columns", "name")
	fill(t, f, "unique", "true")
	m = send(t, m, special(tea.KeyEnter, 0))
	m, body := commitAll(t, m)
	if !strings.Contains(body, `CREATE UNIQUE INDEX "grid_name_idx" ON "grid" ("name")`) {
		t.Fatalf("commit preview = %q", body)
	}
	if len(m.meta.indexes) == 0 || m.meta.indexes[0].Name != "grid_name_idx" {
		t.Fatalf("indexes after the commit = %+v", m.meta.indexes)
	}

	m, mm := schemaMenu(t, m)
	if _, ok := menuLabel(mm, "x"); !ok {
		t.Fatal("no drop index entry with an index present")
	}
	m = send(t, m, press('x'))
	m = send(t, m, special(tea.KeyEnter, 0)) // pick the only index
	cm, ok := m.modal.(*confirmModal)
	if !ok || !cm.danger {
		t.Fatalf("picking the index opened %T, want the destructive confirm", m.modal)
	}
	m = send(t, m, special(tea.KeyEnter, 0))
	m, _ = commitAll(t, m)
	if len(m.meta.indexes) != 0 {
		t.Fatalf("indexes after the drop = %+v", m.meta.indexes)
	}
}

// The create-table draft: name and first column, another column, stage,
// commit — and the new table shows up in [2].
func TestCreateTableDraft(t *testing.T) {
	m := dataBrowsing(t)
	m = send(t, m, press('2'))
	m, _ = schemaMenu(t, m)
	m = send(t, m, press('n'))
	f := formOf(t, m)
	fill(t, f, "table", "fresh")
	m = send(t, m, special(tea.KeyEnter, 0))
	if _, ok := m.modal.(*menuModal); !ok {
		t.Fatalf("the first form led to %T, want the draft menu", m.modal)
	}
	m = send(t, m, press('a'))
	f = formOf(t, m)
	fill(t, f, "name", "label")
	fill(t, f, "type", "text")
	fill(t, f, "default", "text")
	fill(t, f, "value", "it's")
	m = send(t, m, special(tea.KeyEnter, 0))
	draft, ok := m.modal.(*menuModal)
	if !ok || !strings.Contains(draft.title, "2 columns") {
		t.Fatalf("back at %T, want the draft with 2 columns", m.modal)
	}
	m = send(t, m, press('s'))
	if n := m.tree.category("", catTables); n == nil || !strings.Contains(n.staged, "fresh") {
		t.Fatal("the Tables category does not name the staged table")
	}
	m, body := commitAll(t, m)
	want := `CREATE TABLE "fresh" ("id" integer NOT NULL, "label" text DEFAULT 'it''s', PRIMARY KEY ("id"))`
	if !strings.Contains(body, want) {
		t.Fatalf("commit preview = %q, want %q", body, want)
	}
	if !m.panels[panelObjects].selectByName("fresh") {
		t.Fatalf("[2] does not list the new table: %v", m.panels[panelObjects].items)
	}
	if _, err := m.driver.Exec(context.Background(), `DROP TABLE fresh`); err != nil {
		t.Fatal(err)
	}
}

// A type that is not a type keeps the form open with the reason, and
// stages nothing.
func TestColumnFormRejectsBadType(t *testing.T) {
	m := dataBrowsing(t)
	m, _ = schemaMenu(t, m)
	m = send(t, m, press('a'))
	f := formOf(t, m)
	fill(t, f, "name", "x")
	fill(t, f, "type", "int, evil int")
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.modal == nil || m.grid.changes.Len() != 0 {
		t.Fatal("a column with a hostile type was staged")
	}
}

// The staged-changes list unstages one change at a time.
func TestUnstageSchemaChange(t *testing.T) {
	m := dataBrowsing(t)
	m.grid.changes.StageSchema(db.DropColumn{Table: "grid", Column: "note"})
	m, mm := schemaMenu(t, m)
	if l, ok := menuLabel(mm, "u"); !ok || !strings.Contains(l, "(1)") {
		t.Fatalf("staged entry = %q", l)
	}
	m = send(t, m, press('u'), special(tea.KeyEnter, 0))
	if m.grid.changes.Len() != 0 {
		t.Fatal("enter in the staged list did not unstage")
	}
}

// A read-only connection refuses `S` before any menu opens, and the key
// is not offered in the options bar.
func TestReadOnlyBlocksSchemaMenu(t *testing.T) {
	m := readOnlyGrid(t)
	m = send(t, m, press('S'))
	if m.modal != nil {
		t.Fatalf("S opened %T on a read-only connection", m.modal)
	}
	if !logContains(m, "schema change blocked: connection is read-only") {
		t.Fatalf("command log = %v", m.commandLog)
	}
	for _, b := range m.optionsBarBindings() {
		if b.Help().Key == "S" {
			t.Fatal("S is offered in the options bar of a read-only connection")
		}
	}
	m = send(t, m, press('2'), press('S'))
	if m.modal != nil {
		t.Fatalf("S in [2] opened %T on a read-only connection", m.modal)
	}
}
