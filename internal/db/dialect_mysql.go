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
	return mysqlSchemaCondOn("", database)
}

// mysqlSchemaCondOn is schemaCond for a joined query, where the column
// needs a table alias in front of it ("k.").
func mysqlSchemaCondOn(prefix, database string) (string, []any) {
	if database == "" {
		return prefix + "table_schema = DATABASE()", nil
	}
	return prefix + "table_schema = ?", []any{database}
}

func (mysqlDialect) listRelations(ctx context.Context, q querier, database string) ([]Relation, error) {
	cond, args := mysqlSchemaCond(database)
	return scanRelations(ctx, q,
		`SELECT table_name, table_type FROM information_schema.tables
		 WHERE `+cond+` ORDER BY table_name`,
		args...)
}

// tableStats reads information_schema.tables, which for InnoDB is filled
// from the same sampled index statistics the optimizer uses: table_rows
// can be off by a large factor on a busy table and is only refreshed by
// ANALYZE TABLE or by InnoDB's own recalculation. data_length +
// index_length is the on-disk footprint of the table and its indexes.
// Views have no storage at all, so only base tables are asked about.
func (mysqlDialect) tableStats(ctx context.Context, q querier, database string) ([]TableStat, error) {
	cond, args := mysqlSchemaCond(database)
	return scanTableStats(ctx, q,
		`SELECT table_name, table_rows, data_length + index_length
		 FROM information_schema.tables
		 WHERE `+cond+` AND table_type = 'BASE TABLE'
		 ORDER BY table_name`,
		args...)
}

func (mysqlDialect) tableColumns(ctx context.Context, q querier, database, table string) ([]Column, error) {
	cond, args := mysqlSchemaCond(database)
	rows, err := q.QueryContext(ctx,
		`SELECT column_name, column_type, is_nullable, column_default, column_key, extra
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
		var def, extra *string
		if err := rows.Scan(&name, &typ, &nullable, &def, &key, &extra); err != nil {
			return nil, err
		}
		c := Column{
			Name:       name,
			DataType:   typ,
			Nullable:   nullable == "YES",
			Default:    def,
			PrimaryKey: key == "PRI",
		}
		if extra != nil {
			c.Extra = *extra
		}
		cols = append(cols, c)
	}
	return cols, rows.Err()
}

func (mysqlDialect) tableForeignKeys(ctx context.Context, q querier, database, table string) ([]ForeignKey, error) {
	cond, args := mysqlSchemaCondOn("k.", database)
	// referential_constraints holds the ON UPDATE/ON DELETE rules;
	// key_column_usage holds one row per key column, in key order.
	rows, err := q.QueryContext(ctx,
		`SELECT k.constraint_name, k.column_name,
		        k.referenced_table_schema, k.referenced_table_name, k.referenced_column_name,
		        r.update_rule, r.delete_rule
		 FROM information_schema.key_column_usage k
		 JOIN information_schema.referential_constraints r
		   ON r.constraint_schema = k.constraint_schema
		  AND r.constraint_name = k.constraint_name
		  AND r.table_name = k.table_name
		 WHERE `+cond+` AND k.table_name = ? AND k.referenced_table_name IS NOT NULL
		 ORDER BY k.constraint_name, k.ordinal_position`,
		append(args, table)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var fks []ForeignKey
	for rows.Next() {
		var name, col, refTable, onUpdate, onDelete string
		var refSchema, refCol *string
		if err := rows.Scan(&name, &col, &refSchema, &refTable, &refCol, &onUpdate, &onDelete); err != nil {
			return nil, err
		}
		if refSchema != nil && database != "" && *refSchema != database {
			refTable = *refSchema + "." + refTable
		}
		fk := ForeignKey{Name: name, RefTable: refTable, OnUpdate: onUpdate, OnDelete: onDelete}
		fks = appendFKColumn(fks, fk, col, derefOr(refCol, ""))
	}
	return fks, rows.Err()
}

// derefOr reads a nullable string column with a fallback.
func derefOr(s *string, fallback string) string {
	if s == nil {
		return fallback
	}
	return *s
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

// mysqlTriggerSchemaCond is the trigger_schema predicate of
// information_schema.triggers; an empty database means the connection's
// current one. Triggers are named per schema, not per table, so the
// listing is keyed on the schema alone.
func mysqlTriggerSchemaCond(database string) (string, []any) {
	if database == "" {
		return "trigger_schema = DATABASE()", nil
	}
	return "trigger_schema = ?", []any{database}
}

func (mysqlDialect) listTriggers(ctx context.Context, q querier, database string) ([]Trigger, error) {
	cond, args := mysqlTriggerSchemaCond(database)
	rows, err := q.QueryContext(ctx,
		`SELECT trigger_name, event_object_table FROM information_schema.triggers
		 WHERE `+cond+` ORDER BY trigger_name`,
		args...)
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

// triggerDDL synthesizes the CREATE TRIGGER statement from
// information_schema rather than running SHOW CREATE TRIGGER: the SHOW
// form needs the TRIGGER privilege on the schema and returns a different
// number of columns across MySQL and MariaDB versions, neither of which
// a read-only browser should depend on. The catalog carries every part
// that matters — timing, event, table and body.
func (d mysqlDialect) triggerDDL(ctx context.Context, q querier, database, trigger string) (string, error) {
	cond, args := mysqlTriggerSchemaCond(database)
	rows, err := q.QueryContext(ctx,
		`SELECT action_timing, event_manipulation, event_object_table,
		        action_orientation, action_statement
		 FROM information_schema.triggers
		 WHERE `+cond+` AND trigger_name = ?`,
		append(args, trigger)...)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return "", err
		}
		return "", fmt.Errorf("db: no DDL for trigger %q", trigger)
	}
	var timing, event, table, orientation, body string
	if err := rows.Scan(&timing, &event, &table, &orientation, &body); err != nil {
		return "", err
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return fmt.Sprintf("CREATE TRIGGER %s %s %s ON %s\nFOR EACH %s\n%s",
		d.QuoteIdent(trigger), timing, event,
		qualifiedTable(d, database, table), orientation, body), nil
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
