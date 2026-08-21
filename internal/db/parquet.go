package db

import (
	"path/filepath"
	"strings"
	"unicode"
)

// Parquet is a file format, not a database: it has no catalog, no engine
// and therefore no dialect here. lazysql browses one by opening an
// in-memory DuckDB and exposing the file as a view over read_parquet(),
// which is the same thing `duckdb file.parquet` would have you type. The
// session is read-only, so the view is never written through — see
// wiki/design/ephemeral-file-connections.md.

// ParquetViewName is the relation a Parquet file is exposed as: its own
// base name, with everything that is not a letter, a digit or an
// underscore folded to "_" so the name needs no quoting to be typed in
// the query editor. A name starting with a digit gets a leading "_" —
// SQL identifiers may not start with one — and a file whose name has
// nothing usable in it at all gets the neutral "data".
func ParquetViewName(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	var b strings.Builder
	for _, r := range base {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			b.WriteRune(r)
			continue
		}
		b.WriteRune('_')
	}
	name := strings.Trim(b.String(), "_")
	if name == "" {
		return "data"
	}
	if unicode.IsDigit(rune(name[0])) {
		name = "_" + name
	}
	return name
}

// ParquetViewSQL is the statement that makes a Parquet file browsable:
// the view every listing, page and DDL lookup then goes through. It runs
// as a session setup statement (Options.Setup), so it is logged like any
// other statement and still applies on a read-only session.
func ParquetViewSQL(view, path string) (string, error) {
	d, err := DialectFor(EngineDuckDB)
	if err != nil {
		return "", err
	}
	return "CREATE VIEW " + QuoteIdentifier(d, view) +
		" AS SELECT * FROM read_parquet(" + QuoteLiteral(d, path) + ")", nil
}
