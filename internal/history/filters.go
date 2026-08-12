package history

// The data grid's inline WHERE input keeps a history of its own, scoped
// to one relation of one connection: `/` on `orders` has to recall what
// was typed on `orders`, not the last filter of whatever table was open
// before it.
//
// It is deliberately not a new storage format. The file is the same JSON
// Lines shape as the statement history — same Entry, same append-only
// write, same owner-only mode, same compaction on load — with Entry.Key
// carrying the scope. That is what lets the query history become
// per-connection later without a second store: it only has to start
// writing a Scope of its own into the same field.

import "path/filepath"

const (
	// FilterFileName is the filter history inside AppDir, next to the
	// statement history.
	FilterFileName = "filters"
	// MaxScopeEntries bounds one scope's recall list. The whole file is
	// still bounded by MaxEntries; this keeps one heavily filtered table
	// from crowding every other relation out of it.
	MaxScopeEntries = 50
)

// scopeSep joins the parts of a scope key. It is the ASCII unit
// separator: no connection name, database or relation can contain it, so
// no scope key can be spelled by another scope's parts.
const scopeSep = "\x1f"

// Scope keys an entry to one relation of one connection. An empty
// database is the pseudo-namespace file engines browse under, and an
// empty table names the connection as a whole — which is the shape a
// per-connection query history would use.
func Scope(conn, database, table string) string {
	return conn + scopeSep + database + scopeSep + table
}

// FilterPath returns the full path of the filter history file.
func FilterPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, FilterFileName), nil
}

// LoadFilters reads every scope's filters, newest first. Like Load, a
// missing file is not an error.
func LoadFilters() ([]Entry, error) {
	path, err := FilterPath()
	if err != nil {
		return nil, err
	}
	return LoadFrom(path)
}

// AppendFilter adds one filter to the end of the file.
func AppendFilter(e Entry) error {
	path, err := FilterPath()
	if err != nil {
		return err
	}
	return AppendTo(path, e)
}

// SaveFilters rewrites the whole file from entries given newest first.
// It is what a de-duplicated recall and a trimmed scope go through: an
// append can only add a line, never drop one.
func SaveFilters(entries []Entry) error {
	path, err := FilterPath()
	if err != nil {
		return err
	}
	return SaveTo(path, entries)
}

// InScope picks the entries of one scope out of a newest-first list,
// keeping that order.
func InScope(entries []Entry, key string) []Entry {
	if key == "" {
		return nil
	}
	var out []Entry
	for _, e := range entries {
		if e.Key == key {
			out = append(out, e)
		}
	}
	return out
}

// TrimScope drops the oldest entries of one scope beyond max, leaving
// every other scope untouched. entries are newest first, and so is the
// result; the input slice is never modified.
func TrimScope(entries []Entry, key string, max int) []Entry {
	if key == "" || max <= 0 {
		return entries
	}
	seen := 0
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if e.Key == key {
			seen++
			if seen > max {
				continue
			}
		}
		out = append(out, e)
	}
	if len(out) == len(entries) {
		return entries
	}
	return out
}
