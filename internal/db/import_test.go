package db

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

// rowsOf is an ImportRequest.Next over fixed rows.
func rowsOf(rows ...[]any) func() (ImportRow, error) {
	i := 0
	return func() (ImportRow, error) {
		if i >= len(rows) {
			return ImportRow{}, io.EOF
		}
		i++
		return ImportRow{Values: rows[i-1], Line: i + 1}, nil
	}
}

func importFixture(t *testing.T, engine Engine) Driver {
	t.Helper()
	drv := openTest(t, engine, "")
	if _, err := drv.Exec(context.Background(),
		`CREATE TABLE items (id INTEGER PRIMARY KEY, name VARCHAR NOT NULL, price DOUBLE, born DATE)`); err != nil {
		t.Fatal(err)
	}
	return drv
}

func countItems(t *testing.T, drv Driver) int64 {
	t.Helper()
	n, err := drv.CountRows(context.Background(), "", "items", nil)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func forImportEngines(t *testing.T, fn func(t *testing.T, drv Driver)) {
	for _, e := range []Engine{EngineSQLite, EngineDuckDB} {
		t.Run(string(e), func(t *testing.T) { fn(t, importFixture(t, e)) })
	}
}

func TestImportRowsInsertsInOneTransaction(t *testing.T) {
	forImportEngines(t, func(t *testing.T, drv Driver) {
		n, err := drv.ImportRows(context.Background(), ImportRequest{
			Table:   "items",
			Columns: []string{"id", "name", "price", "born"},
			Next: rowsOf(
				[]any{int64(1), "a, with comma\nand newline", 1.5, "2026-01-02"},
				[]any{int64(2), "b", nil, nil},
			),
		})
		if err != nil || n != 2 {
			t.Fatalf("ImportRows = %d, %v", n, err)
		}
		rs, err := drv.Query(context.Background(), `SELECT name, price, born FROM items ORDER BY id`)
		if err != nil {
			t.Fatal(err)
		}
		if rs.Rows[0][0] != "a, with comma\nand newline" {
			t.Fatalf("name = %#v", rs.Rows[0][0])
		}
		if rs.Rows[1][1] != nil || rs.Rows[1][2] != nil {
			t.Fatalf("NULLs came back as %#v", rs.Rows[1])
		}
		if FormatTemporalValue(rs.Rows[0][2], KindDate, "") != "2026-01-02" {
			t.Fatalf("born = %#v", rs.Rows[0][2])
		}
		var sqls []string
		for _, e := range drv.Logger().Entries() {
			sqls = append(sqls, e.SQL)
		}
		log := strings.Join(sqls, "\n")
		if !strings.Contains(log, "BEGIN") || !strings.Contains(log, "-- ×2 rows") || !strings.Contains(log, "COMMIT") {
			t.Fatalf("command log = %s", log)
		}
	})
}

// A row the engine refuses mid-file rolls every earlier row back and is
// named in the error.
func TestImportRowsRollsBackOnAFailingRow(t *testing.T) {
	forImportEngines(t, func(t *testing.T, drv Driver) {
		n, err := drv.ImportRows(context.Background(), ImportRequest{
			Table:   "items",
			Columns: []string{"id", "name"},
			Next: rowsOf(
				[]any{int64(1), "a"},
				[]any{int64(2), "b"},
				[]any{int64(3), nil}, // NOT NULL
				[]any{int64(4), "d"},
			),
		})
		var re *ImportRowError
		if !errors.As(err, &re) {
			t.Fatalf("err = %v, want an ImportRowError", err)
		}
		if re.Row != 3 || re.Line != 4 || n != 2 {
			t.Fatalf("failing row = %d line %d, n = %d", re.Row, re.Line, n)
		}
		entries := drv.Logger().Entries()
		if last := entries[len(entries)-1]; last.SQL != "ROLLBACK" || last.Err != nil {
			t.Fatalf("last log entry = %+v", last)
		}
		// The refused row's values are on the INSERT's log line.
		if ins := entries[len(entries)-2]; len(ins.Args) != 2 || ins.Err == nil {
			t.Fatalf("INSERT log entry = %+v", ins)
		}
		if got := countItems(t, drv); got != 0 {
			t.Fatalf("%d rows survived the rollback", got)
		}
	})
}

// A source error — the CSV side's type mismatch — rolls back the same way.
func TestImportRowsRollsBackOnASourceError(t *testing.T) {
	forImportEngines(t, func(t *testing.T, drv Driver) {
		next := rowsOf([]any{int64(1), "a"})
		calls := 0
		mismatch := errors.New(`line 3, column id (INTEGER): "x" is not an integer`)
		_, err := drv.ImportRows(context.Background(), ImportRequest{
			Table: "items", Columns: []string{"id", "name"},
			Next: func() (ImportRow, error) {
				calls++
				if calls == 2 {
					return ImportRow{}, mismatch
				}
				return next()
			},
		})
		if !errors.Is(err, mismatch) {
			t.Fatalf("err = %v", err)
		}
		if got := countItems(t, drv); got != 0 {
			t.Fatalf("%d rows survived the rollback", got)
		}
	})
}

func TestImportRowsCancelRollsBack(t *testing.T) {
	forImportEngines(t, func(t *testing.T, drv Driver) {
		ctx, cancel := context.WithCancel(context.Background())
		i := int64(0)
		_, err := drv.ImportRows(ctx, ImportRequest{
			Table: "items", Columns: []string{"id", "name"},
			ProgressEvery: 10,
			Progress: func(rows int64) {
				if rows == 50 {
					cancel()
				}
			},
			Next: func() (ImportRow, error) {
				i++
				return ImportRow{Values: []any{i, "x"}}, nil
			},
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		if got := countItems(t, drv); got != 0 {
			t.Fatalf("%d rows survived the cancel", got)
		}
	})
}

// The read-only guard refuses an import at the driver, before anything
// is read from the source, and logs the refusal.
func TestImportRowsRefusedOnReadOnly(t *testing.T) {
	path := seedFile(t)
	drv := openReadOnlyTest(t, path)
	read := false
	_, err := drv.ImportRows(context.Background(), ImportRequest{
		Table: "t", Columns: []string{"id", "name"},
		Next: func() (ImportRow, error) { read = true; return ImportRow{}, io.EOF },
	})
	if !errors.Is(err, ErrReadOnly) {
		t.Fatalf("err = %v, want ErrReadOnly", err)
	}
	if read {
		t.Fatal("a read-only import read from its source")
	}
	entries := drv.Logger().Entries()
	if last := entries[len(entries)-1]; !strings.HasPrefix(last.SQL, rejectedPrefix+"INSERT INTO") {
		t.Fatalf("last log entry = %q", last.SQL)
	}
}

func TestImportInsertSQLIsParameterized(t *testing.T) {
	d, _ := DialectFor(EnginePostgres)
	got := ImportInsertSQL(d, "public", "we\"ird", []string{"a", "b"})
	want := `INSERT INTO "public"."we""ird" ("a", "b") VALUES ($1, $2)`
	if got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}

func TestConvertText(t *testing.T) {
	ok := []struct {
		typ, in string
		want    any
	}{
		{"INTEGER", " 42 ", int64(42)},
		{"bigint unsigned", "18446744073709551615", "18446744073709551615"},
		{"DOUBLE PRECISION", "1e3", 1000.0},
		{"NUMERIC(38,10)", "12345678901234567890.123", "12345678901234567890.123"},
		{"boolean", "yes", true},
		{"BOOL", "0", false},
		{"DATE", "2026-01-02", "2026-01-02"},
		{"timestamp with time zone", "2026-01-02T03:04:05Z", "2026-01-02T03:04:05Z"},
		{"TEXT", " keep spaces ", " keep spaces "},
		{"uuid", "not checked", "not checked"},
	}
	for _, c := range ok {
		got, err := ConvertText(c.typ, c.in)
		if err != nil || got != c.want {
			t.Errorf("ConvertText(%s, %q) = %#v, %v; want %#v", c.typ, c.in, got, err, c.want)
		}
	}
	bad := [][2]string{
		{"INTEGER", "1.5"}, {"int", "abc"}, {"real", "n/a"}, {"decimal", "1,5"},
		{"boolean", "maybe"}, {"date", "tomorrow"}, {"time", "noon"},
	}
	for _, c := range bad {
		if v, err := ConvertText(c[0], c[1]); err == nil {
			t.Errorf("ConvertText(%s, %q) coerced to %#v", c[0], c[1], v)
		}
	}
}
