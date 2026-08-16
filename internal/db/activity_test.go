package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// ---------- the dialect SQL ----------

// The process-list queries cannot be run here — there is no MySQL or
// PostgreSQL in the test suite — so what is asserted is the contract
// scanProcesses depends on: the catalog each engine reads, and the nine
// columns in the documented order.
func TestProcessListSQLPerDialect(t *testing.T) {
	tests := []struct {
		engine Engine
		want   []string
	}{
		{EngineMySQL, []string{"information_schema.processlist", "CONNECTION_ID()", "p.info"}},
		{EngineMariaDB, []string{"information_schema.processlist", "CONNECTION_ID()", "p.info"}},
		{EnginePostgres, []string{
			"pg_catalog.pg_stat_activity",
			"pg_catalog.pg_blocking_pids(a.pid)",
			"pg_catalog.pg_backend_pid()",
			"backend_type",
		}},
	}
	for _, tt := range tests {
		d, err := DialectFor(tt.engine)
		if err != nil {
			t.Fatal(err)
		}
		sql, err := ProcessListSQL(d)
		if err != nil {
			t.Fatalf("%s: ProcessListSQL: %v", tt.engine, err)
		}
		for _, want := range tt.want {
			if !strings.Contains(sql, want) {
				t.Errorf("%s process list is missing %q:\n%s", tt.engine, want, sql)
			}
		}
		if n := strings.Count(sql, ","); n < 8 {
			t.Errorf("%s process list has fewer than the nine contract columns:\n%s", tt.engine, sql)
		}
		// The listing must never change anything: it is run on read-only
		// connections too.
		if IsWrite(tt.engine, sql) {
			t.Errorf("%s process list classifies as a write:\n%s", tt.engine, sql)
		}
	}
}

// An idle session has no runtime to sort by, and both server engines
// have to spell that as NULL rather than as "0 seconds".
func TestProcessListSQLDropsIdleDuration(t *testing.T) {
	for _, e := range []Engine{EngineMySQL, EngineMariaDB} {
		d, _ := DialectFor(e)
		sql, _ := ProcessListSQL(d)
		if !strings.Contains(sql, "IF(p.command = 'Sleep', NULL, p.time)") {
			t.Errorf("%s does not drop a sleeping session's duration:\n%s", e, sql)
		}
	}
	d, _ := DialectFor(EnginePostgres)
	sql, _ := ProcessListSQL(d)
	if !strings.Contains(sql, "WHEN a.state = 'idle' THEN NULL") {
		t.Errorf("postgres does not drop an idle backend's duration:\n%s", sql)
	}
}

func TestProcessListSQLUnsupported(t *testing.T) {
	for _, e := range []Engine{EngineSQLite, EngineDuckDB} {
		d, _ := DialectFor(e)
		if _, err := ProcessListSQL(d); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s ProcessListSQL = %v, want ErrUnsupported", e, err)
		}
		if _, err := KillProcessSQL(d, "1"); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s KillProcessSQL = %v, want ErrUnsupported", e, err)
		}
	}
}

func TestMySQLLockWaitCatalogPerEngine(t *testing.T) {
	// MySQL 8 moved lock waits into performance_schema; MariaDB kept the
	// information_schema view it later removed.
	if got := mysqlLockWaitsSQL(EngineMySQL); !strings.Contains(got, "performance_schema.data_lock_waits") {
		t.Errorf("mysql lock waits =\n%s", got)
	}
	if got := mysqlLockWaitsSQL(EngineMariaDB); !strings.Contains(got, "information_schema.innodb_lock_waits") {
		t.Errorf("mariadb lock waits =\n%s", got)
	}
}

func TestKillProcessSQL(t *testing.T) {
	tests := []struct {
		engine     Engine
		id         string
		want       string
		returnsRow bool
	}{
		{EngineMySQL, "42", "KILL CONNECTION 42", false},
		{EngineMariaDB, " 42 ", "KILL CONNECTION 42", false},
		{EnginePostgres, "42", "SELECT pg_catalog.pg_terminate_backend(42)", true},
	}
	for _, tt := range tests {
		d, _ := DialectFor(tt.engine)
		stmt, err := KillProcessSQL(d, tt.id)
		if err != nil {
			t.Fatalf("%s: %v", tt.engine, err)
		}
		if stmt.SQL != tt.want {
			t.Errorf("%s kill = %q, want %q", tt.engine, stmt.SQL, tt.want)
		}
		if stmt.ReturnsRow != tt.returnsRow {
			t.Errorf("%s ReturnsRow = %v, want %v", tt.engine, stmt.ReturnsRow, tt.returnsRow)
		}
	}
}

