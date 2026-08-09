package db

import (
	"context"
	"reflect"
	"testing"
)

func labels(phs []Placeholder) []string {
	var out []string
	for _, p := range phs {
		out = append(out, p.Label)
	}
	return out
}

func TestExtractPlaceholders(t *testing.T) {
	cases := []struct {
		engine Engine
		sql    string
		want   []string
	}{
		{EngineSQLite, "SELECT 1", nil},
		{EngineSQLite, "SELECT * FROM t WHERE id = ?", []string{"? (1)"}},
		{EngineSQLite, "SELECT * FROM t WHERE id = ? AND name = :n", []string{"? (1)", ":n"}},
		// A repeated name is one prompt, in order of first appearance.
		{EngineSQLite, "SELECT :a, :b, :a", []string{":a", ":b"}},
		{EngineSQLite, "SELECT ?, ?", []string{"? (1)", "? (2)"}},
		// Inside a string literal or a comment nothing is a placeholder.
		{EngineSQLite, "SELECT '?' -- :name", nil},
		{EngineMySQL, `SELECT "?", 'It''s :x' # :y`, nil},
		{EngineSQLite, "SELECT /* ? :a */ 1", nil},
		// A PostgreSQL cast is an operator, and `$1` is already-bound SQL,
		// not a hole to prompt for.
		{EnginePostgres, "SELECT a::text FROM t WHERE b = $1", nil},
		{EnginePostgres, "SELECT a::text FROM t WHERE b = :b", []string{":b"}},
		// MySQL user variables are server state, not parameters.
		{EngineMySQL, "SELECT @v, ?", []string{"? (1)"}},
	}
	for _, c := range cases {
		got := labels(ExtractPlaceholders(c.engine, c.sql))
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("ExtractPlaceholders(%s, %q) = %v, want %v", c.engine, c.sql, got, c.want)
		}
	}
}

func TestBindPlaceholdersRewritesPerDialect(t *testing.T) {
	sqlite, err := DialectFor(EngineSQLite)
	if err != nil {
		t.Fatal(err)
	}
	postgres, err := DialectFor(EnginePostgres)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		d        Dialect
		engine   Engine
		sql      string
		values   []string
		wantSQL  string
		wantArgs []any
	}{
		{sqlite, EngineSQLite,
			"SELECT * FROM t WHERE id = ? AND name = :n",
			[]string{"7", "bob"},
			"SELECT * FROM t WHERE id = ? AND name = ?",
			[]any{"7", "bob"}},
		{postgres, EnginePostgres,
			"SELECT * FROM t WHERE id = ? AND name = :n",
			[]string{"7", "bob"},
			"SELECT * FROM t WHERE id = $1 AND name = $2",
			[]any{"7", "bob"}},
		// A repeated name binds its one value at every position.
		{sqlite, EngineSQLite,
			"SELECT :a, :b, :a",
			[]string{"x", "y"},
			"SELECT ?, ?, ?",
			[]any{"x", "y", "x"}},
		// String literals, comments and casts pass through untouched.
		{postgres, EnginePostgres,
			"SELECT a::text, '?' FROM t WHERE b = :b -- :c",
			[]string{"v"},
			"SELECT a::text, '?' FROM t WHERE b = $1 -- :c",
			[]any{"v"}},
	}
	for _, c := range cases {
		gotSQL, gotArgs, err := BindPlaceholders(c.d, c.engine, c.sql, c.values)
		if err != nil {
			t.Errorf("BindPlaceholders(%q): %v", c.sql, err)
			continue
		}
		if gotSQL != c.wantSQL || !reflect.DeepEqual(gotArgs, c.wantArgs) {
			t.Errorf("BindPlaceholders(%q) = %q %v, want %q %v",
				c.sql, gotSQL, gotArgs, c.wantSQL, c.wantArgs)
		}
	}

	if _, _, err := BindPlaceholders(sqlite, EngineSQLite, "SELECT ?", nil); err == nil {
		t.Error("BindPlaceholders accepted a missing value")
	}
}

// A hostile value stays data: bound as a parameter it cannot break out of
// the statement, whatever it spells.
func TestBoundPlaceholderValueIsNotInterpolated(t *testing.T) {
	drv, err := Open(EngineSQLite)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := drv.Connect(ctx, ":memory:"); err != nil {
		t.Fatal(err)
	}
	defer drv.Close()
	if _, err := drv.Exec(ctx, `CREATE TABLE t (id INTEGER, name TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := drv.Exec(ctx, `INSERT INTO t VALUES (1, 'alice')`); err != nil {
		t.Fatal(err)
	}

	sqlite, err := DialectFor(EngineSQLite)
	if err != nil {
		t.Fatal(err)
	}
	bound, args, err := BindPlaceholders(sqlite, EngineSQLite,
		"SELECT * FROM t WHERE id = ? AND name = :n",
		[]string{"1", "'; DROP TABLE t;--"})
	if err != nil {
		t.Fatal(err)
	}
	rs, err := drv.Query(ctx, bound, args...)
	if err != nil {
		t.Fatalf("the bound value broke the statement: %v", err)
	}
	if len(rs.Rows) != 0 {
		t.Fatalf("rows = %v, want none for a name that is an injection string", rs.Rows)
	}
	// The table survived: the value never reached the statement text.
	if _, err := drv.Query(ctx, "SELECT COUNT(*) FROM t"); err != nil {
		t.Fatalf("table t is gone: %v", err)
	}
}
