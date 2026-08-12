// Package pathcomplete is the shared filesystem path completion engine:
// given a partially typed path it returns the matching directory entries and
// the longest unambiguous extension of the input — the pieces a caller needs
// for shell-style tab completion plus a suggestion list. The engine is pure
// presentation-agnostic logic; rendering and key handling stay with the
// hosting input (in lazysql: the connection form's file field).
//
// Inputs are completed in the user's own notation: a query written with a
// leading "~" or a "$VAR"/"${VAR}" reference keeps that notation in every
// candidate — only the directory read against the filesystem is expanded.
// Hidden entries are offered only when the typed base name explicitly
// starts with a dot. Matching is case-sensitive first and falls back to
// case-insensitive, so "~/dev" still finds "~/Development". Candidates rank
// directories first, then database-file extensions (.db, .sqlite,
// .sqlite3, .duckdb), then everything else — nothing is ever filtered out.
package pathcomplete

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// MaxCandidates caps the candidate list: completion beyond this many matches
// is not helpful as a suggestion list and the common-prefix extension already
// covers the typing help.
const MaxCandidates = 50

// Result is one completion query's outcome.
type Result struct {
	// Candidates are the full completed inputs, one per matching entry, in
	// the notation the input used (a leading ~ stays a ~). Directories carry
	// a trailing separator so accepting one and completing again descends.
	Candidates []string
	// Completed is the input extended by the longest prefix shared by every
	// candidate — what a tab press should replace the input with. It equals
	// the input when nothing matches or the input is already unambiguous.
	Completed string
}

// Complete returns the completion of input against the filesystem, offering
// files and directories.
func Complete(input string) Result { return complete("", input, false) }

// CompleteFrom completes input like Complete, but resolves a relative input
// against baseDir instead of the process working directory. Absolute and ~
// inputs ignore baseDir; the candidates keep the typed relative notation.
func CompleteFrom(baseDir, input string) Result { return complete(baseDir, input, false) }

// Dirs returns the completion of input offering directories only — the
// project picker's flavor.
func Dirs(input string) Result { return complete("", input, true) }

// Expand resolves a leading "~" or "~/" against the home directory, then
// expands any "$VAR" or "${VAR}" environment variable references. It is the
// single expansion helper; callers should not keep local copies. Undefined
// variables expand to "", matching shell behaviour.
func Expand(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			p = home + p[1:]
		}
	}
	if strings.ContainsRune(p, '$') {
		p = os.Expand(p, os.Getenv)
	}
	return p
}

func complete(baseDir, input string, dirsOnly bool) Result {
	res := Result{Completed: input}
	if input == "" && baseDir == "" {
		return res
	}
	dir, base := split(input)
	real := Expand(dir)
	if real == "" {
		real = "."
	}
	if baseDir != "" && !filepath.IsAbs(real) {
		real = filepath.Join(baseDir, real)
	}
	entries, err := os.ReadDir(real)
	if err != nil {
		return res
	}
	names := match(entries, real, base, dirsOnly)
	if len(names) == 0 {
		return res
	}
	for _, n := range names {
		res.Candidates = append(res.Candidates, dir+n)
		if len(res.Candidates) == MaxCandidates {
			break
		}
	}
	res.Completed = dir + commonPrefix(names)
	return res
}

// split separates input into the directory part (kept verbatim, including
// its trailing separator) and the partial base name being completed.
// "~/Dev" -> ("~/", "Dev"); "/usr" -> ("/", "usr"); "foo" -> ("", "foo").
func split(input string) (dir, base string) {
	i := strings.LastIndexByte(input, filepath.Separator)
	if i < 0 {
		return "", input
	}
	return input[:i+1], input[i+1:]
}

// dbExts are the file extensions this field cares about — SQLite and DuckDB
// database files. Entries with one of these extensions rank above other
// files (but never above directories); any other file still matches, it is
// just listed last, and directories are never filtered by extension at all.
var dbExts = map[string]bool{
	".db":      true,
	".sqlite":  true,
	".sqlite3": true,
	".duckdb":  true,
}

// entryMatch is a matched directory entry before it is turned into a
// completed candidate string, kept around long enough to rank it.
type entryMatch struct {
	name  string
	isDir bool
}

// match filters the entries of realDir against the partial base name: hidden
// entries only on an explicit leading dot, exact-case prefix matches when any
// exist, case-insensitive ones otherwise. The result is ranked directories
// first, then database-file extensions, then everything else, alphabetically
// within each group, and directory names (including symlinks to
// directories) get the trailing separator.
func match(entries []os.DirEntry, realDir, base string, dirsOnly bool) []string {
	var exact, folded []entryMatch
	lower := strings.ToLower(base)
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(base, ".") {
			continue
		}
		isDir := e.IsDir()
		if !isDir && e.Type()&os.ModeSymlink != 0 {
			if st, err := os.Stat(filepath.Join(realDir, name)); err == nil && st.IsDir() {
				isDir = true
			}
		}
		if dirsOnly && !isDir {
			continue
		}
		m := entryMatch{name: name, isDir: isDir}
		switch {
		case strings.HasPrefix(name, base):
			exact = append(exact, m)
		case strings.HasPrefix(strings.ToLower(name), lower):
			folded = append(folded, m)
		}
	}
	out := exact
	if len(out) == 0 {
		out = folded
	}
	return rank(out)
}

// rank orders matches directories-first, then database extensions, then
// everything else, alphabetically within each tier, and appends the
// trailing separator a directory completes with.
func rank(matches []entryMatch) []string {
	sort.SliceStable(matches, func(i, j int) bool {
		ri, rj := matchTier(matches[i]), matchTier(matches[j])
		if ri != rj {
			return ri < rj
		}
		return matches[i].name < matches[j].name
	})
	out := make([]string, len(matches))
	for i, m := range matches {
		if m.isDir {
			out[i] = m.name + string(filepath.Separator)
		} else {
			out[i] = m.name
		}
	}
	return out
}

// matchTier is the sort priority of a matched entry: lower sorts first.
func matchTier(m entryMatch) int {
	switch {
	case m.isDir:
		return 0
	case dbExts[strings.ToLower(filepath.Ext(m.name))]:
		return 1
	default:
		return 2
	}
}

// commonPrefix returns the longest prefix shared by all names. With mixed
// case among the candidates the shared prefix uses the first name's casing
// and stops where the fold diverges.
func commonPrefix(names []string) string {
	first := names[0]
	for _, n := range names[1:] {
		first = first[:sharedLen(first, n)]
		if first == "" {
			return ""
		}
	}
	return first
}

// sharedLen is the byte length of the case-insensitively shared prefix of a
// and b, never splitting a multi-byte rune.
func sharedLen(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	n := 0
	for i := 0; i < len(ra) && i < len(rb); i++ {
		if ra[i] != rb[i] && !strings.EqualFold(string(ra[i]), string(rb[i])) {
			break
		}
		n += len(string(ra[i]))
	}
	return n
}
