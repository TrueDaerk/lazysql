package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

func (c *conn) QueryPage(ctx context.Context, database, table, filter string, sortBy *Sort, limit, offset int) (*ResultSet, error) {
	if c.db == nil {
		return nil, errNotConnected
	}
	var b strings.Builder
	b.WriteString("SELECT * FROM ")
	b.WriteString(qualifiedTable(c.dialect, database, table))
	if strings.TrimSpace(filter) != "" {
		b.WriteString(" WHERE ")
		b.WriteString(filter)
	}
	if sortBy != nil && sortBy.Column != "" {
		b.WriteString(" ORDER BY ")
		b.WriteString(c.dialect.QuoteIdent(sortBy.Column))
		if sortBy.Desc {
			b.WriteString(" DESC")
		}
	}
	b.WriteString(c.dialect.LimitOffset(limit, offset))
	return c.Query(ctx, b.String())
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
