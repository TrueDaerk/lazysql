package dump

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lazysql/internal/db"
)

// found pretends every named binary is installed at /usr/bin/<name>.
func found(names ...string) func(string) (string, error) {
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	return func(name string) (string, error) {
		if set[name] {
			return "/usr/bin/" + name, nil
		}
		return "", errors.New("not found")
	}
}

func pgRequest(action Action) Request {
	return Request{
		Action: action, Engine: db.EnginePostgres,
		Host: "db.example.com", Port: 5432, User: "app", Database: "shop",
		Password: "hunter2", Path: "/tmp/shop.sql",
	}.WithLookPath(found("pg_dump", "psql"))
}

func myRequest(action Action, engine db.Engine) Request {
	return Request{
		Action: action, Engine: engine,
		Host: "db.example.com", Port: 3306, User: "root", Database: "shop",
		Password: "hunter2", Path: "/tmp/shop.sql",
	}.WithLookPath(found("mysqldump", "mysql", "mariadb-dump", "mariadb"))
}

// The whole point of the package: nothing that carries the password ends
// up somewhere another process can read it.
func TestNoPasswordInArgvEnvOrPreview(t *testing.T) {
	cases := []struct {
		name string
		req  Request
	}{
		{"pg dump", pgRequest(Dump)},
		{"pg restore", pgRequest(Restore)},
		{"mysql dump", myRequest(Dump, db.EngineMySQL)},
		{"mysql restore", myRequest(Restore, db.EngineMySQL)},
		{"mariadb dump", myRequest(Dump, db.EngineMariaDB)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, err := Build(tc.req)
			if err != nil {
				t.Fatal(err)
			}
			defer cmd.Cleanup()

			for _, a := range cmd.Args {
				if strings.Contains(a, "hunter2") {
					t.Fatalf("password in argv: %q", a)
				}
			}
			for _, e := range cmd.Env {
				if strings.Contains(e, "hunter2") {
					t.Fatalf("password in env: %q", e)
				}
			}
			if s := cmd.String(); strings.Contains(s, "hunter2") {
				t.Fatalf("password in rendered command: %q", s)
			}

			prev, err := Preview(tc.req)
			if err != nil {
				t.Fatal(err)
			}
			if s := prev.String(); strings.Contains(s, "hunter2") {
				t.Fatalf("password in preview: %q", s)
			}
			// A preview creates nothing; only the real build writes a
			// credential file.
			if strings.Contains(prev.String(), os.TempDir()) {
				t.Fatalf("preview leaks the credential path: %q", prev.String())
			}
			if !strings.Contains(prev.String(), CredPlaceholder) {
				t.Fatalf("preview does not name the credential file: %q", prev.String())
			}
		})
	}
}

// The credential file is what the password travels in, so its mode has to
// be 0600 — both engines refuse a wider one — and it has to be gone as
// soon as the run is over.
func TestCredentialFileIsPrivateAndRemoved(t *testing.T) {
	for _, req := range []Request{pgRequest(Dump), myRequest(Dump, db.EngineMySQL)} {
		cmd, err := Build(req)
		if err != nil {
			t.Fatal(err)
		}
		if cmd.credFile == "" {
			t.Fatalf("%s: no credential file written", req.Engine)
		}
		info, err := os.Stat(cmd.credFile)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("%s: credential file mode = %o, want 600", req.Engine, perm)
		}
		data, err := os.ReadFile(cmd.credFile)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "hunter2") {
			t.Fatalf("%s: credential file does not carry the password: %q", req.Engine, data)
		}
		path := cmd.credFile
		if err := cmd.Cleanup(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s: credential file survived Cleanup", req.Engine)
		}
		// A second Cleanup is a no-op, not an error.
		if err := cmd.Cleanup(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPostgresDumpArgv(t *testing.T) {
	req := pgRequest(Dump)
	req.Schema = "reporting"
	req.ExtraArgs = []string{"--data-only"}
	cmd, err := Build(req)
	if err != nil {
		t.Fatal(err)
	}
	defer cmd.Cleanup()

	if cmd.Name != "pg_dump" {
		t.Fatalf("binary = %q, want pg_dump", cmd.Name)
	}
	want := []string{
		"--host=db.example.com", "--port=5432", "--dbname=shop", "--no-password",
		"--username=app", "--schema=reporting", "--file=/tmp/shop.sql", "--data-only",
	}
	if strings.Join(cmd.Args, " ") != strings.Join(want, " ") {
		t.Fatalf("args =\n%v\nwant\n%v", cmd.Args, want)
	}
	if len(cmd.Env) != 1 || !strings.HasPrefix(cmd.Env[0], "PGPASSFILE=") {
		t.Fatalf("env = %v, want a single PGPASSFILE entry", cmd.Env)
	}
}

// psql must stop on the first failed statement, or a broken restore exits
// 0 and looks like it worked.
func TestPostgresRestoreStopsOnError(t *testing.T) {
	cmd, err := Build(pgRequest(Restore))
	if err != nil {
		t.Fatal(err)
	}
	defer cmd.Cleanup()

	if cmd.Name != "psql" {
		t.Fatalf("binary = %q, want psql", cmd.Name)
	}
	joined := strings.Join(cmd.Args, " ")
	for _, want := range []string{"--set=ON_ERROR_STOP=1", "--file=/tmp/shop.sql", "--no-password"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args %v missing %q", cmd.Args, want)
		}
	}
}

