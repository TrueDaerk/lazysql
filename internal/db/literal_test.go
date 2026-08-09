package db

import (
	"math"
	"testing"
	"time"
)

func mustDialect(t *testing.T, e Engine) Dialect {
	t.Helper()
	d, err := DialectFor(e)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// Every scalar the scanner produces has one correct literal spelling
// per dialect, and NULL is spelled NULL in all of them.
func TestQuoteLiteral(t *testing.T) {
	ts := time.Date(2026, 8, 9, 12, 34, 56, 0, time.UTC)
	tests := []struct {
		engine Engine
		in     any
		want   string
	}{
		{EngineSQLite, nil, "NULL"},
		{EngineMySQL, nil, "NULL"},
		{EnginePostgres, nil, "NULL"},

		{EnginePostgres, "plain", "'plain'"},
		{EnginePostgres, "it's", "'it''s'"},
		// standard_conforming_strings: the backslash is data.
		{EnginePostgres, `C:\tmp`, `'C:\tmp'`},
		// MySQL reads a backslash as an escape, so it has to be doubled.
		{EngineMySQL, `C:\tmp`, `'C:\\tmp'`},
		{EngineMySQL, `it's\`, `'it''s\\'`},
		{EngineMariaDB, `a\b`, `'a\\b'`},
		{EngineSQLite, `a\b`, `'a\b'`},

		{EngineSQLite, int64(-7), "-7"},
		{EngineSQLite, 1.5, "1.5"},
		{EngineSQLite, math.NaN(), "'NaN'"},
		{EngineSQLite, math.Inf(1), "'+Inf'"},

		{EnginePostgres, true, "TRUE"},
		{EngineDuckDB, false, "FALSE"},
		{EngineMySQL, true, "1"},
		{EngineMariaDB, false, "0"},

		{EnginePostgres, ts, "'2026-08-09T12:34:56Z'"},
		{EngineMySQL, ts, "'2026-08-09 12:34:56'"},
	}
	for _, tt := range tests {
		d := mustDialect(t, tt.engine)
		if got := QuoteLiteral(d, tt.in); got != tt.want {
			t.Errorf("%s QuoteLiteral(%#v) = %s, want %s", tt.engine, tt.in, got, tt.want)
		}
	}
}

// A nil dialect is the no-connection case: literals still render, with
// the standard-conforming escaping.
func TestQuoteLiteralWithoutDialect(t *testing.T) {
	if got := QuoteLiteral(nil, `it's\`); got != `'it''s\'` {
		t.Errorf("QuoteLiteral(nil, …) = %s", got)
	}
	if got := QualifiedTable(nil, "main", "users"); got != "main.users" {
		t.Errorf("QualifiedTable(nil, …) = %s", got)
	}
}

// The generated INSERT quotes identifiers per dialect and inlines every
// value as a literal — it is text for the user, never something the app
// executes.
func TestInsertStatement(t *testing.T) {
	cols := []Column{{Name: "id"}, {Name: "name"}, {Name: "note"}}
	values := []any{int64(1), "O'Hara", nil}

	pg := mustDialect(t, EnginePostgres)
	want := `INSERT INTO "app"."users" ("id", "name", "note") VALUES (1, 'O''Hara', NULL);`
	if got := InsertStatement(pg, "app", "users", cols, values); got != want {
		t.Errorf("postgres INSERT =\n%s\nwant\n%s", got, want)
	}

	my := mustDialect(t, EngineMySQL)
	want = "INSERT INTO `users` (`id`, `name`, `note`) VALUES (1, 'O''Hara', NULL);"
	if got := InsertStatement(my, "", "users", cols, values); got != want {
		t.Errorf("mysql INSERT =\n%s\nwant\n%s", got, want)
	}

	// A row shorter than the column list binds NULL for the rest rather
	// than producing a statement with too few values.
	want = `INSERT INTO "users" ("id", "name", "note") VALUES (1, NULL, NULL);`
	if got := InsertStatement(mustDialect(t, EngineSQLite), "", "users", cols, values[:1]); got != want {
		t.Errorf("short row INSERT =\n%s\nwant\n%s", got, want)
	}
}
