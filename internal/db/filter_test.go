package db

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"
)

func TestParseFilterBindsSimpleComparisons(t *testing.T) {
	d, _ := DialectFor(EngineSQLite)
	cases := []struct {
		in       string
		wantExpr string
		wantArgs []any
	}{
		{`id = 1`, `"id" = ?`, []any{int64(1)}},
		{`id>=10`, `"id" >= ?`, []any{int64(10)}},
		{`price < 9.5`, `"price" < ?`, []any{9.5}},
		{`name = 'bob'`, `"name" = ?`, []any{"bob"}},
		{`name LIKE 'a%'`, `"name" LIKE ?`, []any{"a%"}},
		{`name not   like 'a%'`, `"name" NOT LIKE ?`, []any{"a%"}},
		{`active = true`, `"active" = ?`, []any{true}},
		{`email IS NULL`, `"email" IS NULL`, nil},
		{`email is not null`, `"email" IS NOT NULL`, nil},
		{`WHERE id <> 3`, `"id" <> ?`, []any{int64(3)}},
		{`id > 5 AND name = 'bob'`, `"id" > ? AND "name" = ?`, []any{int64(5), "bob"}},
		{`"odd col" = 'x'`, `"odd col" = ?`, []any{"x"}},
		{"`odd col` = 'x'", `"odd col" = ?`, []any{"x"}},
		// A quote closed by doubling stays one literal.
		{`name = 'O''Hara'`, `"name" = ?`, []any{"O'Hara"}},
		// AND inside a literal is data, not a separator.
		{`name = 'a and b'`, `"name" = ?`, []any{"a and b"}},
	}
	for _, c := range cases {
		got := ParseFilter(d, c.in)
		if got == nil {
			t.Errorf("ParseFilter(%q) = nil", c.in)
			continue
		}
		if got.Verbatim {
			t.Errorf("ParseFilter(%q) fell back to verbatim", c.in)
			continue
		}
		if got.Expr != c.wantExpr {
			t.Errorf("ParseFilter(%q).Expr = %q, want %q", c.in, got.Expr, c.wantExpr)
		}
		if !slices.Equal(got.Args, c.wantArgs) {
			t.Errorf("ParseFilter(%q).Args = %v, want %v", c.in, got.Args, c.wantArgs)
		}
		if got.Raw != c.in {
			t.Errorf("ParseFilter(%q).Raw = %q", c.in, got.Raw)
		}
	}
}

func TestParseFilterNumbersPostgresPlaceholders(t *testing.T) {
	d, _ := DialectFor(EnginePostgres)
	f := ParseFilter(d, `id > 5 AND name = 'bob'`)
	if want := `"id" > $1 AND "name" = $2`; f.Expr != want {
		t.Fatalf("Expr = %q, want %q", f.Expr, want)
	}
}

// Anything the parser does not fully recognise must survive as the user
// typed it and be flagged, never silently dropped or half-rewritten.
func TestParseFilterFallsBackToVerbatim(t *testing.T) {
	d, _ := DialectFor(EngineSQLite)
	for _, in := range []string{
		`id IN (1,2,3)`,
		`id = 1 OR id = 2`,
		`lower(name) = 'bob'`,
		`id = other_column`,
		`name = 'unterminated`,
		`id = 1; DROP TABLE users`,
		`id = NULL`,
		`id BETWEEN 1 AND 5`,
	} {
		f := ParseFilter(d, in)
		if f == nil {
			t.Errorf("ParseFilter(%q) = nil, want a verbatim filter", in)
			continue
		}
		if !f.Verbatim {
			t.Errorf("ParseFilter(%q) claimed to be parameterized: %+v", in, f)
		}
		if f.Expr != in {
			t.Errorf("ParseFilter(%q).Expr = %q, want it unchanged", in, f.Expr)
		}
	}
}

func TestParseFilterEmpty(t *testing.T) {
	d, _ := DialectFor(EngineSQLite)
	for _, in := range []string{"", "   ", "\t"} {
		if f := ParseFilter(d, in); f != nil {
			t.Errorf("ParseFilter(%q) = %+v, want nil", in, f)
		}
	}
}

func TestPageSQL(t *testing.T) {
	d, _ := DialectFor(EngineSQLite)
	f := ParseFilter(d, "id > 5")
	got := PageSQL(d, "", "users", f, &Sort{Column: "name", Desc: true}, 100, 200)
	want := `SELECT * FROM "users" WHERE "id" > ? ORDER BY "name" DESC LIMIT 100 OFFSET 200`
	if got != want {
		t.Errorf("PageSQL = %q, want %q", got, want)
	}
	if got := CountSQL(d, "app", "users", f); got != `SELECT COUNT(*) FROM "app"."users" WHERE "id" > ?` {
		t.Errorf("CountSQL = %q", got)
	}
	if got := PageSQL(d, "", "users", nil, nil, 10, 0); got != `SELECT * FROM "users" LIMIT 10 OFFSET 0` {
		t.Errorf("unfiltered PageSQL = %q", got)
	}
}

// Browsing a big table must cost one page, not one table scan into
// memory: the page query is bounded by LIMIT and stays fast at any
// offset.
func TestQueryPageOnLargeTableFetchesOnlyThePage(t *testing.T) {
	ctx := context.Background()
	drv := openTest(t, EngineSQLite, ":memory:")
	if _, err := drv.Exec(ctx, `CREATE TABLE big (id INTEGER PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatal(err)
	}
	const rows = 100_000
	_, err := drv.Exec(ctx, fmt.Sprintf(`
		INSERT INTO big (id, name)
		WITH RECURSIVE seq(n) AS (
			SELECT 1 UNION ALL SELECT n + 1 FROM seq WHERE n < %d
		)
		SELECT n, 'row-' || n FROM seq`, rows))
	if err != nil {
		t.Fatal(err)
	}

	n, err := drv.CountRows(ctx, "", "big", nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != rows {
		t.Fatalf("CountRows = %d, want %d", n, rows)
	}

	start := time.Now()
	rs, err := drv.QueryPage(ctx, "", "big", nil, &Sort{Column: "id"}, 100, 99_900)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs.Rows) != 100 {
		t.Fatalf("got %d rows, want the 100-row page", len(rs.Rows))
	}
	if got := rs.Rows[0][0]; got != int64(99_901) {
		t.Fatalf("first row id = %v, want 99901", got)
	}
	// Generous by two orders of magnitude: this only has to fail if the
	// page query degenerates into materializing the whole table.
	if el := time.Since(start); el > 2*time.Second {
		t.Fatalf("last-page query took %s", el)
	}
}
