package ui

import (
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"lazysql/internal/db"
)

// Staged DDL. `S` opens the schema menu: in [2] Objects it offers the
// relation-level operations (create, rename, truncate, drop), in the main
// view the column- and index-level ones (add/alter/drop column, create/drop
// index). Every entry ends in a centered modal, and every modal ends in
// the changeset — nothing here executes SQL. The driver renders each change
// at staging time (Driver.SchemaSQL), which is what refuses an operation
// the engine cannot do, an invalid type or default, and any write on a
// read-only session, before it is ever staged. See
// wiki/design/staged-ddl.md.

// ---------- menus ----------

// openObjectSchemaMenu is `S` in [2] Objects: the operations on the
// relation under the cursor, plus creating a table in its namespace.
func (m *Model) openObjectSchemaMenu() tea.Cmd {
	if m.driver == nil {
		return logCmd("-- schema changes skipped: not connected")
	}
	if m.readOnly() {
		return readOnlyBlocked("schema change")
	}
	database := m.database
	n := m.selectedNode()
	if n != nil {
		database = n.database
	}
	title := "Schema — " + displayDatabase(database)
	entries := []menuEntry{
		m.schemaEntry("n", "create table…", []db.SchemaOp{db.OpCreateTable},
			func(mm *Model) tea.Cmd { return mm.openCreateTable(database) }),
	}
	if n != nil && n.kind == nodeObject && n.cat.relational() {
		kind, _ := n.cat.relationKind()
		name, word := n.name, relationWord(kind)
		title = fmt.Sprintf("Schema — %s %s", word, name)
		renameOp, dropOp := db.OpRenameTable, db.OpDropTable
		if kind == db.RelationView {
			renameOp, dropOp = db.OpRenameView, db.OpDropView
		}
		entries = append(entries, m.schemaEntry("r", "rename "+word+"…", []db.SchemaOp{renameOp},
			func(mm *Model) tea.Cmd { return mm.openRenameRelation(database, name, kind) }))
		if kind == db.RelationTable {
			entries = append(entries, m.schemaEntry("t", "truncate table…", []db.SchemaOp{db.OpTruncateTable},
				func(mm *Model) tea.Cmd {
					return mm.confirmSchema("Truncate table "+name,
						fmt.Sprintf("Stage emptying %s? Every row goes.", name),
						db.TruncateTable{Database: database, Table: name})
				}))
		}
		entries = append(entries, m.schemaEntry("d", "drop "+word+"…", []db.SchemaOp{dropOp},
			func(mm *Model) tea.Cmd {
				return mm.confirmSchema("Drop "+word+" "+name,
					fmt.Sprintf("Stage dropping %s %s? It and everything in it go.", word, name),
					db.DropRelation{Database: database, Name: name, Kind: kind})
			}))
	}
	entries = append(entries, m.stagedSchemaEntries()...)
	entries = append(entries, menuEntry{key: "esc", label: "cancel"})
	m.modal = &menuModal{title: title, entries: entries}
	return nil
}

