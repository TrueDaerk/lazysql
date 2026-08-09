package db

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func strPtr(s string) *string { return &s }

func fixtureSchema(engine Engine, label string) *Schema {
	return &Schema{
		Engine: engine,
		Label:  label,
		Tables: []SchemaTable{
			{
				Name: "users",
				Columns: []Column{
					{Name: "id", DataType: "INTEGER", PrimaryKey: true},
					{Name: "name", DataType: "TEXT", Nullable: false},
					{Name: "age", DataType: "INTEGER", Nullable: true},
				},
				Indexes: []Index{
					{Name: "idx_users_name", Columns: []string{"name"}, Unique: true},
				},
			},
			{
				Name: "orders",
				Columns: []Column{
					{Name: "id", DataType: "INTEGER", PrimaryKey: true},
					{Name: "user_id", DataType: "INTEGER"},
				},
				ForeignKeys: []ForeignKey{
					{Name: "fk_orders_user", Columns: []string{"user_id"},
						RefTable: "users", RefColumns: []string{"id"}, OnDelete: "CASCADE"},
				},
			},
		},
	}
}

func TestDiffIdenticalSchemas(t *testing.T) {
	a := fixtureSchema(EngineSQLite, "a.db")
	b := fixtureSchema(EngineSQLite, "b.db")
	d := DiffSchemas(a, b)
	if !d.Empty() {
		t.Fatalf("identical schemas: diff not empty: %+v", d)
	}
	text := d.RenderText()
	if !strings.Contains(text, "no differences") {
		t.Fatalf("report of empty diff must say so, got:\n%s", text)
	}
}

func TestDiffTypeSynonymsSameEngine(t *testing.T) {
	a := fixtureSchema(EngineSQLite, "a.db")
	b := fixtureSchema(EngineSQLite, "b.db")
	// SQLite INT vs INTEGER is the same type; the diff must not flag it.
	b.Tables[1].Columns[0].DataType = "INT"
	b.Tables[1].Columns[1].DataType = "int"
	if d := DiffSchemas(a, b); !d.Empty() {
		t.Fatalf("INT vs INTEGER on SQLite flagged as change: %+v", d.TableDiffs)
	}
}

func TestDiffCrossEngineNoSynonyms(t *testing.T) {
	a := fixtureSchema(EngineMySQL, "mysql.app")
	b := fixtureSchema(EnginePostgres, "pg.app")
	b.Tables[1].Columns[0].DataType = "INT" // A says INTEGER
	d := DiffSchemas(a, b)
	if d.SameFamily {
		t.Fatal("mysql vs postgres reported as same family")
	}
	// Across engines nothing maps synonyms, so INTEGER vs INT is reported.
	if len(d.TableDiffs) != 1 || d.TableDiffs[0].Name != "orders" {
		t.Fatalf("expected one diff on orders, got %+v", d.TableDiffs)
	}
	if got := d.TableDiffs[0].ColumnsChanged; len(got) != 1 || got[0].Fields[0].Field != "type" {
		t.Fatalf("expected a type change on orders.id, got %+v", got)
	}
	if text := d.RenderText(); !strings.Contains(text, "cross-engine") {
		t.Fatalf("cross-engine report must state the limitation, got header:\n%s", text)
	}
}

func TestDiffMariaDBCountsAsMySQLFamily(t *testing.T) {
	a := fixtureSchema(EngineMySQL, "a")
	b := fixtureSchema(EngineMariaDB, "b")
	a.Tables[0].Columns[2].DataType = "INTEGER"
	b.Tables[0].Columns[2].DataType = "INT"
	if d := DiffSchemas(a, b); !d.Empty() {
		t.Fatalf("MySQL INTEGER vs MariaDB INT flagged: %+v", d.TableDiffs)
	}
}

