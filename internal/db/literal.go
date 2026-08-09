package db

import (
	"math"
	"strconv"
	"strings"
	"time"
)

// SQL literals. Everything lazysql *executes* is parameterized — see
// changeset.go — so this file exists only for the SQL text lazysql
// *hands to the user*: the INSERT statements of a copy or an export.
// Those have to carry their values inline, which makes correct
// per-dialect escaping the whole job.

// QuoteLiteral renders a normalized cell value (nil, string, int64,
// float64, bool, time.Time) as a SQL literal for the dialect. Anything
// else is rendered through FormatValue and quoted as text, which is the
// safe reading for a type the scanner could not normalize.
func QuoteLiteral(d Dialect, v any) string {
	switch x := v.(type) {
	case nil:
		return "NULL"
	case string:
		return quoteString(d, x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		// Neither infinity nor NaN is a portable numeric literal; every
		// engine that accepts them at all accepts the quoted spelling.
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return quoteString(d, strconv.FormatFloat(x, 'g', -1, 64))
		}
		return strconv.FormatFloat(x, 'g', -1, 64)
	case bool:
		return boolLiteral(d, x)
	case time.Time:
		return quoteString(d, timeLiteral(d, x))
	default:
		return quoteString(d, FormatValue(x, ""))
	}
}

// quoteString escapes a string literal. Doubling the single quote is
// universal; MySQL and MariaDB additionally read a backslash as an
// escape character (NO_BACKSLASH_ESCAPES off, which is the default), so
// there the backslash has to be doubled too. Doing that unconditionally
// would corrupt every Windows path exported from PostgreSQL, where
// standard_conforming_strings leaves the backslash literal.
func quoteString(d Dialect, s string) string {
	// The backslash pass runs first: doubling a quote never introduces a
	// backslash, and doubling a backslash never introduces a quote, so
	// neither pass can see the other's output.
	if backslashEscapes(d) {
		s = strings.ReplaceAll(s, "\\", "\\\\")
	}
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// backslashEscapes reports whether the engine treats a backslash inside
// a string literal as an escape character.
func backslashEscapes(d Dialect) bool {
	switch engineOf(d) {
	case EngineMySQL, EngineMariaDB:
		return true
	default:
		return false
	}
}

// boolLiteral spells a boolean. MySQL and MariaDB have no real boolean
// type — TRUE/FALSE are aliases for 1/0 there — and a TINYINT column
// round-trips more predictably as the number it stores.
func boolLiteral(d Dialect, b bool) string {
	switch engineOf(d) {
	case EngineMySQL, EngineMariaDB:
		if b {
			return "1"
		}
		return "0"
	default:
		return strings.ToUpper(strconv.FormatBool(b))
	}
}

// timeLiteral formats a timestamp for the dialect. MySQL and MariaDB
// reject the RFC 3339 "T" separator and the zone suffix in a DATETIME
// literal, so they get the SQL spelling in UTC; every other engine
// parses RFC 3339 and keeps the offset.
func timeLiteral(d Dialect, t time.Time) string {
	switch engineOf(d) {
	case EngineMySQL, EngineMariaDB:
		return t.UTC().Format("2006-01-02 15:04:05.999999")
	default:
		return t.Format(time.RFC3339Nano)
	}
}

// engineOf is nil-safe so serialization code can render values without
// a connection — the fallback is the standard-conforming spelling.
func engineOf(d Dialect) Engine {
	if d == nil {
		return ""
	}
	return d.Engine()
}

// QualifiedTable renders database.table with per-dialect quoting,
// omitting the database part when empty. With no dialect the name is
// returned unquoted, which is what a preview without a connection wants.
func QualifiedTable(d Dialect, database, table string) string {
	if d == nil {
		if database == "" {
			return table
		}
		return database + "." + table
	}
	return qualifiedTable(d, database, table)
}

// QuoteIdentifier quotes one identifier, nil-safe like QualifiedTable.
func QuoteIdentifier(d Dialect, ident string) string {
	if d == nil {
		return ident
	}
	return d.QuoteIdent(ident)
}

// InsertStatement renders a literal (non-parameterized) INSERT for the
// copy and export flows. Unlike InsertSQL it is text the user takes
// away, never something lazysql executes.
func InsertStatement(d Dialect, database, table string, cols []Column, values []any) string {
	var b strings.Builder
	b.WriteString("INSERT INTO ")
	b.WriteString(QualifiedTable(d, database, table))
	b.WriteString(" (")
	for i, c := range cols {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(QuoteIdentifier(d, c.Name))
	}
	b.WriteString(") VALUES (")
	for i := range cols {
		if i > 0 {
			b.WriteString(", ")
		}
		var v any
		if i < len(values) {
			v = values[i]
		}
		b.WriteString(QuoteLiteral(d, v))
	}
	b.WriteString(");")
	return b.String()
}
