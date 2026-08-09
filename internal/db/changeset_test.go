package db

import (
	"context"
	"slices"
	"testing"
)

func dialect(t *testing.T, e Engine) Dialect {
	t.Helper()
	d, err := DialectFor(e)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// Every engine gets its own quoting and placeholder style, and both the
// new value and the key values travel as parameters.
func TestUpdateSQLPerDialect(t *testing.T) {
	c := CellChange{
		Table:    "users",
		PKCols:   []string{"id"},
		PKVals:   []any{int64(3)},
		Column:   "email",
		NewValue: "x@example.com",
	}
	cases := []struct {
		engine Engine
		want   string
	}{
		{EngineSQLite, `UPDATE "users" SET "email" = ? WHERE "id" = ?`},
		{EnginePostgres, `UPDATE "users" SET "email" = $1 WHERE "id" = $2`},
		{EngineMySQL, "UPDATE `users` SET `email` = ? WHERE `id` = ?"},
	}
	for _, tc := range cases {
		st := UpdateSQL(dialect(t, tc.engine), c)
		if st.SQL != tc.want {
			t.Errorf("%s: SQL = %q, want %q", tc.engine, st.SQL, tc.want)
		}
		if !slices.Equal(st.Args, []any{any("x@example.com"), any(int64(3))}) {
			t.Errorf("%s: args = %v", tc.engine, st.Args)
		}
	}
}

// A composite key contributes one predicate per column, a qualified
// table keeps the database part, and NULL is a bound nil parameter.
func TestUpdateSQLCompositeKeyAndNull(t *testing.T) {
	c := CellChange{
		Database: "app",
		Table:    "grades",
		PKCols:   []string{"student", "course"},
		PKVals:   []any{int64(1), "math"},
		Column:   "grade",
		NewValue: nil,
	}
	st := UpdateSQL(dialect(t, EnginePostgres), c)
	want := `UPDATE "app"."grades" SET "grade" = $1 WHERE "student" = $2 AND "course" = $3`
	if st.SQL != want {
		t.Fatalf("SQL = %q, want %q", st.SQL, want)
	}
	if len(st.Args) != 3 || st.Args[0] != nil {
		t.Fatalf("args = %v, want a leading nil", st.Args)
	}
}

// Staging the same cell twice replaces the change and keeps the original
// OldValue; unstaging removes exactly one cell.
func TestChangesetStageReplaceUnstage(t *testing.T) {
	cs := NewChangeset()
	base := CellChange{Table: "t", PKCols: []string{"id"}, PKVals: []any{int64(1)}, Column: "c"}

	first := base
	first.OldValue, first.NewValue = "orig", "v1"
	cs.Stage(first)
	second := base
	second.OldValue, second.NewValue = "v1", "v2"
	cs.Stage(second)

	if cs.Len() != 1 {
		t.Fatalf("Len = %d, want the second edit to replace the first", cs.Len())
	}
	got, ok := cs.Lookup("", "t", []any{int64(1)}, "c")
	if !ok || got.NewValue != "v2" || got.OldValue != "orig" {
		t.Fatalf("Lookup = %+v, want NewValue v2 with the original OldValue", got)
	}

	other := base
	other.PKVals = []any{int64(2)}
	other.NewValue = "x"
	cs.Stage(other)
	if !cs.Unstage("", "t", []any{int64(1)}, "c") {
		t.Fatal("Unstage missed a staged cell")
	}
	if cs.Len() != 1 {
		t.Fatalf("Len = %d after unstage, want 1", cs.Len())
	}
	if _, ok := cs.Lookup("", "t", []any{int64(2)}, "c"); !ok {
		t.Fatal("unstaging one cell dropped another")
	}
	cs.Clear()
	if cs.Len() != 0 {
		t.Fatal("Clear left changes behind")
	}
}

// Key values are compared with their type, so row "1" and row 1 are
// different cells.
func TestChangesetKeysAreTyped(t *testing.T) {
	cs := NewChangeset()
	cs.Stage(CellChange{Table: "t", PKCols: []string{"id"}, PKVals: []any{int64(1)}, Column: "c", NewValue: "a"})
	cs.Stage(CellChange{Table: "t", PKCols: []string{"id"}, PKVals: []any{"1"}, Column: "c", NewValue: "b"})
	if cs.Len() != 2 {
		t.Fatalf("Len = %d, want int64(1) and \"1\" kept apart", cs.Len())
	}
}

// The whole commit is one transaction: a failing statement rolls back
// the ones before it.
func TestExecTxRollsBackOnError(t *testing.T) {
	ctx := context.Background()
	drv := openTest(t, EngineSQLite, ":memory:")
	seed(t, drv)
	d := drv.Dialect()

	stmts := []Statement{
		UpdateSQL(d, CellChange{Table: "users", PKCols: []string{"id"}, PKVals: []any{int64(1)},
			Column: "email", NewValue: "changed@example.com"}),
		// name is NOT NULL, so this one fails.
		UpdateSQL(d, CellChange{Table: "users", PKCols: []string{"id"}, PKVals: []any{int64(2)},
			Column: "name", NewValue: nil}),
	}
	if _, err := drv.ExecTx(ctx, stmts); err == nil {
		t.Fatal("ExecTx succeeded, want the NOT NULL violation to fail it")
	}
	rs, err := drv.Query(ctx, "SELECT email FROM users WHERE id = "+d.Placeholder(1), 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := rs.Rows[0][0]; got != "alice@example.com" {
		t.Fatalf("email = %v, want the first UPDATE rolled back", got)
	}

	// The same first statement alone commits fine.
	results, err := drv.ExecTx(ctx, stmts[:1])
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].RowsAffected != 1 {
		t.Fatalf("results = %+v, want one row affected", results)
	}
	rs, err = drv.Query(ctx, "SELECT email FROM users WHERE id = "+d.Placeholder(1), 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := rs.Rows[0][0]; got != "changed@example.com" {
		t.Fatalf("email = %v, want the committed value", got)
	}
}
