package db

import (
	"context"
	"testing"
)

// statOf finds one table's stat in a namespace listing.
func statOf(t *testing.T, stats []TableStat, table string) TableStat {
	t.Helper()
	for _, s := range stats {
		if s.Table == table {
			return s
		}
	}
	t.Fatalf("TableStats has no entry for %q: %+v", table, stats)
	return TableStat{}
}

// TestTableStatsSQLite exercises the ANALYZE-backed row estimate and the
// optional dbstat size, against the real engine.
func TestTableStatsSQLite(t *testing.T) {
	ctx := context.Background()
	drv := openTest(t, EngineSQLite, ":memory:")
	seed(t, drv)

	// Before ANALYZE there is no sqlite_stat1 at all: the listing must
	// still come back, just without row counts.
	stats, err := drv.TableStats(ctx, "")
	if err != nil {
		t.Fatalf("TableStats before ANALYZE: %v", err)
	}
	if len(stats) > 0 {
		if got := statOf(t, stats, "users").Rows; got != StatUnknown {
			t.Errorf("rows before ANALYZE = %d, want unknown", got)
		}
	}

	if _, err := drv.Exec(ctx, "ANALYZE"); err != nil {
		t.Fatalf("ANALYZE: %v", err)
	}
	stats, err = drv.TableStats(ctx, "")
	if err != nil {
		t.Fatalf("TableStats: %v", err)
	}
	users := statOf(t, stats, "users")
	if users.Rows != 3 {
		t.Errorf("users rows = %d, want 3", users.Rows)
	}
	// The view must not be reported: it has no storage.
	for _, s := range stats {
		if s.Table == "named_users" {
			t.Errorf("TableStats reports the view named_users: %+v", s)
		}
	}
	// dbstat is optional; when the build has it, a seeded table occupies
	// at least one page.
	if users.Bytes != StatUnknown && users.Bytes <= 0 {
		t.Errorf("users bytes = %d, want unknown or positive", users.Bytes)
	}
	t.Logf("sqlite users stat: %+v", users)
}

// TestTableStatsDuckDB checks the duckdb_tables() estimate; DuckDB
// reports no per-table size, so that half stays unknown.
func TestTableStatsDuckDB(t *testing.T) {
	ctx := context.Background()
	drv := openTest(t, EngineDuckDB, "")
	seed(t, drv)

	stats, err := drv.TableStats(ctx, "")
	if err != nil {
		t.Fatalf("TableStats: %v", err)
	}
	users := statOf(t, stats, "users")
	if users.Rows != 3 {
		t.Errorf("users rows = %d, want 3", users.Rows)
	}
	if users.Bytes != StatUnknown {
		t.Errorf("users bytes = %d, want unknown (DuckDB reports no size)", users.Bytes)
	}
	for _, s := range stats {
		if s.Table == "named_users" {
			t.Errorf("TableStats reports the view named_users: %+v", s)
		}
	}
}

// TestTableStatsUnconnected is the guard every Driver method shares: no
// connection is an error, not a panic.
func TestTableStatsUnconnected(t *testing.T) {
	drv, err := Open(EngineSQLite)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := drv.TableStats(context.Background(), ""); err == nil {
		t.Fatal("TableStats on an unconnected driver: want error")
	}
}

func TestStatInt(t *testing.T) {
	tests := []struct {
		in   any
		want int64
	}{
		{nil, StatUnknown},
		{int64(42), 42},
		{int32(7), 7},
		{uint64(9), 9},
		{float64(1234.7), 1234},
		{"1234", 1234},
		{[]byte("560"), 560},
		{"1.5e3", 1500},
		{"not a number", StatUnknown},
		{int64(-1), StatUnknown}, // PostgreSQL's "never analyzed"
		{struct{}{}, StatUnknown},
	}
	for _, tt := range tests {
		if got := statInt(tt.in); got != tt.want {
			t.Errorf("statInt(%#v) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

// TestSQLiteStatsSQLVariants pins the shape of the fallback chain: each
// variant is a valid statement naming only the sources it claims.
func TestSQLiteStatsSQLVariants(t *testing.T) {
	ctx := context.Background()
	drv := openTest(t, EngineSQLite, ":memory:")
	seed(t, drv)
	if _, err := drv.Exec(ctx, "ANALYZE"); err != nil {
		t.Fatalf("ANALYZE: %v", err)
	}
	// The stat1-only variant needs neither dbstat nor luck: it must run
	// on every build and answer the row count.
	rs, err := drv.Query(ctx, sqliteStatsSQL(`"main"`, true, false))
	if err != nil {
		t.Fatalf("stat1-only variant: %v", err)
	}
	found := false
	for _, row := range rs.Rows {
		if row[0] == "users" {
			found = true
			if got := statInt(row[1]); got != 3 {
				t.Errorf("users rows = %d, want 3", got)
			}
			if got := statInt(row[2]); got != StatUnknown {
				t.Errorf("users bytes = %d, want unknown without dbstat", got)
			}
		}
	}
	if !found {
		t.Fatalf("stat1-only variant returned no users row: %v", rs.Rows)
	}
}

func TestTableStatMap(t *testing.T) {
	m := TableStatMap([]TableStat{
		{Table: "users", Rows: 3, Bytes: 4096},
		{Table: "orders", Rows: StatUnknown, Bytes: StatUnknown},
	})
	if got := m["users"].Bytes; got != 4096 {
		t.Errorf("users bytes = %d, want 4096", got)
	}
	if m["orders"].Known() {
		t.Error("an all-unknown stat reports Known")
	}
	if !m["users"].Known() {
		t.Error("a filled stat reports not Known")
	}
}
