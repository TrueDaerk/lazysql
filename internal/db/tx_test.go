package db

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

// txEngines are the in-process engines the interactive transaction is
// tested against. SQLite gets a file, not the default temporary database:
// that one is private to each pooled connection, and the whole point of
// these tests is what a second connection sees.
func txEngines(t *testing.T) map[Engine]string {
	return map[Engine]string{
		EngineSQLite: filepath.Join(t.TempDir(), "tx.db"),
		EngineDuckDB: "",
	}
}

func openTxFixture(t *testing.T, engine Engine, dsn string) Driver {
	t.Helper()
	drv := openTest(t, engine, dsn)
	if _, err := drv.Exec(context.Background(),
		"CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	return drv
}

// countTxItems reads through the pool — the way the grid does — never
// through the transaction.
func countTxItems(t *testing.T, drv Driver) int64 {
	t.Helper()
	rs, err := drv.Query(context.Background(), "SELECT COUNT(*) FROM items")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	switch n := rs.Rows[0][0].(type) {
	case int64:
		return n
	default:
		t.Fatalf("count of type %T", n)
	}
	return 0
}

func TestTxCommitPersists(t *testing.T) {
	for engine, dsn := range txEngines(t) {
		t.Run(string(engine), func(t *testing.T) {
			ctx := context.Background()
			drv := openTxFixture(t, engine, dsn)
			tx, err := drv.Begin(ctx)
			if err != nil {
				t.Fatalf("Begin: %v", err)
			}
			if _, err := tx.Exec(ctx, "INSERT INTO items VALUES (1, 'a')"); err != nil {
				t.Fatalf("insert: %v", err)
			}
			// The transaction sees its own write…
			rs, _, err := tx.QueryLimit(ctx, "SELECT COUNT(*) FROM items", 0)
			if err != nil || rs.Rows[0][0] != int64(1) {
				t.Fatalf("in-tx count = %v, %v; want 1", rs, err)
			}
			// …and the pool does not: the grid never joins it.
			if n := countTxItems(t, drv); n != 0 {
				t.Fatalf("pool saw %d uncommitted rows", n)
			}
			if got := tx.Statements(); got != 2 {
				t.Errorf("Statements = %d, want 2", got)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatalf("Commit: %v", err)
			}
			if tx.State() != TxClosed {
				t.Errorf("state after commit = %v", tx.State())
			}
			if n := countTxItems(t, drv); n != 1 {
				t.Fatalf("count after commit = %d, want 1", n)
			}
			// The handle is spent, and the session can open another.
			if _, err := tx.Exec(ctx, "INSERT INTO items VALUES (2, 'b')"); !errors.Is(err, ErrTxClosed) {
				t.Errorf("Exec after commit = %v, want ErrTxClosed", err)
			}
			tx2, err := drv.Begin(ctx)
			if err != nil {
				t.Fatalf("second Begin: %v", err)
			}
			tx2.Rollback(ctx)
		})
	}
}

func TestTxRollbackDiscards(t *testing.T) {
	for engine, dsn := range txEngines(t) {
		t.Run(string(engine), func(t *testing.T) {
			ctx := context.Background()
			drv := openTxFixture(t, engine, dsn)
			tx, err := drv.Begin(ctx)
			if err != nil {
				t.Fatalf("Begin: %v", err)
			}
			if _, err := tx.Exec(ctx, "INSERT INTO items VALUES (1, 'a')"); err != nil {
				t.Fatalf("insert: %v", err)
			}
			if err := tx.Rollback(ctx); err != nil {
				t.Fatalf("Rollback: %v", err)
			}
			if n := countTxItems(t, drv); n != 0 {
				t.Fatalf("count after rollback = %d, want 0", n)
			}
			if err := tx.Commit(ctx); !errors.Is(err, ErrTxClosed) {
				t.Errorf("Commit after rollback = %v, want ErrTxClosed", err)
			}
		})
	}
}

// TestTxErrorMidTransaction is the dialect difference the state exists
// for: SQLite undoes the failed statement alone and keeps the transaction,
// DuckDB aborts it — and would then answer COMMIT with success while
// rolling everything back, which is why Commit must refuse.
func TestTxErrorMidTransaction(t *testing.T) {
	want := map[Engine]TxState{EngineSQLite: TxActive, EngineDuckDB: TxAborted}
	for engine, dsn := range txEngines(t) {
		t.Run(string(engine), func(t *testing.T) {
			ctx := context.Background()
			drv := openTxFixture(t, engine, dsn)
			tx, err := drv.Begin(ctx)
			if err != nil {
				t.Fatalf("Begin: %v", err)
			}
			if _, err := tx.Exec(ctx, "INSERT INTO items VALUES (1, 'a')"); err != nil {
				t.Fatalf("insert: %v", err)
			}
			if _, err := tx.Exec(ctx, "INSERT INTO items VALUES (1, 'dup')"); err == nil {
				t.Fatal("duplicate key did not fail")
			}
			if got := tx.State(); got != want[engine] {
				t.Fatalf("state after the error = %v, want %v", got, want[engine])
			}
			if tx.State() == TxAborted {
				if _, err := tx.Exec(ctx, "INSERT INTO items VALUES (2, 'b')"); !errors.Is(err, ErrTxAborted) {
					t.Errorf("Exec in aborted tx = %v, want ErrTxAborted", err)
				}
				if err := tx.Commit(ctx); !errors.Is(err, ErrTxAborted) {
					t.Fatalf("Commit of aborted tx = %v, want ErrTxAborted", err)
				}
				if tx.State() != TxAborted {
					t.Fatalf("a refused commit changed the state to %v", tx.State())
				}
				if err := tx.Rollback(ctx); err != nil {
					t.Fatalf("Rollback: %v", err)
				}
				if n := countTxItems(t, drv); n != 0 {
					t.Fatalf("count = %d, want 0", n)
				}
				return
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatalf("Commit: %v", err)
			}
			if n := countTxItems(t, drv); n != 1 {
				t.Fatalf("count = %d, want 1", n)
			}
		})
	}
}

// TestTxBindErrorDoesNotAbortDuckDB: DuckDB only aborts on errors raised
// while executing; an unknown table fails at bind time and leaves the
// transaction usable. The probe tells the two apart.
func TestTxBindErrorDoesNotAbortDuckDB(t *testing.T) {
	ctx := context.Background()
	drv := openTxFixture(t, EngineDuckDB, "")
	tx, err := drv.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, _, err := tx.QueryLimit(ctx, "SELECT * FROM nosuch", 0); err == nil {
		t.Fatal("unknown table did not fail")
	}
	if tx.State() != TxActive {
		t.Fatalf("state = %v, want open", tx.State())
	}
	if _, err := tx.Exec(ctx, "INSERT INTO items VALUES (1, 'a')"); err != nil {
		t.Fatalf("insert after bind error: %v", err)
	}
}

func TestTxSQLiteSavepointKeepsTransaction(t *testing.T) {
	ctx := context.Background()
	drv := openTxFixture(t, EngineSQLite, filepath.Join(t.TempDir(), "sp.db"))
	tx, err := drv.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	for _, s := range []string{
		"INSERT INTO items VALUES (1, 'kept')",
		"SAVEPOINT sp",
		"INSERT INTO items VALUES (2, 'undone')",
		"ROLLBACK TO SAVEPOINT sp",
	} {
		if _, err := tx.Exec(ctx, s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	if tx.State() != TxActive {
		t.Fatalf("state after ROLLBACK TO = %v, want open", tx.State())
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if n := countTxItems(t, drv); n != 1 {
		t.Fatalf("count = %d, want 1", n)
	}
}

func TestTxRefusesControlStatements(t *testing.T) {
	ctx := context.Background()
	drv := openTxFixture(t, EngineSQLite, filepath.Join(t.TempDir(), "ctl.db"))
	tx, err := drv.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer tx.Rollback(ctx)
	for _, s := range []string{"COMMIT", "rollback", "BEGIN", "END", "/* c */ COMMIT"} {
		if _, err := tx.Exec(ctx, s); !errors.Is(err, ErrTxControl) {
			t.Errorf("Exec(%q) = %v, want ErrTxControl", s, err)
		}
	}
	if tx.State() != TxActive {
		t.Fatalf("state = %v", tx.State())
	}
}

func TestTxOnePerSession(t *testing.T) {
	ctx := context.Background()
	drv := openTxFixture(t, EngineDuckDB, "")
	tx, err := drv.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := drv.Begin(ctx); !errors.Is(err, ErrTxOpen) {
		t.Fatalf("second Begin = %v, want ErrTxOpen", err)
	}
}

func TestTxReadOnlyRefusesBegin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ro.db")
	openTxFixture(t, EngineSQLite, path)
	drv, err := OpenOpts(EngineSQLite, Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := drv.Connect(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	defer drv.Close()
	if _, err := drv.Begin(context.Background()); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("Begin on read-only = %v, want ErrReadOnly", err)
	}
	entries := drv.Logger().Entries()
	if len(entries) == 0 || entries[len(entries)-1].SQL != rejectedPrefix+"BEGIN" {
		t.Errorf("rejected BEGIN not logged: %+v", entries)
	}
}

func TestTxCloseRollsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "close.db")
	ctx := context.Background()
	drv := openTxFixture(t, EngineSQLite, path)
	tx, err := drv.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO items VALUES (1, 'a')"); err != nil {
		t.Fatal(err)
	}
	if err := drv.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if tx.State() != TxClosed {
		t.Errorf("state after Close = %v", tx.State())
	}
	if last := drv.Logger().Entries(); last[len(last)-1].SQL != "ROLLBACK" {
		t.Errorf("last logged = %q, want ROLLBACK", last[len(last)-1].SQL)
	}
	again := openTest(t, EngineSQLite, path)
	if n := countTxItems(t, again); n != 0 {
		t.Fatalf("count after close = %d, want 0", n)
	}
}

func TestTxStatementsAreLoggedInTx(t *testing.T) {
	ctx := context.Background()
	drv := openTxFixture(t, EngineDuckDB, "")
	tx, err := drv.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tx.Exec(ctx, "INSERT INTO items VALUES (1, 'a')")
	tx.Commit(ctx)
	var got []string
	for _, e := range drv.Logger().Entries() {
		if e.InTx {
			got = append(got, e.SQL)
		}
	}
	want := []string{"BEGIN TRANSACTION", "INSERT INTO items VALUES (1, 'a')", "COMMIT"}
	if len(got) != len(want) {
		t.Fatalf("in-tx log = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("in-tx log = %q, want %q", got, want)
		}
	}
}

func TestIsTxControl(t *testing.T) {
	cases := []struct {
		engine Engine
		sql    string
		want   bool
	}{
		{EnginePostgres, "BEGIN", true},
		{EnginePostgres, "begin isolation level serializable", true},
		{EngineMySQL, "START TRANSACTION", true},
		{EnginePostgres, "COMMIT", true},
		{EnginePostgres, "END", true},
		{EnginePostgres, "ABORT", true},
		{EnginePostgres, "ROLLBACK", true},
		{EnginePostgres, "ROLLBACK TO SAVEPOINT a", false},
		{EngineMySQL, "ROLLBACK WORK TO a", false},
		{EnginePostgres, "SAVEPOINT a", false},
		{EnginePostgres, "RELEASE SAVEPOINT a", false},
		{EngineMySQL, "SET autocommit = 1", true},
		{EngineMySQL, "SET @@session.autocommit = 1", true},
		{EnginePostgres, "SET search_path = x", false},
		{EnginePostgres, "SELECT 'commit'", false},
		{EnginePostgres, "-- BEGIN\nSELECT 1", false},
	}
	for _, c := range cases {
		if got := IsTxControl(c.engine, c.sql); got != c.want {
			t.Errorf("IsTxControl(%s, %q) = %v, want %v", c.engine, c.sql, got, c.want)
		}
	}
}

func TestCommitsImplicitly(t *testing.T) {
	cases := []struct {
		engine Engine
		sql    string
		want   bool
	}{
		{EngineMySQL, "CREATE TABLE t (id int)", true},
		{EngineMariaDB, "alter table t add c int", true},
		{EngineMySQL, "TRUNCATE t", true},
		{EngineMySQL, "CREATE TEMPORARY TABLE t (id int)", false},
		{EngineMySQL, "DROP TEMPORARY TABLE t", false},
		{EngineMySQL, "INSERT INTO t VALUES (1)", false},
		{EnginePostgres, "CREATE TABLE t (id int)", false},
		{EngineSQLite, "DROP TABLE t", false},
	}
	for _, c := range cases {
		if got := CommitsImplicitly(c.engine, c.sql); got != c.want {
			t.Errorf("CommitsImplicitly(%s, %q) = %v, want %v", c.engine, c.sql, got, c.want)
		}
	}
}
