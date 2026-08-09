package db

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Schema diff: a normalized snapshot of one namespace's tables, and the
// comparison of two such snapshots. Everything here works on the types
// the Driver interface already returns — no engine is consulted beyond
// the introspection calls, so two snapshots from different engines can
// be compared (with the type-synonym caveat DiffSchemas documents).

// SchemaTable is one table's introspected shape.
type SchemaTable struct {
	Name        string
	Columns     []Column
	Indexes     []Index
	ForeignKeys []ForeignKey
}

// Schema is the normalized snapshot of one namespace: its tables in
// name order. Views are deliberately absent — their shape is their
// query, which TableColumns cannot meaningfully compare.
type Schema struct {
	Engine Engine
	// Label names the snapshot in reports, e.g. "prod.appdb".
	Label  string
	Tables []SchemaTable
}

// IntrospectSchema reads every table of a namespace into a Schema.
// progress, when non-nil, is called after each table with how many of
// the total are done — it runs on the introspecting goroutine, so a UI
// caller must forward, not render, from it.
func IntrospectSchema(ctx context.Context, drv Driver, database, label string, progress func(done, total int)) (*Schema, error) {
	rels, err := drv.ListRelations(ctx, database)
	if err != nil {
		return nil, err
	}
	names := FilterRelations(rels, RelationTable)
	s := &Schema{Engine: drv.Engine(), Label: label, Tables: make([]SchemaTable, 0, len(names))}
	for i, name := range names {
		t := SchemaTable{Name: name}
		if t.Columns, err = drv.TableColumns(ctx, database, name); err != nil {
			return nil, fmt.Errorf("table %s: %w", name, err)
		}
		if t.Indexes, err = drv.TableIndexes(ctx, database, name); err != nil {
			return nil, fmt.Errorf("table %s: %w", name, err)
		}
		if t.ForeignKeys, err = drv.TableForeignKeys(ctx, database, name); err != nil {
			return nil, fmt.Errorf("table %s: %w", name, err)
		}
		s.Tables = append(s.Tables, t)
		if progress != nil {
			progress(i+1, len(names))
		}
	}
	sort.Slice(s.Tables, func(i, j int) bool { return s.Tables[i].Name < s.Tables[j].Name })
	return s, nil
}

// ---------- type normalization ----------

// typeSynonyms maps an engine family's spelling variants onto one
// canonical base type. Only the base (the part before any "(...)") is
// mapped; length and precision arguments travel unchanged. The maps are
// deliberately conservative: a missing synonym costs a false "changed"
// line, a wrong one hides a real difference.
var typeSynonyms = map[Engine]map[string]string{
	EngineSQLite: {
		"INT": "INTEGER",
	},
	EngineMySQL: {
		"INTEGER":           "INT",
		"DEC":               "DECIMAL",
		"FIXED":             "DECIMAL",
		"BOOL":              "TINYINT",
		"BOOLEAN":           "TINYINT",
		"CHARACTER VARYING": "VARCHAR",
	},
	EnginePostgres: {
		"INT":               "INTEGER",
		"INT4":              "INTEGER",
		"INT2":              "SMALLINT",
		"INT8":              "BIGINT",
		"SERIAL":            "INTEGER",
		"BIGSERIAL":         "BIGINT",
		"SMALLSERIAL":       "SMALLINT",
		"BOOL":              "BOOLEAN",
		"CHARACTER VARYING": "VARCHAR",
		"CHARACTER":         "CHAR",
		"FLOAT4":            "REAL",
		"FLOAT8":            "DOUBLE PRECISION",
		"DECIMAL":           "NUMERIC",
		"TIMESTAMPTZ":       "TIMESTAMP WITH TIME ZONE",
		"TIMETZ":            "TIME WITH TIME ZONE",
	},
	EngineDuckDB: {
		"INT":              "INTEGER",
		"INT4":             "INTEGER",
		"SIGNED":           "INTEGER",
		"INT8":             "BIGINT",
		"LONG":             "BIGINT",
		"INT2":             "SMALLINT",
		"SHORT":            "SMALLINT",
		"INT1":             "TINYINT",
		"BOOL":             "BOOLEAN",
		"LOGICAL":          "BOOLEAN",
		"TEXT":             "VARCHAR",
		"STRING":           "VARCHAR",
		"BPCHAR":           "VARCHAR",
		"CHAR":             "VARCHAR",
		"NUMERIC":          "DECIMAL",
		"FLOAT4":           "FLOAT",
		"REAL":             "FLOAT",
		"FLOAT8":           "DOUBLE",
		"DOUBLE PRECISION": "DOUBLE",
		"TIMESTAMPTZ":      "TIMESTAMP WITH TIME ZONE",
	},
}

