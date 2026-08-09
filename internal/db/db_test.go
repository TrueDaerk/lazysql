package db

import (
	"context"
	"errors"
	"slices"
	"testing"
)

// openTest connects a driver to an in-process database and registers
// cleanup. DSN "" works for both SQLite (temp memory db) and DuckDB
// (in-memory database).
func openTest(t *testing.T, engine Engine, dsn string) Driver {
	t.Helper()
	drv, err := Open(engine)
	if err != nil {
		t.Fatalf("Open(%s): %v", engine, err)
	}
	if err := drv.Connect(context.Background(), dsn); err != nil {
		t.Fatalf("Connect(%s): %v", engine, err)
	}
	t.Cleanup(func() { drv.Close() })
	return drv
}

// seed creates the same fixture schema on any engine.
func seed(t *testing.T, drv Driver) {
	t.Helper()
	ctx := context.Background()
	d := drv.Dialect()
	stmts := []string{
		`CREATE TABLE users (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			email TEXT
		)`,
		`CREATE INDEX idx_users_name ON users (name)`,
		`CREATE VIEW named_users AS SELECT id, name FROM users`,
		// orders exists so the Indexes tab has a foreign key to show.
		`CREATE TABLE orders (
			id INTEGER PRIMARY KEY,
			user_id INTEGER NOT NULL REFERENCES users (id)
		)`,
	}
	for _, s := range stmts {
		if _, err := drv.Exec(ctx, s); err != nil {
			t.Fatalf("seed %q: %v", s, err)
		}
	}
	insert := "INSERT INTO users (id, name, email) VALUES (" +
		d.Placeholder(1) + ", " + d.Placeholder(2) + ", " + d.Placeholder(3) + ")"
	for i, row := range [][]any{
		{1, "alice", "alice@example.com"},
		{2, "bob", nil},
		{3, "carol", "carol@example.com"},
	} {
		res, err := drv.Exec(ctx, insert, row...)
		if err != nil {
			t.Fatalf("insert row %d: %v", i, err)
		}
		if res.RowsAffected != 1 {
			t.Fatalf("insert row %d: RowsAffected = %d, want 1", i, res.RowsAffected)
		}
	}
}

