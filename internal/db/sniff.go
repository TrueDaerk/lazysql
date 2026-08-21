package db

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// FileFormat is what a local database file turned out to be. It is not an
// Engine: Parquet has no dialect of its own — it is browsed through DuckDB
// (see ParquetViewSQL and wiki/design/ephemeral-file-connections.md).
type FileFormat string

const (
	FormatSQLite  FileFormat = "sqlite"
	FormatDuckDB  FileFormat = "duckdb"
	FormatParquet FileFormat = "parquet"
)

// Engine is the engine a file of this format is opened with. Parquet maps
// to DuckDB, which reads it through read_parquet().
func (f FileFormat) Engine() Engine {
	switch f {
	case FormatSQLite:
		return EngineSQLite
	case FormatDuckDB, FormatParquet:
		return EngineDuckDB
	}
	return ""
}

// ErrUnknownFormat is returned for a file that is neither recognizable by
// its leading bytes nor by an extension lazysql knows.
var ErrUnknownFormat = errors.New("db: not a SQLite, DuckDB or Parquet file")

// The magic each format starts with. lazysql sniffs rather than trusting
// the extension because `.db` alone says nothing: it is as common for
// DuckDB as it is for SQLite.
//
//   - SQLite writes its header string, NUL included, at offset 0.
//   - DuckDB's main header is an 8-byte checksum followed by "DUCK" at
//     offset 8.
//   - Parquet brackets the file with "PAR1" at both ends; the leading one
//     is enough to recognize it, and reading the footer would mean a seek
//     to the end for no extra certainty.
const (
	sqliteMagic  = "SQLite format 3\x00"
	duckdbMagic  = "DUCK"
	duckdbOffset = 8
	parquetMagic = "PAR1"

	// sniffLen is how much of the head every check together needs.
	sniffLen = 16
)

// SniffFormat classifies a file from its leading bytes. head may be
// shorter than sniffLen — a truncated read simply matches nothing.
func SniffFormat(head []byte) (FileFormat, bool) {
	s := string(head)
	switch {
	case strings.HasPrefix(s, sqliteMagic):
		return FormatSQLite, true
	case strings.HasPrefix(s, parquetMagic):
		return FormatParquet, true
	case len(s) >= duckdbOffset+len(duckdbMagic) && s[duckdbOffset:duckdbOffset+len(duckdbMagic)] == duckdbMagic:
		return FormatDuckDB, true
	}
	return "", false
}

// FormatForExt is the fallback for a file whose magic said nothing — an
// empty file a driver would create from scratch, or a Parquet file behind
// a wrapper. `.db` is deliberately absent: it is the ambiguous one, and
// guessing it wrong opens the file with the wrong engine.
func FormatForExt(path string) (FileFormat, bool) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".sqlite", ".sqlite3", ".sq3", ".s3db":
		return FormatSQLite, true
	case ".duckdb", ".ddb":
		return FormatDuckDB, true
	case ".parquet", ".parq", ".pqt":
		return FormatParquet, true
	}
	return "", false
}

// SniffFile reports what kind of database file path holds. The file must
// already exist: SQLite and DuckDB both create a database on open, so a
// typo'd path must fail here rather than leave a stray empty file behind.
// The magic bytes decide; the extension is only consulted when they say
// nothing.
func SniffFile(path string) (FileFormat, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s: is a directory", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	head := make([]byte, sniffLen)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", err
	}
	if format, ok := SniffFormat(head[:n]); ok {
		return format, nil
	}
	if format, ok := FormatForExt(path); ok {
		return format, nil
	}
	return "", fmt.Errorf("%s: %w", path, ErrUnknownFormat)
}