// openTableSchemaMenu is `S` in the main view: the column and index
// operations on the open table. It needs the table's columns and indexes,
// so a cold cache fetches them first and the menu opens when they land.
func (m *Model) openTableSchemaMenu() tea.Cmd {
	if m.driver == nil || !m.grid.data.browsing() {
		return logCmd("-- schema changes skipped: no table open")
	}
	if m.readOnly() {
		return readOnlyBlocked("schema change")
	}
	if m.openRelationKind() == db.RelationView {
		m.modal = &confirmModal{
			title: "Schema — view " + m.grid.data.table,
			body:  "A view has no columns or indexes of its own to change.\n\nRename or drop it from [2] Objects (S there).",
		}
		return nil
	}
	if !m.meta.loaded {
		return m.deferUntilMeta(actTableSchemaMenu)
	}
	if m.meta.err != "" {
		return logCmd("-- schema changes skipped: %s", m.meta.err)
	}
	database, table := m.grid.data.database, m.grid.data.table
	entries := []menuEntry{
		m.schemaEntry("a", "add column…", []db.SchemaOp{db.OpAddColumn},
			func(mm *Model) tea.Cmd { return mm.openAddColumn(database, table) }),
	}
	if col, ok := m.cursorColumn(); ok {
		// Alter is offered when the engine can do either half of it: on
		// SQLite that is the rename alone, and the form says so.
		alterOps := []db.SchemaOp{db.OpAlterColumn}
		if m.driver.SchemaSupport(db.OpAlterColumn) != nil {
			alterOps = []db.SchemaOp{db.OpRenameColumn}
		}
		entries = append(entries,
			m.schemaEntry("e", "alter column "+col.Name+"…", alterOps,
				func(mm *Model) tea.Cmd { return mm.openAlterColumn(database, table, col) }),
			m.schemaEntry("d", "drop column "+col.Name+"…", []db.SchemaOp{db.OpDropColumn},
				func(mm *Model) tea.Cmd {
					return mm.confirmSchema("Drop column "+col.Name,
						fmt.Sprintf("Stage dropping column %s of %s? Its values go with it.", col.Name, table),
						db.DropColumn{Database: database, Table: table, Column: col.Name})
				}))
	}
	entries = append(entries, m.schemaEntry("i", "create index…", []db.SchemaOp{db.OpCreateIndex},
		func(mm *Model) tea.Cmd { return mm.openCreateIndex(database, table) }))
	if len(droppableIndexes(m.meta.indexes)) > 0 {
		entries = append(entries, m.schemaEntry("x", "drop index…", []db.SchemaOp{db.OpDropIndex},
			func(mm *Model) tea.Cmd { return mm.openDropIndex(database, table) }))
	}
	entries = append(entries, m.stagedSchemaEntries()...)
	entries = append(entries, menuEntry{key: "esc", label: "cancel"})
	m.modal = &menuModal{title: "Schema — table " + table, entries: entries}
	return nil
}

// schemaEntry is one menu entry for an operation. An operation the engine
// cannot perform is not offered as if it could: the entry says so, and
// choosing it explains why instead of opening a form.
func (m *Model) schemaEntry(k, label string, ops []db.SchemaOp, open func(*Model) tea.Cmd) menuEntry {
	for _, op := range ops {
		err := m.driver.SchemaSupport(op)
		if err == nil {
			continue
		}
		note := " — unavailable"
		if errors.Is(err, db.ErrUnsupported) {
			note = " — not supported by " + m.driver.Dialect().DisplayName()
		}
		return menuEntry{key: k, label: label + note, action: func(mm *Model) tea.Cmd {
			mm.modal = &confirmModal{title: "Not available: " + op.String(), body: err.Error()}
			return logCmd("-- %s skipped: %v", op, err)
		}}
	}
	return menuEntry{key: k, label: label, action: open}
}

// stagedSchemaEntries is the menu's way into the staged schema changes,
// present only while there are some.
func (m *Model) stagedSchemaEntries() []menuEntry {
	n := len(m.grid.changes.SchemaChanges())
	if n == 0 {
		return nil
	}
	return []menuEntry{{key: "u", label: fmt.Sprintf("staged schema changes (%d)…", n),
		action: func(mm *Model) tea.Cmd { mm.openStagedSchema(); return nil }}}
}

// openStagedSchema lists every staged schema change; choosing one
// unstages it. The list reopens after each unstage while any remain, so
// several can be dropped in a row.
func (m *Model) openStagedSchema() {
	changes := m.grid.changes.SchemaChanges()
	entries := make([]menuEntry, 0, len(changes)+1)
	for _, c := range changes {
		c := c
		entries = append(entries, menuEntry{label: c.Describe(), action: func(mm *Model) tea.Cmd {
			mm.grid.changes.UnstageSchema(c)
			mm.refreshTree()
			if len(mm.grid.changes.SchemaChanges()) > 0 {
				mm.openStagedSchema()
			}
			return logCmd("-- unstage %s", c.Describe())
		}})
	}
	entries = append(entries, menuEntry{key: "esc", label: "close"})
	m.modal = &menuModal{title: "Staged schema changes — enter unstages", entries: entries}
}

// ---------- staging ----------

// stageSchema stages a change the driver accepts and logs the exact
// statements the commit will run for it.
func (m *Model) stageSchema(c db.SchemaChange) tea.Cmd {
	stmts, err := m.driver.SchemaSQL(c)
	if err != nil {
		return m.schemaRefused(c, err)
	}
	return m.stageRendered(c, stmts)
}

