package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// conn is the one Driver implementation; all engine differences are
// delegated to its Dialect.
type conn struct {
	dialect Dialect
	db      *sql.DB
}

var errNotConnected = errors.New("db: not connected")

func (c *conn) Connect(ctx context.Context, dsn string) error {
	if c.db != nil {
		return errors.New("db: already connected")
	}
	sqlDB, err := sql.Open(c.dialect.driverName(), dsn)
	if err != nil {
		return fmt.Errorf("db: open %s: %w", c.dialect.DisplayName(), err)
	}
	if err := sqlDB.PingContext(ctx); err != nil {
		sqlDB.Close()
		return fmt.Errorf("db: connect %s: %w", c.dialect.DisplayName(), err)
	}
	c.db = sqlDB
	return nil
}

func (c *conn) Close() error {
	if c.db == nil {
		return nil
	}
	err := c.db.Close()
	c.db = nil
	return err
}

func (c *conn) Engine() Engine   { return c.dialect.Engine() }
func (c *conn) Dialect() Dialect { return c.dialect }

// querierAdapter narrows *sql.DB to the querier interface the dialects use.
type querierAdapter struct{ db *sql.DB }

func (q querierAdapter) QueryContext(ctx context.Context, query string, args ...any) (rowsScanner, error) {
	return q.db.QueryContext(ctx, query, args...)
}

func (c *conn) q() (querier, error) {
	if c.db == nil {
		return nil, errNotConnected
	}
	return querierAdapter{c.db}, nil
}

func (c *conn) ListDatabases(ctx context.Context) ([]string, error) {
	q, err := c.q()
	if err != nil {
		return nil, err
	}
	return c.dialect.listDatabases(ctx, q)
}

func (c *conn) ListTables(ctx context.Context, database string) ([]string, error) {
	rels, err := c.ListRelations(ctx, database)
	if err != nil {
		return nil, err
	}
	return RelationNames(rels), nil
}

func (c *conn) ListRelations(ctx context.Context, database string) ([]Relation, error) {
	q, err := c.q()
	if err != nil {
		return nil, err
	}
	return c.dialect.listRelations(ctx, q, database)
}

func (c *conn) TableColumns(ctx context.Context, database, table string) ([]Column, error) {
	q, err := c.q()
	if err != nil {
		return nil, err
	}
	return c.dialect.tableColumns(ctx, q, database, table)
}

func (c *conn) TableIndexes(ctx context.Context, database, table string) ([]Index, error) {
	q, err := c.q()
	if err != nil {
		return nil, err
	}
	return c.dialect.tableIndexes(ctx, q, database, table)
}

func (c *conn) TableForeignKeys(ctx context.Context, database, table string) ([]ForeignKey, error) {
	q, err := c.q()
	if err != nil {
		return nil, err
	}
	return c.dialect.tableForeignKeys(ctx, q, database, table)
}

func (c *conn) TableDDL(ctx context.Context, database, table string) (string, error) {
	q, err := c.q()
	if err != nil {
		return "", err
	}
	return c.dialect.tableDDL(ctx, q, database, table)
}

// qualifiedTable renders database.table with per-dialect quoting,
// omitting the database part when empty.
func qualifiedTable(d Dialect, database, table string) string {
	if database == "" {
		return d.QuoteIdent(table)
	}
	return d.QuoteIdent(database) + "." + d.QuoteIdent(table)
}

// writeWhere appends the filter's WHERE clause, if it has one.
func writeWhere(b *strings.Builder, filter *Filter) {
	if filter.empty() {
		return
	}
	b.WriteString(" WHERE ")
	b.WriteString(filter.Expr)
}

// PageSQL renders the statement QueryPage executes. The UI uses it to
// show the exact SQL in the command log without running it twice.
func PageSQL(d Dialect, database, table string, filter *Filter, sortBy *Sort, limit, offset int) string {
	var b strings.Builder
	b.WriteString("SELECT * FROM ")
	b.WriteString(qualifiedTable(d, database, table))
	writeWhere(&b, filter)
	if sortBy != nil && sortBy.Column != "" {
		b.WriteString(" ORDER BY ")
		b.WriteString(d.QuoteIdent(sortBy.Column))
		if sortBy.Desc {
			b.WriteString(" DESC")
		} else {
			b.WriteString(" ASC")
		}
	}
	b.WriteString(d.LimitOffset(limit, offset))
	return b.String()
}

// CountSQL renders the statement CountRows executes.
func CountSQL(d Dialect, database, table string, filter *Filter) string {
	var b strings.Builder
	b.WriteString("SELECT COUNT(*) FROM ")
	b.WriteString(qualifiedTable(d, database, table))
	writeWhere(&b, filter)
	return b.String()
}

func (c *conn) QueryPage(ctx context.Context, database, table string, filter *Filter, sortBy *Sort, limit, offset int) (*ResultSet, error) {
	if c.db == nil {
		return nil, errNotConnected
	}
	q := PageSQL(c.dialect, database, table, filter, sortBy, limit, offset)
	return c.Query(ctx, q, FilterArgs(filter)...)
}

func (c *conn) CountRows(ctx context.Context, database, table string, filter *Filter) (int64, error) {
	if c.db == nil {
		return 0, errNotConnected
	}
	rs, err := c.Query(ctx, CountSQL(c.dialect, database, table, filter), FilterArgs(filter)...)
	if err != nil {
		return 0, err
	}
	if len(rs.Rows) == 0 || len(rs.Rows[0]) == 0 {
		return 0, errors.New("db: count returned no rows")
	}
	switch n := rs.Rows[0][0].(type) {
	case int64:
		return n, nil
	case float64:
		return int64(n), nil
	case string:
		// Some drivers hand big integers back as decimal text.
		v, err := strconv.ParseInt(n, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("db: unreadable count %q", n)
		}
		return v, nil
	default:
		return 0, fmt.Errorf("db: unreadable count of type %T", n)
	}
}

func (c *conn) Exec(ctx context.Context, query string, args ...any) (ExecResult, error) {
	if c.db == nil {
		return ExecResult{}, errNotConnected
	}
	res, err := c.db.ExecContext(ctx, query, args...)
	if err != nil {
		return ExecResult{}, err
	}
	out := ExecResult{}
	// Not every engine reports these; ignore the errors.
	if n, err := res.RowsAffected(); err == nil {
		out.RowsAffected = n
	}
	if id, err := res.LastInsertId(); err == nil {
		out.LastInsertID = id
	}
	return out, nil
}

func (c *conn) ExecTx(ctx context.Context, stmts []Statement) ([]ExecResult, error) {
	if c.db == nil {
		return nil, errNotConnected
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	out := make([]ExecResult, 0, len(stmts))
	for i, s := range stmts {
		res, err := tx.ExecContext(ctx, s.SQL, s.Args...)
		if err != nil {
			tx.Rollback()
			return nil, fmt.Errorf("statement %d of %d: %w", i+1, len(stmts), err)
		}
		r := ExecResult{}
		if n, err := res.RowsAffected(); err == nil {
			r.RowsAffected = n
		}
		if id, err := res.LastInsertId(); err == nil {
			r.LastInsertID = id
		}
		out = append(out, r)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *conn) Query(ctx context.Context, query string, args ...any) (*ResultSet, error) {
	if c.db == nil {
		return nil, errNotConnected
	}
	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanResultSet(ctx, rows)
}
