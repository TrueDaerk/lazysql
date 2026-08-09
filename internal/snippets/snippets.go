// Package snippets persists named, reusable SQL statements to
// ${XDG_STATE_HOME:-~/.local/state}/lazysql/snippets, next to the query
// history it complements: the history is chronological and self-pruning,
// a snippet is kept until it is deleted and is found by its name.
//
// The file is JSON Lines, one record per line, so a statement keeps its
// newlines without the file losing its line orientation — the same
// format and the same reasoning as internal/history. Unlike the history
// it is always rewritten as a whole: a save may replace an existing name
// and the list is held sorted, neither of which an append can express.
//
// The file is written owner-only: a snippet is SQL text the user chose
// to keep, and can name schemas and values the config file never holds.
// Credentials are never part of a snippet — only the statement is
// stored.
package snippets

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// AppDir is the lazysql subdirectory under the user state home.
	AppDir = "lazysql"
	// FileName is the snippet file inside AppDir.
	FileName = "snippets"
)

// Snippet is one named statement.
type Snippet struct {
	Name string `json:"name"`
	SQL  string `json:"sql"`
	// Engine is the engine the statement was saved from ("postgres",
	// "sqlite", …), empty when it was saved while disconnected. It is
	// advisory: a snippet stays runnable everywhere, the pane just
	// annotates where it came from.
	Engine    string    `json:"engine,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// writeMu serializes writers, which run in tea.Cmd goroutines.
var writeMu sync.Mutex

// ---------- file location ----------

// Dir returns the lazysql state directory, honouring XDG_STATE_HOME.
func Dir() (string, error) {
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, AppDir), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("snippets: locate home directory: %w", err)
	}
	return filepath.Join(home, ".local", "state", AppDir), nil
}

// Path returns the full path of the snippet file.
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, FileName), nil
}

// ---------- the in-memory list ----------

// SameName reports whether two snippet names denote the same snippet.
// Names are compared case-insensitively after trimming, so "Recent
// orders" cannot end up next to "recent orders" as two entries the eye
// reads as one.
func SameName(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

// Find returns the snippet stored under name, if any.
func Find(list []Snippet, name string) (Snippet, bool) {
	for _, s := range list {
		if SameName(s.Name, name) {
			return s, true
		}
	}
	return Snippet{}, false
}

// Put inserts s into list, replacing an entry of the same name, and
// returns the new list sorted by name. replaced reports whether a
// snippet was overwritten — the caller has already confirmed it, this
// is what its log line reads.
//
// The input list is not mutated: the model holds it while a save runs.
func Put(list []Snippet, s Snippet) (out []Snippet, replaced bool) {
	s.Name = strings.TrimSpace(s.Name)
	out = make([]Snippet, 0, len(list)+1)
	for _, e := range list {
		if SameName(e.Name, s.Name) {
			replaced = true
			continue
		}
		out = append(out, e)
	}
	out = append(out, s)
	Sort(out)
	return out, replaced
}

// Delete removes the snippet stored under name and reports whether one
// was there. The input list is not mutated.
func Delete(list []Snippet, name string) (out []Snippet, deleted bool) {
	out = make([]Snippet, 0, len(list))
	for _, e := range list {
		if SameName(e.Name, name) {
			deleted = true
			continue
		}
		out = append(out, e)
	}
	return out, deleted
}

// Sort orders a list by name, case-insensitively: the pane is browsed by
// name, so alphabetical is the only order that makes a snippet findable.
// Equal-folding names keep a deterministic order by their raw spelling.
func Sort(list []Snippet) {
	sort.SliceStable(list, func(i, j int) bool {
		a, b := strings.ToLower(list[i].Name), strings.ToLower(list[j].Name)
		if a != b {
			return a < b
		}
		return list[i].Name < list[j].Name
	})
}

// ---------- load ----------

// Load reads the snippets, sorted by name. A missing file is not an
// error: a first run has no snippets.
func Load() ([]Snippet, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	return LoadFrom(path)
}

// LoadFrom reads a snippet file from an explicit path.
//
// Unparsable lines are skipped rather than failing the load — a
// truncated last line after a crash must not cost every snippet — and so
// are entries without a name or without SQL, which nothing could recall.
// A duplicate name keeps the last line, the one a rewrite would have
// left.
func LoadFrom(path string) ([]Snippet, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("snippets: read %s: %w", path, err)
	}
	defer f.Close()

	var out []Snippet
	sc := bufio.NewScanner(f)
	// A snippet can be long; the default 64 KiB token limit is not
	// enough for a generated INSERT.
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var s Snippet
		if err := json.Unmarshal([]byte(line), &s); err != nil {
			continue
		}
		s.Name = strings.TrimSpace(s.Name)
		if s.Name == "" || strings.TrimSpace(s.SQL) == "" {
			continue
		}
		out, _ = Put(out, s)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("snippets: read %s: %w", path, err)
	}
	Sort(out)
	return out, nil
}

// ---------- write ----------

// Save rewrites the whole snippet file.
func Save(list []Snippet) error {
	path, err := Path()
	if err != nil {
		return err
	}
	return SaveTo(path, list)
}

// SaveTo rewrites the snippet file at an explicit path. The write is
// atomic (temp file + rename) so an interrupted save cannot truncate the
// snippets it was rewriting.
func SaveTo(path string, list []Snippet) error {
	writeMu.Lock()
	defer writeMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("snippets: create %s: %w", filepath.Dir(path), err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".snippets-*")
	if err != nil {
		return fmt.Errorf("snippets: create temp file: %w", err)
	}
	defer os.Remove(tmp.Name())

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("snippets: chmod temp file: %w", err)
	}
	w := bufio.NewWriter(tmp)
	for _, s := range list {
		line, err := json.Marshal(s)
		if err != nil {
			tmp.Close()
			return fmt.Errorf("snippets: encode %q: %w", s.Name, err)
		}
		if _, err := w.Write(append(line, '\n')); err != nil {
			tmp.Close()
			return fmt.Errorf("snippets: write temp file: %w", err)
		}
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		return fmt.Errorf("snippets: write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("snippets: close temp file: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("snippets: replace %s: %w", path, err)
	}
	return nil
}
