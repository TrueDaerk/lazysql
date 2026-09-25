package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strconv"
	"strings"
	"time"
)

// Importing rows into an existing table (issue #225) is the one bulk write
// lazysql runs that is not a staged changeset: a CSV of a million rows
// cannot be staged cell by cell, and a changeset commit materializes every
// statement before it runs. ImportRows streams instead — one prepared,
// parameterized INSERT executed per row inside one transaction — so memory
// stays at one row whatever the file's size, a failing row rolls the whole
// import back, and cancelling the context does the same.
//
// It is still a write through the conn, so it sits behind the same
// read-only guard as Exec and ExecTx, and it logs to the same Logger. What
// it does not do is log every row: a ring buffer of LogCapacity entries
// would lose the rest of the session's history to one import. The log gets
// BEGIN, the INSERT once with the number of rows it ran for (and the
// failing row's values when one failed), and COMMIT or ROLLBACK.

// ImportRow is one row an import source yields: the values for
// ImportRequest.Columns, in that order, and the line of the source file
// the row started on, for error reports. Line 0 means "not from a file".
type ImportRow struct {
	Values []any
	Line   int
}

// ImportRequest describes one import. Next yields rows until it returns
// io.EOF; any other error it returns aborts the import and rolls it back,
// which is how a source reports a value that does not fit its column.
type ImportRequest struct {
	Database string
	Table    string
	// Columns are the target columns, in the order every row's Values
	// follow. Columns the source does not map are left to the engine's
	// default.
	Columns []string
	Next    func() (ImportRow, error)
	// Progress, when set, is called with the running row count every
	// ProgressEvery rows (default 1000), from the goroutine ImportRows
	// runs on.
	Progress      func(rows int64)
	ProgressEvery int
}

// ImportRowError is an INSERT the engine refused: the row (1-based, in
// source order), the line it came from, the values it carried and the
// engine's reason. Everything before it has been rolled back.
type ImportRowError struct {
	Row    int64
	Line   int
	Values []any
	Err    error
}

func (e *ImportRowError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("row %d (line %d): %v", e.Row, e.Line, e.Err)
	}
	return fmt.Sprintf("row %d: %v", e.Row, e.Err)
}

func (e *ImportRowError) Unwrap() error { return e.Err }

// ImportInsertSQL renders the statement ImportRows prepares: a plain
// parameterized INSERT naming exactly the mapped columns.
func ImportInsertSQL(d Dialect, database, table string, columns []string) string {
	var b strings.Builder
	b.WriteString("INSERT INTO ")
	b.WriteString(qualifiedTable(d, database, table))
	b.WriteString(" (")
	for i, c := range columns {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(d.QuoteIdent(c))
	}
	b.WriteString(") VALUES (")
	for i := range columns {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(d.Placeholder(i + 1))
	}
	b.WriteString(")")
	return b.String()
}

// ImportRows runs req in one transaction and returns how many rows it
// inserted. On any error — a refused row, a source error, a cancelled
// context — the transaction is rolled back and the count is how many rows
// had been inserted before it, none of which remain.
func (c *conn) ImportRows(ctx context.Context, req ImportRequest) (int64, error) {
	if c.db == nil {
		return 0, errNotConnected
	}
	if len(req.Columns) == 0 {
		return 0, errors.New("db: import maps no columns")
	}
	if req.Next == nil {
		return 0, errors.New("db: import has no row source")
	}
	insert := ImportInsertSQL(c.dialect, req.Database, req.Table, req.Columns)
	if c.readOnly {
		return 0, c.rejectWrite(insert, nil)
	}
	every := int64(req.ProgressEvery)
	if every <= 0 {
		every = 1000
	}

	beginStart := time.Now()
	tx, err := c.db.BeginTx(ctx, nil)
	c.logger.record("BEGIN", nil, beginStart, err)
	if err != nil {
		return 0, err
	}
	rollback := func() {
		start := time.Now()
		err := tx.Rollback()
		// A cancelled ctx has database/sql roll the transaction back on
		// its own; that is the outcome asked for, not a failure.
		if errors.Is(err, sql.ErrTxDone) {
			err = nil
		}
		c.logger.record("ROLLBACK", nil, start, err)
	}

	start := time.Now()
	stmt, err := tx.PrepareContext(ctx, insert)
	if err != nil {
		c.logger.record(insert, nil, start, err)
		rollback()
		return 0, err
	}
	var (
		rows    int64
		failErr error
		failArg []any
	)
	for {
		if err := ctx.Err(); err != nil {
			failErr = err
			break
		}
		row, err := req.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			failErr = err
			break
		}
		if len(row.Values) != len(req.Columns) {
			failErr = &ImportRowError{Row: rows + 1, Line: row.Line, Values: row.Values,
				Err: fmt.Errorf("%d values for %d columns", len(row.Values), len(req.Columns))}
			break
		}
		if _, err := stmt.ExecContext(ctx, row.Values...); err != nil {
			if ctx.Err() != nil {
				failErr = ctx.Err()
			} else {
				failErr = &ImportRowError{Row: rows + 1, Line: row.Line, Values: row.Values, Err: err}
				failArg = row.Values
			}
			break
		}
		rows++
		if req.Progress != nil && rows%every == 0 {
			req.Progress(rows)
		}
	}
	stmt.Close()
	c.logger.record(fmt.Sprintf("%s -- ×%d rows", insert, rows), failArg, start, failErr)
	if failErr != nil {
		rollback()
		return rows, failErr
	}

	commitStart := time.Now()
	err = tx.Commit()
	c.logger.record("COMMIT", nil, commitStart, err)
	if err != nil {
		return 0, err
	}
	return rows, nil
}

