package db

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// The header bytes each format really starts with, spelled out here so a
// change to the sniffer has to disagree with the file format itself, not
// just with its own constants.
func sqliteHeader() []byte  { return append([]byte("SQLite format 3\x00"), 0x10, 0x00) }
func duckdbHeader() []byte  { return append([]byte{1, 2, 3, 4, 5, 6, 7, 8}, []byte("DUCK....")...) }
func parquetHeader() []byte { return []byte("PAR1abcdefghijkl") }

func TestSniffFormat(t *testing.T) {
	cases := []struct {
		name string
		head []byte
		want FileFormat
		ok   bool
	}{
		{"sqlite", sqliteHeader(), FormatSQLite, true},
		{"duckdb", duckdbHeader(), FormatDuckDB, true},
		{"parquet", parquetHeader(), FormatParquet, true},
		{"empty", nil, "", false},
		{"truncated duckdb", []byte("\x00\x00\x00\x00DUCK"), "", false},
		{"text", []byte("SELECT 1;\n"), "", false},
		// "DUCK" anywhere but offset 8 is not a DuckDB header.
		{"duck at offset 0", []byte("DUCK............"), "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := SniffFormat(tc.head)
			if got != tc.want || ok != tc.ok {
				t.Errorf("SniffFormat = (%q, %v), want (%q, %v)", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestFormatEngine(t *testing.T) {
	// Parquet has no dialect: it is browsed through DuckDB.
	cases := map[FileFormat]Engine{
		FormatSQLite:  EngineSQLite,
		FormatDuckDB:  EngineDuckDB,
		FormatParquet: EngineDuckDB,
		"nonsense":    "",
	}
	for format, want := range cases {
		if got := format.Engine(); got != want {
			t.Errorf("%s.Engine() = %q, want %q", format, got, want)
		}
	}
}

// writeFile drops one fixture file and returns its path.
func writeFile(t *testing.T, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The magic wins over the extension: a DuckDB database called .sqlite is
// still a DuckDB database, and `.db` — which says nothing on its own — is
// resolved by its content alone.
func TestSniffFileUsesMagicOverExtension(t *testing.T) {
	cases := []struct {
		name    string
		content []byte
		want    FileFormat
	}{
		{"data.db", sqliteHeader(), FormatSQLite},
		{"data.db", duckdbHeader(), FormatDuckDB},
		{"lies.sqlite", duckdbHeader(), FormatDuckDB},
		{"lies.duckdb", sqliteHeader(), FormatSQLite},
		{"sales.parquet", parquetHeader(), FormatParquet},
		{"noext", parquetHeader(), FormatParquet},
	}
	for _, tc := range cases {
		got, err := SniffFile(writeFile(t, tc.name, tc.content))
		if err != nil {
			t.Fatalf("SniffFile(%s): %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("SniffFile(%s) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// A file whose head says nothing — an empty database file a driver would
// have created — falls back to the extension, except for the ambiguous
// `.db`, which stays an error rather than a coin flip.
func TestSniffFileExtensionFallback(t *testing.T) {
	cases := []struct {
		name string
		want FileFormat
		err  bool
	}{
		{"empty.sqlite3", FormatSQLite, false},
		{"empty.ddb", FormatDuckDB, false},
		{"empty.PARQUET", FormatParquet, false},
		{"empty.db", "", true},
		{"empty.txt", "", true},
	}
	for _, tc := range cases {
		got, err := SniffFile(writeFile(t, tc.name, nil))
		switch {
		case tc.err && !errors.Is(err, ErrUnknownFormat):
			t.Errorf("SniffFile(%s) error = %v, want ErrUnknownFormat", tc.name, err)
		case !tc.err && err != nil:
			t.Errorf("SniffFile(%s): %v", tc.name, err)
		case !tc.err && got != tc.want:
			t.Errorf("SniffFile(%s) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// Sniffing must never bring the file into existence: SQLite and DuckDB
// both create a database on open, so a typo'd path has to fail here.
func TestSniffFileMissingPathCreatesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope.sqlite")
	if _, err := SniffFile(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("SniffFile error = %v, want ErrNotExist", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("SniffFile created the file it was asked about")
	}
}

func TestSniffFileRejectsDirectory(t *testing.T) {
	if _, err := SniffFile(t.TempDir()); err == nil {
		t.Fatal("expected an error for a directory")
	}
}
