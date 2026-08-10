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

// The read-only flag round trips, is written only when set, and a config
// from before the flag existed still loads as read-write.
func TestReadOnlyRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := &Config{Connections: []Connection{
		{Name: "prod", Engine: db.EnginePostgres, Host: "db.example", Port: 5432, ReadOnly: true},
		{Name: "dev", Engine: db.EngineSQLite, File: "/tmp/dev.sqlite"},
	}}
	if err := cfg.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "read_only = true") {
		t.Fatalf("read_only not written:\n%s", raw)
	}
	if strings.Count(string(raw), "read_only") != 1 {
		t.Fatalf("read_only written for the read-write profile too:\n%s", raw)
	}

	back, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if prod, _ := back.Find("prod"); !prod.ReadOnly {
		t.Fatal("read_only did not round trip")
	}
	if dev, _ := back.Find("dev"); dev.ReadOnly {
		t.Fatal("a profile without read_only loaded as read-only")
	}
}

// A config file written before the flag existed loads unchanged, and the
// parameters it produces ask for no engine-level read-only mode.
func TestConfigWithoutReadOnlyLoadsReadWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	old := "[[connections]]\nname = \"legacy\"\nengine = \"sqlite\"\nfile = \"/tmp/legacy.db\"\n"
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	c := cfg.Connections[0]
	if c.ReadOnly {
		t.Fatal("an absent read_only key loaded as read-only")
	}
	if c.Params("").ReadOnly {
		t.Fatal("ConnParams asked for a read-only session")
	}
}

// Params carries the flag into the DSN layer, and Clone keeps it.
func TestReadOnlyReachesParamsAndClone(t *testing.T) {
	cfg := &Config{Connections: []Connection{
		{Name: "prod", Engine: db.EngineSQLite, File: "/tmp/p.db", ReadOnly: true},
	}}
	if !cfg.Connections[0].Params("").ReadOnly {
		t.Fatal("Params dropped the read-only flag")
	}
	if !cfg.Clone().Connections[0].ReadOnly {
		t.Fatal("Clone dropped the read-only flag")
	}
}

// The color tag round trips, is written only when set, and a config from
// before the field existed still loads with no tag.
func TestColorRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := &Config{Connections: []Connection{
		{Name: "prod", Engine: db.EnginePostgres, Host: "db.example", Port: 5432, Color: "red"},
		{Name: "dev", Engine: db.EngineSQLite, File: "/tmp/dev.sqlite"},
	}}
	if err := cfg.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `color = "red"`) {
		t.Fatalf("color not written:\n%s", raw)
	}
	if strings.Count(string(raw), "color") != 1 {
		t.Fatalf("color written for the untagged profile too:\n%s", raw)
	}

	back, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if prod, _ := back.Find("prod"); prod.Color != "red" {
		t.Fatalf("color did not round trip: got %q", prod.Color)
	}
	if dev, _ := back.Find("dev"); dev.Color != "" {
		t.Fatalf("a profile without color loaded with one: %q", dev.Color)
	}
}

// A config file written before the color field existed loads unchanged.
func TestConfigWithoutColorLoadsUntagged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	old := "[[connections]]\nname = \"legacy\"\nengine = \"sqlite\"\nfile = \"/tmp/legacy.db\"\n"
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Connections[0].Color != "" {
		t.Fatal("an absent color key loaded with a tag")
	}
}

// Clone keeps the color tag — it is a plain string field, but this guards
// against a future refactor that turns it into something Clone must deep-copy.
func TestColorReachesClone(t *testing.T) {
	cfg := &Config{Connections: []Connection{
		{Name: "prod", Engine: db.EngineSQLite, File: "/tmp/p.db", Color: "#ff8800"},
	}}
	if got := cfg.Clone().Connections[0].Color; got != "#ff8800" {
		t.Fatalf("Clone dropped the color tag: got %q", got)
	}
}

// An absent restore_session key — every config written before the field
// existed, and a fresh default one — must default to restoring.
func TestRestoreSessionDefaultsEnabled(t *testing.T) {
	cfg := &Config{}
	if !cfg.RestoreSessionEnabled() {
		t.Fatal("RestoreSessionEnabled() = false with the key absent, want true")
	}
}

func TestRestoreSessionRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	off := false
	cfg := &Config{RestoreSession: &off}
	if err := cfg.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "restore_session = false") {
		t.Fatalf("restore_session not written:\n%s", raw)
	}
	back, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if back.RestoreSessionEnabled() {
		t.Fatal("RestoreSessionEnabled() = true after loading restore_session = false")
	}
}

// A config file without the key at all — the common case — never writes it
// back out, and Clone does not share the pointer with the original.
func TestRestoreSessionAbsentStaysAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := &Config{Connections: []Connection{
		{Name: "dev", Engine: db.EngineSQLite, File: "/tmp/dev.sqlite"},
	}}
	if err := cfg.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "restore_session") {
		t.Fatalf("restore_session written despite being unset:\n%s", raw)
	}

	on := true
	cfg.RestoreSession = &on
	clone := cfg.Clone()
	*cfg.RestoreSession = false
	if !clone.RestoreSessionEnabled() {
		t.Fatal("Clone shared the RestoreSession pointer with the original")
	}
}

func TestStatePathHonoursXDGConfigHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg")
	got, err := StatePath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("/tmp/xdg", AppDir, StateFileName); got != want {
		t.Fatalf("StatePath() = %q, want %q", got, want)
	}
}

func TestStateRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.toml")
	st := &State{ScreenMode: "full"}
	if err := st.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	back := LoadStateFrom(path)
	if back.ScreenMode != "full" {
		t.Fatalf("ScreenMode = %q, want %q", back.ScreenMode, "full")
	}
}

// State is disposable: a missing file must never error, just yield defaults.
func TestStateMissingFileLoadsZeroValue(t *testing.T) {
	back := LoadStateFrom(filepath.Join(t.TempDir(), "absent.toml"))
	if back.ScreenMode != "" {
		t.Fatalf("ScreenMode = %q, want empty", back.ScreenMode)
	}
}

// A corrupt state file must degrade to defaults too, never block startup.
func TestStateCorruptFileLoadsZeroValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.toml")
	if err := os.WriteFile(path, []byte("not valid toml [["), 0o600); err != nil {
		t.Fatal(err)
	}
	back := LoadStateFrom(path)
	if back.ScreenMode != "" {
		t.Fatalf("ScreenMode = %q, want empty", back.ScreenMode)
	}
}

func TestStateSaveNeverModifiesConfigFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, FileName)
	cfg := &Config{Connections: []Connection{
		{Name: "dev", Engine: db.EngineSQLite, File: "/tmp/dev.sqlite"},
	}}
	if err := cfg.SaveTo(cfgPath); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	statePath := filepath.Join(dir, StateFileName)
	st := &State{ScreenMode: "half"}
	if err := st.SaveTo(statePath); err != nil {
		t.Fatal(err)
	}

	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("saving state modified the config file")
	}
}
