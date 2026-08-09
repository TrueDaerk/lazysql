package db

import (
	"context"
	"fmt"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func init() {
	register(postgresDialect{})
}

type postgresDialect struct{}

func (postgresDialect) Engine() Engine      { return EnginePostgres }
func (postgresDialect) DisplayName() string { return "PostgreSQL" }
func (postgresDialect) driverName() string  { return "pgx" }

func (postgresDialect) QuoteIdent(ident string) string {
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}

func (postgresDialect) Placeholder(n int) string { return fmt.Sprintf("$%d", n) }

func (postgresDialect) LimitOffset(limit, offset int) string {
	return fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)
}

// PostgreSQL cannot query across databases on one connection, so the
// browsable namespaces are the schemas of the connected database.
func (postgresDialect) listDatabases(ctx context.Context, q querier) ([]string, error) {
	return scanStrings(ctx, q,
		`SELECT nspname FROM pg_catalog.pg_namespace
		 WHERE nspname NOT LIKE 'pg\_%' AND nspname <> 'information_schema'
		 ORDER BY nspname`)
}

func pgSchema(database string) string {
	if database == "" {
		return "public"
	}
	return database
}

func (postgresDialect) listTables(ctx context.Context, q querier, database string) ([]string, error) {
	return scanStrings(ctx, q,
		`SELECT table_name FROM information_schema.tables
		 WHERE table_schema = $1 ORDER BY table_name`,
		pgSchema(database))
}

func (d postgresDialect) tableColumns(ctx context.Context, q querier, database, table string) ([]Column, error) {
	schema := pgSchema(database)
	rows, err := q.QueryContext(ctx,
		`SELECT c.column_name, c.data_type, c.is_nullable, c.column_default,
		        EXISTS (
		          SELECT 1 FROM pg_index i
		          JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY (i.indkey)
		          WHERE i.indrelid = to_regclass($3) AND i.indisprimary
		            AND a.attname = c.column_name
		        )
		 FROM information_schema.columns c
		 WHERE c.table_schema = $1 AND c.table_name = $2
		 ORDER BY c.ordinal_position`,
		schema, table, d.QuoteIdent(schema)+"."+d.QuoteIdent(table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cols []Column
	for rows.Next() {
		var name, typ, nullable string
		var def *string
		var pk bool
		if err := rows.Scan(&name, &typ, &nullable, &def, &pk); err != nil {
			return nil, err
		}
		cols = append(cols, Column{
			Name:       name,
			DataType:   typ,
			Nullable:   nullable == "YES",
			Default:    def,
			PrimaryKey: pk,
		})
	}
	return cols, rows.Err()
}

func (d postgresDialect) tableIndexes(ctx context.Context, q querier, database, table string) ([]Index, error) {
	schema := pgSchema(database)
	rows, err := q.QueryContext(ctx,
		`SELECT ci.relname, a.attname, i.indisunique, i.indisprimary
		 FROM pg_index i
		 JOIN pg_class ci ON ci.oid = i.indexrelid
		 JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY (i.indkey)
		 WHERE i.indrelid = to_regclass($1)
		 ORDER BY ci.relname, a.attnum`,
		d.QuoteIdent(schema)+"."+d.QuoteIdent(table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var idx []Index
	for rows.Next() {
		var name, col string
		var unique, primary bool
		if err := rows.Scan(&name, &col, &unique, &primary); err != nil {
			return nil, err
		}
		if n := len(idx); n > 0 && idx[n-1].Name == name {
			idx[n-1].Columns = append(idx[n-1].Columns, col)
			continue
		}
		idx = append(idx, Index{Name: name, Columns: []string{col}, Unique: unique, Primary: primary})
	}
	return idx, rows.Err()
}

// PostgreSQL does not keep the original CREATE TABLE text, so the DDL
// is synthesized from the introspected columns.
func (d postgresDialect) tableDDL(ctx context.Context, q querier, database, table string) (string, error) {
	cols, err := d.tableColumns(ctx, q, database, table)
	if err != nil {
		return "", err
	}
	if len(cols) == 0 {
		return "", fmt.Errorf("db: no DDL for table %q", table)
	}
	return synthesizeDDL(d, pgSchema(database), table, cols), nil
}
