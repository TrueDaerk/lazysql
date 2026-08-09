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

// synthesizeDDL builds a CREATE TABLE statement from introspected
// columns, for engines that do not store the original DDL.
func synthesizeDDL(d Dialect, database, table string, cols []Column) string {
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
	b.WriteString("\n);")
	return b.String()
}
