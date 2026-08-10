// Package session persists lazysql's last-browsed location — connection,
// database, table, active main tab and grid cursor — to
// ${XDG_STATE_HOME:-~/.local/state}/lazysql/session.json, so a restart can
// pick up where the previous one left off.
//
// The file never contains credentials: the connection is referenced by
// name only, the same split history and config use between "what to dial"
// and "how to authenticate."
package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	// AppDir is the lazysql subdirectory under the user state home.
	AppDir = "lazysql"
	// FileName is the session file inside AppDir.
	FileName = "session.json"
)

// Session is the last place the user was browsing.
type Session struct {
	Connection string `json:"connection"`
	Database   string `json:"database"`
	Table      string `json:"table"`
	// Tab is the main view's selected tab (ui.mainTab), stored as a plain
	// int so this package does not depend on internal/ui.
	Tab int `json:"tab"`
	// Row and Col are the grid cursor. A restore clamps them to whatever
	// the table actually has, so a stale value never crashes anything —
	// it is a best-effort restore, not a guarantee.
	Row int `json:"row"`
	Col int `json:"col"`
}

// ---------- file location ----------

// Dir returns the lazysql state directory, honouring XDG_STATE_HOME.
func Dir() (string, error) {
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, AppDir), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("session: locate home directory: %w", err)
	}
	return filepath.Join(home, ".local", "state", AppDir), nil
}

// Path returns the full path of the session file.
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, FileName), nil
}

// ---------- load ----------

// Load reads the saved session. A missing file is not an error: a first
// run has no session to restore.
func Load() (*Session, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	return LoadFrom(path)
}

// LoadFrom reads a session file from an explicit path. A missing or
// corrupt file yields (nil, nil) rather than an error: a startup restore
// degrades to the normal start screen instead of failing, and the next
// Save overwrites whatever was unreadable.
func LoadFrom(path string) (*Session, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("session: read %s: %w", path, err)
	}
	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, nil
	}
	if s.Connection == "" {
		return nil, nil
	}
	return &s, nil
}

// ---------- save ----------

// Save writes the session file, creating the directory if needed.
func Save(s Session) error {
	path, err := Path()
	if err != nil {
		return err
	}
	return SaveTo(path, s)
}

// SaveTo writes the session to an explicit path. The write is atomic
// (temp file + rename) so a crash cannot leave a truncated file behind,
// and both directory and file are owner-only — same posture as history,
// even though this file only ever holds a connection name.
func SaveTo(path string, s Session) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("session: encode: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("session: create %s: %w", filepath.Dir(path), err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".session-*")
	if err != nil {
		return fmt.Errorf("session: create temp file: %w", err)
	}
	defer os.Remove(tmp.Name())

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("session: chmod temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("session: write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("session: close temp file: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("session: write %s: %w", path, err)
	}
	return nil
}
