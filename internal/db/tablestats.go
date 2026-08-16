package db

import (
	"context"
	"strconv"
)

// Table statistics for the [2] Objects tree: how many rows a table holds
// and how much space it occupies. Every figure comes from the engine's
// own catalog — pg_class.reltuples, information_schema.tables.table_rows,
// duckdb_tables().estimated_size, SQLite's ANALYZE output — so one query
// answers for a whole namespace and no table is ever counted. A COUNT(*)
// per table would turn expanding a branch into a full scan of every table
// under it, which is exactly what the annotation exists to warn about.
//
// The price is that every figure is an estimate and some are badly stale;
// the UI says so with a leading `~`. See
// wiki/reference/table-size-estimates.md.

// StatUnknown is the value of TableStat.Rows/Bytes when the engine has
// no figure to give: never analyzed, no such catalog, or a kind of
// relation the catalog does not size.
const StatUnknown int64 = -1

// TableStat is one table's approximate size. Rows and Bytes are
// StatUnknown when unavailable — an engine that reports neither still
// yields an entry, so "asked and got nothing" stays distinguishable from
// "never asked".
//
// Bytes, where an engine reports it, includes the table's indexes: it
// answers "how much disk does this table cost me", not "how wide are its
// rows".
type TableStat struct {
	Table string
	Rows  int64
	Bytes int64
}

// Known reports whether the stat carries anything worth showing.
func (s TableStat) Known() bool { return s.Rows != StatUnknown || s.Bytes != StatUnknown }

// TableStatMap keys a namespace's stats by table name.
func TableStatMap(stats []TableStat) map[string]TableStat {
	out := make(map[string]TableStat, len(stats))
	for _, s := range stats {
		out[s.Table] = s
	}
	return out
}

// scanTableStats runs a (name, rows, bytes) query and normalizes it.
// Missing, NULL and negative figures all become StatUnknown: PostgreSQL
// spells "never analyzed" as reltuples = -1, and a negative size is
// meaningless in every engine.
func scanTableStats(ctx context.Context, q querier, query string, args ...any) ([]TableStat, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TableStat
	for rows.Next() {
		var name string
		var rowCount, size any
		if err := rows.Scan(&name, &rowCount, &size); err != nil {
			return nil, err
		}
		out = append(out, TableStat{
			Table: name,
			Rows:  statInt(rowCount),
			Bytes: statInt(size),
		})
	}
	return out, rows.Err()
}

// statInt coerces one catalog figure to a count. Engines and drivers
// disagree on the type a catalog number arrives as — MySQL hands
// information_schema numbers back as text or as uint64, DuckDB as an
// int32 for a small estimate — so the type switch is wider than the
// column types suggest.
func statInt(v any) int64 {
	var n int64
	switch x := v.(type) {
	case nil:
		return StatUnknown
	case int64:
		n = x
	case int32:
		n = int64(x)
	case int:
		n = int64(x)
	case uint64:
		n = int64(x)
	case uint32:
		n = int64(x)
	case float64:
		n = int64(x)
	case []byte:
		return statInt(string(x))
	case string:
		p, err := strconv.ParseInt(x, 10, 64)
		if err != nil {
			f, ferr := strconv.ParseFloat(x, 64)
			if ferr != nil {
				return StatUnknown
			}
			p = int64(f)
		}
		n = p
	default:
		return StatUnknown
	}
	if n < 0 {
		return StatUnknown
	}
	return n
}
