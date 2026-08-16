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

// tableStats reads the two optional statistics sources SQLite has, both
// of which may be absent:
//
//   - sqlite_stat1 exists only after an ANALYZE. Its stat column starts
//     with the table's row count, which CAST(... AS INTEGER) picks off
//     the front of "1234 5 2"; every index of a table repeats the same
//     count, so MAX collapses them.
//   - dbstat is a virtual table the build may not include
//     (SQLITE_ENABLE_DBSTAT_VTAB). Its rows are per b-tree, so index
//     pages are folded onto their table through sqlite_master.tbl_name,
//     matching the table+indexes footprint the other engines report.
//
// Naming an absent source is a query-time error, so the sources are
// asked about first and only the ones that exist are read. The probe is
// a query of its own rather than a failed attempt at the real one
// because every statement lands in the command log: browsing a database
// nobody has ever ANALYZEd must not paint a red "no such table:
// sqlite_stat1" line there on every expand. With neither source present
// the second query is skipped entirely and the tables render
// unannotated.
func (d sqliteDialect) tableStats(ctx context.Context, q querier, database string) ([]TableStat, error) {
	schema := sqliteSchema(d, database)
	stat1, dbstat, err := sqliteStatSources(ctx, q, schema)
	if err != nil || (!stat1 && !dbstat) {
		return nil, err
	}
	return scanTableStats(ctx, q, sqliteStatsSQL(schema, stat1, dbstat))
}

// sqliteStatSources reports which statistics sources this database and
// this build have: sqlite_stat1 is an ordinary table and shows up in the
// schema, dbstat is a virtual table module and shows up in the module
// list.
func sqliteStatSources(ctx context.Context, q querier, schema string) (stat1, dbstat bool, err error) {
	rows, err := q.QueryContext(ctx,
		`SELECT (SELECT COUNT(*) FROM `+schema+`.sqlite_master WHERE name = 'sqlite_stat1'),
		        (SELECT COUNT(*) FROM pragma_module_list WHERE name = 'dbstat')`)
	if err != nil {
		return false, false, err
	}
	defer rows.Close()
	if !rows.Next() {
		return false, false, rows.Err()
	}
	var haveStat1, haveDBStat int64
	if err := rows.Scan(&haveStat1, &haveDBStat); err != nil {
		return false, false, err
	}
	return haveStat1 > 0, haveDBStat > 0, rows.Err()
}

// sqliteStatsSQL builds the stats query over whichever sources are being
// tried. sqlite_master drives it so a table with no statistics at all
// still gets a row.
func sqliteStatsSQL(schema string, stat1, dbstat bool) string {
	rows := "NULL"
	if stat1 {
		rows = "(SELECT MAX(CAST(s.stat AS INTEGER))" +
			" FROM " + schema + ".sqlite_stat1 s WHERE s.tbl = m.name)"
	}
	size := "NULL"
	if dbstat {
		size = "(SELECT SUM(d.pgsize) FROM " + schema + ".dbstat d" +
			" JOIN " + schema + ".sqlite_master x ON x.name = d.name" +
			" WHERE x.tbl_name = m.name)"
	}
	return "SELECT m.name, " + rows + ", " + size +
		"\n FROM " + schema + ".sqlite_master m" +
		"\n WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite_%'" +
		"\n ORDER BY m.name"
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