// stageRendered stages a change already rendered by Driver.SchemaSQL.
func (m *Model) stageRendered(c db.SchemaChange, stmts []db.Statement) tea.Cmd {
	m.grid.changes.StageSchema(c)
	m.refreshTree()
	return logCmd("-- stage: %s;  (c commits)", joinSQL(stmts))
}

// schemaRefused reports why a change was not staged. A validation or
// capability problem opens a modal — the user asked for something and
// deserves to see why not — while an alter that changes nothing is only a
// log line.
func (m *Model) schemaRefused(c db.SchemaChange, err error) tea.Cmd {
	switch {
	case errors.Is(err, db.ErrNoSchemaChange):
		return logCmd("-- not staged: %s — nothing to change", c.Describe())
	case errors.Is(err, db.ErrReadOnly):
		return readOnlyBlocked("schema change")
	}
	m.modal = &confirmModal{title: "Cannot stage", body: c.Describe() + "\n\n" + err.Error(), danger: true}
	return logCmd("-- not staged: %s: %v", c.Describe(), err)
}

// confirmSchema is the extra confirm a destructive operation gets on top
// of staging. It shows the statement the commit will run, and says that
// confirming only stages it.
func (m *Model) confirmSchema(title, question string, c db.SchemaChange) tea.Cmd {
	stmts, err := m.driver.SchemaSQL(c)
	if err != nil {
		return m.schemaRefused(c, err)
	}
	m.modal = &confirmModal{
		title:  title,
		danger: true,
		body: question + "\n\n" + joinSQL(stmts) + ";" +
			"\n\nThis only stages it: nothing runs until you commit with c. S → staged schema changes unstages it.",
		onConfirm: func(mm *Model) tea.Cmd { return mm.stageRendered(c, stmts) },
	}
	return nil
}

// joinSQL spells rendered statements as one line.
func joinSQL(stmts []db.Statement) string {
	sql := make([]string, 0, len(stmts))
	for _, s := range stmts {
		sql = append(sql, s.SQL)
	}
	return strings.Join(sql, "; ")
}

func relationWord(k db.RelationKind) string {
	if k == db.RelationView {
		return "view"
	}
	return "table"
}

// openRelationKind is whether the open relation is a table or a view, as
// the [2] listing reported it. A relation the listing has not seen is
// taken for a table: the engine has the last word on commit anyway.
func (m Model) openRelationKind() db.RelationKind {
	if m.tree == nil {
		return db.RelationTable
	}
	for _, r := range m.tree.relations[m.grid.data.database] {
		if r.Name == m.grid.data.table {
			return r.Kind
		}
	}
	return db.RelationTable
}

// cursorColumn is the column the main view's cursor stands on: the row of
// the Structure tab, or the grid column of the Data tab. The other tabs
// have no column cursor.
func (m Model) cursorColumn() (db.Column, bool) {
	var name string
	switch m.tab {
	case mainTabStructure:
		if i := m.meta.row[mainTabStructure]; i >= 0 && i < len(m.meta.cols) {
			return m.meta.cols[i], true
		}
		return db.Column{}, false
	case mainTabData:
		if m.grid.data.col < 0 || m.grid.data.col >= len(m.grid.data.cols) || m.onPhantomRow() {
			return db.Column{}, false
		}
		name = m.grid.data.cols[m.grid.data.col].Name
	default:
		return db.Column{}, false
	}
	for _, c := range m.meta.cols {
		if c.Name == name {
			return c, true
		}
	}
	return db.Column{}, false
}

// droppableIndexes are the indexes DROP INDEX can remove: not the primary
// key, which is a constraint of the table rather than an index of it.
func droppableIndexes(idx []db.Index) []db.Index {
	var out []db.Index
	for _, ix := range idx {
		if !ix.Primary {
			out = append(out, ix)
		}
	}
	return out
}

// ---------- rename / drop index ----------

