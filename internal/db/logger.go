package db

import (
	"sync"
	"time"
)

// LogCapacity bounds the Logger's ring buffer.
const LogCapacity = 500

// LogEntry is one statement a Driver sent to the server: a page/count
// query, a staged edit's UPDATE/INSERT/DELETE, a query-editor statement,
// a transaction boundary (SQL is "BEGIN"/"COMMIT"), or an introspection
// query. Err is the failure it returned, nil on success.
type LogEntry struct {
	SQL      string
	Args     []any
	At       time.Time
	Duration time.Duration
	Err      error
}

// Logger is a fixed-capacity ring buffer of every statement a conn runs.
// It is appended to from the small number of places conn touches
// database/sql — Exec, ExecTx, QueryLimit (which Query and the page/count
// helpers funnel through) and the querier introspection queries — so
// every statement is captured exactly once no matter which UI code path
// triggered it, and nothing outside this package has to remember to log
// anything.
//
// A nil *Logger is valid and every method on it is a no-op: conn always
// has one, but the zero value is convenient for tests that build a conn
// by hand.
type Logger struct {
	mu      sync.Mutex
	entries []LogEntry
	start   int // index of the oldest live entry
	count   int
}

// NewLogger returns an empty Logger with room for LogCapacity entries.
func NewLogger() *Logger {
	return &Logger{entries: make([]LogEntry, LogCapacity)}
}

// record appends one entry, evicting the oldest once the buffer is full.
func (l *Logger) record(sql string, args []any, start time.Time, err error) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	idx := (l.start + l.count) % len(l.entries)
	l.entries[idx] = LogEntry{SQL: sql, Args: args, At: start, Duration: time.Since(start), Err: err}
	if l.count < len(l.entries) {
		l.count++
	} else {
		l.start = (l.start + 1) % len(l.entries)
	}
}

// Entries returns the logged statements, oldest first.
func (l *Logger) Entries() []LogEntry {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]LogEntry, l.count)
	for i := range out {
		out[i] = l.entries[(l.start+i)%len(l.entries)]
	}
	return out
}
