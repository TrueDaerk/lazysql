package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lazysql/internal/db"
)

func TestPathHonoursXDGConfigHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg")
	got, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("/tmp/xdg", AppDir, FileName); got != want {
		t.Fatalf("Path() = %q, want %q", got, want)
	}
}

func TestMissingFileLoadsEmptyConfig(t *testing.T) {
	cfg, err := LoadFrom(filepath.Join(t.TempDir(), "absent.toml"))
	if err != nil {
		t.Fatalf("missing config should not error: %v", err)
	}
	if len(cfg.Connections) != 0 {
		t.Fatalf("connections = %v, want none", cfg.Connections)
	}
}

// Connections must survive a restart, and the file must never hold a secret.
func TestRoundTripKeepsProfilesAndNoSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := &Config{Connections: []Connection{
		{Name: "pg", Engine: db.EnginePostgres, Host: "db.example", Port: 5433, User: "app",
			Database: "app_dev", Options: map[string]string{"sslmode": "require"}},
		{Name: "notes", Engine: db.EngineSQLite, File: "/tmp/notes.sqlite"},
		{Name: "asks", Engine: db.EngineMySQL, Host: "localhost", Port: 3306, AskPassword: true},
	}}
	if err := cfg.SaveTo(path); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(raw)), "password") &&
		!strings.Contains(string(raw), "ask_password") {
		t.Fatalf("config file mentions a password:\n%s", raw)
	}
	for _, banned := range []string{"passwd", "secret"} {
		if strings.Contains(strings.ToLower(string(raw)), banned) {
			t.Fatalf("config file contains %q:\n%s", banned, raw)
		}
	}

	back, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Connections) != 3 {
		t.Fatalf("loaded %d connections, want 3", len(back.Connections))
	}
	pg, ok := back.Find("pg")
	if !ok {
		t.Fatal("pg profile missing after reload")
	}
	if pg.Port != 5433 || pg.Options["sslmode"] != "require" || pg.Database != "app_dev" {
		t.Fatalf("pg = %+v, fields did not round trip", pg)
	}
	if asks, _ := back.Find("asks"); !asks.AskPassword {
		t.Fatal("ask_password did not round trip")
	}
}

func TestSavedFileIsOwnerOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.toml")
	cfg := &Config{Connections: []Connection{{Name: "x", Engine: db.EngineSQLite, File: "x.db"}}}
	if err := cfg.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("config file mode = %o, want 600", perm)
	}
}

func TestMissingPortFallsBackToEngineDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[[connections]]\nname = \"pg\"\nengine = \"postgres\"\nhost = \"h\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Connections[0].Port; got != 5432 {
		t.Fatalf("port = %d, want the postgres default 5432", got)
	}
}

func TestValidateRejectsIncompleteProfiles(t *testing.T) {
	cases := []struct {
		name string
		c    Connection
		want string
	}{
		{"no name", Connection{Engine: db.EngineSQLite, File: "x"}, "name"},
		{"bad engine", Connection{Name: "a", Engine: "oracle"}, "engine"},
		{"no host", Connection{Name: "a", Engine: db.EnginePostgres}, "host"},
		{"bad port", Connection{Name: "a", Engine: db.EnginePostgres, Host: "h", Port: 70000}, "port"},
		{"no sqlite file", Connection{Name: "a", Engine: db.EngineSQLite}, "file"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.c.Validate()
			if err == nil {
				t.Fatal("expected a validation error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
	// DuckDB with no file is legitimate: that is an in-memory database.
	if err := (Connection{Name: "mem", Engine: db.EngineDuckDB}).Validate(); err != nil {
		t.Fatalf("in-memory duckdb rejected: %v", err)
	}
}

func TestUpsertRenamesAndRejectsCollisions(t *testing.T) {
	cfg := &Config{}
	a := Connection{Name: "a", Engine: db.EngineSQLite, File: "a.db"}
	b := Connection{Name: "b", Engine: db.EngineSQLite, File: "b.db"}
	if err := cfg.Upsert("", a); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Upsert("", b); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Upsert("", a); err == nil {
		t.Fatal("expected a duplicate-name error")
	}
	renamed := a
	renamed.Name = "c"
	if err := cfg.Upsert("a", renamed); err != nil {
		t.Fatal(err)
	}
	if got := cfg.Names(); len(got) != 2 || got[0] != "c" {
		t.Fatalf("names = %v, want the rename in place", got)
	}
	if err := cfg.Upsert("c", Connection{Name: "b", Engine: db.EngineSQLite, File: "x"}); err == nil {
		t.Fatal("renaming onto an existing name should fail")
	}
}

func TestRemoveAndClone(t *testing.T) {
	cfg := &Config{Connections: []Connection{
		{Name: "a", Engine: db.EngineSQLite, File: "a.db", Options: map[string]string{"k": "v"}},
	}}
	clone := cfg.Clone()
	clone.Connections[0].Options["k"] = "changed"
	if cfg.Connections[0].Options["k"] != "v" {
		t.Fatal("Clone shares its options map with the original")
	}
	if !cfg.Remove("a") || cfg.Remove("a") {
		t.Fatal("Remove should succeed once and then report a miss")
	}
	if len(clone.Connections) != 1 {
		t.Fatal("removing from the original mutated the clone")
	}
}

// A clone that shares the Keys/Theme maps with the original would let a
// connection save silently corrupt the user's hand-edited [keys]/[theme]
// sections the next time SaveTo re-encodes a mutated clone.
func TestCloneDeepCopiesKeysAndTheme(t *testing.T) {
	cfg := &Config{
		Keys:  map[string]string{"quit": "q"},
		Theme: map[string]string{"theme": "light"},
	}
	clone := cfg.Clone()
	clone.Keys["quit"] = "x"
	clone.Theme["theme"] = "default"
	if cfg.Keys["quit"] != "q" {
		t.Fatal("Clone shares its Keys map with the original")
	}
	if cfg.Theme["theme"] != "light" {
		t.Fatal("Clone shares its Theme map with the original")
	}
}

func TestKeysAndThemeSurviveSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	cfg := &Config{
		Connections: []Connection{{Name: "a", Engine: db.EngineSQLite, File: "a.db"}},
		Keys:        map[string]string{"quit": "x", "edit-cell": "e"},
		Theme:       map[string]string{"theme": "light", "border-focused": "#00ff00"},
	}
	if err := cfg.SaveTo(path); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	got, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if got.Keys["quit"] != "x" || got.Keys["edit-cell"] != "e" {
		t.Fatalf("Keys did not round-trip: %+v", got.Keys)
	}
	if got.Theme["theme"] != "light" || got.Theme["border-focused"] != "#00ff00" {
		t.Fatalf("Theme did not round-trip: %+v", got.Theme)
	}
}

// Saving connections (e.g. after adding one through the UI) must not drop a
// [keys]/[theme] section the user hand-edited into the file.
func TestSavingConnectionsPreservesKeysAndTheme(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	cfg := &Config{Keys: map[string]string{"quit": "x"}}
	if err := cfg.SaveTo(path); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	loaded, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	clone := loaded.Clone()
	if err := clone.Upsert("", Connection{Name: "a", Engine: db.EngineSQLite, File: "a.db"}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := clone.SaveTo(path); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	reloaded, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if reloaded.Keys["quit"] != "x" {
		t.Fatalf("saving a connection dropped the [keys] section: %+v", reloaded.Keys)
	}
}