// The session id is the only value that reaches a KILL statement, and it
// is rendered into it rather than bound — so anything that is not a
// decimal integer has to be refused outright.
func TestKillProcessSQLRejectsNonNumericIDs(t *testing.T) {
	bad := []string{"", "  ", "1; DROP TABLE users", "1 OR 1=1", "-1", "0x2a", "abc", "1.5"}
	for _, e := range []Engine{EngineMySQL, EngineMariaDB, EnginePostgres} {
		d, _ := DialectFor(e)
		for _, id := range bad {
			stmt, err := KillProcessSQL(d, id)
			if err == nil {
				t.Errorf("%s: KillProcessSQL(%q) built %q, want an error", e, id, stmt.SQL)
			}
		}
	}
}

// ---------- ordering ----------

func TestSortProcessesLongestRunningFirst(t *testing.T) {
	ps := []Process{
		{ID: "7"},
		{ID: "3", Duration: 2 * time.Second, HasDuration: true},
		{ID: "1", Duration: 90 * time.Second, HasDuration: true},
		{ID: "2"},
		{ID: "12", Duration: 2 * time.Second, HasDuration: true},
	}
	SortProcesses(ps)
	want := []string{"1", "3", "12", "2", "7"}
	for i, id := range want {
		if ps[i].ID != id {
			t.Fatalf("order = %v, want %v", ids(ps), want)
		}
	}
}

func ids(ps []Process) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.ID
	}
	return out
}

// ---------- the shared column contract ----------

// fakeRows plays a driver's result back through the rowsScanner the
// dialects read, so the nine-column contract can be tested with the
// value types a real driver hands over: MySQL's []byte, PostgreSQL's
// float64 and bool, and a NULL as an untyped nil.
type fakeRows struct {
	cols []string
	rows [][]any
	at   int
}

func (r *fakeRows) Columns() ([]string, error) { return r.cols, nil }
func (r *fakeRows) Next() bool                 { r.at++; return r.at <= len(r.rows) }
func (r *fakeRows) Err() error                 { return nil }
func (r *fakeRows) Close() error               { return nil }

func (r *fakeRows) Scan(dest ...any) error {
	row := r.rows[r.at-1]
	if len(dest) != len(row) {
		return fmt.Errorf("scan into %d columns, row has %d", len(dest), len(row))
	}
	for i, d := range dest {
		p, ok := d.(*any)
		if !ok {
			return fmt.Errorf("column %d: scanned into %T, want *any", i, d)
		}
		*p = row[i]
	}
	return nil
}

// fakeQuerier answers every query with the same rows and records what it
// was asked.
type fakeQuerier struct {
	rows    [][]any
	queries []string
}

func (q *fakeQuerier) QueryContext(_ context.Context, query string, _ ...any) (rowsScanner, error) {
	q.queries = append(q.queries, query)
	return &fakeRows{rows: q.rows}, nil
}

func TestScanProcesses(t *testing.T) {
	q := &fakeQuerier{rows: [][]any{
		// MySQL's shapes: []byte text, int64 duration, int64 boolean.
		{[]byte("11"), []byte("app"), []byte("shop"), []byte("10.0.0.4:51002"),
			[]byte("Query: Sending data"), int64(12), []byte("SELECT * FROM orders"), nil, int64(0)},
		// PostgreSQL's: strings, a fractional duration, a real boolean, and
		// two blockers.
		{"12", "app", "shop", "10.0.0.5", "active", 1.5, "UPDATE orders SET x = 1", "11,9", true},
		// An idle session: no duration at all, and a NULL database.
		{int64(13), "app", nil, nil, "idle", nil, "", "", false},
	}}
	ps, err := scanProcesses(context.Background(), q, "SELECT …")
	if err != nil {
		t.Fatalf("scanProcesses: %v", err)
	}
	if len(ps) != 3 {
		t.Fatalf("got %d processes, want 3", len(ps))
	}

	if ps[0].ID != "11" || ps[0].User != "app" || ps[0].Database != "shop" {
		t.Errorf("row 0 = %+v", ps[0])
	}
	if ps[0].Duration != 12*time.Second || !ps[0].HasDuration {
		t.Errorf("row 0 duration = %v (has=%v), want 12s", ps[0].Duration, ps[0].HasDuration)
	}
	if ps[0].Blocked() {
		t.Errorf("row 0 is marked blocked: %v", ps[0].BlockedBy)
	}
	if ps[0].Self {
		t.Error("row 0 is marked as lazysql's own session")
	}

	if ps[1].Duration != 1500*time.Millisecond {
		t.Errorf("row 1 duration = %v, want 1.5s", ps[1].Duration)
	}
	if !ps[1].Blocked() || ps[1].BlockedByText() != "11,9" {
		t.Errorf("row 1 blockers = %v, want [11 9]", ps[1].BlockedBy)
	}
	if !ps[1].Self {
		t.Error("row 1 is not marked as lazysql's own session")
	}

	if ps[2].HasDuration {
		t.Errorf("an idle session got a duration: %v", ps[2].Duration)
	}
	if ps[2].Database != "" {
		t.Errorf("row 2 database = %q, want empty for NULL", ps[2].Database)
	}
	if ps[2].Blocked() {
		t.Errorf("an empty blocker column made row 2 blocked: %v", ps[2].BlockedBy)
	}
}

