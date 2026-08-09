package db

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func TestFKFilterSingleColumn(t *testing.T) {
	d := dialectFor(t, EngineSQLite)
	f, err := FKFilter(d, []string{"id"}, []any{int64(7)})
	if err != nil {
		t.Fatal(err)
	}
	if f.Expr != `"id" = ?` {
		t.Errorf("Expr = %q", f.Expr)
	}
	if !slices.Equal(f.Args, []any{int64(7)}) {
		t.Errorf("Args = %v", f.Args)
	}
	if f.Raw != "id = 7" {
		t.Errorf("Raw = %q", f.Raw)
	}
	if f.Verbatim {
		t.Error("an FK filter is always parameterized, never verbatim")
	}
}

// A composite key produces one placeholder per column, numbered the way
// the dialect numbers them.
func TestFKFilterComposite(t *testing.T) {
	d := dialectFor(t, EnginePostgres)
	f, err := FKFilter(d, []string{"tenant", "code"}, []any{int64(1), "a'b"})
	if err != nil {
		t.Fatal(err)
	}
	if f.Expr != `"tenant" = $1 AND "code" = $2` {
		t.Errorf("Expr = %q", f.Expr)
	}
	if !slices.Equal(f.Args, []any{any(int64(1)), any("a'b")}) {
		t.Errorf("Args = %v", f.Args)
	}
	// The value travels as a parameter; the display copy escapes its
	// quote so the status line stays readable.
	if f.Raw != "tenant = 1 AND code = 'a''b'" {
		t.Errorf("Raw = %q", f.Raw)
	}
}

func TestFKFilterRejectsNULL(t *testing.T) {
	d := dialectFor(t, EngineSQLite)
	_, err := FKFilter(d, []string{"user_id"}, []any{nil})
	if err == nil {
		t.Fatal("a NULL key must not build a filter")
	}
	if !strings.Contains(err.Error(), "user_id") {
		t.Errorf("error %q does not name the column", err)
	}
}

func TestFKFilterRejectsMismatch(t *testing.T) {
	d := dialectFor(t, EngineSQLite)
	if _, err := FKFilter(d, []string{"a", "b"}, []any{int64(1)}); err == nil {
		t.Fatal("column/value mismatch must be an error")
	}
	if _, err := FKFilter(d, nil, nil); err == nil {
		t.Fatal("an empty key must be an error")
	}
}

func TestSplitQualified(t *testing.T) {
	cases := []struct{ in, ns, table string }{
		{"users", "", "users"},
		{"public.users", "public", "users"},
		{`"public"."users"`, "public", "users"},
		{"`app`.`users`", "app", "users"},
		{" main.users ", "main", "users"},
	}
	for _, c := range cases {
		ns, table := SplitQualified(c.in)
		if ns != c.ns || table != c.table {
			t.Errorf("SplitQualified(%q) = (%q, %q), want (%q, %q)", c.in, ns, table, c.ns, c.table)
		}
	}
}

func TestFKAt(t *testing.T) {
	fks := []ForeignKey{
		{Columns: []string{"user_id"}, RefTable: "users", RefColumns: []string{"id"}},
		{Columns: []string{"tenant", "code"}, RefTable: "codes", RefColumns: []string{"tenant", "code"}},
	}
	if got := FKAt(fks, "USER_ID"); len(got) != 1 || got[0].RefTable != "users" {
		t.Errorf("FKAt(user_id) = %+v", got)
	}
	if got := FKAt(fks, "tenant"); len(got) != 1 || got[0].RefTable != "codes" {
		t.Errorf("FKAt(tenant) = %+v", got)
	}
	if got := FKAt(fks, "name"); len(got) != 0 {
		t.Errorf("FKAt(name) = %+v, want none", got)
	}
}

// dialectFor opens a driver only for its dialect, which is what the
// filter builder needs — no connection is involved.
func dialectFor(t *testing.T, engine Engine) Dialect {
	t.Helper()
	drv, err := Open(engine)
	if err != nil {
		t.Fatalf("Open(%s): %v", engine, err)
	}
	return drv.Dialect()
}

