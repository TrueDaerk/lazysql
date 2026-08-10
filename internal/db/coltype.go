package db

import (
	"strings"
	"time"
)

// Coarse classification of a dialect-reported column type. The UI asks for
// it to decide which editor a value gets — a calendar and time spinners
// instead of a bare text field — so the only distinctions that matter are
// "does this carry a date part" and "does it carry a time part".
//
// Every engine spells the same three concepts differently (MySQL
// `DATETIME`, PostgreSQL `timestamp without time zone`, DuckDB
// `TIMESTAMP_NS`, SQLite whatever the CREATE TABLE happened to declare),
// so the mapping lives here, next to the dialects, rather than in UI code.
type TypeKind int

const (
	// KindOther is the zero value: anything that is not a date or a time,
	// which is most columns. It keeps the plain text editor.
	KindOther TypeKind = iota
	KindDate
	KindTime
	KindDateTime
)

// HasDate reports whether values of the kind carry a calendar date.
func (k TypeKind) HasDate() bool { return k == KindDate || k == KindDateTime }

// HasTime reports whether values of the kind carry a clock time.
func (k TypeKind) HasTime() bool { return k == KindTime || k == KindDateTime }

// Temporal reports whether the kind is any of the date/time kinds.
func (k TypeKind) Temporal() bool { return k != KindOther }

// Layout is the ISO rendering every supported engine accepts as a literal
// for the kind — the format the date picker produces.
func (k TypeKind) Layout() string {
	switch k {
	case KindDate:
		return "2006-01-02"
	case KindTime:
		return "15:04:05"
	case KindDateTime:
		return "2006-01-02 15:04:05"
	}
	return ""
}

// ClassifyType maps a declared column type to its coarse kind. Matching is
// case-insensitive and ignores precision (`timestamp(6)`), array markers
// (`date[]`) and the SQL spelling of the time zone qualifier. SQLite has no
// real date type at all, so the declared affinity string is all there is to
// go on — which is exactly what this reads.
//
// Anything unrecognized is KindOther: guessing a date editor onto a column
// that only looks temporal would replace a working text field with one that
// cannot express the value.
func ClassifyType(dataType string) TypeKind {
	switch normalizeTypeName(dataType) {
	case "date":
		return KindDate
	case "time", "timetz", "time with time zone", "time without time zone":
		return KindTime
	case "datetime", "datetime2", "smalldatetime", "datetimeoffset",
		"timestamp", "timestamptz",
		"timestamp with time zone", "timestamp without time zone",
		// DuckDB's per-precision timestamp aliases.
		"timestamp_s", "timestamp_ms", "timestamp_us", "timestamp_ns":
		return KindDateTime
	}
	return KindOther
}

// normalizeTypeName lowercases a declared type and strips the parts that
// never change what the type is: the precision in parentheses, a trailing
// array marker, and any run of whitespace collapsed to single spaces (so
// `TIMESTAMP (6)  WITH TIME ZONE` reads as `timestamp with time zone`).
func normalizeTypeName(dataType string) string {
	s := strings.ToLower(strings.TrimSpace(dataType))
	for strings.HasSuffix(s, "[]") {
		s = strings.TrimSpace(strings.TrimSuffix(s, "[]"))
	}
	for {
		open := strings.IndexByte(s, '(')
		if open < 0 {
			break
		}
		end := strings.IndexByte(s[open:], ')')
		if end < 0 {
			s = s[:open]
			break
		}
		s = s[:open] + " " + s[open+end+1:]
	}
	return strings.Join(strings.Fields(s), " ")
}

// dateTimeLayouts are the spellings a stored or displayed date/time value
// may arrive in. Drivers hand back time.Time for the engines that have a
// real date type; SQLite and DuckDB hand back whatever text the row holds,
// which is why parsing has to be lenient. Ordered longest-first so a value
// is never truncated by an earlier, shorter layout matching its prefix.
var dateTimeLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02 15:04:05.999999999 -0700 MST",
	"2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006-01-02T15:04",
	"2006-01-02",
	"15:04:05.999999999",
	"15:04:05",
	"15:04",
}

// ParseDateTimeIn parses a displayed date/time value in loc, trying every
// layout lazysql may have rendered or an engine may have stored. A
// time-only value lands on the zero date and a date-only value at
// midnight; callers that care merge the missing half themselves.
func ParseDateTimeIn(text string, loc *time.Location) (time.Time, bool) {
	s := strings.TrimSpace(text)
	if s == "" {
		return time.Time{}, false
	}
	if loc == nil {
		loc = time.UTC
	}
	for _, layout := range dateTimeLayouts {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// ParseDateTime is ParseDateTimeIn in UTC.
func ParseDateTime(text string) (time.Time, bool) { return ParseDateTimeIn(text, time.UTC) }