// mysqldump reads --defaults-extra-file only when it comes first.
func TestMySQLDefaultsFileIsFirstArgument(t *testing.T) {
	for _, engine := range []db.Engine{db.EngineMySQL, db.EngineMariaDB} {
		cmd, err := Build(myRequest(Dump, engine))
		if err != nil {
			t.Fatal(err)
		}
		defer cmd.Cleanup()
		if len(cmd.Args) == 0 || !strings.HasPrefix(cmd.Args[0], "--defaults-extra-file=") {
			t.Fatalf("%s: args[0] = %q, want --defaults-extra-file=…", engine, cmd.Args)
		}
	}
}

func TestMySQLDumpAndRestoreShape(t *testing.T) {
	dumpCmd, err := Build(myRequest(Dump, db.EngineMySQL))
	if err != nil {
		t.Fatal(err)
	}
	defer dumpCmd.Cleanup()
	if dumpCmd.Name != "mysqldump" {
		t.Fatalf("binary = %q, want mysqldump", dumpCmd.Name)
	}
	if got := dumpCmd.Args[len(dumpCmd.Args)-1]; got != "shop" {
		t.Fatalf("last arg = %q, want the database name", got)
	}
	if !strings.Contains(strings.Join(dumpCmd.Args, " "), "--result-file=/tmp/shop.sql") {
		t.Fatalf("args %v do not name the output file", dumpCmd.Args)
	}
	if dumpCmd.StdinPath != "" {
		t.Fatalf("dump should not read stdin, got %q", dumpCmd.StdinPath)
	}

	restoreCmd, err := Build(myRequest(Restore, db.EngineMySQL))
	if err != nil {
		t.Fatal(err)
	}
	defer restoreCmd.Cleanup()
	if restoreCmd.Name != "mysql" {
		t.Fatalf("binary = %q, want mysql", restoreCmd.Name)
	}
	// The client has no --file of its own; the dump arrives on stdin.
	if restoreCmd.StdinPath != "/tmp/shop.sql" {
		t.Fatalf("stdin = %q, want the dump file", restoreCmd.StdinPath)
	}
}

// MariaDB prefers its own binary names but still accepts the compatibility
// ones when that is all that is installed.
func TestMariaDBPrefersNativeBinary(t *testing.T) {
	req := myRequest(Dump, db.EngineMariaDB)
	cmd, err := Build(req)
	if err != nil {
		t.Fatal(err)
	}
	defer cmd.Cleanup()
	if cmd.Name != "mariadb-dump" {
		t.Fatalf("binary = %q, want mariadb-dump", cmd.Name)
	}

	only := myRequest(Dump, db.EngineMariaDB).WithLookPath(found("mysqldump"))
	cmd2, err := Build(only)
	if err != nil {
		t.Fatal(err)
	}
	defer cmd2.Cleanup()
	if cmd2.Name != "mysqldump" {
		t.Fatalf("fallback binary = %q, want mysqldump", cmd2.Name)
	}
}