// openRenameRelation prompts for a relation's new name.
func (m *Model) openRenameRelation(database, name string, kind db.RelationKind) tea.Cmd {
	m.modal = newPromptModal(fmt.Sprintf("Rename %s %s to", relationWord(kind), name), "new name", name,
		func(mm *Model, value string) tea.Cmd {
			return mm.stageSchema(db.RenameRelation{Database: database, Name: name, NewName: value, Kind: kind})
		})
	return nil
}

// openDropIndex picks the index to drop; the drop itself gets the
// destructive confirm like every other.
func (m *Model) openDropIndex(database, table string) tea.Cmd {
	var entries []menuEntry
	for _, ix := range droppableIndexes(m.meta.indexes) {
		ix := ix
		label := ix.Name + "  (" + strings.Join(ix.Columns, ", ") + ")"
		if ix.Unique {
			label += " unique"
		}
		entries = append(entries, menuEntry{label: label, action: func(mm *Model) tea.Cmd {
			return mm.confirmSchema("Drop index "+ix.Name,
				fmt.Sprintf("Stage dropping index %s of %s?", ix.Name, table),
				db.DropIndex{Database: database, Table: table, Name: ix.Name})
		}})
	}
	entries = append(entries, menuEntry{key: "esc", label: "cancel"})
	m.modal = &menuModal{title: "Drop which index of " + table + "?", entries: entries}
	return nil
}

// ---------- create index ----------

// openCreateIndex asks for the index's name, columns and uniqueness. The
// column under the cursor is the starting point, which is the common case
// of indexing the column one is looking at.
func (m *Model) openCreateIndex(database, table string) tea.Cmd {
	cols := ""
	if c, ok := m.cursorColumn(); ok {
		cols = c.Name
	}
	known := make([]string, 0, len(m.meta.cols))
	for _, c := range m.meta.cols {
		known = append(known, c.Name)
	}
	suggest := func(f *formModal) string {
		parts := append([]string{table}, splitColumnList(f.value("columns"))...)
		return strings.Join(parts, "_") + "_idx"
	}
	fields := []*formField{
		newTextField("columns", "Columns", cols, "a, b").
			withHelp("comma-separated, in index order").
			withValidate(func(_ *formModal, v string) string {
				for _, c := range splitColumnList(v) {
					if !containsName(known, c) {
						return "no column " + c + " in " + table
					}
				}
				return requiredField("columns")(nil, v)
			}),
		newTextField("name", "Index name", "", "default: table_columns_idx"),
		newBoolField("unique", "Unique", false),
	}
	m.modal = newFormModal("Create index on "+table, fields, func(mm *Model, f *formModal) (bool, tea.Cmd) {
		name := f.value("name")
		if name == "" {
			name = suggest(f)
		}
		c := db.CreateIndex{Database: database, Table: table, Name: name,
			Columns: splitColumnList(f.value("columns")), Unique: f.value("unique") == "true"}
		return mm.submitSchema(f, c)
	})
	return nil
}

