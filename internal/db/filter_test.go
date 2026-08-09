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

// ---------- structured filter (the `/` modal) ----------

func TestBuildFilterQuotesAndBinds(t *testing.T) {
	sqlite, _ := DialectFor(EngineSQLite)
	pg, _ := DialectFor(EnginePostgres)
	mysql, _ := DialectFor(EngineMySQL)

	cases := []struct {
		name     string
		dialect  Dialect
		conds    []FilterCond
		wantExpr string
		wantArgs []any
		wantRaw  string
	}{
		{
			name:     "string equality",
			dialect:  sqlite,
			conds:    []FilterCond{{Column: "name", Op: OpEq, Value: "bob", Type: "TEXT"}},
			wantExpr: `"name" = ?`,
			wantArgs: []any{"bob"},
			wantRaw:  `name = 'bob'`,
		},
		{
			// The value stays data: the quote is not escaped into the
			// statement, it never reaches it.
			name:     "quote and wildcard in the value",
			dialect:  sqlite,
			conds:    []FilterCond{{Column: "name", Op: OpLike, Value: `o'%brien`, Type: "TEXT"}},
			wantExpr: `"name" LIKE ?`,
			wantArgs: []any{`o'%brien`},
			wantRaw:  `name LIKE 'o''%brien'`,
		},
		{
			name:     "numeric column binds a number",
			dialect:  pg,
			conds:    []FilterCond{{Column: "id", Op: OpGe, Value: "42", Type: "integer"}},
			wantExpr: `"id" >= $1`,
			wantArgs: []any{int64(42)},
			wantRaw:  `id >= 42`,
		},
		{
			name:     "float column",
			dialect:  sqlite,
			conds:    []FilterCond{{Column: "price", Op: OpLt, Value: "9.5", Type: "DOUBLE PRECISION"}},
			wantExpr: `"price" < ?`,
			wantArgs: []any{9.5},
		},
		{
			name:     "boolean column",
			dialect:  sqlite,
			conds:    []FilterCond{{Column: "active", Op: OpNe, Value: "true", Type: "BOOLEAN"}},
			wantExpr: `"active" != ?`,
			wantArgs: []any{true},
		},
		{
			// A LIKE pattern is text even against a numeric column.
			name:     "LIKE against an integer column stays text",
			dialect:  sqlite,
			conds:    []FilterCond{{Column: "id", Op: OpLike, Value: "1%", Type: "INTEGER"}},
			wantExpr: `"id" LIKE ?`,
			wantArgs: []any{"1%"},
		},
		{
			name:     "unknown type sniffs a number",
			dialect:  sqlite,
			conds:    []FilterCond{{Column: "n", Op: OpEq, Value: "7"}},
			wantExpr: `"n" = ?`,
			wantArgs: []any{int64(7)},
		},
		{
			name:     "NULL tests take no parameter",
			dialect:  sqlite,
			conds:    []FilterCond{{Column: "email", Op: OpIsNull, Value: "ignored"}},
			wantExpr: `"email" IS NULL`,
			wantArgs: nil,
			wantRaw:  `email IS NULL`,
		},
		{
			name:    "conditions are ANDed with numbered placeholders",
			dialect: pg,
			conds: []FilterCond{
				{Column: "email", Op: OpIsNotNull},
				{Column: "name", Op: OpEq, Value: "bob", Type: "text"},
				{Column: "id", Op: OpGt, Value: "3", Type: "int8"},
			},
			wantExpr: `"email" IS NOT NULL AND "name" = $1 AND "id" > $2`,
			wantArgs: []any{"bob", int64(3)},
			wantRaw:  `email IS NOT NULL AND name = 'bob' AND id > 3`,
		},
		{
			name:     "identifier quoting follows the dialect",
			dialect:  mysql,
			conds:    []FilterCond{{Column: "odd`col", Op: OpEq, Value: "x", Type: "varchar(20)"}},
			wantExpr: "`odd``col` = ?",
			wantArgs: []any{"x"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, err := BuildFilter(c.dialect, c.conds)
			if err != nil {
				t.Fatalf("BuildFilter: %v", err)
			}
			if f.Verbatim {
				t.Errorf("filter is verbatim, want parameterized")
			}
			if f.Expr != c.wantExpr {
				t.Errorf("Expr = %q, want %q", f.Expr, c.wantExpr)
			}
			if !slices.Equal(f.Args, c.wantArgs) {
				t.Errorf("Args = %#v, want %#v", f.Args, c.wantArgs)
			}
			if c.wantRaw != "" && f.Raw != c.wantRaw {
				t.Errorf("Raw = %q, want %q", f.Raw, c.wantRaw)
			}
		})
	}
}

func TestBuildFilterEmptyIsNoFilter(t *testing.T) {
	d, _ := DialectFor(EngineSQLite)
	f, err := BuildFilter(d, nil)
	if err != nil || f != nil {
		t.Fatalf("BuildFilter(nil) = %+v, %v; want nil, nil", f, err)
	}
}

// A value the column's type cannot hold is reported instead of being sent
// as a string the engine would reject with a less useful message.
func TestBuildFilterRejectsBadValues(t *testing.T) {
	d, _ := DialectFor(EngineSQLite)
	cases := []FilterCond{
		{Column: "id", Op: OpEq, Value: "abc", Type: "INTEGER"},
		{Column: "price", Op: OpEq, Value: "cheap", Type: "NUMERIC"},
		{Column: "active", Op: OpEq, Value: "maybe", Type: "BOOLEAN"},
		{Column: "", Op: OpEq, Value: "x"},
		{Column: "id", Op: FilterOp("DROP"), Value: "1"},
	}
	for _, c := range cases {
		if _, err := BuildFilter(d, []FilterCond{c}); err == nil {
			t.Errorf("BuildFilter(%+v) = nil error, want one", c)
		}
	}
}

// An integer typed against an integer column but written as a decimal is
// still a number, so it binds rather than failing.
func TestBuildFilterAcceptsFloatOnIntegerColumn(t *testing.T) {
	d, _ := DialectFor(EngineSQLite)
	f, err := BuildFilter(d, []FilterCond{{Column: "id", Op: OpLt, Value: "4.5", Type: "INTEGER"}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.Args, []any{4.5}) {
		t.Fatalf("Args = %#v, want [4.5]", f.Args)
	}
}

// PostgreSQL's point and interval contain "int" but hold no number the
// filter should coerce.
func TestTypeClassMatchesWholeTypeNames(t *testing.T) {
	for _, c := range []struct {
		typ  string
		want valueClass
	}{
		{"INTEGER", classInt}, {"int4", classInt}, {"BIGINT", classInt},
		{"INT UNSIGNED", classInt}, {"tinyint(1)", classInt},
		{"double precision", classFloat}, {"NUMERIC(10,2)", classFloat},
		{"boolean", classBool},
		{"VARCHAR(20)", classText}, {"text", classText},
		{"point", classUnknown}, {"interval", classUnknown},
		{"", classUnknown}, {"timestamp", classUnknown},
	} {
		if got := typeClass(c.typ); got != c.want {
			t.Errorf("typeClass(%q) = %v, want %v", c.typ, got, c.want)
		}
	}
}

func TestFilterOpNeedsValue(t *testing.T) {
	for _, op := range FilterOps() {
		want := op != OpIsNull && op != OpIsNotNull
		if got := op.NeedsValue(); got != want {
			t.Errorf("%s.NeedsValue() = %v, want %v", op, got, want)
		}
	}
}