// A missing binary is an instructive error naming what to install, never a
// panic and never a half-built command.
func TestMissingToolNamesTheBinary(t *testing.T) {
	req := pgRequest(Dump).WithLookPath(found())
	_, err := Build(req)
	var missing *ErrMissingTool
	if !errors.As(err, &missing) {
		t.Fatalf("err = %v, want *ErrMissingTool", err)
	}
	if !strings.Contains(err.Error(), "pg_dump") || !strings.Contains(err.Error(), "PATH") {
		t.Fatalf("error %q names neither the binary nor PATH", err)
	}

	_, err = Build(myRequest(Restore, db.EngineMySQL).WithLookPath(found()))
	if err == nil {
		t.Fatal("want an error when neither mysql nor mariadb is installed")
	}
	for _, want := range []string{"mysql", "mariadb"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not name %q", err, want)
		}
	}
}

// A failed build must not leave the credential file behind.
func TestBuildRemovesCredentialFileOnFailure(t *testing.T) {
	before := tempEntries(t)
	req := pgRequest(Dump).WithLookPath(found())
	if _, err := Build(req); err == nil {
		t.Fatal("want a missing-tool error")
	}
	after := tempEntries(t)
	for name := range after {
		if !before[name] && strings.HasPrefix(name, "lazysql-") {
			t.Fatalf("leaked credential file %q", name)
		}
	}
}

func tempEntries(t *testing.T) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, e := range entries {
		out[e.Name()] = true
	}
	return out
}

// The file engines need no external tool; their jobs are statements the
// caller runs through the Driver, or a plain file copy.
func TestFileEngineJobs(t *testing.T) {
	sqliteDump, err := Build(Request{
		Action: Dump, Engine: db.EngineSQLite,
		File: "/data/app.db", Path: "/tmp/backup's.db",
	})
	if err != nil {
		t.Fatal(err)
	}
	if sqliteDump.Kind != KindSQL {
		t.Fatalf("kind = %v, want KindSQL", sqliteDump.Kind)
	}
	// The path is a SQL literal, so a quote in it must be doubled rather
	// than ending the string.
	if want := `VACUUM INTO '/tmp/backup''s.db'`; sqliteDump.SQL[0] != want {
		t.Fatalf("sql = %q, want %q", sqliteDump.SQL[0], want)
	}

	sqliteRestore, err := Build(Request{
		Action: Restore, Engine: db.EngineSQLite,
		File: "/data/app.db", Path: "/tmp/backup.db",
	})
	if err != nil {
		t.Fatal(err)
	}
	if sqliteRestore.Kind != KindCopy ||
		sqliteRestore.CopyFrom != "/tmp/backup.db" || sqliteRestore.CopyTo != "/data/app.db" {
		t.Fatalf("restore = %+v, want a copy of the dump over the database file", sqliteRestore)
	}

	duckDump, err := Build(Request{
		Action: Dump, Engine: db.EngineDuckDB, File: "/data/app.duckdb", Path: "/tmp/export",
	})
	if err != nil {
		t.Fatal(err)
	}
	if duckDump.SQL[0] != `EXPORT DATABASE '/tmp/export'` {
		t.Fatalf("sql = %q", duckDump.SQL[0])
	}
	duckRestore, err := Build(Request{
		Action: Restore, Engine: db.EngineDuckDB, File: "/data/app.duckdb", Path: "/tmp/export",
	})
	if err != nil {
		t.Fatal(err)
	}
	if duckRestore.SQL[0] != `IMPORT DATABASE '/tmp/export'` {
		t.Fatalf("sql = %q", duckRestore.SQL[0])
	}
}

// A tunnelled connection is expressed by pointing the request at the local
// forward, which must reach the credential file too: libpq matches the
// .pgpass host field against the host it connected to.
func TestTunnelEndpointReachesCredentialFile(t *testing.T) {
	req := pgRequest(Dump)
	req.Host, req.Port = "127.0.0.1", 54321
	cmd, err := Build(req)
	if err != nil {
		t.Fatal(err)
	}
	defer cmd.Cleanup()

	if !strings.Contains(strings.Join(cmd.Args, " "), "--host=127.0.0.1 --port=54321") {
		t.Fatalf("args %v do not point at the forward", cmd.Args)
	}
	data, err := os.ReadFile(cmd.credFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "127.0.0.1:54321:shop:app:hunter2") {
		t.Fatalf("pgpass = %q, want the forwarded endpoint", data)
	}
}

