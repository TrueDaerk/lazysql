package db

import (
	"strings"
	"testing"
)

func TestBuildDSNPerEngine(t *testing.T) {
	cases := []struct {
		name   string
		engine Engine
		params ConnParams
		want   string
	}{
		{
			"postgres with options",
			EnginePostgres,
			ConnParams{Host: "db.example", Port: 5433, User: "app", Password: "p@ss w/ord",
				Database: "app_dev", Options: map[string]string{"sslmode": "require"}},
			"postgres://app:p%40ss%20w%2Ford@db.example:5433/app_dev?sslmode=require",
		},
		{
			"postgres defaults the port",
			EnginePostgres,
			ConnParams{User: "app", Database: "app"},
			"postgres://app@localhost:5432/app",
		},
		{
			"mysql tcp",
			EngineMySQL,
			ConnParams{Host: "127.0.0.1", Port: 3307, User: "root", Password: "s3cret", Database: "shop"},
			"root:s3cret@tcp(127.0.0.1:3307)/shop?parseTime=true",
		},
		{
			"mysql unix socket",
			EngineMariaDB,
			ConnParams{Host: "/tmp/mysql.sock", User: "root", Database: "shop"},
			"root@unix(/tmp/mysql.sock)/shop?parseTime=true",
		},
		{
			"sqlite plain path",
			EngineSQLite,
			ConnParams{File: "/tmp/notes.sqlite"},
			"/tmp/notes.sqlite",
		},
		{
			"sqlite with options uses the file: URI form",
			EngineSQLite,
			ConnParams{File: "/tmp/notes.sqlite", Options: map[string]string{"_pragma": "busy_timeout(1000)"}},
			"file:/tmp/notes.sqlite?_pragma=busy_timeout%281000%29",
		},
		{
			"duckdb in-memory",
			EngineDuckDB,
			ConnParams{},
			"",
		},
		{
			"duckdb with options",
			EngineDuckDB,
			ConnParams{File: "/tmp/a.duckdb", Options: map[string]string{"access_mode": "read_only"}},
			"/tmp/a.duckdb?access_mode=read_only",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := BuildDSN(tc.engine, tc.params)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("BuildDSN = %q, want %q", got, tc.want)
			}
		})
	}
}

// Option order must not depend on map iteration order.
func TestBuildDSNOptionsAreStable(t *testing.T) {
	p := ConnParams{Host: "h", User: "u", Database: "d",
		Options: map[string]string{"a": "1", "b": "2", "c": "3", "d": "4"}}
	first, err := BuildDSN(EnginePostgres, p)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		got, err := BuildDSN(EnginePostgres, p)
		if err != nil {
			t.Fatal(err)
		}
		if got != first {
			t.Fatalf("DSN varies between calls: %q vs %q", got, first)
		}
	}
}

func TestBuildDSNUnknownEngine(t *testing.T) {
	if _, err := BuildDSN("oracle", ConnParams{}); err == nil {
		t.Fatal("expected an error for an unregistered engine")
	}
}

// The command log shows the DSN, so the password must be masked there.
func TestRedactDSNHidesThePassword(t *testing.T) {
	for _, engine := range []Engine{EnginePostgres, EngineMySQL, EngineMariaDB} {
		p := ConnParams{Host: "h", Port: 1234, User: "u", Password: "hunter2", Database: "d"}
		got, err := RedactDSN(engine, p)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(got, "hunter2") {
			t.Fatalf("%s: redacted DSN still leaks the password: %s", engine, got)
		}
		if !strings.Contains(got, PasswordMask) {
			t.Fatalf("%s: expected a mask in %s", engine, got)
		}
	}
}

func TestFileBasedAndDefaultPort(t *testing.T) {
	for _, e := range []Engine{EngineSQLite, EngineDuckDB} {
		if !FileBased(e) {
			t.Errorf("%s should be file based", e)
		}
		if DefaultPort(e) != 0 {
			t.Errorf("%s should have no default port", e)
		}
	}
	for e, want := range map[Engine]int{EngineMySQL: 3306, EngineMariaDB: 3306, EnginePostgres: 5432} {
		if FileBased(e) {
			t.Errorf("%s should not be file based", e)
		}
		if got := DefaultPort(e); got != want {
			t.Errorf("%s default port = %d, want %d", e, got, want)
		}
	}
}
