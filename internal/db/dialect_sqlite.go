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

// listTriggers reads sqlite_master, which stores triggers next to the
// tables and views. tbl_name is the relation the trigger fires on.
func (d sqliteDialect) listTriggers(ctx context.Context, q querier, database string) ([]Trigger, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT name, tbl_name FROM `+sqliteSchema(d, database)+`.sqlite_master
		 WHERE type = 'trigger' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Trigger
	for rows.Next() {
		var name string
		var table *string
		if err := rows.Scan(&name, &table); err != nil {
			return nil, err
		}
		out = append(out, Trigger{Name: name, Table: derefOr(table, "")})
	}
	return out, rows.Err()
}

// triggerDDL returns the CREATE TRIGGER text SQLite stored verbatim.
func (d sqliteDialect) triggerDDL(ctx context.Context, q querier, database, trigger string) (string, error) {
	ddls, err := scanStrings(ctx, q,
		`SELECT sql FROM `+sqliteSchema(d, database)+`.sqlite_master
		 WHERE type = 'trigger' AND name = ? AND sql IS NOT NULL`,
		trigger)
	if err != nil {
		return "", err
	}
	if len(ddls) == 0 {
		return "", fmt.Errorf("db: no DDL for trigger %q", trigger)
	}
	return ddls[0], nil
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	markSQLiteAutoincrement(ctx, d, q, database, table, cols)
	return cols, nil
}

// markSQLiteAutoincrement fills in the Extra note for an AUTOINCREMENT
// primary key. PRAGMA table_info does not report the keyword at all —
// it exists only in the stored CREATE TABLE text — so the DDL is read
// back to find it. A failure here costs a display detail and nothing
// else, so the error is dropped rather than failing the whole listing.
func markSQLiteAutoincrement(ctx context.Context, d sqliteDialect, q querier, database, table string, cols []Column) {
	ddl, err := d.tableDDL(ctx, q, database, table)
	if err != nil || !strings.Contains(strings.ToUpper(ddl), "AUTOINCREMENT") {
		return
	}
	// AUTOINCREMENT is only legal on a single INTEGER PRIMARY KEY.
	for i := range cols {
		if cols[i].PrimaryKey && strings.EqualFold(cols[i].DataType, "INTEGER") {
			cols[i].Extra = "autoincrement"
			return
		}
	}
}

func (d sqliteDialect) tableForeignKeys(ctx context.Context, q querier, database, table string) ([]ForeignKey, error) {
	rows, err := q.QueryContext(ctx,
		fmt.Sprintf(`PRAGMA %s.foreign_key_list(%s)`, sqliteSchema(d, database), d.QuoteIdent(table)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var fks []ForeignKey
	for rows.Next() {
		// id groups the columns of one constraint; seq is their order.
		var id, seq int64
		var refTable, from, onUpdate, onDelete, match string
		var to *string
		if err := rows.Scan(&id, &seq, &refTable, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			return nil, err
		}
		// PRAGMA foreign_key_list never reports a constraint name, so
		// the id doubles as one and as the grouping key.
		fk := ForeignKey{
			Name:     fmt.Sprintf("fk_%d", id),
			RefTable: refTable,
			OnUpdate: onUpdate,
			OnDelete: onDelete,
		}
		// A NULL "to" means the reference targets the referenced
		// table's primary key implicitly.
		fks = appendFKColumn(fks, fk, from, derefOr(to, ""))
	}
	return fks, rows.Err()
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
