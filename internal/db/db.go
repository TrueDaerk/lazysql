// Package db is the only place lazysql touches SQL databases. UI code
// depends on the Driver interface and the types in this file; concrete
// database/sql drivers are imported only by the dialect files in this
// package, never by UI packages.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"
)

// DialFunc replaces a driver's own TCP transport. lazysql uses it to send a
// connection through an SSH tunnel: the sshtunnel package supplies one and
// this package hands it to the concrete driver, which is the only place that
// knows how to accept one.
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

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
// shows them under separate categories of the [2] Objects tree.
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

// Trigger is one entry of a namespace's trigger listing. Table names the
// relation the trigger fires on; it is empty when the engine reports no
// owning relation.
type Trigger struct {
	Name  string
	Table string
}

// TriggerNames extracts the names of a trigger slice, keeping order.
func TriggerNames(trs []Trigger) []string {
	out := make([]string, 0, len(trs))
	for _, t := range trs {
		out = append(out, t.Name)
	}
	return out
}

// ErrUnsupported marks introspection an engine has no concept of at all —
// DuckDB has no triggers, so listing them is not a failure to report but
// a fact about the engine. UI code shows it as a note on the node rather
// than as an error, which is why it is a sentinel and not a plain error
// string.
var ErrUnsupported = errors.New("db: not supported by this engine")

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

	// ReadOnly reports that this session refuses every write: Exec and
	// ExecTx always fail with ErrReadOnly, and so does any statement
	// IsWrite classifies as a write. UI code asks it to disable its own
	// write entry points; the refusal itself does not depend on that.
	ReadOnly() bool

	// Logger is the ring buffer every statement this Driver runs lands
	// in, for the command log panel. Never nil.
	Logger() *Logger

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
	// ListTriggers returns the triggers defined in the given namespace,
	// ordered by name. An engine without triggers answers ErrUnsupported
	// rather than an empty list, so "none defined" and "no such concept"
	// stay distinguishable.
	ListTriggers(ctx context.Context, database string) ([]Trigger, error)
	// TriggerDDL returns the CREATE TRIGGER statement, as the engine
	// stores it where it stores one and synthesized from the catalog where
	// it does not.
	TriggerDDL(ctx context.Context, database, trigger string) (string, error)
	// TableStats returns one namespace's approximate table sizes in a
	// single query, read from the engine's own statistics — never a
	// COUNT(*) per table. Tables the engine has no figures for are
	// reported with StatUnknown rather than left out. Callers treat a
	// failure as "no annotation": the sizes are decoration, so an engine
	// or a permission that withholds them must not break browsing.
	TableStats(ctx context.Context, database string) ([]TableStat, error)
	TableColumns(ctx context.Context, database, table string) ([]Column, error)
	TableIndexes(ctx context.Context, database, table string) ([]Index, error)
	TableForeignKeys(ctx context.Context, database, table string) ([]ForeignKey, error)
	TableDDL(ctx context.Context, database, table string) (string, error)

	// ListProcesses returns the sessions the server currently has, longest
	// running first (see SortProcesses). An engine without a server —
	// SQLite, DuckDB — answers ErrUnsupported rather than an empty list,
	// so "nothing is running" and "no such concept" stay distinguishable.
	ListProcesses(ctx context.Context) ([]Process, error)
	// KillProcess ends one session. It is the one destructive operation
	// that is not staged, so the caller confirms it first; a read-only
	// session refuses it like any other write.
	KillProcess(ctx context.Context, id string) error

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
	// Explain returns the engine's query plan for one statement. It never
	// adds ANALYZE, so the statement is not executed and explaining a
	// write is safe. The Plan carries whichever rendering the dialect
	// produces; UI code asks it for lines rather than branching on the
	// engine.
	Explain(ctx context.Context, sql string) (*Plan, error)
	// QueryLimit is Query with a cap on how many rows it materializes;
	// max <= 0 means unlimited. The second result reports that the
	// server had more rows than the cap, so a capped result is never
	// mistaken for a complete one. Free-form queries from the editor go
	// through it: an unbounded `SELECT *` on a large table would
	// otherwise be read entirely into memory.
	QueryLimit(ctx context.Context, query string, max int, args ...any) (*ResultSet, bool, error)
	// QueryStream runs query exactly once and hands each row to onRow as
	// it arrives, with the same columns passed every call. Unlike
	// QueryLimit it never materializes a ResultSet at all, which is what
	// a query-result export needs: the statement cannot be safely
	// re-issued with a different LIMIT/OFFSET, so exporting it in full
	// means reading it once and streaming straight through.
	QueryStream(ctx context.Context, query string, args []any, onRow func(cols []Column, row []any) error) error
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
	// openDB opens the database/sql handle for a DSN. dial, when non-nil,
	// replaces the driver's transport; engines that are not reached over
	// the network reject one. The returned func undoes whatever per-handle
	// registration the driver needed and must run on Close.
	openDB(dsn string, dial DialFunc) (*sql.DB, func(), error)

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
	// listTriggers returns the namespace's triggers, or ErrUnsupported for
	// an engine that has none.
	listTriggers(ctx context.Context, q querier, database string) ([]Trigger, error)
	// triggerDDL returns one trigger's CREATE statement.
	triggerDDL(ctx context.Context, q querier, database, trigger string) (string, error)
	// tableStats returns the namespace's per-table row and size
	// estimates from the engine's statistics catalog.
	tableStats(ctx context.Context, q querier, database string) ([]TableStat, error)
	tableColumns(ctx context.Context, q querier, database, table string) ([]Column, error)
	tableIndexes(ctx context.Context, q querier, database, table string) ([]Index, error)
	tableForeignKeys(ctx context.Context, q querier, database, table string) ([]ForeignKey, error)
	tableDDL(ctx context.Context, q querier, database, table string) (string, error)
	// explain runs the dialect's EXPLAIN prefix over one statement and
	// shapes the answer into a Plan.
	explain(ctx context.Context, q querier, sql string) (*Plan, error)

	// listProcesses reads the server's session list, or ErrUnsupported
	// for an engine that has no server.
	listProcesses(ctx context.Context, q querier) ([]Process, error)
	// processListSQL is the statement listProcesses runs. It is separate
	// from listProcesses so the dialect SQL can be asserted without a
	// server to run it against — see activity_test.go.
	processListSQL() (string, error)
	// killProcessSQL is the statement that ends session id.
	killProcessSQL(id string) (KillStatement, error)
}

