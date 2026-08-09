package db

import (
	"context"
	"fmt"
	"strings"

	_ "github.com/go-sql-driver/mysql"
)

func init() {
	register(mysqlDialect{engine: EngineMySQL, name: "MySQL"})
	// MariaDB is wire-compatible with MySQL: same driver, same dialect,
	// separate engine and display name.
	register(mysqlDialect{engine: EngineMariaDB, name: "MariaDB"})
}

type mysqlDialect struct {
	engine Engine
	name   string
}

func (d mysqlDialect) Engine() Engine      { return d.engine }
func (d mysqlDialect) DisplayName() string { return d.name }
func (d mysqlDialect) driverName() string  { return "mysql" }

func (mysqlDialect) QuoteIdent(ident string) string {
	return "`" + strings.ReplaceAll(ident, "`", "``") + "`"
}

func (mysqlDialect) Placeholder(int) string { return "?" }

func (mysqlDialect) LimitOffset(limit, offset int) string {
	return fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)
}

func (mysqlDialect) listDatabases(ctx context.Context, q querier) ([]string, error) {
	return scanStrings(ctx, q,
		`SELECT schema_name FROM information_schema.schemata ORDER BY schema_name`)
}

// schemaCond returns the table_schema predicate and its args; an empty
// database means the connection's current one.
func mysqlSchemaCond(database string) (string, []any) {
	if database == "" {
		return "table_schema = DATABASE()", nil
	}
	return "table_schema = ?", []any{database}
}

func (mysqlDialect) listRelations(ctx context.Context, q querier, database string) ([]Relation, error) {
	cond, args := mysqlSchemaCond(database)
	return scanRelations(ctx, q,
		`SELECT table_name, table_type FROM information_schema.tables
		 WHERE `+cond+` ORDER BY table_name`,
		args...)
}

func (mysqlDialect) tableColumns(ctx context.Context, q querier, database, table string) ([]Column, error) {
	cond, args := mysqlSchemaCond(database)
	rows, err := q.QueryContext(ctx,
		`SELECT column_name, column_type, is_nullable, column_default, column_key
		 FROM information_schema.columns
		 WHERE `+cond+` AND table_name = ?
		 ORDER BY ordinal_position`,
		append(args, table)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cols []Column
	for rows.Next() {
		var name, typ, nullable, key string
		var def *string
		if err := rows.Scan(&name, &typ, &nullable, &def, &key); err != nil {
			return nil, err
		}
		cols = append(cols, Column{
			Name:       name,
			DataType:   typ,
			Nullable:   nullable == "YES",
			Default:    def,
			PrimaryKey: key == "PRI",
		})
	}
	return cols, rows.Err()
}

func (mysqlDialect) tableIndexes(ctx context.Context, q querier, database, table string) ([]Index, error) {
	cond, args := mysqlSchemaCond(database)
	rows, err := q.QueryContext(ctx,
		`SELECT index_name, column_name, non_unique
		 FROM information_schema.statistics
		 WHERE `+cond+` AND table_name = ?
		 ORDER BY index_name, seq_in_index`,
		append(args, table)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var idx []Index
	for rows.Next() {
		var name, col string
		var nonUnique int64
		if err := rows.Scan(&name, &col, &nonUnique); err != nil {
			return nil, err
		}
		if n := len(idx); n > 0 && idx[n-1].Name == name {
			idx[n-1].Columns = append(idx[n-1].Columns, col)
			continue
		}
		idx = append(idx, Index{
			Name:    name,
			Columns: []string{col},
			Unique:  nonUnique == 0,
			Primary: name == "PRIMARY",
		})
	}
	return idx, rows.Err()
}

func (d mysqlDialect) tableDDL(ctx context.Context, q querier, database, table string) (string, error) {
	rows, err := q.QueryContext(ctx,
		"SHOW CREATE TABLE "+qualifiedTable(d, database, table))
	if err != nil {
		return "", err
	}
	defer rows.Close()
	if !rows.Next() {
		return "", fmt.Errorf("db: no DDL for table %q", table)
	}
	var name, ddl string
	if err := rows.Scan(&name, &ddl); err != nil {
		return "", err
	}
	return ddl, rows.Err()
}
