package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tempPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "session.json")
}

func TestLoadMissingFileIsNil(t *testing.T) {
	got, err := LoadFrom(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("LoadFrom() error = %v, want nil for a missing file", err)
	}
	if got != nil {
		t.Fatalf("LoadFrom() = %#v, want nil", got)
	}
}

func TestSaveThenLoadRoundTrips(t *testing.T) {
	path := tempPath(t)
	want := Session{
		Connection: "prod",
		Database:   "app",
		Table:      "users",
		Tab:        2,
		Row:        7,
		Col:        3,
	}
	if err := SaveTo(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || *got != want {
		t.Fatalf("LoadFrom() = %#v, want %#v", got, want)
	}
}

// The whole point of this file: it names a connection, never a password —
// AskPassword connections must still prompt on restore.
func TestSavedFileHasNoCredentials(t *testing.T) {
	path := tempPath(t)
	sess := Session{Connection: "prod", Database: "app", Table: "users", Tab: 1, Row: 2, Col: 1}
	if err := SaveTo(path, sess); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"password", "secret", "token", "key"} {
		if strings.Contains(strings.ToLower(string(raw)), field) {
			t.Fatalf("session file mentions %q, want no credential fields at all:\n%s", field, raw)
		}
	}
}

func TestSaveIsOwnerOnly(t *testing.T) {
	path := tempPath(t)
	if err := SaveTo(path, Session{Connection: "prod"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("session file mode = %o, want 600", perm)
	}
}

func TestLoadIgnoresCorruptFile(t *testing.T) {
	path := tempPath(t)
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom() error = %v, want a corrupt file to be ignored, not fail", err)
	}
	if got != nil {
		t.Fatalf("LoadFrom() = %#v, want nil for a corrupt file", got)
	}
}

func TestLoadIgnoresEmptyConnection(t *testing.T) {
	path := tempPath(t)
	if err := os.WriteFile(path, []byte(`{"database":"app","table":"users"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("LoadFrom() = %#v, want nil when the file names no connection", got)
	}
}

func TestSaveOverwritesCorruptFile(t *testing.T) {
	path := tempPath(t)
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	want := Session{Connection: "prod", Table: "users"}
	if err := SaveTo(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || *got != want {
		t.Fatalf("LoadFrom() after overwrite = %#v, want %#v", got, want)
	}
}

func TestDirHonoursXDGStateHome(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/tmp/state")
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("/tmp/state", AppDir); dir != want {
		t.Fatalf("Dir() = %q, want %q", dir, want)
	}
}

func TestPathHonoursXDGStateHome(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/tmp/state")
	got, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("/tmp/state", AppDir, FileName); got != want {
		t.Fatalf("Path() = %q, want %q", got, want)
	}
}
