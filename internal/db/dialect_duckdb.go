package db

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	_ "github.com/marcboeker/go-duckdb/v2"
)

func init() {
	register(duckdbDialect{})
}

// duckdbDialect uses marcboeker/go-duckdb, which requires CGO (the
// DuckDB engine is a bundled C++ library).
type duckdbDialect struct{}

func (duckdbDialect) Engine() Engine      { return EngineDuckDB }
func (duckdbDialect) DisplayName() string { return "DuckDB" }
func (duckdbDialect) driverName() string  { return "duckdb" }

func (duckdbDialect) QuoteIdent(ident string) string {
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}

func (duckdbDialect) Placeholder(int) string { return "?" }

func (duckdbDialect) LimitOffset(limit, offset int) string {
	return fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)
}

func (duckdbDialect) listDatabases(ctx context.Context, q querier) ([]string, error) {
	return scanStrings(ctx, q,
		`SELECT database_name FROM duckdb_databases()
		 WHERE NOT internal ORDER BY database_name`)
}

// duckdbDBCond returns the database_name predicate and its args; an
// empty database means the connection's current one.
func duckdbDBCond(database string) (string, []any) {
	if database == "" {
		return "database_name = current_database()", nil
	}
	return "database_name = ?", []any{database}
}

func (duckdbDialect) listRelations(ctx context.Context, q querier, database string) ([]Relation, error) {
	cond, args := duckdbDBCond(database)
	// duckdb_tables() and duckdb_views() are separate functions, so the
	// kind column is a literal rather than a catalog value.
	return scanRelations(ctx, q,
		`SELECT table_name, 'table' FROM duckdb_tables() WHERE `+cond+`
		 UNION ALL
		 SELECT view_name, 'view' FROM duckdb_views() WHERE NOT internal AND `+cond+`
		 ORDER BY 1`,
		append(append([]any{}, args...), args...)...)
}

// tableStats reads duckdb_tables().estimated_size, the cardinality the
// optimizer plans with. DuckDB's catalog reports no per-table byte size —
// pragma_database_size is whole-database only — so the size half stays
// unknown and the tree shows a row estimate alone.
func (duckdbDialect) tableStats(ctx context.Context, q querier, database string) ([]TableStat, error) {
	cond, args := duckdbDBCond(database)
	return scanTableStats(ctx, q,
		`SELECT table_name, estimated_size, NULL FROM duckdb_tables()
		 WHERE `+cond+` ORDER BY table_name`,
		args...)
}