// ValueClass is the coarse type an import converts a text field to before
// it binds it. It is coarser than the engines' own types on purpose: what
// matters is which Go value the driver is handed and which texts are
// refused before they reach the engine, not the exact width.
type ValueClass int

const (
	// ClassText binds the field as a string — any type lazysql does not
	// recognize, so an unknown type is never refused on a guess.
	ClassText ValueClass = iota
	ClassInteger
	ClassFloat
	// ClassDecimal is validated as a number but bound as its text, so
	// NUMERIC(38,10) keeps every digit a float64 would lose.
	ClassDecimal
	ClassBool
	ClassDate
	ClassTime
	ClassDateTime
)

// String names the class the way a type mismatch reads it.
func (c ValueClass) String() string {
	switch c {
	case ClassInteger:
		return "an integer"
	case ClassFloat, ClassDecimal:
		return "a number"
	case ClassBool:
		return "a boolean"
	case ClassDate:
		return "a date"
	case ClassTime:
		return "a time"
	case ClassDateTime:
		return "a date/time"
	}
	return "text"
}

// ClassifyValue maps a declared column type to the class its import values
// are converted to. Matching is on the normalized type's first word, so
// `INT UNSIGNED`, `bigint(20)` and `DOUBLE PRECISION` land where they
// should; temporal types defer to ClassifyType.
func ClassifyValue(dataType string) ValueClass {
	switch ClassifyType(dataType) {
	case KindDate:
		return ClassDate
	case KindTime:
		return ClassTime
	case KindDateTime:
		return ClassDateTime
	}
	words := strings.Fields(normalizeTypeName(dataType))
	if len(words) == 0 {
		return ClassText
	}
	switch words[0] {
	case "int", "integer", "tinyint", "smallint", "mediumint", "bigint",
		"int2", "int4", "int8", "serial", "smallserial", "bigserial",
		"serial2", "serial4", "serial8",
		// DuckDB's unsigned spellings.
		"utinyint", "usmallint", "uinteger", "ubigint":
		return ClassInteger
	case "real", "float", "float4", "float8", "double":
		return ClassFloat
	case "numeric", "decimal", "number", "hugeint", "uhugeint", "money":
		return ClassDecimal
	case "bool", "boolean":
		return ClassBool
	}
	return ClassText
}

// ConvertText turns one text field into the value bound for a column of
// dataType. A field that does not read as the column's class is refused
// with an error naming the class, never coerced: silently turning "n/a"
// into 0 is exactly the data loss an import must not cause.
func ConvertText(dataType, text string) (any, error) {
	class := ClassifyValue(dataType)
	if class == ClassText {
		return text, nil
	}
	s := strings.TrimSpace(text)
	fail := func() (any, error) {
		return nil, fmt.Errorf("%q is not %s", text, class)
	}
	switch class {
	case ClassInteger:
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n, nil
		}
		// An unsigned 64-bit value past int64 still fits the column; the
		// drivers take it as text.
		if _, err := strconv.ParseUint(s, 10, 64); err == nil {
			return s, nil
		}
		return fail()
	case ClassFloat:
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return fail()
		}
		return f, nil
	case ClassDecimal:
		if _, ok := new(big.Float).SetString(s); !ok {
			return fail()
		}
		return s, nil
	case ClassBool:
		switch strings.ToLower(s) {
		case "1", "t", "true", "y", "yes", "on":
			return true, nil
		case "0", "f", "false", "n", "no", "off":
			return false, nil
		}
		return fail()
	case ClassDate, ClassTime, ClassDateTime:
		// Validated with the same lenient layouts the grid reads, then
		// bound as the text itself: every engine parses its own ISO
		// spellings, and re-rendering would drop a zone the text carried.
		if _, ok := ParseDateTime(s); !ok {
			return fail()
		}
		return s, nil
	}
	return text, nil
}
