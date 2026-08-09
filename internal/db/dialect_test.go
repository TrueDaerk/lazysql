package db

import (
	"context"
	"strings"
	"testing"
)

func TestQuoteIdent(t *testing.T) {
	tests := []struct {
		engine Engine
		in     string
		want   string
	}{
		{EngineMySQL, "users", "`users`"},
		{EngineMySQL, "we`ird", "`we``ird`"},
		{EngineMariaDB, "users", "`users`"},
		{EnginePostgres, "users", `"users"`},
		{EnginePostgres, `we"ird`, `"we""ird"`},
		{EngineSQLite, `we"ird`, `"we""ird"`},
		{EngineDuckDB, "users", `"users"`},
	}
	for _, tt := range tests {
		d, err := DialectFor(tt.engine)
		if err != nil {
			t.Fatal(err)
		}
		if got := d.QuoteIdent(tt.in); got != tt.want {
			t.Errorf("%s QuoteIdent(%q) = %s, want %s", tt.engine, tt.in, got, tt.want)
		}
	}
}

func TestPlaceholders(t *testing.T) {
	pg, _ := DialectFor(EnginePostgres)
	if got := pg.Placeholder(3); got != "$3" {
		t.Errorf("postgres Placeholder(3) = %s, want $3", got)
	}
	for _, e := range []Engine{EngineMySQL, EngineMariaDB, EngineSQLite, EngineDuckDB} {
		d, _ := DialectFor(e)
		if got := d.Placeholder(3); got != "?" {
			t.Errorf("%s Placeholder(3) = %s, want ?", e, got)
		}
	}
}

func TestLimitOffset(t *testing.T) {
	for _, e := range Engines() {
		d, _ := DialectFor(e)
		if got := d.LimitOffset(50, 100); got != " LIMIT 50 OFFSET 100" {
			t.Errorf("%s LimitOffset = %q", e, got)
		}
	}
}

func TestDisplayNames(t *testing.T) {
	want := map[Engine]string{
		EngineMySQL:    "MySQL",
		EngineMariaDB:  "MariaDB",
		EnginePostgres: "PostgreSQL",
		EngineSQLite:   "SQLite",
		EngineDuckDB:   "DuckDB",
	}
	for e, name := range want {
		d, err := DialectFor(e)
		if err != nil {
			t.Fatal(err)
		}
		if d.DisplayName() != name {
			t.Errorf("%s DisplayName = %s, want %s", e, d.DisplayName(), name)
		}
	}
}

func TestFormatValue(t *testing.T) {
	if got := FormatValue(nil, "NULL"); got != "NULL" {
		t.Errorf("nil = %q", got)
	}
	if got := FormatValue(int64(42), ""); got != "42" {
		t.Errorf("int64 = %q", got)
	}
	if got := FormatValue("x", ""); got != "x" {
		t.Errorf("string = %q", got)
	}
}

// synthesizeDDL is what PostgreSQL's TableDDL renders, so the shape is
// asserted here rather than behind a server the test suite has no
// access to.
func TestSynthesizeDDL(t *testing.T) {
	d, _ := DialectFor(EnginePostgres)
	def := "nextval('users_id_seq'::regclass)"
	cols := []Column{
		{Name: "id", DataType: "integer", Default: &def, PrimaryKey: true, Extra: "serial"},
		{Name: "email", DataType: "text", Nullable: true},
		{Name: "org_id", DataType: "integer"},
	}
	idx := []Index{
		{Name: "users_pkey", Columns: []string{"id"}, Unique: true, Primary: true},
		{Name: "users_email_key", Columns: []string{"email"}, Unique: true},
		{Name: "idx_users_org", Columns: []string{"org_id"}},
	}
	fks := []ForeignKey{{
		Name:       "users_org_id_fkey",
		Columns:    []string{"org_id"},
		RefTable:   "other.orgs",
		RefColumns: []string{"id"},
		OnDelete:   "CASCADE",
		OnUpdate:   "NO ACTION",
	}}

	ddl := synthesizeDDL(d, "public", "users", cols, idx, fks)
	for _, want := range []string{
		`CREATE TABLE "public"."users"`,
		`"id" integer NOT NULL DEFAULT nextval('users_id_seq'::regclass)`,
		`"email" text`,
		`PRIMARY KEY ("id")`,
		`CONSTRAINT "users_org_id_fkey" FOREIGN KEY ("org_id") REFERENCES "other"."orgs" ("id") ON DELETE CASCADE`,
		`CREATE UNIQUE INDEX "users_email_key" ON "public"."users" ("email");`,
		`CREATE INDEX "idx_users_org" ON "public"."users" ("org_id");`,
	} {
		if !strings.Contains(ddl, want) {
			t.Errorf("DDL is missing %q:\n%s", want, ddl)
		}
	}
	// The default rule contributes no clause, and the primary key index
	// is already covered by the PRIMARY KEY clause.
	if strings.Contains(ddl, "ON UPDATE") {
		t.Errorf("NO ACTION should not be spelled out:\n%s", ddl)
	}
	if strings.Contains(ddl, "users_pkey") {
		t.Errorf("primary key index restated as CREATE INDEX:\n%s", ddl)
	}
}

// SQLite keeps AUTOINCREMENT only in the stored DDL, so the column
// listing has to read it back to report it.
func TestSQLiteAutoincrementExtra(t *testing.T) {
	ctx := context.Background()
	drv := openTest(t, EngineSQLite, "")
	if _, err := drv.Exec(ctx,
		`CREATE TABLE seq (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT)`); err != nil {
		t.Fatal(err)
	}
	cols, err := drv.TableColumns(ctx, "", "seq")
	if err != nil {
		t.Fatal(err)
	}
	if cols[0].Extra != "autoincrement" {
		t.Errorf("id Extra = %q, want autoincrement", cols[0].Extra)
	}
	if cols[1].Extra != "" {
		t.Errorf("name Extra = %q, want empty", cols[1].Extra)
	}
}
