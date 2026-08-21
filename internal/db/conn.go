package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// conn is the one Driver implementation; all engine differences are
// delegated to its Dialect.
type conn struct {
	dialect Dialect
	db      *sql.DB
	logger  *Logger

	// dial is the SSH tunnel's transport, nil for a direct connection.
	// release undoes the driver-side registration dial needed.
	dial    DialFunc
	release func()

	// readOnly refuses every write this conn is asked to run. It is set
	// once at Open and never changes: a session cannot be talked out of
	// read-only mode while it is live. See readonly.go.
	readOnly bool

	// setup are the statements Connect runs to prepare the session; see
	// Options.Setup. They bypass the read-only guard, which is why they
	// are only ever set by this program's own code.
	setup []string
}

func (c *conn) Logger() *Logger { return c.logger }

var errNotConnected = errors.New("db: not connected")

func (c *conn) Connect(ctx context.Context, dsn string) error {
	if c.db != nil {
		return errors.New("db: already connected")
	}
	sqlDB, release, err := c.dialect.openDB(dsn, c.dial)
	if err != nil {
		return fmt.Errorf("db: open %s: %w", c.dialect.DisplayName(), err)
	}
	if err := sqlDB.PingContext(ctx); err != nil {
		sqlDB.Close()
		if release != nil {
			release()
		}
		return fmt.Errorf("db: connect %s: %w", c.dialect.DisplayName(), err)
	}
	c.db, c.release = sqlDB, release
	if err := c.runSetup(ctx); err != nil {
		c.Close()
		return err
	}
	return nil
}

// runSetup runs Options.Setup on the fresh session. A failure leaves the
// caller with no connection at all rather than a half-prepared one — the
// Parquet view is the whole point of its session, so a session without it
// is worth nothing.
func (c *conn) runSetup(ctx context.Context) error {
	for _, stmt := range c.setup {
		start := time.Now()
		_, err := c.db.ExecContext(ctx, stmt)
		c.logger.record(stmt, nil, start, err)
		if err != nil {
			return fmt.Errorf("db: session setup: %w", err)
		}
	}
	return nil
}

func (c *conn) Close() error {
	if c.db == nil {
		return nil
	}
	err := c.db.Close()
	c.db = nil
	// The driver-side registration outlives the handle, so it is dropped
	// only here — a tunnel that reconnects gets a fresh one.
	if c.release != nil {
		c.release()
		c.release = nil
	}
	return err
}

func (c *conn) Engine() Engine   { return c.dialect.Engine() }
func (c *conn) Dialect() Dialect { return c.dialect }
func (c *conn) ReadOnly() bool   { return c.readOnly }

// querierAdapter narrows *sql.DB to the querier interface the dialects use.
// It logs through the same Logger as every other call the conn makes, so
// introspection (columns, indexes, foreign keys, DDL) shows up in the
// command log exactly like a browsed page or an edited row does.
type querierAdapter struct {
	db     *sql.DB
	logger *Logger
}

func (q querierAdapter) QueryContext(ctx context.Context, query string, args ...any) (rowsScanner, error) {
	start := time.Now()
	rows, err := q.db.QueryContext(ctx, query, args...)
	q.logger.record(query, args, start, err)
	return rows, err
}

