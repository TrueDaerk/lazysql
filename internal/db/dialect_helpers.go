package db

import (
	"context"
	"strings"
)

// scanStrings runs a query whose result is a single string column.
func scanStrings(ctx context.Context, q querier, query string, args ...any) ([]string, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// scanRelations runs a query whose result is (name, kind) and normalizes
// the engine's kind spelling. Anything containing "view" is a view;
// everything else (BASE TABLE, LOCAL TEMPORARY, …) is a table.
func scanRelations(ctx context.Context, q querier, query string, args ...any) ([]Relation, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Relation
	for rows.Next() {
		var name, kind string
		if err := rows.Scan(&name, &kind); err != nil {
			return nil, err
		}
		rel := Relation{Name: name, Kind: RelationTable}
		if strings.Contains(strings.ToLower(kind), "view") {
			rel.Kind = RelationView
		}
		out = append(out, rel)
	}
	return out, rows.Err()
}

// synthesizeDDL builds a CREATE TABLE statement from introspected
// metadata, for engines that do not store the original DDL. Indexes and
// foreign keys may be nil; the primary-key index is skipped because the
// PRIMARY KEY clause already carries it.
func synthesizeDDL(d Dialect, database, table string, cols []Column, idx []Index, fks []ForeignKey) string {
	var b strings.Builder
	b.WriteString("CREATE TABLE ")
	b.WriteString(qualifiedTable(d, database, table))
	b.WriteString(" (\n")
	var pk []string
	for i, c := range cols {
		if i > 0 {
			b.WriteString(",\n")
		}
		b.WriteString("  ")
		b.WriteString(d.QuoteIdent(c.Name))
		b.WriteString(" ")
		b.WriteString(c.DataType)
		if !c.Nullable {
			b.WriteString(" NOT NULL")
		}
		if c.Default != nil {
			b.WriteString(" DEFAULT ")
			b.WriteString(*c.Default)
		}
		if c.PrimaryKey {
			pk = append(pk, d.QuoteIdent(c.Name))
		}
	}
	if len(pk) > 0 {
		b.WriteString(",\n  PRIMARY KEY (")
		b.WriteString(strings.Join(pk, ", "))
		b.WriteString(")")
	}
	for _, fk := range fks {
		b.WriteString(",\n  ")
		if fk.Name != "" {
			b.WriteString("CONSTRAINT " + d.QuoteIdent(fk.Name) + " ")
		}
		b.WriteString("FOREIGN KEY (" + quoteAll(d, fk.Columns) + ")")
		b.WriteString(" REFERENCES " + quoteQualified(d, fk.RefTable))
		if len(fk.RefColumns) > 0 {
			b.WriteString(" (" + quoteAll(d, fk.RefColumns) + ")")
		}
		if act := referentialAction(fk.OnDelete); act != "" {
			b.WriteString(" ON DELETE " + act)
		}
		if act := referentialAction(fk.OnUpdate); act != "" {
			b.WriteString(" ON UPDATE " + act)
		}
	}
	b.WriteString("\n);")

	// Secondary indexes are separate statements in every dialect that
	// needs a synthesized DDL.
	for _, ix := range idx {
		if ix.Primary || len(ix.Columns) == 0 {
			continue
		}
		b.WriteString("\n\nCREATE ")
		if ix.Unique {
			b.WriteString("UNIQUE ")
		}
		b.WriteString("INDEX " + d.QuoteIdent(ix.Name) + " ON ")
		b.WriteString(qualifiedTable(d, database, table))
		b.WriteString(" (" + quoteAll(d, ix.Columns) + ");")
	}
	return b.String()
}

// quoteAll quotes a column list for a parenthesized clause.
func quoteAll(d Dialect, names []string) string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, d.QuoteIdent(n))
	}
	return strings.Join(out, ", ")
}

// quoteQualified quotes a possibly schema-qualified table name, so
// "other.users" becomes two quoted parts rather than one odd identifier.
func quoteQualified(d Dialect, name string) string {
	parts := strings.Split(name, ".")
	for i, p := range parts {
		parts[i] = d.QuoteIdent(p)
	}
	return strings.Join(parts, ".")
}

// referentialAction normalizes an ON UPDATE/ON DELETE rule for display
// and for generated DDL. The default rule contributes no clause.
func referentialAction(rule string) string {
	rule = strings.ToUpper(strings.TrimSpace(rule))
	if rule == "" || rule == "NO ACTION" {
		return ""
	}
	return rule
}

// appendFKColumn accumulates row-per-column foreign key listings: rows
// arrive ordered by constraint and key position, so a row either
// extends the constraint being built or starts a new one.
func appendFKColumn(fks []ForeignKey, fk ForeignKey, col, refCol string) []ForeignKey {
	if n := len(fks); n > 0 && fks[n-1].Name == fk.Name && fks[n-1].RefTable == fk.RefTable {
		fks[n-1].Columns = append(fks[n-1].Columns, col)
		if refCol != "" {
			fks[n-1].RefColumns = append(fks[n-1].RefColumns, refCol)
		}
		return fks
	}
	fk.Columns = []string{col}
	if refCol != "" {
		fk.RefColumns = []string{refCol}
	}
	return append(fks, fk)
}