func (duckdbDialect) tableColumns(ctx context.Context, q querier, database, table string) ([]Column, error) {
	cond, args := duckdbDBCond(database)

	pkCols := map[string]bool{}
	pkRows, err := scanStrings(ctx, q,
		`SELECT unnest(constraint_column_names) FROM duckdb_constraints()
		 WHERE `+cond+` AND table_name = ? AND constraint_type = 'PRIMARY KEY'`,
		append(append([]any{}, args...), table)...)
	if err != nil {
		return nil, err
	}
	for _, c := range pkRows {
		pkCols[c] = true
	}

	rows, err := q.QueryContext(ctx,
		`SELECT column_name, data_type, is_nullable, column_default
		 FROM duckdb_columns()
		 WHERE `+cond+` AND table_name = ?
		 ORDER BY column_index`,
		append(append([]any{}, args...), table)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cols []Column
	for rows.Next() {
		var name, typ string
		var nullable bool
		var def *string
		if err := rows.Scan(&name, &typ, &nullable, &def); err != nil {
			return nil, err
		}
		c := Column{
			Name:       name,
			DataType:   typ,
			Nullable:   nullable,
			Default:    def,
			PrimaryKey: pkCols[name],
		}
		if def != nil && strings.HasPrefix(strings.ToLower(*def), "nextval(") {
			c.Extra = "sequence"
		}
		cols = append(cols, c)
	}
	return cols, rows.Err()
}

// duckdbFKRef pulls the referenced table and columns out of a
// constraint_text such as
// "FOREIGN KEY (user_id) REFERENCES users(id)". duckdb_constraints()
// exposes the constrained columns as a list but spells the reference
// only inside that text, and its column names have changed between
// DuckDB versions — parsing the text works on every version.
var duckdbFKRef = regexp.MustCompile(`(?i)REFERENCES\s+([^\s(]+)\s*\(([^)]*)\)`)

func (duckdbDialect) tableForeignKeys(ctx context.Context, q querier, database, table string) ([]ForeignKey, error) {
	cond, args := duckdbDBCond(database)
	rows, err := q.QueryContext(ctx,
		`SELECT constraint_text, CAST(constraint_column_names AS VARCHAR)
		 FROM duckdb_constraints()
		 WHERE `+cond+` AND table_name = ? AND constraint_type = 'FOREIGN KEY'
		 ORDER BY constraint_index`,
		append(append([]any{}, args...), table)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var fks []ForeignKey
	for rows.Next() {
		var text, colList string
		if err := rows.Scan(&text, &colList); err != nil {
			return nil, err
		}
		fk := ForeignKey{Columns: splitDuckList(colList)}
		if m := duckdbFKRef.FindStringSubmatch(text); m != nil {
			fk.RefTable = strings.Trim(m[1], `"`)
			fk.RefColumns = splitDuckList(m[2])
		}
		fks = append(fks, fk)
	}
	return fks, rows.Err()
}

// splitDuckList splits a VARCHAR-rendered DuckDB list ("[a, b]") or a
// plain comma-separated column list into its trimmed, unquoted parts.
func splitDuckList(s string) []string {
	var out []string
	for _, c := range strings.Split(strings.Trim(s, "[]"), ",") {
		if c = strings.Trim(strings.TrimSpace(c), `'"`); c != "" {
			out = append(out, c)
		}
	}
	return out
}

func (duckdbDialect) tableIndexes(ctx context.Context, q querier, database, table string) ([]Index, error) {
	cond, args := duckdbDBCond(database)
	// duckdb_indexes() reports index expressions as a list; it is cast
	// to VARCHAR ('[a, b]') and split, which is fine for plain column
	// indexes (expression indexes keep the raw expression text).
	rows, err := q.QueryContext(ctx,
		`SELECT index_name, is_unique, CAST(expressions AS VARCHAR)
		 FROM duckdb_indexes()
		 WHERE `+cond+` AND table_name = ?
		 ORDER BY index_name`,
		append(append([]any{}, args...), table)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var idx []Index
	for rows.Next() {
		var name, exprs string
		var unique bool
		if err := rows.Scan(&name, &unique, &exprs); err != nil {
			return nil, err
		}
		idx = append(idx, Index{Name: name, Columns: splitDuckList(exprs), Unique: unique})
	}
	return idx, rows.Err()
}

// DuckDB has no triggers at all — there is no CREATE TRIGGER statement
// and no catalog function to list one — so both trigger entry points
// answer ErrUnsupported rather than an empty list the UI would render as
// "none defined". See wiki/reference/trigger-introspection.md.
func (duckdbDialect) listTriggers(context.Context, querier, string) ([]Trigger, error) {
	return nil, ErrUnsupported
}

func (duckdbDialect) triggerDDL(context.Context, querier, string, string) (string, error) {
	return "", ErrUnsupported
}

func (duckdbDialect) tableDDL(ctx context.Context, q querier, database, table string) (string, error) {
	cond, args := duckdbDBCond(database)
	ddls, err := scanStrings(ctx, q,
		`SELECT sql FROM duckdb_tables() WHERE `+cond+` AND table_name = ?
		 UNION ALL
		 SELECT sql FROM duckdb_views() WHERE NOT internal AND `+cond+` AND view_name = ?`,
		append(append(append([]any{}, args...), table), append(append([]any{}, args...), table)...)...)
	if err != nil {
		return "", err
	}
	if len(ddls) == 0 {
		return "", fmt.Errorf("db: no DDL for table %q", table)
	}
	return ddls[0], nil
}
