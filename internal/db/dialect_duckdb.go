package db

import (
	"context"
	"fmt"
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

func (duckdbDialect) listTables(ctx context.Context, q querier, database string) ([]string, error) {
	cond, args := duckdbDBCond(database)
	return scanStrings(ctx, q,
		`SELECT table_name FROM duckdb_tables() WHERE `+cond+`
		 UNION ALL
		 SELECT view_name FROM duckdb_views() WHERE NOT internal AND `+cond+`
		 ORDER BY 1`,
		append(args, args...)...)
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
		cols = append(cols, Column{
			Name:       name,
			DataType:   typ,
			Nullable:   nullable,
			Default:    def,
			PrimaryKey: pkCols[name],
		})
	}
	return cols, rows.Err()
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
		var cols []string
		for _, c := range strings.Split(strings.Trim(exprs, "[]"), ",") {
			if c = strings.Trim(strings.TrimSpace(c), `'"`); c != "" {
				cols = append(cols, c)
			}
		}
		idx = append(idx, Index{Name: name, Columns: cols, Unique: unique})
	}
	return idx, rows.Err()
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