// synonymEngine folds MariaDB onto MySQL: the two report the same type
// vocabulary, so they share one synonym table and count as one family.
func synonymEngine(e Engine) Engine {
	if e == EngineMariaDB {
		return EngineMySQL
	}
	return e
}

// NormalizeType canonicalizes a declared column type for comparison:
// case and whitespace always, the engine's type synonyms only when
// mapSynonyms is set. "int (11)" and "INT(11)" always compare equal;
// "INT" and "INTEGER" only do within one engine family, where the
// synonym table knows they are the same type.
func NormalizeType(engine Engine, raw string, mapSynonyms bool) string {
	t := strings.ToUpper(strings.Join(strings.Fields(raw), " "))
	base, args := t, ""
	if i := strings.Index(t, "("); i >= 0 {
		base, args = strings.TrimSpace(t[:i]), t[i:]
	}
	if mapSynonyms {
		if canon, ok := typeSynonyms[synonymEngine(engine)][base]; ok {
			base = canon
		}
	}
	return base + args
}

// ---------- diff model ----------

// FieldDiff is one attribute that differs between the two sides.
type FieldDiff struct {
	Field string // "type", "nullable", "default", …
	A, B  string
}

// NamedDiff is one changed column, index or foreign key: the object's
// name plus every attribute that differs.
type NamedDiff struct {
	Name   string
	Fields []FieldDiff
}

// TableDiff is everything that differs about one table present on both
// sides. The OnlyA/OnlyB slices carry a short description next to the
// name so the report can say what was added or removed.
type TableDiff struct {
	Name string

	ColumnsOnlyA, ColumnsOnlyB []string
	ColumnsChanged             []NamedDiff

	IndexesOnlyA, IndexesOnlyB []string
	IndexesChanged             []NamedDiff

	FKsOnlyA, FKsOnlyB []string
	FKsChanged         []NamedDiff
}

// Empty reports whether the table compared identical.
func (d TableDiff) Empty() bool {
	return len(d.ColumnsOnlyA) == 0 && len(d.ColumnsOnlyB) == 0 && len(d.ColumnsChanged) == 0 &&
		len(d.IndexesOnlyA) == 0 && len(d.IndexesOnlyB) == 0 && len(d.IndexesChanged) == 0 &&
		len(d.FKsOnlyA) == 0 && len(d.FKsOnlyB) == 0 && len(d.FKsChanged) == 0
}

// SchemaDiff is the full comparison of two snapshots.
type SchemaDiff struct {
	A, B *Schema
	// SameFamily records whether type synonyms were normalized: true
	// when both sides speak the same engine's type vocabulary.
	SameFamily bool

	TablesOnlyA, TablesOnlyB []string
	TableDiffs               []TableDiff // only non-empty ones
}

// Empty reports whether the two schemas compared identical.
func (d *SchemaDiff) Empty() bool {
	return len(d.TablesOnlyA) == 0 && len(d.TablesOnlyB) == 0 && len(d.TableDiffs) == 0
}