func TestApplyBlockers(t *testing.T) {
	ps := []Process{{ID: "1"}, {ID: "2"}}
	applyBlockers(ps, map[string][]string{"2": {"1"}})
	if ps[0].Blocked() {
		t.Error("the blocker itself was marked blocked")
	}
	if !ps[1].Blocked() || ps[1].BlockedByText() != "1" {
		t.Errorf("waiter blockers = %v, want [1]", ps[1].BlockedBy)
	}
}

// mysqlDialect.listProcesses reads two catalogs and the second one is
// allowed to be missing: a server without the InnoDB lock views still
// answers with its process list.
func TestMySQLListProcessesSurvivesAMissingLockCatalog(t *testing.T) {
	q := &failingSecondQuerier{rows: [][]any{
		{int64(1), "app", "shop", "h", "Query", int64(3), "SELECT 1", nil, int64(0)},
	}}
	d := mysqlDialect{engine: EngineMySQL, name: "MySQL"}
	ps, err := d.listProcesses(context.Background(), q)
	if err != nil {
		t.Fatalf("listProcesses: %v", err)
	}
	if len(ps) != 1 {
		t.Fatalf("got %d processes, want 1", len(ps))
	}
	if len(q.queries) != 2 {
		t.Fatalf("ran %d queries, want the listing plus the lock waits", len(q.queries))
	}
}

// failingSecondQuerier answers the first query and fails every one after
// it — the shape of a server whose lock-wait catalog is not there.
type failingSecondQuerier struct {
	rows    [][]any
	queries []string
}

func (q *failingSecondQuerier) QueryContext(_ context.Context, query string, _ ...any) (rowsScanner, error) {
	q.queries = append(q.queries, query)
	if len(q.queries) > 1 {
		return nil, errors.New("Table 'performance_schema.data_lock_waits' doesn't exist")
	}
	return &fakeRows{rows: q.rows}, nil
}

// ---------- the file engines, against a live in-process engine ----------

func TestListProcessesUnsupportedOnFileEngines(t *testing.T) {
	ctx := context.Background()
	for _, e := range []Engine{EngineSQLite, EngineDuckDB} {
		drv := openTest(t, e, "")
		if _, err := drv.ListProcesses(ctx); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s ListProcesses = %v, want ErrUnsupported", e, err)
		}
		if err := drv.KillProcess(ctx, "1"); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s KillProcess = %v, want ErrUnsupported", e, err)
		}
	}
}

// A read-only session refuses to kill anything, and says so in the
// command log the way every other refused write does. PostgreSQL spells
// the kill as a SELECT, which the write classifier would wave through —
// so the guard is asserted on that dialect in particular.
//
// The handle is opened but never dialed: sql.Open does not connect, and
// the guard runs before the statement would.
func TestKillProcessRefusedOnAReadOnlySession(t *testing.T) {
	for _, e := range []Engine{EngineMySQL, EnginePostgres} {
		d, _ := DialectFor(e)
		sqlDB, release, err := d.openDB(unreachableDSN(e), nil)
		if err != nil {
			t.Fatalf("%s openDB: %v", e, err)
		}
		c := &conn{dialect: d, db: sqlDB, logger: NewLogger(), readOnly: true}
		err = c.KillProcess(context.Background(), "7")
		if !errors.Is(err, ErrReadOnly) {
			t.Errorf("%s KillProcess = %v, want ErrReadOnly", e, err)
		}
		var logged bool
		for _, entry := range c.Logger().Entries() {
			if strings.HasPrefix(entry.SQL, rejectedPrefix) {
				logged = true
			}
		}
		if !logged {
			t.Errorf("%s: the refused kill is missing from the command log", e)
		}
		sqlDB.Close()
		if release != nil {
			release()
		}
	}
}

// unreachableDSN names a port nothing listens on: the test must not
// depend on a server, and never opens a connection anyway.
func unreachableDSN(e Engine) string {
	if e == EnginePostgres {
		return "postgres://u:p@127.0.0.1:1/db"
	}
	return "u:p@tcp(127.0.0.1:1)/db"
}
