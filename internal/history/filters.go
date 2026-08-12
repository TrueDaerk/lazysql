package history

// The data grid's inline WHERE input keeps a history of its own, scoped
// to one relation of one connection: `/` on `orders` has to recall what
// was typed on `orders`, not the last filter of whatever table was open
// before it.
//
// It is deliberately neither a new storage format nor a second way of
// keying one. The file is the same JSON Lines shape as the statement
// history — same Entry, same append-only write, same owner-only mode,
// same compaction on load — and the scope rides in the same
// Entry.Connection the editor's history is already filtered by, narrowed
// by Entry.Database and Entry.Table. A statement is scoped by connection,
// a filter by relation; one set of fields covers both.

import "path/filepath"

const (
	// FilterFileName is the filter history inside AppDir, next to the
	// statement history. It is a file of its own because a WHERE clause
	// is not a statement: the history pane would offer fragments it
	// cannot run, and one relation's filters would push statements out of
	// the shared cap.
	FilterFileName = "filters"
	// MaxRelationEntries bounds one relation's recall list. The whole
	// file is still bounded by MaxEntries; this keeps one heavily
	// filtered table from crowding every other relation out of it.
	MaxRelationEntries = 50
)

// FilterPath returns the full path of the filter history file.
func FilterPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, FilterFileName), nil
}

// LoadFilters reads every relation's filters, newest first. Like Load, a
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
// It is what a de-duplicated recall and a trimmed relation go through:
// an append can only add a line, never drop one.
func SaveFilters(entries []Entry) error {
	path, err := FilterPath()
	if err != nil {
		return err
	}
	return SaveTo(path, entries)
}

// InRelation picks the entries of one relation out of a newest-first
// list, keeping that order.
//
// Unlike ForConnection this matches all three fields exactly, with no
// leniency for an entry that recorded none: an unscoped entry belongs to
// no relation, and the filter file is new enough to have none anyway. An
// empty database still matches an empty database — that is the
// pseudo-namespace of the file engines, not a missing value.
func InRelation(entries []Entry, connection, database, table string) []Entry {
	if connection == "" || table == "" {
		return nil
	}
	var out []Entry
	for _, e := range entries {
		if e.Connection == connection && e.Database == database && e.Table == table {
			out = append(out, e)
		}
	}
	return out
}

// TrimRelation drops the oldest entries of one relation beyond max,
// leaving every other relation untouched. entries are newest first, and
// so is the result; the input slice is never modified.
func TrimRelation(entries []Entry, connection, database, table string, max int) []Entry {
	if connection == "" || table == "" || max <= 0 {
		return entries
	}
	seen := 0
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if e.Connection == connection && e.Database == database && e.Table == table {
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