// DiffSchemas compares two snapshots. Within one engine family the
// declared column types are normalized through the family's synonym
// table (SQLite INT vs INTEGER is not a difference); across families
// the types are compared verbatim after case/whitespace folding, so a
// cross-engine diff over-reports type changes rather than guessing at
// equivalences.
func DiffSchemas(a, b *Schema) *SchemaDiff {
	d := &SchemaDiff{
		A: a, B: b,
		SameFamily: synonymEngine(a.Engine) == synonymEngine(b.Engine),
	}
	byName := func(s *Schema) map[string]SchemaTable {
		m := make(map[string]SchemaTable, len(s.Tables))
		for _, t := range s.Tables {
			m[t.Name] = t
		}
		return m
	}
	ta, tb := byName(a), byName(b)
	for _, t := range a.Tables {
		other, ok := tb[t.Name]
		if !ok {
			d.TablesOnlyA = append(d.TablesOnlyA, t.Name)
			continue
		}
		if td := d.diffTable(t, other); !td.Empty() {
			d.TableDiffs = append(d.TableDiffs, td)
		}
	}
	for _, t := range b.Tables {
		if _, ok := ta[t.Name]; !ok {
			d.TablesOnlyB = append(d.TablesOnlyB, t.Name)
		}
	}
	return d
}

func (d *SchemaDiff) diffTable(a, b SchemaTable) TableDiff {
	td := TableDiff{Name: a.Name}
	td.diffColumns(d, a.Columns, b.Columns)
	td.diffIndexes(a.Indexes, b.Indexes)
	td.diffFKs(a.ForeignKeys, b.ForeignKeys)
	return td
}

func (td *TableDiff) diffColumns(d *SchemaDiff, a, b []Column) {
	bByName := make(map[string]Column, len(b))
	for _, c := range b {
		bByName[c.Name] = c
	}
	aNames := make(map[string]bool, len(a))
	for _, ca := range a {
		aNames[ca.Name] = true
		cb, ok := bByName[ca.Name]
		if !ok {
			td.ColumnsOnlyA = append(td.ColumnsOnlyA, ca.Name+" ("+ca.DataType+")")
			continue
		}
		var fields []FieldDiff
		na := NormalizeType(d.A.Engine, ca.DataType, d.SameFamily)
		nb := NormalizeType(d.B.Engine, cb.DataType, d.SameFamily)
		if na != nb {
			// The raw spellings are what the user recognizes; the
			// normalized forms only decide whether to report at all.
			fields = append(fields, FieldDiff{Field: "type", A: ca.DataType, B: cb.DataType})
		}
		if ca.Nullable != cb.Nullable {
			fields = append(fields, FieldDiff{Field: "nullable", A: yesNoWord(ca.Nullable), B: yesNoWord(cb.Nullable)})
		}
		if defaultText(ca.Default) != defaultText(cb.Default) {
			fields = append(fields, FieldDiff{Field: "default", A: defaultText(ca.Default), B: defaultText(cb.Default)})
		}
		if ca.PrimaryKey != cb.PrimaryKey {
			fields = append(fields, FieldDiff{Field: "primary key", A: yesNoWord(ca.PrimaryKey), B: yesNoWord(cb.PrimaryKey)})
		}
		if len(fields) > 0 {
			td.ColumnsChanged = append(td.ColumnsChanged, NamedDiff{Name: ca.Name, Fields: fields})
		}
	}
	for _, cb := range b {
		if !aNames[cb.Name] {
			td.ColumnsOnlyB = append(td.ColumnsOnlyB, cb.Name+" ("+cb.DataType+")")
		}
	}
}