// splitColumnList reads "a, b ,c" as its column names.
func splitColumnList(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// submitSchema is a form's submit: the driver renders the change, and a
// refusal keeps the form open with the reason in its status line, so the
// user fixes what they typed instead of starting over.
func (m *Model) submitSchema(f *formModal, c db.SchemaChange) (bool, tea.Cmd) {
	stmts, err := m.driver.SchemaSQL(c)
	switch {
	case err == nil:
		return true, m.stageRendered(c, stmts)
	case errors.Is(err, db.ErrReadOnly), errors.Is(err, db.ErrNoSchemaChange):
		return true, m.schemaRefused(c, err)
	}
	f.err = err.Error()
	return false, nil
}

// ---------- column forms ----------

// defaultKinds are the choices of a column form's default selector, in
// the order they cycle. keep is only offered when altering: a new column
// has no default to keep.
var defaultKinds = []struct {
	value, label string
	kind         db.DefaultKind
}{
	{"keep", "keep current", db.DefaultKeep},
	{"none", "none", db.DefaultNone},
	{"text", "text literal", db.DefaultText},
	{"number", "number", db.DefaultNumber},
	{"null", "NULL", db.DefaultNull},
	{"expr", "SQL expression", db.DefaultExpr},
}

// columnFields are the fields of a column form. pk adds the primary-key
// toggle a CREATE TABLE column has; alter puts "keep current" first in
// the default selector and renames "none" to what it does there.
func columnFields(c db.ColumnDef, pk, alter bool) []*formField {
	var labels, values []string
	for _, k := range defaultKinds {
		if k.kind == db.DefaultKeep && !alter {
			continue
		}
		label := k.label
		if alter && k.kind == db.DefaultNone {
			label = "none (drop it)"
		}
		labels, values = append(labels, label), append(values, k.value)
	}
	selected := "none"
	if alter {
		selected = "keep"
	}
	for _, k := range defaultKinds {
		if k.kind == c.Default.Kind && (alter || k.kind != db.DefaultKeep) {
			selected = k.value
		}
	}
	hasValue := func(f *formModal) bool {
		switch f.rawValue("default") {
		case "text", "number", "expr":
			return true
		}
		return false
	}
	fields := []*formField{
		newTextField("name", "Name", c.Name, "column name").withValidate(requiredField("name")),
		newTextField("type", "Type", c.Type, "e.g. integer, varchar(40)").
			withValidate(func(_ *formModal, v string) string {
				if v == "" {
					return "type is required"
				}
				if err := db.ValidateTypeName(v); err != nil {
					return err.Error()
				}
				return ""
			}),
		newBoolField("notnull", "NOT NULL", c.NotNull),
	}
	if pk {
		fields = append(fields, newBoolField("pk", "Primary key", c.PrimaryKey))
	}
	return append(fields,
		newSelectField("default", "Default", labels, values, selected),
		newTextField("value", "Default value", c.Default.Value, "").withVisible(hasValue),
	)
}

// columnFromForm reads a column form back into a definition.
func columnFromForm(f *formModal) db.ColumnDef {
	def := db.ColumnDefault{Kind: db.DefaultNone}
	for _, k := range defaultKinds {
		if k.value == f.value("default") {
			def.Kind = k.kind
		}
	}
	if def.Kind == db.DefaultText {
		// A text default keeps its spaces: they are part of the literal.
		if fl := f.field("value"); fl != nil {
			def.Value = fl.input.Value()
		}
	} else {
		def.Value = f.value("value")
	}
	return db.ColumnDef{
		Name:       f.value("name"),
		Type:       f.value("type"),
		NotNull:    f.value("notnull") == "true",
		PrimaryKey: f.value("pk") == "true",
		Default:    def,
	}
}

// openAddColumn is the add-column form.
func (m *Model) openAddColumn(database, table string) tea.Cmd {
	m.modal = newFormModal("Add column to "+table,
		columnFields(db.ColumnDef{Default: db.ColumnDefault{Kind: db.DefaultNone}}, false, false),
		func(mm *Model, f *formModal) (bool, tea.Cmd) {
			return mm.submitSchema(f, db.AddColumn{Database: database, Table: table, Column: columnFromForm(f)})
		})
	return nil
}

// openAlterColumn is the alter-column form, prefilled with what the column
// is now. Only what the user changes ends up in the statement. On an
// engine that can only rename a column the form is the name field alone,
// and says why.
func (m *Model) openAlterColumn(database, table string, col db.Column) tea.Cmd {
	renameOnly := m.driver.SchemaSupport(db.OpAlterColumn)
	current := db.ColumnDef{Name: col.Name, Type: col.DataType, NotNull: !col.Nullable,
		Default: db.ColumnDefault{Kind: db.DefaultKeep}}
	fields := columnFields(current, false, true)
	if renameOnly != nil {
		fields = fields[:1]
	}
	curDefault := "none"
	if col.Default != nil {
		curDefault = flatten(*col.Default)
	}
	nullText := "NULL"
	if !col.Nullable {
		nullText = "NOT NULL"
	}
	notes := []string{fmt.Sprintf("now: %s · %s · default %s", col.DataType, nullText, curDefault)}
	// The notes are wrapped narrow enough for an 80-column terminal: the
	// form clips its body block rather than wrapping it.
	wrap := lipgloss.NewStyle().Width(60)
	if renameOnly != nil {
		notes = append(notes, strings.Split(wrap.Render(renameOnly.Error()), "\n")...)
	} else if db.AlterRewritesColumn(m.driver.Engine()) {
		notes = append(notes, strings.Split(wrap.Render("Changing the type or NULL rewrites the whole "+
			"column: its comment, character set and collation are not kept."), "\n")...)
	}
	form := newFormModal("Alter column "+table+"."+col.Name, fields,
		func(mm *Model, f *formModal) (bool, tea.Cmd) {
			c := db.AlterColumn{Database: database, Table: table, Old: col, NewName: f.value("name"),
				Type: col.DataType, NotNull: !col.Nullable, Default: db.ColumnDefault{Kind: db.DefaultKeep}}
			if renameOnly == nil {
				def := columnFromForm(f)
				c.Type, c.NotNull, c.Default = def.Type, def.NotNull, def.Default
			}
			return mm.submitSchema(f, c)
		})
	m.modal = form.withBody(func(*formModal) []string { return notes })
	return nil
}

// ---------- create table ----------

// tableDraft is a CREATE TABLE being put together. It lives only as long
// as its modals: esc on the draft's menu throws it away, and staging it
// hands a copy to the changeset.
type tableDraft struct {
	database string
	name     string
	cols     []db.ColumnDef
}

// openCreateTable starts a draft with its name and first column, which is
// usually the primary key.
func (m *Model) openCreateTable(database string) tea.Cmd {
	d := &tableDraft{database: database}
	first := db.ColumnDef{Name: "id", Type: "integer", NotNull: true, PrimaryKey: true,
		Default: db.ColumnDefault{Kind: db.DefaultNone}}
	fields := append([]*formField{
		newTextField("table", "Table name", "", "new table").withValidate(requiredField("table name")),
	}, columnFields(first, true, false)...)
	m.modal = newFormModal("Create table in "+displayDatabase(database)+" — first column", fields,
		func(mm *Model, f *formModal) (bool, tea.Cmd) {
			d.name = f.value("table")
			col := columnFromForm(f)
			if err := mm.checkDraftColumn(d, col, -1); err != "" {
				f.err = err
				return false, nil
			}
			d.cols = []db.ColumnDef{col}
			mm.openTableDraft(d)
			return true, nil
		})
	return nil
}

// checkDraftColumn validates one column of a draft through the dialect,
// the same way the finished table will be, so a bad type or default is
// caught on the column form that typed it. skip is the index of the
// column being edited, -1 for a new one.
func (m *Model) checkDraftColumn(d *tableDraft, col db.ColumnDef, skip int) string {
	for i, c := range d.cols {
		if i != skip && c.Name == col.Name {
			return "the table already has a column " + col.Name
		}
	}
	name := d.name
	if name == "" {
		name = "t"
	}
	_, err := db.SchemaSQL(m.driver.Dialect(), db.CreateTable{Database: d.database, Table: name,
		Columns: []db.ColumnDef{col}})
	if err != nil {
		return err.Error()
	}
	return ""
}

// openTableDraft is the draft's menu: its columns (enter edits one), and
// the ways to add another, rename the table, or stage the whole CREATE.
func (m *Model) openTableDraft(d *tableDraft) {
	var entries []menuEntry
	for i, c := range d.cols {
		i := i
		entries = append(entries, menuEntry{label: describeColumnDef(c), action: func(mm *Model) tea.Cmd {
			mm.openDraftColumn(d, i)
			return nil
		}})
	}
	add := len(entries)
	entries = append(entries,
		menuEntry{key: "a", label: "add column…", action: func(mm *Model) tea.Cmd {
			mm.openDraftColumn(d, -1)
			return nil
		}},
		menuEntry{key: "r", label: "rename table…", action: func(mm *Model) tea.Cmd {
			mm.modal = newPromptModal("Table name", "new table", d.name, func(m3 *Model, v string) tea.Cmd {
				if v != "" {
					d.name = v
				}
				m3.openTableDraft(d)
				return nil
			})
			return nil
		}},
		menuEntry{key: "s", label: "stage CREATE TABLE", action: func(mm *Model) tea.Cmd {
			c := db.CreateTable{Database: d.database, Table: d.name,
				Columns: append([]db.ColumnDef(nil), d.cols...)}
			stmts, err := mm.driver.SchemaSQL(c)
			if err != nil {
				// Back to the draft rather than losing it.
				mm.modal = &confirmModal{title: "Cannot stage", body: err.Error() + "\n\nenter returns to the draft.",
					danger: true, onConfirm: func(m3 *Model) tea.Cmd { m3.openTableDraft(d); return nil }}
				return nil
			}
			return mm.stageRendered(c, stmts)
		}},
		menuEntry{key: "esc", label: "cancel (discard the draft)"},
	)
	m.modal = &menuModal{
		title:   fmt.Sprintf("Create table %s in %s — %d columns", d.name, displayDatabase(d.database), len(d.cols)),
		entries: entries,
		cursor:  add,
	}
}

// openDraftColumn edits column i of a draft, or adds one when i < 0. An
// existing column gets a remove toggle; esc goes back to the draft
// either way.
func (m *Model) openDraftColumn(d *tableDraft, i int) {
	col := db.ColumnDef{Type: "", Default: db.ColumnDefault{Kind: db.DefaultNone}}
	title := "Add column to " + d.name
	if i >= 0 {
		col = d.cols[i]
		title = "Column " + col.Name + " of " + d.name
	}
	fields := columnFields(col, true, false)
	if i >= 0 {
		fields = append(fields, newBoolField("remove", "Remove column", false))
	}
	form := newFormModal(title, fields, func(mm *Model, f *formModal) (bool, tea.Cmd) {
		if f.value("remove") == "true" {
			d.cols = append(d.cols[:i], d.cols[i+1:]...)
			mm.openTableDraft(d)
			return true, nil
		}
		c := columnFromForm(f)
		if err := mm.checkDraftColumn(d, c, i); err != "" {
			f.err = err
			return false, nil
		}
		if i >= 0 {
			d.cols[i] = c
		} else {
			d.cols = append(d.cols, c)
		}
		mm.openTableDraft(d)
		return true, nil
	})
	form.onCancel = func(mm *Model) { mm.openTableDraft(d) }
	m.modal = form
}

// describeColumnDef is a draft column's line in the draft menu.
func describeColumnDef(c db.ColumnDef) string {
	parts := []string{c.Name, c.Type}
	if c.PrimaryKey {
		parts = append(parts, "· PK")
	}
	if c.NotNull {
		parts = append(parts, "· NOT NULL")
	}
	switch c.Default.Kind {
	case db.DefaultText:
		parts = append(parts, "· default '"+c.Default.Value+"'")
	case db.DefaultNumber, db.DefaultExpr:
		parts = append(parts, "· default "+c.Default.Value)
	case db.DefaultNull:
		parts = append(parts, "· default NULL")
	}
	return strings.Join(parts, " ")
}

// ---------- after a commit ----------

// afterSchemaCommit brings every cache that described the schema back in
// line with the server once a commit carrying schema changes is through:
// the [2] listings of the namespaces it touched are re-read (so a dropped
// relation does not linger), the foreign-key and completion caches are
// dropped, and the open relation is reloaded — or, when the commit
// dropped or renamed it, closed or reopened under its new name.
func (m *Model) afterSchemaCommit(changes []db.SchemaChange) tea.Cmd {
	var cmds []tea.Cmd
	namespaces := map[string]bool{}
	open := m.grid.data.browsing() && m.grid.data.conn == m.active
	gone, renamed := false, ""
	for _, c := range changes {
		database, table := db.ChangeTarget(c)
		namespaces[database] = true
		if !open || database != m.grid.data.database || table != m.grid.data.table {
			continue
		}
		switch c := c.(type) {
		case db.DropRelation:
			gone = true
		case db.RenameRelation:
			renamed = c.NewName
		}
	}

	m.grid.fkCache = map[fkKey][]db.ForeignKey{}
	m.grid.refsCache = map[fkKey][]namespaceFK{}
	// A nil column map is what makes syncSchema rebuild the completion
	// cache under a new generation on its next use.
	m.query.schema.cols = nil
	for database := range namespaces {
		cmds = append(cmds, m.reloadRelationsOf(database))
	}

	switch {
	case !open:
	case gone || (renamed != "" && m.grid.data.database != m.database):
		table := m.grid.data.table
		m.closeRelation()
		cmds = append(cmds, logCmd("-- %s is gone from the server; closed", table))
	case renamed != "":
		cmds = append(cmds, m.openTable(renamed))
	default:
		m.resetMeta()
		cmds = append(cmds, m.reloadPage(), m.ensureMeta(), m.ensureFKs())
	}
	m.refreshTree()
	return tea.Batch(cmds...)
}

// reloadRelationsOf re-reads one namespace's tables and views when the
// tree holds a listing of them. A namespace that was never expanded has
// nothing cached to go stale, and reads fresh when it is.
func (m *Model) reloadRelationsOf(database string) tea.Cmd {
	if m.tree == nil {
		return nil
	}
	cat := m.tree.category(database, catTables)
	if cat == nil {
		return nil
	}
	_, cached := m.tree.relations[database]
	if !cached && !cat.loading {
		return nil
	}
	for _, c := range m.tree.categoryNodes(database) {
		if c.cat.relational() {
			c.loaded = false
		}
	}
	return m.loadCategory(cat)
}

// closeRelation empties the main view after the relation in it went away.
func (m *Model) closeRelation() {
	m.grid.stopPageQueries()
	m.closeFilterInput()
	m.table = ""
	m.grid.data = dataView{req: m.grid.data.req}
	m.tab = mainTabData
	m.resetMeta()
	m.grid.clearBrowse()
	if m.focus == panelMain {
		m.focus = panelObjects
	}
}

// ---------- staged markers in [2] ----------

// markStagedSchema annotates the [2] tree with what the changeset will do
// to it: a relation staged for a drop, rename or truncate — or for column
// and index changes — says so, and a Tables category names the tables a
// staged CREATE TABLE will add. It runs on every re-flatten, so the marks
// follow staging, unstaging, discarding and committing alike.
func (m *Model) markStagedSchema() {
	if m.tree == nil {
		return
	}
	type rel struct{ database, name string }
	marks := map[rel][]string{}
	altered := map[rel]int{}
	created := map[string][]string{}
	if m.grid.changes != nil {
		for _, c := range m.grid.changes.SchemaChanges() {
			database, table := db.ChangeTarget(c)
			r := rel{database, table}
			switch c := c.(type) {
			case db.CreateTable:
				created[database] = append(created[database], table)
			case db.DropRelation:
				marks[r] = append(marks[r], "drop")
			case db.RenameRelation:
				marks[r] = append(marks[r], "rename → "+c.NewName)
			case db.TruncateTable:
				marks[r] = append(marks[r], "truncate")
			default:
				altered[r]++
			}
		}
	}
	var walk func(nodes []*treeNode)
	walk = func(nodes []*treeNode) {
		for _, n := range nodes {
			n.staged = ""
			switch {
			case n.kind == nodeCategory && n.cat == catTables && len(created[n.database]) > 0:
				n.staged = "staged: + " + strings.Join(created[n.database], ", ")
			case n.kind == nodeObject && n.cat.relational():
				r := rel{n.database, n.name}
				words := marks[r]
				if k := altered[r]; k > 0 {
					words = append(words, countSchemaChanges(k))
				}
				if len(words) > 0 {
					n.staged = "staged: " + strings.Join(words, ", ")
				}
			}
			walk(n.children)
		}
	}
	walk(m.tree.roots)
}

// countSchemaChanges spells "1 change" / "3 changes" for a tree note.
func countSchemaChanges(n int) string {
	if n == 1 {
		return "1 change"
	}
	return fmt.Sprintf("%d changes", n)
}

// stagedSchemaLines is the block the Structure and Indexes tabs end with
// while the open table has schema changes staged, so what the commit will
// do to it is visible next to what it is now.
func (m Model) stagedSchemaLines(w int) []string {
	if m.grid.changes == nil {
		return nil
	}
	changes := m.grid.changes.SchemaChangesFor(m.grid.data.database, m.grid.data.table)
	if len(changes) == 0 {
		return nil
	}
	lines := []string{"", m.style.pending.Render(truncate(
		fmt.Sprintf("Staged schema changes (%d) — c commits, S → staged unstages:", len(changes)), w))}
	for _, c := range changes {
		lines = append(lines, m.style.pending.Render(truncate("  "+c.Describe(), w)))
	}
	return lines
}
