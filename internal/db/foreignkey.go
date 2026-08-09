package db

import (
	"fmt"
	"strings"
)

// Following a foreign key means running the referenced table's page
// query with a WHERE clause that pins the referenced columns to the
// values of the row the user came from. The clause is built here rather
// than in the UI for the same reason every other statement fragment is:
// identifiers need dialect quoting and values must travel as bound
// parameters, and neither belongs above this package.

// FKFilter builds the equality filter that selects the row a foreign key
// points at: columns[i] = values[i], joined with AND.
//
// A NULL value is an error naming the column. SQL's `= NULL` matches
// nothing, so a filter built from one would silently show an empty grid
// instead of telling the user the cell has no target.
func FKFilter(d Dialect, columns []string, values []any) (*Filter, error) {
	if len(columns) == 0 {
		return nil, fmt.Errorf("foreign key: no columns")
	}
	if len(columns) != len(values) {
		return nil, fmt.Errorf("foreign key: %d columns but %d values", len(columns), len(values))
	}
	exprs := make([]string, 0, len(columns))
	raws := make([]string, 0, len(columns))
	args := make([]any, 0, len(columns))
	for i, c := range columns {
		if strings.TrimSpace(c) == "" {
			return nil, fmt.Errorf("foreign key: unnamed column")
		}
		if values[i] == nil {
			return nil, fmt.Errorf("%s is NULL", c)
		}
		args = append(args, values[i])
		exprs = append(exprs, d.QuoteIdent(c)+" = "+d.Placeholder(len(args)))
		raws = append(raws, c+" = "+displayLiteral(values[i]))
	}
	return &Filter{
		Expr: strings.Join(exprs, " AND "),
		Args: args,
		Raw:  strings.Join(raws, " AND "),
	}, nil
}

// displayLiteral renders a bound value the way the status line shows it:
// text quoted, everything else bare. It never reaches a statement — the
// value itself is a parameter — so this is display only.
func displayLiteral(v any) string {
	if s, ok := v.(string); ok {
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	}
	return FormatValue(v, "NULL")
}

// SplitQualified splits an optionally schema-qualified relation name
// into its namespace and its bare name. Engines that report a foreign
// key's target as `schema.table` (PostgreSQL across schemas, SQLite with
// an attached database) are the reason the two are not assumed equal to
// the browsing namespace. An unqualified name yields an empty namespace,
// which every Driver method reads as "the current one".
func SplitQualified(name string) (namespace, table string) {
	trimmed := strings.TrimSpace(name)
	if i := strings.LastIndex(trimmed, "."); i >= 0 {
		return unquoteRelation(trimmed[:i]), unquoteRelation(trimmed[i+1:])
	}
	return "", unquoteRelation(trimmed)
}

// unquoteRelation strips whatever quoting an engine put around one part
// of a qualified name, so the Driver can apply its own.
func unquoteRelation(s string) string {
	s = strings.TrimSpace(s)
	if len(s) < 2 {
		return s
	}
	switch s[0] {
	case '"':
		if s[len(s)-1] == '"' {
			return strings.ReplaceAll(s[1:len(s)-1], `""`, `"`)
		}
	case '`':
		if s[len(s)-1] == '`' {
			return strings.ReplaceAll(s[1:len(s)-1], "``", "`")
		}
	case '[':
		if s[len(s)-1] == ']' {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// FKAt returns the foreign keys of a table that the named column takes
// part in, keeping the order the engine reported them in. A column can
// belong to more than one constraint, which is why this is a slice: the
// UI then asks which one to follow.
func FKAt(fks []ForeignKey, column string) []ForeignKey {
	var out []ForeignKey
	for _, fk := range fks {
		for _, c := range fk.Columns {
			if strings.EqualFold(c, column) {
				out = append(out, fk)
				break
			}
		}
	}
	return out
}