// The end-to-end shape of following a foreign key: introspect the child
// table, build the filter from a child row, and read exactly the parent
// row back. It runs on both in-process engines because the introspection
// behind it is per-dialect.
func TestFollowForeignKeyEndToEnd(t *testing.T) {
	for _, engine := range []Engine{EngineSQLite, EngineDuckDB} {
		t.Run(string(engine), func(t *testing.T) {
			ctx := context.Background()
			dsn := ""
			if engine == EngineSQLite {
				dsn = ":memory:"
			}
			drv := openTest(t, engine, dsn)
			seed(t, drv)
			d := drv.Dialect()
			for _, s := range []string{
				`INSERT INTO orders (id, user_id) VALUES (10, 2)`,
				`CREATE TABLE tenants (tenant INTEGER, code TEXT, label TEXT,
					PRIMARY KEY (tenant, code))`,
				`CREATE TABLE assignments (
					id INTEGER PRIMARY KEY,
					tenant INTEGER,
					code TEXT,
					FOREIGN KEY (tenant, code) REFERENCES tenants (tenant, code))`,
				`INSERT INTO tenants (tenant, code, label) VALUES (1, 'a', 'one-a'), (2, 'a', 'two-a')`,
				`INSERT INTO assignments (id, tenant, code) VALUES (1, 2, 'a')`,
			} {
				if _, err := drv.Exec(ctx, s); err != nil {
					t.Fatalf("fixture %q: %v", s, err)
				}
			}

			// Single-column key: orders.user_id → users.id.
			fks, err := drv.TableForeignKeys(ctx, "", "orders")
			if err != nil || len(fks) != 1 {
				t.Fatalf("TableForeignKeys(orders) = %+v, %v", fks, err)
			}
			f, err := FKFilter(d, fks[0].RefColumns, []any{int64(2)})
			if err != nil {
				t.Fatal(err)
			}
			_, target := SplitQualified(fks[0].RefTable)
			rs, err := drv.QueryPage(ctx, "", target, f, nil, 10, 0)
			if err != nil {
				t.Fatal(err)
			}
			if len(rs.Rows) != 1 {
				t.Fatalf("got %d rows, want exactly the referenced one: %+v", len(rs.Rows), rs.Rows)
			}
			if name := FormatValue(rs.Rows[0][1], "NULL"); name != "bob" {
				t.Errorf("followed to %q, want bob", name)
			}

			// Composite key: assignments(tenant, code) → tenants(tenant, code).
			cfks, err := drv.TableForeignKeys(ctx, "", "assignments")
			if err != nil || len(cfks) != 1 {
				t.Fatalf("TableForeignKeys(assignments) = %+v, %v", cfks, err)
			}
			if len(cfks[0].RefColumns) != 2 {
				t.Fatalf("composite key reported as %+v", cfks[0])
			}
			// Pair the values the way the constraint pairs the columns:
			// the engine may report them in either order.
			vals := make([]any, len(cfks[0].Columns))
			for i, c := range cfks[0].Columns {
				switch strings.ToLower(c) {
				case "tenant":
					vals[i] = int64(2)
				case "code":
					vals[i] = "a"
				default:
					t.Fatalf("unexpected key column %q", c)
				}
			}
			cf, err := FKFilter(d, cfks[0].RefColumns, vals)
			if err != nil {
				t.Fatal(err)
			}
			_, ctarget := SplitQualified(cfks[0].RefTable)
			crs, err := drv.QueryPage(ctx, "", ctarget, cf, nil, 10, 0)
			if err != nil {
				t.Fatal(err)
			}
			if len(crs.Rows) != 1 {
				t.Fatalf("composite follow got %d rows: %+v", len(crs.Rows), crs.Rows)
			}
			if label := FormatValue(crs.Rows[0][2], "NULL"); label != "two-a" {
				t.Errorf("followed to %q, want two-a", label)
			}
		})
	}
}
