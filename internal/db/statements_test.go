package db

import (
	"reflect"
	"testing"
)

func TestSplitStatements(t *testing.T) {
	cases := []struct {
		name   string
		engine Engine
		script string
		want   []string
	}{
		{"empty", EngineSQLite, "   \n\t ", nil},
		{"one, no terminator", EngineSQLite, "SELECT 1", []string{"SELECT 1"}},
		{"one, terminated", EngineSQLite, "SELECT 1;", []string{"SELECT 1"}},
		{"stray separators", EngineSQLite, ";;SELECT 1;;", []string{"SELECT 1"}},
		{"two", EngineSQLite, "SELECT 1;\nSELECT 2;", []string{"SELECT 1", "SELECT 2"}},
		{
			"semicolon in a string literal", EngineSQLite,
			`SELECT ';'; SELECT 2`,
			[]string{`SELECT ';'`, "SELECT 2"},
		},
		{
			"doubled quote inside a literal", EnginePostgres,
			`SELECT 'it''s; fine'; SELECT 2`,
			[]string{`SELECT 'it''s; fine'`, "SELECT 2"},
		},
		{
			"semicolon in a quoted identifier", EnginePostgres,
			`SELECT "a;b" FROM t; SELECT 2`,
			[]string{`SELECT "a;b" FROM t`, "SELECT 2"},
		},
		{
			"semicolon in a line comment", EngineSQLite,
			"SELECT 1 -- ; not a separator\n; SELECT 2",
			[]string{"SELECT 1 -- ; not a separator", "SELECT 2"},
		},
		{
			"semicolon in a block comment", EngineSQLite,
			"SELECT /* ; */ 1; SELECT 2",
			[]string{"SELECT /* ; */ 1", "SELECT 2"},
		},
		{
			"mysql backtick identifier", EngineMySQL,
			"SELECT `a;b` FROM t; SELECT 2",
			[]string{"SELECT `a;b` FROM t", "SELECT 2"},
		},
		{
			"mysql hash comment", EngineMariaDB,
			"SELECT 1 # ; nope\n; SELECT 2",
			[]string{"SELECT 1 # ; nope", "SELECT 2"},
		},
		{
			"mysql backslash escape", EngineMySQL,
			`SELECT 'a\'; b'; SELECT 2`,
			[]string{`SELECT 'a\'; b'`, "SELECT 2"},
		},
		{
			// Without backslash escapes the literal ends at the second
			// quote, so the semicolon after it does separate.
			"postgres has no backslash escape", EnginePostgres,
			`SELECT 'a\'; SELECT 2`,
			[]string{`SELECT 'a\'`, "SELECT 2"},
		},
		{
			"postgres dollar-quoted body", EnginePostgres,
			"CREATE FUNCTION f() RETURNS int AS $$ BEGIN; RETURN 1; END; $$ LANGUAGE plpgsql; SELECT 2",
			[]string{
				"CREATE FUNCTION f() RETURNS int AS $$ BEGIN; RETURN 1; END; $$ LANGUAGE plpgsql",
				"SELECT 2",
			},
		},
		{
			"postgres positional parameters are not tags", EnginePostgres,
			"SELECT $1; SELECT $2",
			[]string{"SELECT $1", "SELECT $2"},
		},
		{
			"unterminated literal swallows the rest", EngineSQLite,
			"SELECT 'oops; SELECT 2",
			[]string{"SELECT 'oops; SELECT 2"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SplitStatements(c.engine, c.script)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("SplitStatements() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestClassifyStatement(t *testing.T) {
	cases := []struct {
		sql  string
		want StatementKind
	}{
		{"SELECT 1", StatementRead},
		{"  select * from t", StatementRead},
		{"-- a comment\nSELECT 1", StatementRead},
		{"/* lead */ SELECT 1", StatementRead},
		{"(SELECT 1) UNION (SELECT 2)", StatementWrite}, // starts with "(" — unknown, so confirmed
		{"WITH x AS (SELECT 1) SELECT * FROM x", StatementRead},
		{"WITH x AS (DELETE FROM t RETURNING *) SELECT * FROM x", StatementWrite},
		{"SHOW TABLES", StatementRead},
		{"EXPLAIN SELECT 1", StatementRead},
		{"DESCRIBE users", StatementRead},
		{"VALUES (1)", StatementRead},
		{"PRAGMA table_info(t)", StatementRead},
		{"PRAGMA journal_mode = WAL", StatementWrite},
		// The two dump statements read the connected database and write
		// a file, so a read-only connection can still be backed up.
		{"VACUUM INTO '/tmp/backup.db'", StatementRead},
		{"vacuum into '/tmp/backup.db'", StatementRead},
		{"EXPORT DATABASE '/tmp/export'", StatementRead},
		// Every other spelling of either keyword rewrites something.
		{"VACUUM", StatementWrite},
		{"VACUUM main", StatementWrite},
		{`VACUUM "INTO"`, StatementWrite},
		{"EXPORT SOMETHING ELSE", StatementWrite},
		{"IMPORT DATABASE '/tmp/export'", StatementWrite},
		{"INSERT INTO t VALUES (1)", StatementWrite},
		{"UPDATE t SET a = 1", StatementWrite},
		{"DELETE FROM t", StatementWrite},
		{"CREATE TABLE t (id int)", StatementWrite},
		{"DROP TABLE t", StatementWrite},
		{"-- only a comment", StatementWrite},
	}
	for _, c := range cases {
		if got := ClassifyStatement(c.sql); got != c.want {
			t.Errorf("ClassifyStatement(%q) = %v, want %v", c.sql, got, c.want)
		}
	}
}

func TestFirstKeyword(t *testing.T) {
	cases := map[string]string{
		"select * from t":       "SELECT",
		"  /* x */ update t":    "UPDATE",
		"DELETE FROM t":         "DELETE",
		"insert into t values(": "INSERT",
		"":                      "",
	}
	for sql, want := range cases {
		if got := FirstKeyword(sql); got != want {
			t.Errorf("FirstKeyword(%q) = %q, want %q", sql, got, want)
		}
	}
}

func TestSplitStatementSpans(t *testing.T) {
	script := "SELECT 1;\n\nSELECT 2 FROM t;\n"
	spans := SplitStatementSpans(EngineSQLite, script)
	if len(spans) != 2 {
		t.Fatalf("got %d spans, want 2", len(spans))
	}
	r := []rune(script)
	for _, s := range spans {
		if string(r[s.Start:s.End]) != s.SQL {
			t.Errorf("span %+v does not match the script text %q", s, string(r[s.Start:s.End]))
		}
	}
}

func TestStatementAt(t *testing.T) {
	script := "SELECT 1;\nSELECT 2 FROM t;\nSELECT 3;"
	cases := []struct {
		name   string
		offset int
		want   string
	}{
		{"inside the first", 3, "SELECT 1"},
		{"on the first semicolon", 8, "SELECT 1"},
		{"in the gap after the first", 9, "SELECT 1"},
		{"at the start of the second", 10, "SELECT 2 FROM t"},
		{"inside the second", 14, "SELECT 2 FROM t"},
		{"inside the last", 30, "SELECT 3"},
		{"past the end", 999, "SELECT 3"},
		{"before everything", 0, "SELECT 1"},
	}
	for _, c := range cases {
		got, ok := StatementAt(EngineSQLite, script, c.offset)
		if !ok {
			t.Fatalf("%s: no statement found", c.name)
		}
		if got.SQL != c.want {
			t.Errorf("%s: StatementAt(%d) = %q, want %q", c.name, c.offset, got.SQL, c.want)
		}
	}
	if _, ok := StatementAt(EngineSQLite, "  \n ;", 0); ok {
		t.Error("an empty script should have no statement")
	}
}