// engineSuite runs the shared acceptance tests against one engine.
func engineSuite(t *testing.T, engine Engine, dsn string) {
	ctx := context.Background()
	drv := openTest(t, engine, dsn)
	seed(t, drv)

	t.Run("ListDatabases", func(t *testing.T) {
		dbs, err := drv.ListDatabases(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(dbs) == 0 {
			t.Fatal("no databases listed")
		}
	})

	t.Run("ListTables", func(t *testing.T) {
		tables, err := drv.ListTables(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(tables, "users") {
			t.Fatalf("ListTables = %v, want to contain users", tables)
		}
	})

	// The [3] Tables panel splits the listing into its Tables and Views
	// sub-tabs, so the kind has to survive the driver boundary.
	t.Run("ListRelations", func(t *testing.T) {
		rels, err := drv.ListRelations(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		kinds := map[string]RelationKind{}
		for _, r := range rels {
			kinds[r.Name] = r.Kind
		}
		if kinds["users"] != RelationTable {
			t.Errorf("users kind = %v, want RelationTable", kinds["users"])
		}
		if kinds["named_users"] != RelationView {
			t.Errorf("named_users kind = %v, want RelationView", kinds["named_users"])
		}
		if got := FilterRelations(rels, RelationView); !slices.Contains(got, "named_users") {
			t.Errorf("FilterRelations(views) = %v, want to contain named_users", got)
		}
	})

	t.Run("TableColumns", func(t *testing.T) {
		cols, err := drv.TableColumns(ctx, "", "users")
		if err != nil {
			t.Fatal(err)
		}
		if len(cols) != 3 {
			t.Fatalf("got %d columns, want 3", len(cols))
		}
		byName := map[string]Column{}
		for _, c := range cols {
			byName[c.Name] = c
		}
		if !byName["id"].PrimaryKey {
			t.Errorf("id not marked primary key: %+v", byName["id"])
		}
		if byName["name"].Nullable {
			t.Errorf("name marked nullable: %+v", byName["name"])
		}
		if !byName["email"].Nullable {
			t.Errorf("email not marked nullable: %+v", byName["email"])
		}
	})

	t.Run("TableIndexes", func(t *testing.T) {
		idx, err := drv.TableIndexes(ctx, "", "users")
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, i := range idx {
			if i.Name == "idx_users_name" {
				found = true
				if !slices.Equal(i.Columns, []string{"name"}) {
					t.Errorf("idx_users_name columns = %v, want [name]", i.Columns)
				}
			}
		}
		if !found {
			t.Fatalf("idx_users_name not in %+v", idx)
		}
	})

	// The Indexes tab lists foreign keys under the indexes, so every
	// engine has to report the referenced table and columns.
	t.Run("TableForeignKeys", func(t *testing.T) {
		fks, err := drv.TableForeignKeys(ctx, "", "orders")
		if err != nil {
			t.Fatal(err)
		}
		if len(fks) != 1 {
			t.Fatalf("got %d foreign keys, want 1: %+v", len(fks), fks)
		}
		fk := fks[0]
		if !slices.Equal(fk.Columns, []string{"user_id"}) {
			t.Errorf("columns = %v, want [user_id]", fk.Columns)
		}
		if fk.RefTable != "users" {
			t.Errorf("referenced table = %q, want users", fk.RefTable)
		}
		if !slices.Equal(fk.RefColumns, []string{"id"}) {
			t.Errorf("referenced columns = %v, want [id]", fk.RefColumns)
		}
	})

	// A table without foreign keys reports none rather than failing.
	t.Run("TableForeignKeysNone", func(t *testing.T) {
		fks, err := drv.TableForeignKeys(ctx, "", "users")
		if err != nil {
			t.Fatal(err)
		}
		if len(fks) != 0 {
			t.Fatalf("got %+v, want no foreign keys", fks)
		}
	})

	t.Run("TableDDL", func(t *testing.T) {
		ddl, err := drv.TableDDL(ctx, "", "users")
		if err != nil {
			t.Fatal(err)
		}
		if ddl == "" {
			t.Fatal("empty DDL")
		}
	})

	t.Run("QueryPage", func(t *testing.T) {
		rs, err := drv.QueryPage(ctx, "", "users", nil, &Sort{Column: "id"}, 2, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(rs.Rows) != 2 {
			t.Fatalf("got %d rows, want 2 (limit 2 offset 1)", len(rs.Rows))
		}
		if got := rs.Rows[0][1]; got != "bob" {
			t.Errorf("first row name = %v, want bob", got)
		}
		if rs.Rows[0][2] != nil {
			t.Errorf("bob email = %v, want nil (NULL)", rs.Rows[0][2])
		}
	})

	t.Run("QueryPageFilterSort", func(t *testing.T) {
		f := ParseFilter(drv.Dialect(), "email IS NOT NULL")
		rs, err := drv.QueryPage(ctx, "", "users", f, &Sort{Column: "name", Desc: true}, 10, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(rs.Rows) != 2 {
			t.Fatalf("got %d rows, want 2", len(rs.Rows))
		}
		if got := rs.Rows[0][1]; got != "carol" {
			t.Errorf("first row = %v, want carol (DESC)", got)
		}
	})

	// A parameterized filter has to reach the engine as a parameter, and
	// the count has to see the same rows the page does.
	t.Run("QueryPageBoundFilter", func(t *testing.T) {
		f := ParseFilter(drv.Dialect(), "name = 'bob'")
		if f.Verbatim || len(f.Args) != 1 {
			t.Fatalf("filter = %+v, want one bound argument", f)
		}
		rs, err := drv.QueryPage(ctx, "", "users", f, nil, 10, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(rs.Rows) != 1 || rs.Rows[0][1] != "bob" {
			t.Fatalf("rows = %v, want just bob", rs.Rows)
		}
		n, err := drv.CountRows(ctx, "", "users", f)
		if err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("CountRows = %d, want 1", n)
		}
	})

	t.Run("CountRows", func(t *testing.T) {
		n, err := drv.CountRows(ctx, "", "users", nil)
		if err != nil {
			t.Fatal(err)
		}
		if n != 3 {
			t.Fatalf("CountRows = %d, want 3", n)
		}
	})

	// A value that would break out of a string literal must not: it is
	// bound, so it can only ever match no rows.
	t.Run("FilterInjectionIsBound", func(t *testing.T) {
		f := ParseFilter(drv.Dialect(), `name = 'x'' OR 1=1 --'`)
		if f.Verbatim {
			t.Fatalf("filter %+v fell back to verbatim", f)
		}
		rs, err := drv.QueryPage(ctx, "", "users", f, nil, 10, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(rs.Rows) != 0 {
			t.Fatalf("rows = %v, want none", rs.Rows)
		}
	})

	t.Run("QueryWithParams", func(t *testing.T) {
		d := drv.Dialect()
		rs, err := drv.Query(ctx, "SELECT name FROM users WHERE id = "+d.Placeholder(1), 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(rs.Rows) != 1 || rs.Rows[0][0] != "bob" {
			t.Fatalf("rows = %v, want [[bob]]", rs.Rows)
		}
	})

	t.Run("ExecWithParams", func(t *testing.T) {
		d := drv.Dialect()
		res, err := drv.Exec(ctx,
			"UPDATE users SET email = "+d.Placeholder(1)+" WHERE id = "+d.Placeholder(2),
			"bob@example.com", 2)
		if err != nil {
			t.Fatal(err)
		}
		if res.RowsAffected != 1 {
			t.Fatalf("RowsAffected = %d, want 1", res.RowsAffected)
		}
	})

	t.Run("QuotedIdentifiers", func(t *testing.T) {
		d := drv.Dialect()
		quoted := d.QuoteIdent(`weird "table`)
		if _, err := drv.Exec(ctx, "CREATE TABLE "+quoted+" (x INTEGER)"); err != nil {
			t.Fatalf("create with quoted ident: %v", err)
		}
		if _, err := drv.QueryPage(ctx, "", `weird "table`, nil, nil, 5, 0); err != nil {
			t.Fatalf("QueryPage on quoted ident: %v", err)
		}
	})

	t.Run("Cancellation", func(t *testing.T) {
		cancelled, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := drv.Query(cancelled, "SELECT * FROM users"); !errors.Is(err, context.Canceled) {
			t.Fatalf("Query on cancelled ctx: err = %v, want context.Canceled", err)
		}
		if _, err := drv.Exec(cancelled, "DELETE FROM users"); !errors.Is(err, context.Canceled) {
			t.Fatalf("Exec on cancelled ctx: err = %v, want context.Canceled", err)
		}
	})
}

func TestSQLite(t *testing.T) { engineSuite(t, EngineSQLite, ":memory:") }
func TestDuckDB(t *testing.T) { engineSuite(t, EngineDuckDB, "") }

func TestEnginesRegistered(t *testing.T) {
	want := []Engine{EngineDuckDB, EngineMariaDB, EngineMySQL, EnginePostgres, EngineSQLite}
	if got := Engines(); !slices.Equal(got, want) {
		t.Fatalf("Engines() = %v, want %v", got, want)
	}
}

func TestNotConnected(t *testing.T) {
	drv, err := Open(EngineSQLite)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := drv.Query(context.Background(), "SELECT 1"); !errors.Is(err, errNotConnected) {
		t.Fatalf("err = %v, want errNotConnected", err)
	}
}
