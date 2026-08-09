package db

import "testing"

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
