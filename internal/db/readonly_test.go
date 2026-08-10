package db

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// Classification is what the read-only guard rejects on, so its edge
// cases are the guard's edge cases: a write verb hiding in a CTE, one
// hiding in a comment or a literal (which is not a write at all), and the
// per-dialect quoting that decides which is which.
func TestIsWriteEdgeCases(t *testing.T) {
	cases := []struct {
		name   string
		engine Engine
		sql    string
		want   bool
	}{
		{"plain select", EngineSQLite, "SELECT * FROM users", false},
		{"lowercase select", EngineSQLite, "  select 1", false},
		{"reading CTE", EnginePostgres, "WITH x AS (SELECT 1) SELECT * FROM x", false},
		{"deleting CTE", EnginePostgres,
			"WITH gone AS (DELETE FROM t WHERE id = 1 RETURNING *) SELECT * FROM gone", true},
		{"updating CTE", EnginePostgres,
			"WITH bumped AS (\n  UPDATE t SET n = n + 1 RETURNING *\n) SELECT * FROM bumped", true},
		{"inserting CTE", EnginePostgres,
			"WITH added AS (INSERT INTO t VALUES (1) RETURNING *) SELECT * FROM added", true},
		{"verb only in a line comment", EnginePostgres,
			"WITH x AS (SELECT 1) -- DELETE FROM t\nSELECT * FROM x", false},
		{"verb only in a block comment", EnginePostgres,
			"WITH x AS (/* UPDATE t SET a = 1 */ SELECT 1) SELECT * FROM x", false},
		{"verb only in a string literal", EnginePostgres,
			"WITH x AS (SELECT 'delete from t' AS s) SELECT * FROM x", false},
		{"verb only in a quoted identifier", EnginePostgres,
			`WITH x AS (SELECT "delete" FROM t) SELECT * FROM x`, false},
		{"verb only in a backticked identifier", EngineMySQL,
			"WITH x AS (SELECT `delete` FROM t) SELECT * FROM x", false},
		{"leading comment before a write", EngineSQLite,
			"-- staged by hand\nDELETE FROM t WHERE id = 1", true},
		{"leading block comment before a read", EngineSQLite, "/* note */ SELECT 1", false},
		{"substring of a column name", EnginePostgres,
			"WITH x AS (SELECT deleted_at FROM t) SELECT * FROM x", false},
		{"insert", EngineSQLite, "INSERT INTO t VALUES (1)", true},
		{"ddl", EngineSQLite, "CREATE TABLE t (id INTEGER)", true},
		{"drop", EngineSQLite, "DROP TABLE t", true},
		{"vacuum", EngineSQLite, "VACUUM", true},
		{"pragma read", EngineSQLite, "PRAGMA table_info(t)", false},
		{"pragma write", EngineSQLite, "PRAGMA journal_mode = WAL", true},
		{"pragma with an = inside a literal", EngineSQLite, "PRAGMA table_info('a=b')", false},
		{"explain does not execute", EnginePostgres, "EXPLAIN DELETE FROM t", false},
		{"explain analyze does execute", EnginePostgres, "EXPLAIN ANALYZE DELETE FROM t", true},
		{"explain analyze of a select still executes", EnginePostgres,
			"explain analyze select * from t", true},
		{"comments only", EngineSQLite, "-- nothing here", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsWrite(c.engine, c.sql); got != c.want {
				t.Errorf("IsWrite(%s, %q) = %v, want %v", c.engine, c.sql, got, c.want)
			}
		})
	}
}

// A script is judged statement by statement: one write among reads is
// enough to stop the run, and the reads around it are not reported.
func TestWriteStatementsMultiStatement(t *testing.T) {
	script := `SELECT 1;
-- a comment with DELETE in it
SELECT 2;
UPDATE t SET a = 1 WHERE id = 2;
SELECT 3`
	stmts := SplitStatements(EngineSQLite, script)
	if len(stmts) != 4 {
		t.Fatalf("split into %d statements, want 4: %q", len(stmts), stmts)
	}
	writes := WriteStatements(EngineSQLite, stmts)
	if len(writes) != 1 || !strings.HasPrefix(writes[0], "UPDATE") {
		t.Fatalf("writes = %q, want only the UPDATE", writes)
	}
}

func TestWriteStatementsAllReads(t *testing.T) {
	stmts := SplitStatements(EngineSQLite, "SELECT 1; SELECT 2;")
	if got := WriteStatements(EngineSQLite, stmts); len(got) != 0 {
		t.Fatalf("writes = %q, want none", got)
	}
}