// querier is the subset of *sql.DB the dialects need.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (rowsScanner, error)
}

// rowsScanner abstracts *sql.Rows for dialect introspection code.
type rowsScanner interface {
	// Columns names the result's columns. Introspection knows them
	// ahead of time; EXPLAIN does not, so it reads them here.
	Columns() ([]string, error)
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

// Options are the per-session settings a Driver is opened with.
type Options struct {
	// Dial replaces the driver's own transport — an SSH tunnel's
	// DialContext. Passing one to a file-based engine fails at Connect.
	Dial DialFunc
	// ReadOnly refuses every write on the session. The DSN should carry
	// the engine's own read-only parameters too (ConnParams.ReadOnly),
	// but this flag is what enforces the mode.
	ReadOnly bool
	// Setup are statements run once at Connect, before the session is
	// handed to anyone. They are how a session is given something to
	// browse that the file itself does not carry — the DuckDB view over
	// a Parquet file (ParquetViewSQL) is the only one so far. They run
	// past the ReadOnly guard on purpose: they are lazysql's own SQL,
	// not the user's, and a read-only Parquet session could not be set
	// up at all otherwise. Each one is logged like every other
	// statement, and a failing one fails the connect.
	Setup []string
}

// Open creates an unconnected Driver for the engine. Call Connect on it.
func Open(engine Engine) (Driver, error) { return OpenOpts(engine, Options{}) }

// OpenWith is Open with a custom transport — an SSH tunnel's DialContext.
func OpenWith(engine Engine, dial DialFunc) (Driver, error) {
	return OpenOpts(engine, Options{Dial: dial})
}

// OpenOpts is Open with every session setting spelled out.
func OpenOpts(engine Engine, o Options) (Driver, error) {
	d, err := DialectFor(engine)
	if err != nil {
		return nil, err
	}
	return &conn{dialect: d, logger: NewLogger(), dial: o.Dial, readOnly: o.ReadOnly, setup: o.Setup}, nil
}

// Tunnelled reports whether an engine can be reached through an SSH tunnel.
// The file-based engines cannot, which is why the form hides their SSH
// section entirely.
func Tunnelled(engine Engine) bool { return !FileBased(engine) }

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
