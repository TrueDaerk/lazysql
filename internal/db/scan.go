package db

import (
	"context"
	"database/sql"
	"time"
)

// scanResultSet materializes *sql.Rows into a driver-agnostic ResultSet.
func scanResultSet(ctx context.Context, rows *sql.Rows) (*ResultSet, error) {
	rs, _, err := scanResultSetLimit(ctx, rows, 0)
	return rs, err
}

// scanResultSetLimit is scanResultSet with a cap on how many rows are
// materialized; max <= 0 means unlimited. truncated reports that the
// server still had rows when the cap was reached — the caller needs to
// say so, because a silently short result reads as a complete one.
//
// Every cell is scanned into `any` and then normalized so the UI never
// sees driver-private types. The context is checked between rows so a
// cancelled query stops promptly even if the driver keeps yielding.
func scanResultSetLimit(ctx context.Context, rows *sql.Rows, max int) (*ResultSet, bool, error) {
	cols, err := columnsOf(rows)
	if err != nil {
		return nil, false, err
	}

	rs := &ResultSet{Columns: cols, Rows: [][]any{}}
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		if max > 0 && len(rs.Rows) >= max {
			return rs, true, rows.Err()
		}
		row, err := scanOneRow(rows, len(cols))
		if err != nil {
			return nil, false, err
		}
		rs.Rows = append(rs.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	return rs, false, nil
}

// streamRows walks rows one at a time, handing each to onRow instead of
// accumulating a ResultSet: this is what lets a query-result export cost
// one row of memory whatever the statement returns. The context is
// checked between rows for the same reason scanResultSetLimit checks it.
func streamRows(ctx context.Context, rows *sql.Rows, onRow func(cols []Column, row []any) error) error {
	cols, err := columnsOf(rows)
	if err != nil {
		return err
	}
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		row, err := scanOneRow(rows, len(cols))
		if err != nil {
			return err
		}
		if err := onRow(cols, row); err != nil {
			return err
		}
	}
	return rows.Err()
}

// columnsOf reads a *sql.Rows result's column metadata into the
// driver-agnostic Column shape, shared by scanResultSetLimit and
// streamRows so the two never disagree about it.
func columnsOf(rows *sql.Rows) ([]Column, error) {
	types, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}
	cols := make([]Column, len(types))
	for i, t := range types {
		nullable, _ := t.Nullable()
		cols[i] = Column{
			Name:     t.Name(),
			DataType: t.DatabaseTypeName(),
			Nullable: nullable,
		}
	}
	return cols, nil
}

// scanOneRow reads and normalizes the row rows is currently positioned
// on, into a fresh slice: the driver may reuse its own scan buffers, but
// nothing scanOneRow returns is ever backed by one.
func scanOneRow(rows *sql.Rows, n int) ([]any, error) {
	raw := make([]any, n)
	ptrs := make([]any, n)
	for i := range raw {
		ptrs[i] = &raw[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return nil, err
	}
	row := make([]any, n)
	for i, v := range raw {
		row[i] = normalizeValue(v)
	}
	return row, nil
}

// normalizeValue maps whatever the driver produced onto the small set of
// types ResultSet promises: nil, string, int64, float64, bool, time.Time.
// []byte is copied into a string because drivers may reuse the buffer.
func normalizeValue(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case []byte:
		return string(x)
	case string, int64, float64, bool, time.Time:
		return x
	case int:
		return int64(x)
	case int32:
		return int64(x)
	case uint64:
		return int64(x)
	case float32:
		return float64(x)
	default:
		return FormatValue(x, "")
	}
}