// openReadOnlyTest opens a read-only session over an existing SQLite
// file, going through BuildDSN so the engine-level `mode=ro` is part of
// what is exercised.
func openReadOnlyTest(t *testing.T, path string) Driver {
	t.Helper()
	dsn, err := BuildDSN(EngineSQLite, ConnParams{File: path, ReadOnly: true})
	if err != nil {
		t.Fatalf("BuildDSN: %v", err)
	}
	drv, err := OpenOpts(EngineSQLite, Options{ReadOnly: true})
	if err != nil {
		t.Fatalf("OpenOpts: %v", err)
	}
	if err := drv.Connect(context.Background(), dsn); err != nil {
		t.Fatalf("Connect(%s): %v", dsn, err)
	}
	t.Cleanup(func() { drv.Close() })
	return drv
}

// seedFile creates a SQLite file with one row in it and returns its path.
func seedFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ro.sqlite")
	drv := openTest(t, EngineSQLite, path)
	ctx := context.Background()
	for _, s := range []string{
		`CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT)`,
		`INSERT INTO t (id, name) VALUES (1, 'one')`,
	} {
		if _, err := drv.Exec(ctx, s); err != nil {
			t.Fatalf("seed %q: %v", s, err)
		}
	}
	if err := drv.Close(); err != nil {
		t.Fatalf("close the seeding connection: %v", err)
	}
	return path
}

func TestReadOnlySessionRefusesExec(t *testing.T) {
	drv := openReadOnlyTest(t, seedFile(t))
	if !drv.ReadOnly() {
		t.Fatal("ReadOnly() = false on a session opened read-only")
	}
	ctx := context.Background()

	if _, err := drv.Exec(ctx, `INSERT INTO t (id, name) VALUES (2, 'two')`); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("Exec error = %v, want ErrReadOnly", err)
	}
	// Reading is untouched, and the row count proves the INSERT never ran.
	rs, err := drv.Query(ctx, `SELECT COUNT(*) FROM t`)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if got := rs.Rows[0][0]; got != int64(1) {
		t.Fatalf("rows in t = %v, want the INSERT rejected", got)
	}
}

func TestReadOnlySessionRefusesExecTx(t *testing.T) {
	drv := openReadOnlyTest(t, seedFile(t))
	_, err := drv.ExecTx(context.Background(), []Statement{
		{SQL: `UPDATE t SET name = ? WHERE id = ?`, Args: []any{"renamed", 1}},
	})
	if !errors.Is(err, ErrReadOnly) {
		t.Fatalf("ExecTx error = %v, want ErrReadOnly", err)
	}
}

// A data-modifying CTE returns rows, so it arrives through Query rather
// than Exec. The guard classifies it there too.
func TestReadOnlySessionRefusesWritingQuery(t *testing.T) {
	drv := openReadOnlyTest(t, seedFile(t))
	ctx := context.Background()
	_, err := drv.Query(ctx, `WITH gone AS (DELETE FROM t WHERE id = 1 RETURNING *) SELECT * FROM gone`)
	if !errors.Is(err, ErrReadOnly) {
		t.Fatalf("Query error = %v, want ErrReadOnly", err)
	}
	_, _, err = drv.QueryLimit(ctx, `DELETE FROM t`, 10)
	if !errors.Is(err, ErrReadOnly) {
		t.Fatalf("QueryLimit error = %v, want ErrReadOnly", err)
	}
	err = drv.QueryStream(ctx, `INSERT INTO t VALUES (9, 'nine')`, nil,
		func([]Column, []any) error { return nil })
	if !errors.Is(err, ErrReadOnly) {
		t.Fatalf("QueryStream error = %v, want ErrReadOnly", err)
	}
}

// A query is judged whole: a write hiding behind a `;` after a harmless
// SELECT — the shape a free-text filter could smuggle in — is refused
// even though the leading keyword reads.
func TestReadOnlyRefusesASecondStatementInAQuery(t *testing.T) {
	drv := openReadOnlyTest(t, seedFile(t))
	_, err := drv.Query(context.Background(), `SELECT 1; DELETE FROM t`)
	if !errors.Is(err, ErrReadOnly) {
		t.Fatalf("Query error = %v, want ErrReadOnly", err)
	}
	// A `;` inside a literal is data, not a second statement.
	if _, err := drv.Query(context.Background(),
		`SELECT name FROM t WHERE name <> '; DELETE FROM t'`); err != nil {
		t.Fatalf("a semicolon inside a literal was read as a statement: %v", err)
	}
}

