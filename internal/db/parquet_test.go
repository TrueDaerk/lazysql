package db

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestParquetViewName(t *testing.T) {
	cases := map[string]string{
		"/tmp/sales.parquet":         "sales",
		"/tmp/sales-2024.parquet":    "sales_2024",
		"/tmp/2024.parquet":          "_2024",
		"/tmp/weird name!.parquet":   "weird_name",
		"/tmp/.parquet":              "data",
		"/tmp/123.parquet":           "_123",
		"/tmp/nyc taxi trips.pqt":    "nyc_taxi_trips",
		"/tmp/übermäßig.parquet":     "übermäßig",
		"/tmp/a/b/c/measurements.pq": "measurements",
	}
	for path, want := range cases {
		if got := ParquetViewName(path); got != want {
			t.Errorf("ParquetViewName(%q) = %q, want %q", path, got, want)
		}
	}
}

// A path is user data: it reaches the statement as a quoted literal, and
// the view name as a quoted identifier.
func TestParquetViewSQLQuotes(t *testing.T) {
	sql, err := ParquetViewSQL(`we"ird`, `/tmp/o'brien.parquet`)
	if err != nil {
		t.Fatal(err)
	}
	want := `CREATE VIEW "we""ird" AS SELECT * FROM read_parquet('/tmp/o''brien.parquet')`
	if sql != want {
		t.Errorf("ParquetViewSQL =\n%s\nwant\n%s", sql, want)
	}
}

// writeParquet exports one row to a Parquet file through DuckDB itself and
// returns its path — the fixture every Parquet test reads back.
func writeParquet(t *testing.T, name string) string {
	t.Helper()
	ctx := context.Background()
	drv := openTest(t, EngineDuckDB, "")
	path := filepath.Join(t.TempDir(), name)
	_, err := drv.Exec(ctx,
		`COPY (SELECT 1 AS id, 'ada' AS name UNION ALL SELECT 2, 'grace')
		 TO `+QuoteLiteral(drv.Dialect(), path)+` (FORMAT PARQUET)`)
	if err != nil {
		t.Fatalf("write parquet: %v", err)
	}
	return path
}

// The whole ephemeral Parquet story end to end: the file DuckDB writes is
// sniffed as Parquet, a read-only session set up with the view browses it,
// and a write through that session is refused rather than failing later.
func TestParquetSessionBrowsableAndReadOnly(t *testing.T) {
	ctx := context.Background()
	path := writeParquet(t, "sales.parquet")

	if format, err := SniffFile(path); err != nil || format != FormatParquet {
		t.Fatalf("SniffFile = (%q, %v), want parquet", format, err)
	}

	view := ParquetViewName(path)
	setup, err := ParquetViewSQL(view, path)
	if err != nil {
		t.Fatal(err)
	}
	drv, err := OpenOpts(EngineDuckDB, Options{ReadOnly: true, Setup: []string{setup}})
	if err != nil {
		t.Fatal(err)
	}
	defer drv.Close()
	// An in-memory DuckDB is the session: the DSN is empty, the file only
	// ever appears inside the view.
	if err := drv.Connect(ctx, ""); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	rels, err := drv.ListRelations(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := FilterRelations(rels, RelationView); !slices.Contains(got, view) {
		t.Fatalf("views = %v, want to contain %q", got, view)
	}

	rs, err := drv.QueryPage(ctx, "", view, nil, nil, 10, 0)
	if err != nil {
		t.Fatalf("QueryPage: %v", err)
	}
	if len(rs.Rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rs.Rows))
	}

	// Staged mutations never reach the file: the session refuses them.
	if _, err := drv.Exec(ctx, "DELETE FROM "+QuoteIdentifier(drv.Dialect(), view)); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("Exec error = %v, want ErrReadOnly", err)
	}

	// The setup statement is in the command log like every other one.
	var logged bool
	for _, e := range drv.Logger().Entries() {
		if strings.Contains(e.SQL, "read_parquet(") {
			logged = true
		}
	}
	if !logged {
		t.Error("the view-creating statement never reached the command log")
	}
}

// A setup statement that fails leaves no half-open session behind.
func TestSetupFailureFailsConnect(t *testing.T) {
	drv, err := OpenOpts(EngineDuckDB, Options{Setup: []string{"SELECT nonsense_function()"}})
	if err != nil {
		t.Fatal(err)
	}
	defer drv.Close()
	if err := drv.Connect(context.Background(), ""); err == nil {
		t.Fatal("expected Connect to fail on a failing setup statement")
	}
	if _, err := drv.ListDatabases(context.Background()); err == nil {
		t.Fatal("expected the failed session to be closed")
	}
}

// A real DuckDB database file is recognized by its own magic, not by its
// extension — the case `.db` alone could never answer.
func TestSniffRealDuckDBFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "warehouse.db")
	drv := openTest(t, EngineDuckDB, path)
	if _, err := drv.Exec(context.Background(), "CREATE TABLE t (id INTEGER)"); err != nil {
		t.Fatal(err)
	}
	drv.Close()
	if format, err := SniffFile(path); err != nil || format != FormatDuckDB {
		t.Fatalf("SniffFile = (%q, %v), want duckdb", format, err)
	}
}

// And so is a real SQLite database file.
func TestSniffRealSQLiteFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	drv := openTest(t, EngineSQLite, path)
	if _, err := drv.Exec(context.Background(), "CREATE TABLE t (id INTEGER)"); err != nil {
		t.Fatal(err)
	}
	drv.Close()
	if format, err := SniffFile(path); err != nil || format != FormatSQLite {
		t.Fatalf("SniffFile = (%q, %v), want sqlite", format, err)
	}
}
