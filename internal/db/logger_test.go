package db

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestLoggerRingBufferWraps drops the oldest entry once the buffer is
// full, keeping only the most recent LogCapacity statements.
func TestLoggerRingBufferWraps(t *testing.T) {
	l := &Logger{entries: make([]LogEntry, 3)}
	now := time.Now()
	for i := 0; i < 5; i++ {
		l.record("SELECT "+string(rune('a'+i)), nil, now, nil)
	}
	got := l.Entries()
	if len(got) != 3 {
		t.Fatalf("len(Entries()) = %d, want 3", len(got))
	}
	want := []string{"SELECT c", "SELECT d", "SELECT e"}
	for i, e := range got {
		if e.SQL != want[i] {
			t.Fatalf("entry %d = %q, want %q", i, e.SQL, want[i])
		}
	}
}

// TestLoggerCapturesDurationAndError checks the two fields the command
// log panel colors and sorts by.
func TestLoggerCapturesDurationAndError(t *testing.T) {
	l := NewLogger()
	start := time.Now().Add(-5 * time.Millisecond)
	failure := errors.New("boom")
	l.record("SELECT 1", []any{42}, start, nil)
	l.record("SELECT 2", nil, start, failure)

	got := l.Entries()
	if len(got) != 2 {
		t.Fatalf("len(Entries()) = %d, want 2", len(got))
	}
	if got[0].Err != nil {
		t.Fatalf("entry 0 Err = %v, want nil", got[0].Err)
	}
	if got[0].Duration < 5*time.Millisecond {
		t.Fatalf("entry 0 Duration = %v, want >= 5ms", got[0].Duration)
	}
	if len(got[0].Args) != 1 || got[0].Args[0] != 42 {
		t.Fatalf("entry 0 Args = %v, want [42]", got[0].Args)
	}
	if !errors.Is(got[1].Err, failure) {
		t.Fatalf("entry 1 Err = %v, want %v", got[1].Err, failure)
	}
}

// A nil *Logger is what a hand-built conn has before Open sets one up;
// every method must tolerate it instead of panicking.
func TestNilLoggerIsSafe(t *testing.T) {
	var l *Logger
	l.record("SELECT 1", nil, time.Now(), nil)
	if got := l.Entries(); got != nil {
		t.Fatalf("Entries() on nil Logger = %v, want nil", got)
	}
}

// TestConnLogsExactlyOnce is the acceptance criterion from end to end:
// a page query, a count, a staged commit (ExecTx) and introspection each
// append exactly one entry per statement to the same Driver.Logger(),
// regardless of which method the UI called.
func TestConnLogsExactlyOnce(t *testing.T) {
	drv := openTest(t, EngineSQLite, "")
	seed(t, drv)
	ctx := context.Background()

	before := len(drv.Logger().Entries())

	if _, err := drv.QueryPage(ctx, "", "users", nil, nil, 10, 0); err != nil {
		t.Fatalf("QueryPage: %v", err)
	}
	if _, err := drv.CountRows(ctx, "", "users", nil); err != nil {
		t.Fatalf("CountRows: %v", err)
	}
	if _, err := drv.ExecTx(ctx, []Statement{{SQL: "UPDATE users SET name = 'ALICE' WHERE id = 1"}}); err != nil {
		t.Fatalf("ExecTx: %v", err)
	}
	if _, err := drv.TableColumns(ctx, "", "users"); err != nil {
		t.Fatalf("TableColumns: %v", err)
	}

	entries := drv.Logger().Entries()[before:]
	// QueryPage(1) + CountRows(1) + ExecTx(BEGIN, statement, COMMIT = 3),
	// then whatever introspection queries the dialect needs for
	// TableColumns (at least 1) — every one of them a single entry, never
	// duplicated and never dropped.
	if len(entries) < 6 {
		t.Fatalf("len(entries) = %d, want at least 6: %+v", len(entries), entries)
	}
	for _, e := range entries {
		if e.Err != nil {
			t.Fatalf("entry %q logged an error: %v", e.SQL, e.Err)
		}
	}
	if entries[2].SQL != "BEGIN" || entries[4].SQL != "COMMIT" {
		t.Fatalf("ExecTx entries = %q, %q, %q, want BEGIN, <stmt>, COMMIT",
			entries[2].SQL, entries[3].SQL, entries[4].SQL)
	}

	// A failing statement is logged too, with its error attached.
	_, err := drv.Exec(ctx, "UPDATE no_such_table SET x = 1")
	if err == nil {
		t.Fatal("Exec against a missing table did not fail")
	}
	last := drv.Logger().Entries()
	got := last[len(last)-1]
	if got.Err == nil {
		t.Fatal("the failing statement's entry has no Err")
	}
}