// Every blocked attempt is in the command log, marked as rejected, so the
// audit trail shows what was refused as well as what ran.
func TestReadOnlyRejectionIsLogged(t *testing.T) {
	drv := openReadOnlyTest(t, seedFile(t))
	ctx := context.Background()
	drv.Exec(ctx, `DELETE FROM t`)
	drv.ExecTx(ctx, []Statement{{SQL: `UPDATE t SET name = 'x'`}})

	entries := drv.Logger().Entries()
	if len(entries) != 2 {
		t.Fatalf("log entries = %d, want the two rejections", len(entries))
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.SQL, rejectedPrefix) {
			t.Errorf("log entry %q is not marked as rejected", e.SQL)
		}
		if !errors.Is(e.Err, ErrReadOnly) {
			t.Errorf("log entry error = %v, want ErrReadOnly", e.Err)
		}
	}
	if !strings.Contains(entries[0].SQL, "DELETE FROM t") {
		t.Errorf("rejected statement not in the log: %q", entries[0].SQL)
	}
}

// The engine's own read-only mode is the second lock: a DSN built with
// ReadOnly opens SQLite with `mode=ro`, so a write fails even when the
// session guard is not in the way.
func TestSQLiteEngineLevelReadOnly(t *testing.T) {
	path := seedFile(t)
	dsn, err := BuildDSN(EngineSQLite, ConnParams{File: path, ReadOnly: true})
	if err != nil {
		t.Fatalf("BuildDSN: %v", err)
	}
	if !strings.Contains(dsn, "mode=ro") || !strings.HasPrefix(dsn, "file:") {
		t.Fatalf("DSN = %q, want the file: URI form with mode=ro", dsn)
	}
	// Deliberately without Options{ReadOnly: true}: only the engine is
	// asked to refuse the write here.
	drv := openTest(t, EngineSQLite, dsn)
	_, err = drv.Exec(context.Background(), `INSERT INTO t (id, name) VALUES (2, 'two')`)
	if err == nil {
		t.Fatal("the engine accepted a write on a mode=ro connection")
	}
	if errors.Is(err, ErrReadOnly) {
		t.Fatalf("error came from the session guard, not the engine: %v", err)
	}
	// Reads still work through the same handle.
	if _, err := drv.Query(context.Background(), `SELECT id FROM t`); err != nil {
		t.Fatalf("read on a mode=ro connection: %v", err)
	}
}

// Each engine gets its own read-only parameter, and a profile option can
// never override it.
func TestReadOnlyDSNParams(t *testing.T) {
	cases := []struct {
		engine Engine
		params ConnParams
		want   string
	}{
		{EngineSQLite, ConnParams{File: "/tmp/a.db", ReadOnly: true}, "mode=ro"},
		{EngineDuckDB, ConnParams{File: "/tmp/a.duckdb", ReadOnly: true}, "access_mode=read_only"},
		{EnginePostgres, ConnParams{Host: "h", Database: "d", ReadOnly: true},
			"default_transaction_read_only=on"},
		{EngineMySQL, ConnParams{Host: "h", ReadOnly: true}, "transaction_read_only=1"},
		{EngineMariaDB, ConnParams{Host: "h", ReadOnly: true}, "transaction_read_only=1"},
		{EngineSQLite, ConnParams{File: "/tmp/a.db", ReadOnly: true,
			Options: map[string]string{"mode": "rwc"}}, "mode=ro"},
	}
	for _, c := range cases {
		dsn, err := BuildDSN(c.engine, c.params)
		if err != nil {
			t.Fatalf("BuildDSN(%s): %v", c.engine, err)
		}
		if !strings.Contains(dsn, c.want) {
			t.Errorf("BuildDSN(%s) = %q, want it to carry %q", c.engine, dsn, c.want)
		}
	}
}

// A read-write profile's DSN is byte-for-byte what it was before the flag
// existed, and the profile's own option map is never written into.
func TestReadOnlyLeavesReadWriteDSNAlone(t *testing.T) {
	opts := map[string]string{"sslmode": "disable"}
	p := ConnParams{Host: "h", Database: "d", Options: opts}
	rw, err := BuildDSN(EnginePostgres, p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rw, "read_only") {
		t.Fatalf("read-write DSN = %q, want no read-only parameter", rw)
	}
	p.ReadOnly = true
	if _, err := BuildDSN(EnginePostgres, p); err != nil {
		t.Fatal(err)
	}
	if len(opts) != 1 {
		t.Fatalf("the profile's option map was mutated: %v", opts)
	}
}

// DuckDB cannot open an in-memory database read-only, so the flag is left
// off there rather than making the connection fail.
func TestDuckDBInMemorySkipsReadOnlyParam(t *testing.T) {
	dsn, err := BuildDSN(EngineDuckDB, ConnParams{File: "", ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dsn, "access_mode") {
		t.Fatalf("DSN = %q, want no access_mode for an in-memory database", dsn)
	}
}
