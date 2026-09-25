package db

import (
	"testing"
	"time"
)

func TestClassifyType(t *testing.T) {
	cases := []struct {
		in   string
		want TypeKind
	}{
		// MySQL / MariaDB.
		{"DATE", KindDate},
		{"datetime", KindDateTime},
		{"DATETIME(6)", KindDateTime},
		{"timestamp", KindDateTime},
		{"TIMESTAMP(3)", KindDateTime},
		{"time", KindTime},
		{"TIME(6)", KindTime},
		{"year", KindOther},
		{"YEAR(4)", KindOther},

		// PostgreSQL.
		{"timestamp without time zone", KindDateTime},
		{"timestamp with time zone", KindDateTime},
		{"TIMESTAMP(6) WITH TIME ZONE", KindDateTime},
		{"timestamptz", KindDateTime},
		{"time without time zone", KindTime},
		{"time with time zone", KindTime},
		{"timetz", KindTime},
		{"date", KindDate},
		{"date[]", KindDate},
		{"interval", KindOther},

		// SQLite declared affinities.
		{"DATE", KindDate},
		{"DATETIME", KindDateTime},
		{"TIMESTAMP", KindDateTime},
		{"TEXT", KindOther},
		{"NUMERIC", KindOther},
		{"INTEGER", KindOther},

		// DuckDB.
		{"TIMESTAMP_NS", KindDateTime},
		{"TIMESTAMP_S", KindDateTime},
		{"TIMESTAMP_MS", KindDateTime},
		{"TIMESTAMP_US", KindDateTime},
		{"TIMESTAMP WITH TIME ZONE", KindDateTime},

		// Noise and near-misses.
		{"", KindOther},
		{"  date  ", KindDate},
		{"varchar(255)", KindOther},
		{"datetime_text", KindOther},
		{"mydate", KindOther},
		{"timestamp_range", KindOther},
	}
	for _, c := range cases {
		if got := ClassifyType(c.in); got != c.want {
			t.Errorf("ClassifyType(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestTypeKindParts(t *testing.T) {
	cases := []struct {
		k                TypeKind
		date, time, temp bool
		layout           string
	}{
		{KindOther, false, false, false, ""},
		{KindDate, true, false, true, "2006-01-02"},
		{KindTime, false, true, true, "15:04:05"},
		{KindDateTime, true, true, true, "2006-01-02 15:04:05"},
	}
	for _, c := range cases {
		if c.k.HasDate() != c.date || c.k.HasTime() != c.time || c.k.Temporal() != c.temp {
			t.Errorf("kind %v: date=%v time=%v temporal=%v",
				c.k, c.k.HasDate(), c.k.HasTime(), c.k.Temporal())
		}
		if got := c.k.Layout(); got != c.layout {
			t.Errorf("kind %v layout = %q, want %q", c.k, got, c.layout)
		}
	}
}

func TestParseDateTime(t *testing.T) {
	cases := []struct {
		in   string
		want string // RFC3339Nano in UTC, empty means "must not parse"
	}{
		{"2026-08-10", "2026-08-10T00:00:00Z"},
		{"2026-08-10 14:32:07", "2026-08-10T14:32:07Z"},
		{"2026-08-10T14:32:07", "2026-08-10T14:32:07Z"},
		{"2026-08-10 14:32", "2026-08-10T14:32:00Z"},
		{"2026-08-10T14:32:07Z", "2026-08-10T14:32:07Z"},
		{"2026-08-10 14:32:07.5", "2026-08-10T14:32:07.5Z"},
		{"14:32:07", "0000-01-01T14:32:07Z"},
		{"14:32", "0000-01-01T14:32:00Z"},
		{"  2026-08-10  ", "2026-08-10T00:00:00Z"},
		{"", ""},
		{"not a date", ""},
		{"now()", ""},
	}
	for _, c := range cases {
		got, ok := ParseDateTime(c.in)
		if c.want == "" {
			if ok {
				t.Errorf("ParseDateTime(%q) parsed as %v, want failure", c.in, got)
			}
			continue
		}
		if !ok {
			t.Errorf("ParseDateTime(%q) failed", c.in)
			continue
		}
		if s := got.Format(time.RFC3339Nano); s != c.want {
			t.Errorf("ParseDateTime(%q) = %q, want %q", c.in, s, c.want)
		}
	}
}

// FormatTemporalValue drops the half of a temporal value its column's
// declared type does not carry, for every engine's spelling of DATE and
// TIME, and leaves everything else — DATETIME/TIMESTAMP values, and
// values that fail to parse — unchanged from FormatValue. Issue #214.
func TestFormatTemporalValue(t *testing.T) {
	utcTime := time.Date(2026, 8, 2, 14, 32, 7, 0, time.UTC)
	cases := []struct {
		name string
		typ  string // declared column type, run through ClassifyType
		v    any
		want string
	}{
		// MySQL / MariaDB.
		{"mysql date, time.Time", "DATE", utcTime, "2026-08-02"},
		{"mysql time, time.Time", "TIME(6)", utcTime, "14:32:07"},
		{"mysql datetime unchanged", "DATETIME", utcTime, "2026-08-02T14:32:07Z"},

		// PostgreSQL.
		{"postgres date, time.Time", "date", utcTime, "2026-08-02"},
		{"postgres time, time.Time", "time without time zone", utcTime, "14:32:07"},
		{"postgres timestamptz unchanged", "timestamptz", utcTime, "2026-08-02T14:32:07Z"},

		// SQLite (declared affinity, value arrives as text).
		{"sqlite date, string", "DATE", "2026-08-02", "2026-08-02"},
		{"sqlite date, full timestamp string", "DATE", "2026-08-02T00:00:00Z", "2026-08-02"},
		{"sqlite time, string", "TIME", "14:32:07", "14:32:07"},
		{"sqlite datetime unchanged", "DATETIME", "2026-08-02 14:32:07", "2026-08-02 14:32:07"},

		// DuckDB.
		{"duckdb timestamp_ns unchanged", "TIMESTAMP_NS", utcTime, "2026-08-02T14:32:07Z"},

		// Non-temporal and unparseable values pass through untouched.
		{"non-temporal column", "TEXT", "hello", "hello"},
		{"date column, unparseable text", "DATE", "not a date", "not a date"},
		{"nil value", "DATE", nil, "NULL"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			kind := ClassifyType(c.typ)
			if got := FormatTemporalValue(c.v, kind, "NULL"); got != c.want {
				t.Errorf("FormatTemporalValue(%v, %v, ...) = %q, want %q", c.v, kind, got, c.want)
			}
		})
	}
}

func TestParseDateTimeInLocation(t *testing.T) {
	loc := time.FixedZone("X", 2*60*60)
	got, ok := ParseDateTimeIn("2026-08-10 14:32:07", loc)
	if !ok {
		t.Fatal("ParseDateTimeIn failed")
	}
	if got.Location() != loc {
		t.Errorf("location = %v, want %v", got.Location(), loc)
	}
	if got.UTC().Format(time.RFC3339) != "2026-08-10T12:32:07Z" {
		t.Errorf("wall clock not kept in loc: %v", got)
	}
}