func TestDiffTablesOnlyOnOneSide(t *testing.T) {
	a := fixtureSchema(EngineSQLite, "a.db")
	b := fixtureSchema(EngineSQLite, "b.db")
	b.Tables = b.Tables[:1] // drop "orders" from B (fixture order: users, orders)
	b.Tables = append(b.Tables, SchemaTable{Name: "audit", Columns: []Column{{Name: "id", DataType: "INTEGER"}}})

	d := DiffSchemas(a, b)
	if len(d.TablesOnlyA) != 1 || d.TablesOnlyA[0] != "orders" {
		t.Fatalf("TablesOnlyA = %v, want [orders]", d.TablesOnlyA)
	}
	if len(d.TablesOnlyB) != 1 || d.TablesOnlyB[0] != "audit" {
		t.Fatalf("TablesOnlyB = %v, want [audit]", d.TablesOnlyB)
	}
	if len(d.TableDiffs) != 0 {
		t.Fatalf("shared table users must compare clean, got %+v", d.TableDiffs)
	}
}

func TestDiffColumnAttributes(t *testing.T) {
	a := fixtureSchema(EngineSQLite, "a.db")
	b := fixtureSchema(EngineSQLite, "b.db")
	u := &b.Tables[1] // users (sorted later; index 1 in fixture order is orders — use lookup)
	for i := range b.Tables {
		if b.Tables[i].Name == "users" {
			u = &b.Tables[i]
		}
	}
	u.Columns[1].Nullable = true        // name: NOT NULL → NULL
	u.Columns[2].DataType = "TEXT"      // age: INTEGER → TEXT
	u.Columns[2].Default = strPtr("''") // age: no default → ''
	u.Columns = append(u.Columns, Column{Name: "email", DataType: "TEXT"})

	d := DiffSchemas(a, b)
	if len(d.TableDiffs) != 1 {
		t.Fatalf("expected one table diff, got %+v", d.TableDiffs)
	}
	td := d.TableDiffs[0]
	if td.Name != "users" {
		t.Fatalf("diff on %q, want users", td.Name)
	}
	if len(td.ColumnsOnlyB) != 1 || !strings.HasPrefix(td.ColumnsOnlyB[0], "email") {
		t.Fatalf("ColumnsOnlyB = %v, want email", td.ColumnsOnlyB)
	}
	fields := map[string]bool{}
	for _, c := range td.ColumnsChanged {
		for _, f := range c.Fields {
			fields[c.Name+"/"+f.Field] = true
		}
	}
	for _, want := range []string{"name/nullable", "age/type", "age/default"} {
		if !fields[want] {
			t.Fatalf("missing change %s in %v", want, fields)
		}
	}
}

func TestDiffIndexesAndFKs(t *testing.T) {
	a := fixtureSchema(EngineSQLite, "a.db")
	b := fixtureSchema(EngineSQLite, "b.db")
	for i := range b.Tables {
		switch b.Tables[i].Name {
		case "users":
			b.Tables[i].Indexes[0].Unique = false
		case "orders":
			b.Tables[i].ForeignKeys[0].OnDelete = "" // CASCADE → default
		}
	}
	d := DiffSchemas(a, b)
	changed := map[string]string{}
	for _, td := range d.TableDiffs {
		for _, ix := range td.IndexesChanged {
			changed[td.Name+"/index/"+ix.Name] = ix.Fields[0].Field
		}
		for _, fk := range td.FKsChanged {
			changed[td.Name+"/fk/"+fk.Name] = fk.Fields[0].Field
		}
	}
	if changed["users/index/idx_users_name"] != "unique" {
		t.Fatalf("index unique change not reported: %v", changed)
	}
	if changed["orders/fk/fk_orders_user"] != "on delete" {
		t.Fatalf("fk on delete change not reported: %v", changed)
	}
}

func TestDiffFKDefaultActionSpellings(t *testing.T) {
	a := fixtureSchema(EngineSQLite, "a.db")
	b := fixtureSchema(EngineSQLite, "b.db")
	// "" and "NO ACTION" both mean the engine default; not a difference.
	for i := range a.Tables {
		if a.Tables[i].Name == "orders" {
			a.Tables[i].ForeignKeys[0].OnUpdate = "NO ACTION"
		}
	}
	if d := DiffSchemas(a, b); !d.Empty() {
		t.Fatalf(`"NO ACTION" vs "" flagged as change: %+v`, d.TableDiffs)
	}
}