// diffIndexes matches indexes by name. Primary-key indexes are skipped:
// engines name them on their own schedule ("PRIMARY", "<table>_pkey",
// nothing at all in SQLite) and the key itself is already compared
// through the columns' PrimaryKey flags.
func (td *TableDiff) diffIndexes(a, b []Index) {
	describe := func(ix Index) string {
		kind := "index"
		if ix.Unique {
			kind = "unique index"
		}
		return ix.Name + " (" + kind + " on " + strings.Join(ix.Columns, ", ") + ")"
	}
	bByName := make(map[string]Index, len(b))
	for _, ix := range b {
		if !ix.Primary {
			bByName[ix.Name] = ix
		}
	}
	aNames := make(map[string]bool, len(a))
	for _, ia := range a {
		if ia.Primary {
			continue
		}
		aNames[ia.Name] = true
		ib, ok := bByName[ia.Name]
		if !ok {
			td.IndexesOnlyA = append(td.IndexesOnlyA, describe(ia))
			continue
		}
		var fields []FieldDiff
		if ja, jb := strings.Join(ia.Columns, ", "), strings.Join(ib.Columns, ", "); ja != jb {
			fields = append(fields, FieldDiff{Field: "columns", A: ja, B: jb})
		}
		if ia.Unique != ib.Unique {
			fields = append(fields, FieldDiff{Field: "unique", A: yesNoWord(ia.Unique), B: yesNoWord(ib.Unique)})
		}
		if len(fields) > 0 {
			td.IndexesChanged = append(td.IndexesChanged, NamedDiff{Name: ia.Name, Fields: fields})
		}
	}
	for _, ib := range b {
		if !ib.Primary && !aNames[ib.Name] {
			td.IndexesOnlyB = append(td.IndexesOnlyB, describe(ib))
		}
	}
}

func (td *TableDiff) diffFKs(a, b []ForeignKey) {
	describe := func(fk ForeignKey) string {
		ref := fk.RefTable
		if len(fk.RefColumns) > 0 {
			ref += " (" + strings.Join(fk.RefColumns, ", ") + ")"
		}
		return fk.Name + " (" + strings.Join(fk.Columns, ", ") + " → " + ref + ")"
	}
	bByName := make(map[string]ForeignKey, len(b))
	for _, fk := range b {
		bByName[fk.Name] = fk
	}
	aNames := make(map[string]bool, len(a))
	for _, fa := range a {
		aNames[fa.Name] = true
		fb, ok := bByName[fa.Name]
		if !ok {
			td.FKsOnlyA = append(td.FKsOnlyA, describe(fa))
			continue
		}
		var fields []FieldDiff
		if ja, jb := strings.Join(fa.Columns, ", "), strings.Join(fb.Columns, ", "); ja != jb {
			fields = append(fields, FieldDiff{Field: "columns", A: ja, B: jb})
		}
		if fa.RefTable != fb.RefTable {
			fields = append(fields, FieldDiff{Field: "references", A: fa.RefTable, B: fb.RefTable})
		}
		if ja, jb := strings.Join(fa.RefColumns, ", "), strings.Join(fb.RefColumns, ", "); ja != jb {
			fields = append(fields, FieldDiff{Field: "ref columns", A: ja, B: jb})
		}
		// referentialAction folds "" and "NO ACTION" together, the way
		// the engines themselves mean them.
		if referentialAction(fa.OnUpdate) != referentialAction(fb.OnUpdate) {
			fields = append(fields, FieldDiff{Field: "on update", A: actionWord(fa.OnUpdate), B: actionWord(fb.OnUpdate)})
		}
		if referentialAction(fa.OnDelete) != referentialAction(fb.OnDelete) {
			fields = append(fields, FieldDiff{Field: "on delete", A: actionWord(fa.OnDelete), B: actionWord(fb.OnDelete)})
		}
		if len(fields) > 0 {
			td.FKsChanged = append(td.FKsChanged, NamedDiff{Name: fa.Name, Fields: fields})
		}
	}
	for _, fb := range b {
		if !aNames[fb.Name] {
			td.FKsOnlyB = append(td.FKsOnlyB, describe(fb))
		}
	}
}

