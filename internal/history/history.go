// Package history persists the SQL statements lazysql has executed to
// ${XDG_STATE_HOME:-~/.local/state}/lazysql/history, so panel [4] survives
// a restart.
//
// The file is JSON Lines, oldest first: one self-describing record per
// line, appended in O(1) and readable with `tail`. A statement is stored
// verbatim, newlines included, because the editor reloads it as typed —
// JSON encoding is what keeps a multi-line statement on one line.
//
// The file is written owner-only: a statement can carry data that the
// config file deliberately never holds.
package history

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// AppDir is the lazysql subdirectory under the user state home.
	AppDir = "lazysql"
	// FileName is the history file inside AppDir.
	FileName = "history"
	// MaxEntries bounds the file. Loading compacts anything above it:
	// the history is a convenience, not an audit log, and the command
	// log already carries the full session.
	MaxEntries = 1000
)

// Entry is one executed statement.
type Entry struct {
	SQL string `json:"sql"`
	// Connection is the name of the connection profile the statement ran
	// against, so the panel can offer only the entries of the connection
	// that is currently active instead of mixing every connection's
	// history together. A connection name can be reused after a rename or
	// a delete-and-recreate, but there is no more stable identifier in
	// config.Connection to key on; ForConnection documents the trade-off.
	//
	// Empty on entries written before this field existed. Those are not
	// migrated or dropped — ForConnection shows them for every connection,
	// since there is no way to know which one they belonged to and hiding
	// them would silently discard history a restart-old file still has.
	Connection string `json:"connection,omitempty"`
	// Engine is the engine the statement ran against ("postgres",
	// "sqlite", …), kept because the same SQL is not portable between
	// them and the panel annotates each entry with it.
	Engine string    `json:"engine"`
	At     time.Time `json:"at"`
	// Database and Table narrow Connection to one relation. They are
	// empty on a statement — the editor's history is scoped by connection
	// alone — and set on a filter, where `/` on `orders` must recall what
	// was typed on `orders` and nothing else. An empty Database is
	// meaningful rather than missing: it is the pseudo-namespace the file
	// engines browse under. See filters.go.
	Database string `json:"database,omitempty"`
	Table    string `json:"table,omitempty"`
}

// ForConnection filters entries newest-first to those belonging to the
// given connection, plus any legacy entry with no Connection recorded
// (see Entry.Connection). The data grid's filter history keys the same
// way, one level finer and without the legacy leniency — see ForRelation
// in filters.go.
func ForConnection(entries []Entry, connection string) []Entry {
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if e.Connection == "" || e.Connection == connection {
			out = append(out, e)
		}
	}
	return out
}

// writeMu serializes writers. Appends run in tea.Cmd goroutines, so two
// statements finishing at once would otherwise interleave their lines.
var writeMu sync.Mutex

// ---------- file location ----------

// Dir returns the lazysql state directory, honouring XDG_STATE_HOME.
func Dir() (string, error) {
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, AppDir), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("history: locate home directory: %w", err)
	}
	return filepath.Join(home, ".local", "state", AppDir), nil
}

// Path returns the full path of the history file.
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, FileName), nil
}

// ---------- load ----------

// Load reads the history newest first. A missing file is not an error:
// a first run has no history.
func Load() ([]Entry, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	return LoadFrom(path)
}

// LoadFrom reads a history file from an explicit path and returns its
// entries newest first. A file grown past MaxEntries is compacted on the
// spot, which is the only moment lazysql knows it is too long; a failed
// compaction is not reported, because the entries were read fine.
//
// Unparsable lines are skipped rather than failing the load: a truncated
// last line after a crash must not cost the whole history.
func LoadFrom(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("history: read %s: %w", path, err)
	}
	defer f.Close()

	var oldestFirst []Entry
	sc := bufio.NewScanner(f)
	// A statement can be long; the default 64 KiB token limit is not
	// enough for a generated INSERT.
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil || strings.TrimSpace(e.SQL) == "" {
			continue
		}
		oldestFirst = append(oldestFirst, e)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("history: read %s: %w", path, err)
	}

	compact := len(oldestFirst) > MaxEntries
	if compact {
		oldestFirst = oldestFirst[len(oldestFirst)-MaxEntries:]
	}
	out := make([]Entry, len(oldestFirst))
	for i, e := range oldestFirst {
		out[len(out)-1-i] = e
	}
	if compact {
		_ = SaveTo(path, out)
	}
	return out, nil
}

// ---------- write ----------

// Append adds one entry to the end of the history file.
func Append(e Entry) error {
	path, err := Path()
	if err != nil {
		return err
	}
	return AppendTo(path, e)
}

// AppendTo adds one entry to an explicit path, creating the directory
// and the file if needed.
func AppendTo(path string, e Entry) error {
	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("history: encode entry: %w", err)
	}
	writeMu.Lock()
	defer writeMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("history: create %s: %w", filepath.Dir(path), err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("history: open %s: %w", path, err)
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return fmt.Errorf("history: write %s: %w", path, err)
	}
	return f.Close()
}

// Save rewrites the whole history from entries given newest first. It is
// what a deletion and a clear go through.
func Save(entries []Entry) error {
	path, err := Path()
	if err != nil {
		return err
	}
	return SaveTo(path, entries)
}

// SaveTo rewrites the history at an explicit path. The write is atomic
// (temp file + rename) so an interrupted delete cannot truncate the
// history it was pruning.
func SaveTo(path string, entries []Entry) error {
	writeMu.Lock()
	defer writeMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("history: create %s: %w", filepath.Dir(path), err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".history-*")
	if err != nil {
		return fmt.Errorf("history: create temp file: %w", err)
	}
	defer os.Remove(tmp.Name())

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("history: chmod temp file: %w", err)
	}
	w := bufio.NewWriter(tmp)
	// entries arrive newest first; the file is oldest first.
	for i := len(entries) - 1; i >= 0; i-- {
		line, err := json.Marshal(entries[i])
		if err != nil {
			tmp.Close()
			return fmt.Errorf("history: encode entry: %w", err)
		}
		if _, err := w.Write(append(line, '\n')); err != nil {
			tmp.Close()
			return fmt.Errorf("history: write temp file: %w", err)
		}
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		return fmt.Errorf("history: write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("history: close temp file: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("history: write %s: %w", path, err)
	}
	return nil
}
