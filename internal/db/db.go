// Package db is the only place lazysql touches SQL databases. UI code
// depends on the Driver interface and the types in this file; concrete
// database/sql drivers are imported only by the dialect files in this
// package, never by UI packages.
package db

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Engine identifies a supported database engine.
type Engine string

const (
	EngineMySQL    Engine = "mysql"
	EngineMariaDB  Engine = "mariadb"
	EnginePostgres Engine = "postgres"
	EngineSQLite   Engine = "sqlite"
	EngineDuckDB   Engine = "duckdb"
)

// Column describes one column of a table or result set.
//
// Extra carries the engine's own note about how the value is produced —
// "auto_increment", "identity always", "autoincrement", "stored" — and
// is empty whenever the engine has nothing to say. It is display-only:
// no code branches on its contents.
type Column struct {
	Name       string
	DataType   string
	Nullable   bool
	Default    *string
	PrimaryKey bool
	Extra      string
}

// Index describes one index on a table.
type Index struct {
	Name    string
	Columns []string
	Unique  bool
	Primary bool
}

// ForeignKey describes one foreign key constraint. Columns and
// RefColumns are positionally paired: Columns[i] references
// RefColumns[i] of RefTable. RefTable is schema-qualified only when the
// referenced table lives outside the constrained table's namespace.
//
// OnUpdate/OnDelete are the referential actions spelled the SQL way
// ("CASCADE", "SET NULL", …); "NO ACTION" and the empty string both
// mean the engine's default.
type ForeignKey struct {
	Name       string
	Columns    []string
	RefTable   string
	RefColumns []string
	OnUpdate   string
	OnDelete   string
}

// RelationKind distinguishes the two kinds of listable relation. The UI
// shows them in separate sub-tabs of the [3] Tables panel.
type RelationKind int

const (
	RelationTable RelationKind = iota
	RelationView
)

// Relation is one entry of a namespace listing: a table or a view.
type Relation struct {
	Name string
	Kind RelationKind
}

// RelationNames extracts the names of a relation slice, keeping order.
func RelationNames(rels []Relation) []string {
	out := make([]string, 0, len(rels))
	for _, r := range rels {
		out = append(out, r.Name)
	}
	return out
}

// FilterRelations returns the names of the relations of one kind.
func FilterRelations(rels []Relation, kind RelationKind) []string {
	var out []string
	for _, r := range rels {
		if r.Kind == kind {
			out = append(out, r.Name)
		}
	}
	return out
}

// ResultSet is a fully materialized, driver-agnostic query result.
// Cell values are nil (SQL NULL), string, int64, float64, bool or
// time.Time — never driver-private types or aliased []byte.
type ResultSet struct {
	Columns []Column
	Rows    [][]any
}

// ExecResult reports the outcome of a statement that returns no rows.
type ExecResult struct {
	RowsAffected int64
	LastInsertID int64 // 0 when the engine does not report one
}

// Sort orders a paged table query by one column.
type Sort struct {
	Column string
	Desc   bool
}

// Filter is the WHERE fragment of a paged query. Expr is a SQL boolean
// expression written with the dialect's placeholders and Args holds the
// values they stand for, so user data never reaches the statement text.
//
// Verbatim marks a fragment lazysql could not take apart: Expr is then
// the user's own SQL, interpolated as typed. The UI warns about that in
// the command log. Raw is always what the user typed, for display.
type Filter struct {
	Expr     string
	Args     []any
	Verbatim bool
	Raw      string
}

// empty reports whether the filter contributes no WHERE clause. A nil
// filter is empty, which is why every method has a nil-safe receiver.
func (f *Filter) empty() bool {
	return f == nil || strings.TrimSpace(f.Expr) == ""
}

// FilterArgs returns the parameters of a possibly nil filter.
func FilterArgs(f *Filter) []any {
	if f == nil {
		return nil
	}
	return f.Args
}