func yesNoWord(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func defaultText(d *string) string {
	if d == nil {
		return "(none)"
	}
	return *d
}

func actionWord(rule string) string {
	if r := referentialAction(rule); r != "" {
		return r
	}
	return "NO ACTION"
}

// ---------- report rendering ----------

// DiffLineKind colors one report line: added is what only B has,
// removed what only A has, changed what both have differently.
type DiffLineKind int

const (
	DiffContext DiffLineKind = iota
	DiffAdded
	DiffRemoved
	DiffChanged
)

// DiffLine is one line of the rendered report. Text carries its own
// indentation; Kind is for color only.
type DiffLine struct {
	Kind DiffLineKind
	Text string
}

// Render turns the diff into the report the UI shows and the export
// writes: a header naming both sides and the comparison rules, then the
// table lists, then one block per differing table.
func (d *SchemaDiff) Render() []DiffLine {
	engineName := func(e Engine) string {
		if dl, err := DialectFor(e); err == nil {
			return dl.DisplayName()
		}
		return string(e)
	}
	out := []DiffLine{
		{DiffContext, "Schema diff"},
		{DiffContext, "A: " + d.A.Label + " (" + engineName(d.A.Engine) + ")"},
		{DiffContext, "B: " + d.B.Label + " (" + engineName(d.B.Engine) + ")"},
	}
	if d.SameFamily {
		out = append(out, DiffLine{DiffContext,
			"types compared with " + engineName(d.A.Engine) + " synonyms normalized (e.g. INT = INTEGER)"})
	} else {
		out = append(out, DiffLine{DiffContext,
			"cross-engine diff: types compared verbatim, no synonym mapping — expect type noise"})
	}
	out = append(out, DiffLine{})

	if d.Empty() {
		return append(out, DiffLine{DiffContext,
			fmt.Sprintf("no differences — %d tables compared", len(d.A.Tables))})
	}

	if len(d.TablesOnlyA) > 0 {
		out = append(out, DiffLine{DiffContext, fmt.Sprintf("Tables only in A (%d):", len(d.TablesOnlyA))})
		for _, t := range d.TablesOnlyA {
			out = append(out, DiffLine{DiffRemoved, "  - " + t})
		}
		out = append(out, DiffLine{})
	}
	if len(d.TablesOnlyB) > 0 {
		out = append(out, DiffLine{DiffContext, fmt.Sprintf("Tables only in B (%d):", len(d.TablesOnlyB))})
		for _, t := range d.TablesOnlyB {
			out = append(out, DiffLine{DiffAdded, "  + " + t})
		}
		out = append(out, DiffLine{})
	}

	for _, td := range d.TableDiffs {
		out = append(out, DiffLine{DiffContext, "Table " + td.Name + ":"})
		out = appendOnly(out, "column", td.ColumnsOnlyA, td.ColumnsOnlyB)
		out = appendChanged(out, "column", td.ColumnsChanged)
		out = appendOnly(out, "index", td.IndexesOnlyA, td.IndexesOnlyB)
		out = appendChanged(out, "index", td.IndexesChanged)
		out = appendOnly(out, "foreign key", td.FKsOnlyA, td.FKsOnlyB)
		out = appendChanged(out, "foreign key", td.FKsChanged)
		out = append(out, DiffLine{})
	}
	// The trailing blank line reads as a cut-off report; drop it.
	return out[:len(out)-1]
}

func appendOnly(out []DiffLine, kind string, onlyA, onlyB []string) []DiffLine {
	for _, s := range onlyA {
		out = append(out, DiffLine{DiffRemoved, "  - " + kind + " " + s + " — only in A"})
	}
	for _, s := range onlyB {
		out = append(out, DiffLine{DiffAdded, "  + " + kind + " " + s + " — only in B"})
	}
	return out
}

func appendChanged(out []DiffLine, kind string, changed []NamedDiff) []DiffLine {
	for _, c := range changed {
		for _, f := range c.Fields {
			out = append(out, DiffLine{DiffChanged,
				"  ~ " + kind + " " + c.Name + " " + f.Field + "  A: " + f.A + " / B: " + f.B})
		}
	}
	return out
}

// RenderText is Render as plain text, for the clipboard and file export.
func (d *SchemaDiff) RenderText() string {
	lines := d.Render()
	var b strings.Builder
	for i, l := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(l.Text)
	}
	return b.String()
}
