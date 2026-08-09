package db

import (
	"context"
	"database/sql"
	"time"
)

// scanResultSet materializes *sql.Rows into a driver-agnostic ResultSet.
// Every cell is scanned into `any` and then normalized so the UI never
// sees driver-private types. The context is checked between rows so a
// cancelled query stops promptly even if the driver keeps yielding.
func scanResultSet(ctx context.Context, rows *sql.Rows) (*ResultSet, error) {
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

	rs := &ResultSet{Columns: cols, Rows: [][]any{}}
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		raw := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make([]any, len(cols))
		for i, v := range raw {
			row[i] = normalizeValue(v)
		}
		rs.Rows = append(rs.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return rs, nil
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
