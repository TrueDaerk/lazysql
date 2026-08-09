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

// A row delete names every key column and binds every key value, in
// each engine's own quoting and placeholder style.
func TestDeleteSQLPerDialect(t *testing.T) {
	r := RowDelete{
		Table:  "users",
		PKCols: []string{"id"},
		PKVals: []any{int64(3)},
	}
	cases := []struct {
		engine Engine
		want   string
	}{
		{EngineSQLite, `DELETE FROM "users" WHERE "id" = ?`},
		{EnginePostgres, `DELETE FROM "users" WHERE "id" = $1`},
		{EngineMySQL, "DELETE FROM `users` WHERE `id` = ?"},
	}
	for _, tc := range cases {
		st := DeleteSQL(dialect(t, tc.engine), r)
		if st.SQL != tc.want {
			t.Errorf("%s: SQL = %q, want %q", tc.engine, st.SQL, tc.want)
		}
		if !slices.Equal(st.Args, []any{any(int64(3))}) {
			t.Errorf("%s: args = %v", tc.engine, st.Args)
		}
	}

	// A composite key contributes one predicate per column.
	st := DeleteSQL(dialect(t, EnginePostgres), RowDelete{
		Database: "app",
		Table:    "grades",
		PKCols:   []string{"student", "course"},
		PKVals:   []any{int64(1), "math"},
	})
	want := `DELETE FROM "app"."grades" WHERE "student" = $1 AND "course" = $2`
	if st.SQL != want {
		t.Fatalf("SQL = %q, want %q", st.SQL, want)
	}
}

// An insert names only the columns the form filled in; every value is a
// bound parameter and NULL is a bound nil.
func TestInsertSQLPerDialect(t *testing.T) {
	r := RowInsert{
		Table:   "users",
		Columns: []string{"name", "email"},
		Values:  []any{"ada", nil},
	}
	cases := []struct {
		engine Engine
		want   string
	}{
		{EngineSQLite, `INSERT INTO "users" ("name", "email") VALUES (?, ?)`},
		{EnginePostgres, `INSERT INTO "users" ("name", "email") VALUES ($1, $2)`},
		{EngineMySQL, "INSERT INTO `users` (`name`, `email`) VALUES (?, ?)"},
	}
	for _, tc := range cases {
		st := InsertSQL(dialect(t, tc.engine), r)
		if st.SQL != tc.want {
			t.Errorf("%s: SQL = %q, want %q", tc.engine, st.SQL, tc.want)
		}
		if !slices.Equal(st.Args, []any{any("ada"), nil}) {
			t.Errorf("%s: args = %v", tc.engine, st.Args)
		}
	}
}

// An insert that leaves every column to its default is still a valid
// statement — and MySQL spells that differently from everyone else.
func TestInsertSQLDefaultValues(t *testing.T) {
	r := RowInsert{Table: "users"}
	cases := []struct {
		engine Engine
		want   string
	}{
		{EngineSQLite, `INSERT INTO "users" DEFAULT VALUES`},
		{EnginePostgres, `INSERT INTO "users" DEFAULT VALUES`},
		{EngineDuckDB, `INSERT INTO "users" DEFAULT VALUES`},
		{EngineMySQL, "INSERT INTO `users` () VALUES ()"},
		{EngineMariaDB, "INSERT INTO `users` () VALUES ()"},
	}
	for _, tc := range cases {
		st := InsertSQL(dialect(t, tc.engine), r)
		if st.SQL != tc.want {
			t.Errorf("%s: SQL = %q, want %q", tc.engine, st.SQL, tc.want)
		}
		if len(st.Args) != 0 {
			t.Errorf("%s: args = %v, want none", tc.engine, st.Args)
		}
	}
}