func (c *conn) q() (querier, error) {
	if c.db == nil {
		return nil, errNotConnected
	}
	return querierAdapter{c.db, c.logger}, nil
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

func (c *conn) ListTriggers(ctx context.Context, database string) ([]Trigger, error) {
	q, err := c.q()
	if err != nil {
		return nil, err
	}
	return c.dialect.listTriggers(ctx, q, database)
}

func (c *conn) TriggerDDL(ctx context.Context, database, trigger string) (string, error) {
	q, err := c.q()
	if err != nil {
		return "", err
	}
	return c.dialect.triggerDDL(ctx, q, database, trigger)
}

func (c *conn) TableStats(ctx context.Context, database string) ([]TableStat, error) {
	q, err := c.q()
	if err != nil {
		return nil, err
	}
	return c.dialect.tableStats(ctx, q, database)
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

func (c *conn) ListProcesses(ctx context.Context) ([]Process, error) {
	q, err := c.q()
	if err != nil {
		return nil, err
	}
	ps, err := c.dialect.listProcesses(ctx, q)
	if err != nil {
		return nil, err
	}
	// The order is part of what ListProcesses promises, and it is settled
	// here rather than in each dialect's ORDER BY: a NULL duration sorts
	// differently on every engine, and the tie-break has to be the same
	// one on all of them or a refresh would shuffle rows under the cursor.
	SortProcesses(ps)
	return ps, nil
}

// KillProcess ends one server session. The statement is built by the
// dialect from a validated decimal id and executed here — never staged,
// never batched — so the command log holds exactly one line per kill.
func (c *conn) KillProcess(ctx context.Context, id string) error {
	if c.db == nil {
		return errNotConnected
	}
	stmt, err := c.dialect.killProcessSQL(id)
	if err != nil {
		return err
	}
	// Killing a session is a write in every sense that matters, and
	// PostgreSQL spells it as a SELECT — which the statement classifier
	// would wave through. The guard is therefore explicit here rather
	// than left to ContainsWrite.
	if c.readOnly {
		return c.rejectWrite(stmt.SQL, nil)
	}
	start := time.Now()
	if !stmt.ReturnsRow {
		_, err := c.db.ExecContext(ctx, stmt.SQL)
		c.logger.record(stmt.SQL, nil, start, err)
		return err
	}
	rows, err := c.db.QueryContext(ctx, stmt.SQL)
	if err != nil {
		c.logger.record(stmt.SQL, nil, start, err)
		return err
	}
	// The single returned value says whether the signal was delivered; a
	// false is not an error from the driver's point of view, so it is
	// turned into one here.
	delivered := true
	if rows.Next() {
		var v any
		if err := rows.Scan(&v); err == nil {
			delivered = processBool(v)
		}
	}
	err = rows.Err()
	rows.Close()
	c.logger.record(stmt.SQL, nil, start, err)
	if err != nil {
		return err
	}
	if !delivered {
		return fmt.Errorf("db: the server did not terminate session %s — it is already gone, or this user may not signal it", id)
	}
	return nil
}

func (c *conn) Explain(ctx context.Context, sql string) (*Plan, error) {
	q, err := c.q()
	if err != nil {
		return nil, err
	}
	return c.dialect.explain(ctx, q, sql)
}

// qualifiedTable renders database.table with per-dialect quoting,
// omitting the database part when empty.
func qualifiedTable(d Dialect, database, table string) string {
	if database == "" {
		return d.QuoteIdent(table)
	}
	return d.QuoteIdent(database) + "." + d.QuoteIdent(table)
}

// FilterPrefixSQL renders the immutable part of the grid's inline filter
// input: the statement a typed WHERE clause completes. The relation is
// quoted by the dialect here rather than spelled by the UI, so the label
// the user types after is the same identifier PageSQL will run against.
func FilterPrefixSQL(d Dialect, database, table string) string {
	return "SELECT * FROM " + qualifiedTable(d, database, table) + " WHERE "
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
	// Exec is the write door: nothing that only reads is routed through
	// it, so a read-only session refuses it without classifying anything.
	if c.readOnly {
		return ExecResult{}, c.rejectWrite(query, args)
	}
	start := time.Now()
	res, err := c.db.ExecContext(ctx, query, args...)
	c.logger.record(query, args, start, err)
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
	if c.readOnly {
		return nil, c.rejectTx(stmts)
	}
	beginStart := time.Now()
	tx, err := c.db.BeginTx(ctx, nil)
	c.logger.record("BEGIN", nil, beginStart, err)
	if err != nil {
		return nil, err
	}
	out := make([]ExecResult, 0, len(stmts))
	for i, s := range stmts {
		start := time.Now()
		res, err := tx.ExecContext(ctx, s.SQL, s.Args...)
		c.logger.record(s.SQL, s.Args, start, err)
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
	commitStart := time.Now()
	err = tx.Commit()
	c.logger.record("COMMIT", nil, commitStart, err)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *conn) Query(ctx context.Context, query string, args ...any) (*ResultSet, error) {
	rs, _, err := c.QueryLimit(ctx, query, 0, args...)
	return rs, err
}

func (c *conn) QueryLimit(ctx context.Context, query string, max int, args ...any) (*ResultSet, bool, error) {
	if c.db == nil {
		return nil, false, errNotConnected
	}
	// Free-form SQL reaches the server through Query too — a data-modifying
	// CTE returns rows — so the read-only session classifies it here as
	// well and only lets reads through.
	if c.readOnly && ContainsWrite(c.Engine(), query) {
		return nil, false, c.rejectWrite(query, args)
	}
	start := time.Now()
	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		c.logger.record(query, args, start, err)
		return nil, false, err
	}
	defer rows.Close()
	rs, capped, err := scanResultSetLimit(ctx, rows, max)
	c.logger.record(query, args, start, err)
	return rs, capped, err
}

func (c *conn) QueryStream(ctx context.Context, query string, args []any, onRow func(cols []Column, row []any) error) error {
	if c.db == nil {
		return errNotConnected
	}
	if c.readOnly && ContainsWrite(c.Engine(), query) {
		return c.rejectWrite(query, args)
	}
	start := time.Now()
	rows, err := c.db.QueryContext(ctx, query, args...)
	c.logger.record(query, args, start, err)
	if err != nil {
		return err
	}
	defer rows.Close()
	return streamRows(ctx, rows, onRow)
}