func TestDiffPrimaryIndexesIgnored(t *testing.T) {
	a := fixtureSchema(EnginePostgres, "a")
	b := fixtureSchema(EnginePostgres, "b")
	a.Tables[0].Indexes = append(a.Tables[0].Indexes, Index{Name: "orders_pkey", Columns: []string{"id"}, Unique: true, Primary: true})
	b.Tables[0].Indexes = append(b.Tables[0].Indexes, Index{Name: "PRIMARY", Columns: []string{"id"}, Unique: true, Primary: true})
	if d := DiffSchemas(a, b); !d.Empty() {
		t.Fatalf("differently named primary indexes flagged: %+v", d.TableDiffs)
	}
}

func TestNormalizeType(t *testing.T) {
	cases := []struct {
		engine   Engine
		raw      string
		synonyms bool
		want     string
	}{
		{EngineSQLite, "int", true, "INTEGER"},
		{EngineSQLite, "INT", false, "INT"},
		{EngineSQLite, "varchar (255)", true, "VARCHAR(255)"},
		{EnginePostgres, "character varying(40)", true, "VARCHAR(40)"},
		{EngineMariaDB, "integer", true, "INT"},
		{EngineDuckDB, "STRING", true, "VARCHAR"},
		{EngineMySQL, "int(11)", true, "INT(11)"},
	}
	for _, c := range cases {
		if got := NormalizeType(c.engine, c.raw, c.synonyms); got != c.want {
			t.Errorf("NormalizeType(%s, %q, %v) = %q, want %q", c.engine, c.raw, c.synonyms, got, c.want)
		}
	}
}

// TestSchemaDiffSQLiteIntegration diffs two real SQLite files end to
// end: introspection through the driver interface, then the diff.
func TestSchemaDiffSQLiteIntegration(t *testing.T) {
	dir := t.TempDir()
	openAndSeed := func(file string, stmts []string) Driver {
		t.Helper()
		drv, err := Open(EngineSQLite)
		if err != nil {
			t.Fatal(err)
		}
		dsn, err := BuildDSN(EngineSQLite, ConnParams{File: filepath.Join(dir, file)})
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		if err := drv.Connect(ctx, dsn); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { drv.Close() })
		for _, s := range stmts {
			if _, err := drv.Exec(ctx, s); err != nil {
				t.Fatalf("%s: %v", s, err)
			}
		}
		return drv
	}

	a := openAndSeed("a.db", []string{
		`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL, age INT)`,
		`CREATE UNIQUE INDEX idx_users_name ON users(name)`,
		`CREATE TABLE orders (id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL,
			CONSTRAINT fk_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE)`,
		`CREATE TABLE legacy (id INTEGER PRIMARY KEY)`,
	})
	b := openAndSeed("b.db", []string{
		// age spelled INTEGER instead of INT: same type, must not be flagged.
		`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, age INTEGER, email TEXT)`,
		`CREATE INDEX idx_users_name ON users(name)`,
		`CREATE TABLE orders (id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL,
			CONSTRAINT fk_user FOREIGN KEY (user_id) REFERENCES users(id))`,
	})

	ctx := context.Background()
	sa, err := IntrospectSchema(ctx, a, "", "a.db", nil)
	if err != nil {
		t.Fatal(err)
	}
	var progressCalls int
	sb, err := IntrospectSchema(ctx, b, "", "b.db", func(done, total int) { progressCalls++ })
	if err != nil {
		t.Fatal(err)
	}
	if progressCalls != len(sb.Tables) {
		t.Fatalf("progress called %d times for %d tables", progressCalls, len(sb.Tables))
	}

	d := DiffSchemas(sa, sb)
	if d.Empty() {
		t.Fatal("diff of differing files reported no differences")
	}
	if len(d.TablesOnlyA) != 1 || d.TablesOnlyA[0] != "legacy" {
		t.Fatalf("TablesOnlyA = %v, want [legacy]", d.TablesOnlyA)
	}
	text := d.RenderText()
	for _, want := range []string{
		"+ column email",         // added in B
		"~ column name nullable", // NOT NULL dropped in B
		"~ index idx_users_name", // unique → plain
		// SQLite reports no constraint names (PRAGMA foreign_key_list),
		// so the dialect synthesizes one; only the change itself is stable.
		"on delete  A: CASCADE / B: NO ACTION",
		"- legacy", // table only in A
	} {
		if !strings.Contains(text, want) {
			t.Errorf("report missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "~ column age") {
		t.Errorf("INT vs INTEGER flagged in same-engine diff:\n%s", text)
	}
}