// Row operations share the changeset with cell edits: they commit in
// staging order, a delete is staged once, and staging it drops the
// pending edits of the row it removes.
func TestChangesetRowOperations(t *testing.T) {
	cs := NewChangeset()
	cs.Stage(CellChange{Table: "t", PKCols: []string{"id"}, PKVals: []any{int64(1)},
		Column: "c", NewValue: "kept"})
	cs.Stage(CellChange{Table: "t", PKCols: []string{"id"}, PKVals: []any{int64(2)},
		Column: "c", NewValue: "doomed"})

	del := RowDelete{Table: "t", PKCols: []string{"id"}, PKVals: []any{int64(2)}}
	if !cs.StageDelete(del) {
		t.Fatal("StageDelete reported the delete as already staged")
	}
	if cs.StageDelete(del) {
		t.Fatal("StageDelete staged the same row twice")
	}
	if _, ok := cs.Lookup("", "t", []any{int64(2)}, "c"); ok {
		t.Fatal("staging a delete kept the edits of the row it removes")
	}
	if _, ok := cs.Lookup("", "t", []any{int64(1)}, "c"); !ok {
		t.Fatal("staging a delete dropped another row's edit")
	}
	if !cs.DeleteStaged("", "t", []any{int64(2)}) {
		t.Fatal("DeleteStaged does not see the staged delete")
	}

	// Two identical inserts are two rows, not one replaced staging.
	a := cs.StageInsert(RowInsert{Table: "t", Columns: []string{"c"}, Values: []any{"new"}})
	b := cs.StageInsert(RowInsert{Table: "t", Columns: []string{"c"}, Values: []any{"new"}})
	if a.ID == b.ID {
		t.Fatal("two staged inserts share an id")
	}
	if got := cs.InsertsFor("", "t"); len(got) != 2 {
		t.Fatalf("InsertsFor = %d, want both inserts", len(got))
	}

	// Commit order is staging order, and every kind renders itself.
	stmts := cs.Statements(dialect(t, EngineSQLite))
	wantSQL := []string{
		`UPDATE "t" SET "c" = ? WHERE "id" = ?`,
		`DELETE FROM "t" WHERE "id" = ?`,
		`INSERT INTO "t" ("c") VALUES (?)`,
		`INSERT INTO "t" ("c") VALUES (?)`,
	}
	if len(stmts) != len(wantSQL) {
		t.Fatalf("statements = %d, want %d", len(stmts), len(wantSQL))
	}
	for i, want := range wantSQL {
		if stmts[i].SQL != want {
			t.Errorf("statement %d = %q, want %q", i, stmts[i].SQL, want)
		}
	}

	// Unstaging is per operation.
	if !cs.UnstageInsert("", "t", a.ID) {
		t.Fatal("UnstageInsert missed a staged insert")
	}
	if !cs.UnstageDelete("", "t", []any{int64(2)}) {
		t.Fatal("UnstageDelete missed the staged delete")
	}
	if cs.Len() != 2 {
		t.Fatalf("Len = %d, want the remaining edit and insert", cs.Len())
	}
	if cs.PKColsFor("", "t") == nil {
		t.Fatal("PKColsFor lost the key columns")
	}
}

// A mixed changeset applies as one transaction.
func TestExecTxMixedRowOperations(t *testing.T) {
	ctx := context.Background()
	drv := openTest(t, EngineSQLite, ":memory:")
	seed(t, drv)
	d := drv.Dialect()

	cs := NewChangeset()
	cs.Stage(CellChange{Table: "users", PKCols: []string{"id"}, PKVals: []any{int64(1)},
		Column: "email", NewValue: "new@example.com"})
	cs.StageDelete(RowDelete{Table: "users", PKCols: []string{"id"}, PKVals: []any{int64(2)}})
	cs.StageInsert(RowInsert{Table: "users",
		Columns: []string{"id", "name", "email"},
		Values:  []any{int64(99), "zoe", nil}})

	if _, err := drv.ExecTx(ctx, cs.Statements(d)); err != nil {
		t.Fatalf("ExecTx: %v", err)
	}
	rs, err := drv.Query(ctx, "SELECT id, name, email FROM users ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	if got := rs.Rows[0][2]; got != "new@example.com" {
		t.Errorf("row 1 email = %v, want the update applied", got)
	}
	for _, row := range rs.Rows {
		if row[0] == int64(2) {
			t.Error("the deleted row is still there")
		}
	}
	last := rs.Rows[len(rs.Rows)-1]
	if last[0] != int64(99) || last[1] != "zoe" || last[2] != nil {
		t.Errorf("inserted row = %v, want (99, zoe, NULL)", last)
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
