package db

import (
	"context"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

func init() {
	register(sqliteDialect{})
}

// sqliteDialect uses modernc.org/sqlite, a pure-Go port — no CGO needed.
type sqliteDialect struct{}

func (sqliteDialect) Engine() Engine      { return EngineSQLite }
func (sqliteDialect) DisplayName() string { return "SQLite" }
func (sqliteDialect) driverName() string  { return "sqlite" }

func (sqliteDialect) QuoteIdent(ident string) string {
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}

func (sqliteDialect) Placeholder(int) string { return "?" }

func (sqliteDialect) LimitOffset(limit, offset int) string {
	return fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)
}

// listDatabases returns the attached databases ("main", "temp", ...).
func (sqliteDialect) listDatabases(ctx context.Context, q querier) ([]string, error) {
	rows, err := q.QueryContext(ctx, `PRAGMA database_list`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var seq int64
		var name, file *string
		if err := rows.Scan(&seq, &name, &file); err != nil {
			return nil, err
		}
		if name != nil {
			out = append(out, *name)
		}
	}
	return out, rows.Err()
}

func sqliteSchema(d Dialect, database string) string {
	if database == "" {
		database = "main"
	}
	return d.QuoteIdent(database)
}

func (d sqliteDialect) listRelations(ctx context.Context, q querier, database string) ([]Relation, error) {
	// sqlite_master lives per attached database and cannot be
	// parameterized by schema, so the (quoted) schema is interpolated.
	return scanRelations(ctx, q,
		`SELECT name, type FROM `+sqliteSchema(d, database)+`.sqlite_master
		 WHERE type IN ('table', 'view') AND name NOT LIKE 'sqlite_%'
		 ORDER BY name`)
}

func (d sqliteDialect) tableColumns(ctx context.Context, q querier, database, table string) ([]Column, error) {
	// PRAGMA accepts no placeholders; identifiers are dialect-quoted.
	rows, err := q.QueryContext(ctx,
		fmt.Sprintf(`PRAGMA %s.table_info(%s)`, sqliteSchema(d, database), d.QuoteIdent(table)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cols []Column
	for rows.Next() {
		var cid, notNull, pk int64
		var name, typ string
		var def *string
		if err := rows.Scan(&cid, &name, &typ, &notNull, &def, &pk); err != nil {
			return nil, err
		}
		cols = append(cols, Column{
			Name:       name,
			DataType:   typ,
			Nullable:   notNull == 0,
			Default:    def,
			PrimaryKey: pk > 0,
		})
	}
	return cols, rows.Err()
}

func (d sqliteDialect) tableIndexes(ctx context.Context, q querier, database, table string) ([]Index, error) {
	schema := sqliteSchema(d, database)
	rows, err := q.QueryContext(ctx,
		fmt.Sprintf(`PRAGMA %s.index_list(%s)`, schema, d.QuoteIdent(table)))
	if err != nil {
		return nil, err
	}
	var idx []Index
	for rows.Next() {
		var seq, unique int64
		var name, origin string
		var partial int64
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			rows.Close()
			return nil, err
		}
		idx = append(idx, Index{Name: name, Unique: unique == 1, Primary: origin == "pk"})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	for i := range idx {
		cols, err := scanIndexColumns(ctx, q,
			fmt.Sprintf(`PRAGMA %s.index_info(%s)`, schema, d.QuoteIdent(idx[i].Name)))
		if err != nil {
			return nil, err
		}
		idx[i].Columns = cols
	}
	return idx, nil
}

// scanIndexColumns reads PRAGMA index_info rows (seqno, cid, name).
func scanIndexColumns(ctx context.Context, q querier, query string) ([]string, error) {
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var seqno, cid int64
		var name *string
		if err := rows.Scan(&seqno, &cid, &name); err != nil {
			return nil, err
		}
		if name != nil {
			out = append(out, *name)
		}
	}
	return out, rows.Err()
}

func (d sqliteDialect) tableDDL(ctx context.Context, q querier, database, table string) (string, error) {
	ddls, err := scanStrings(ctx, q,
		`SELECT sql FROM `+sqliteSchema(d, database)+`.sqlite_master
		 WHERE name = ? AND sql IS NOT NULL`,
		table)
	if err != nil {
		return "", err
	}
	if len(ddls) == 0 {
		return "", fmt.Errorf("db: no DDL for table %q", table)
	}
	return ddls[0], nil
}