func TestValidateRejectsIncompleteRequests(t *testing.T) {
	cases := []struct {
		name string
		req  Request
		want string
	}{
		{"no path", Request{Engine: db.EnginePostgres, Host: "h", Database: "d"}, "no output path"},
		{"no path on restore",
			Request{Action: Restore, Engine: db.EnginePostgres, Host: "h", Database: "d"}, "no input path"},
		{"no host", Request{Engine: db.EnginePostgres, Database: "d", Path: "x"}, "no host"},
		{"no database", Request{Engine: db.EnginePostgres, Host: "h", Path: "x"}, "names no database"},
		{"no file", Request{Engine: db.EngineSQLite, Path: "x"}, "no database file"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Build(tc.req)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

func TestPreviewRendersTheWholeCommand(t *testing.T) {
	prev, err := Preview(pgRequest(Dump))
	if err != nil {
		t.Fatal(err)
	}
	s := prev.String()
	for _, want := range []string{"PGPASSFILE=" + CredPlaceholder, "pg_dump", "--dbname=shop"} {
		if !strings.Contains(s, want) {
			t.Fatalf("preview %q missing %q", s, want)
		}
	}

	restore, err := Preview(myRequest(Restore, db.EngineMySQL))
	if err != nil {
		t.Fatal(err)
	}
	// The stdin redirect is part of what the command does, so the preview
	// has to show it.
	if !strings.Contains(restore.String(), "< /tmp/shop.sql") {
		t.Fatalf("preview %q does not show the stdin redirect", restore.String())
	}
}

func TestShellQuoteMakesArgumentsUnambiguous(t *testing.T) {
	cases := map[string]string{
		"plain":         "plain",
		"":              "''",
		"two words":     "'two words'",
		"it's":          `'it'\''s'`,
		"--file=/a/b.c": "--file=/a/b.c",
	}
	for in, want := range cases {
		if got := shellQuote(in); got != want {
			t.Fatalf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPgpassEscaping(t *testing.T) {
	path, err := writePgpass("h:1", 5432, `d\b`, "u", "pass:word")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Every literal colon and backslash inside a field is escaped, so the
	// five fields still parse as five.
	if want := `h\:1:5432:d\\b:u:pass\:word` + "\n"; string(data) != want {
		t.Fatalf("pgpass = %q, want %q", data, want)
	}
}

func TestMyCnfEscaping(t *testing.T) {
	path, err := writeMyCnf(`pa"ss\word#1`)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "[client]\npassword=\"pa\\\"ss\\\\word#1\"\n"
	if string(data) != want {
		t.Fatalf("my.cnf = %q, want %q", data, want)
	}
}

func TestDefaultExtension(t *testing.T) {
	if got := DefaultExtension(db.EnginePostgres); got != ".sql" {
		t.Fatalf("postgres extension = %q", got)
	}
	// DuckDB's EXPORT DATABASE writes a directory, so there is no suffix.
	if got := DefaultExtension(db.EngineDuckDB); got != "" {
		t.Fatalf("duckdb extension = %q, want none", got)
	}
}

func TestEndpoint(t *testing.T) {
	if got := Endpoint("db.example.com", 5432); got != "db.example.com:5432" {
		t.Fatalf("endpoint = %q", got)
	}
	if got := Endpoint("::1", 5432); got != "[::1]:5432" {
		t.Fatalf("endpoint = %q, want the IPv6 literal bracketed", got)
	}
}

// A dump written into a temp directory is what the integration tests in
// internal/ui exercise end to end; here only the path plumbing is checked.
func TestBuildAcceptsRelativeLookingPaths(t *testing.T) {
	dir := t.TempDir()
	req := Request{
		Action: Dump, Engine: db.EngineSQLite,
		File: filepath.Join(dir, "app.db"), Path: filepath.Join(dir, "out.db"),
	}
	cmd, err := Build(req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cmd.SQL[0], dir) {
		t.Fatalf("sql = %q, want it to name %q", cmd.SQL[0], dir)
	}
}