// Driver is the single abstraction UI code uses for database access.
// Every method that touches the database takes a context and aborts
// when it is cancelled.
type Driver interface {
	// Connect opens and verifies a connection. It must be called before
	// any other method.
	Connect(ctx context.Context, dsn string) error
	Close() error

	Engine() Engine
	Dialect() Dialect

	// ListDatabases returns the browsable namespaces: databases for
	// MySQL/MariaDB, schemas of the connected database for PostgreSQL,
	// attached databases for SQLite and DuckDB.
	ListDatabases(ctx context.Context) ([]string, error)
	// ListTables returns the names of tables and views in the given
	// namespace. An empty database means the connection's
	// current/default one.
	ListTables(ctx context.Context, database string) ([]string, error)
	// ListRelations is ListTables with the table/view distinction kept.
	ListRelations(ctx context.Context, database string) ([]Relation, error)
	TableColumns(ctx context.Context, database, table string) ([]Column, error)
	TableIndexes(ctx context.Context, database, table string) ([]Index, error)
	TableForeignKeys(ctx context.Context, database, table string) ([]ForeignKey, error)
	TableDDL(ctx context.Context, database, table string) (string, error)

	// QueryPage reads one page of a table. filter and sortBy may be nil.
	// Identifiers are quoted per dialect and filter arguments travel as
	// query parameters.
	QueryPage(ctx context.Context, database, table string, filter *Filter, sortBy *Sort, limit, offset int) (*ResultSet, error)
	// CountRows returns how many rows the same table+filter matches. It
	// is a separate round trip so the page can render before it lands.
	CountRows(ctx context.Context, database, table string, filter *Filter) (int64, error)

	// Exec runs a statement with parameters (dialect placeholder style).
	Exec(ctx context.Context, query string, args ...any) (ExecResult, error)
	// ExecTx runs the statements in one transaction: the first error
	// rolls everything back, so either all of them applied or none did.
	ExecTx(ctx context.Context, stmts []Statement) ([]ExecResult, error)
	// Query runs an arbitrary SQL query with parameters.
	Query(ctx context.Context, query string, args ...any) (*ResultSet, error)
}

// Dialect captures everything engine-specific: identifier quoting,
// placeholder style, pagination syntax and introspection queries.
type Dialect interface {
	Engine() Engine
	// DisplayName is what the UI shows, e.g. "MariaDB".
	DisplayName() string
	// driverName is the database/sql registration name; unexported so
	// only this package can open concrete connections.
	driverName() string

	// QuoteIdent quotes a single identifier (never a dotted path).
	QuoteIdent(ident string) string
	// Placeholder returns the parameter marker for 1-based position n.
	Placeholder(n int) string
	// LimitOffset returns the pagination clause including leading space.
	LimitOffset(limit, offset int) string

	listDatabases(ctx context.Context, q querier) ([]string, error)
	// listRelations returns tables and views with their kind; the
	// name-only ListTables is derived from it.
	listRelations(ctx context.Context, q querier, database string) ([]Relation, error)
	tableColumns(ctx context.Context, q querier, database, table string) ([]Column, error)
	tableIndexes(ctx context.Context, q querier, database, table string) ([]Index, error)
	tableForeignKeys(ctx context.Context, q querier, database, table string) ([]ForeignKey, error)
	tableDDL(ctx context.Context, q querier, database, table string) (string, error)
}

// querier is the subset of *sql.DB the dialects need.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (rowsScanner, error)
}

// rowsScanner abstracts *sql.Rows for dialect introspection code.
type rowsScanner interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close() error
}

var dialects = map[Engine]Dialect{}

func register(d Dialect) {
	dialects[d.Engine()] = d
}

// Engines lists the registered engines in stable order.
func Engines() []Engine {
	out := make([]Engine, 0, len(dialects))
	for e := range dialects {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// DialectFor returns the dialect for an engine.
func DialectFor(engine Engine) (Dialect, error) {
	d, ok := dialects[engine]
	if !ok {
		return nil, fmt.Errorf("db: unknown engine %q", engine)
	}
	return d, nil
}

// Open creates an unconnected Driver for the engine. Call Connect on it.
func Open(engine Engine) (Driver, error) {
	d, err := DialectFor(engine)
	if err != nil {
		return nil, err
	}
	return &conn{dialect: d}, nil
}

// FormatValue renders a normalized cell value for display. NULL renders
// as the given null marker.
func FormatValue(v any, null string) string {
	switch x := v.(type) {
	case nil:
		return null
	case string:
		return x
	case time.Time:
		return x.Format(time.RFC3339)
	default:
		return fmt.Sprintf("%v", x)
	}
}
