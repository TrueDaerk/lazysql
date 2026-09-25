package csvimport

import (
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"lazysql/internal/db"
)

var people = []db.Column{
	{Name: "id", DataType: "INTEGER"},
	{Name: "name", DataType: "TEXT"},
	{Name: "score", DataType: "REAL"},
	{Name: "born", DataType: "DATE"},
}

// bom is the UTF-8 byte-order mark spreadsheet exports start with.
const bom = "\xef\xbb\xbf"

func TestSniffDelimiterAndHeader(t *testing.T) {
	cases := []struct {
		name   string
		sample string
		delim  rune
		header bool
	}{
		{"comma with header", "id,name\n1,a\n2,b\n", ',', true},
		{"semicolon with header", "id;name;score\n1;a;1,5\n", ';', true},
		{"tab without header", "1\ta\t2.5\n2\tb\t3\n", '\t', false},
		{"pipe", "x|y\n1|2\n", '|', true},
		{"header by numeric shape", "code,label\n7,seven\n", ',', true},
		{"no header, all data", "1,a\n2,b\n", ',', false},
		{"quoted comma does not split", "id;name\n1;\"a,b\"\n", ';', true},
		{"BOM before header", bom + "id,name\n1,a\n", ',', true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := Sniff([]byte(c.sample), false, people)
			if s.Delimiter != c.delim || s.Header != c.header {
				t.Fatalf("Sniff = %q/%v, want %q/%v", s.Delimiter, s.Header, c.delim, c.header)
			}
		})
	}
}

// A sample cut in the middle of a record must not make the delimiter look
// inconsistent.
func TestSniffIgnoresTheCutRecord(t *testing.T) {
	sample := "id;name\n1;a\n2;b\n3;\"unterminat"
	if s := Sniff([]byte(sample), true, people); s.Delimiter != ';' {
		t.Fatalf("delimiter = %q, want ;", s.Delimiter)
	}
}

func TestDefaultMapping(t *testing.T) {
	if got := DefaultMapping([]string{"Name", "extra", "ID"}, 3, true, people); !slices.Equal(got, []int{1, Skip, 0}) {
		t.Fatalf("by header = %v", got)
	}
	if got := DefaultMapping(nil, 5, false, people); !slices.Equal(got, []int{0, 1, 2, 3, Skip}) {
		t.Fatalf("by position = %v", got)
	}
}

func TestMappingRoundTripAndErrors(t *testing.T) {
	m := []int{1, Skip, 0}
	text := FormatMapping(m, people)
	if text != "name, -, id" {
		t.Fatalf("FormatMapping = %q", text)
	}
	got, err := ParseMapping(text, 3, people)
	if err != nil || !slices.Equal(got, m) {
		t.Fatalf("ParseMapping = %v, %v", got, err)
	}
	if got, err := ParseMapping("id", 3, people); err != nil || !slices.Equal(got, []int{0, Skip, Skip}) {
		t.Fatalf("short mapping = %v, %v", got, err)
	}
	for _, bad := range []string{"nope", "id, id", "-, -", "id,name,score,born,id", ""} {
		if _, err := ParseMapping(bad, 4, people); err == nil {
			t.Errorf("ParseMapping(%q) accepted", bad)
		}
	}
}

func readAll(t *testing.T, input string, s Settings, p Plan) ([]db.ImportRow, error) {
	t.Helper()
	rd, err := NewReader(strings.NewReader(input), s)
	if err != nil {
		t.Fatal(err)
	}
	if p.Mapping == nil {
		p.Mapping = DefaultMapping(rd.Header, rd.Width, s.Header, p.Table)
	}
	p.Settings = s
	next := Source(rd, p)
	var out []db.ImportRow
	for {
		r, err := next()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		out = append(out, r)
	}
}

func TestHeaderAndNoHeader(t *testing.T) {
	withHeader, err := readAll(t, "name,id\nann,1\nbob,2\n", Settings{Delimiter: ',', Header: true}, Plan{Table: people})
	if err != nil {
		t.Fatal(err)
	}
	if len(withHeader) != 2 || withHeader[0].Values[0] != "ann" || withHeader[0].Values[1] != int64(1) || withHeader[0].Line != 2 {
		t.Fatalf("with header = %+v", withHeader)
	}
	noHeader, err := readAll(t, "1,ann\n2,bob\n", Settings{Delimiter: ','}, Plan{Table: people[:2]})
	if err != nil {
		t.Fatal(err)
	}
	if len(noHeader) != 2 || noHeader[0].Values[0] != int64(1) || noHeader[0].Line != 1 {
		t.Fatalf("without header = %+v", noHeader)
	}
}

func TestTypeMismatchIsReportedNotCoerced(t *testing.T) {
	_, err := readAll(t, "id,name\n1,a\nn/a,b\n", Settings{Delimiter: ',', Header: true}, Plan{Table: people})
	var fe *FieldError
	if !errors.As(err, &fe) {
		t.Fatalf("err = %v, want a FieldError", err)
	}
	if fe.Line != 3 || fe.Column != "id" || !strings.Contains(err.Error(), `"n/a" is not an integer`) {
		t.Fatalf("err = %v", err)
	}
}

func TestNullHandling(t *testing.T) {
	rows, err := readAll(t, "id,name,score\n1,,\n", Settings{Delimiter: ',', Header: true}, Plan{Table: people})
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Values[1] != nil || rows[0].Values[2] != nil {
		t.Fatalf("empty fields = %#v, want NULLs", rows[0].Values)
	}
	// With an explicit marker an empty field stays an empty string and
	// only the marker is NULL.
	rows, err = readAll(t, "id,name,score\n1,,\\N\n", Settings{Delimiter: ',', Header: true, Null: `\N`}, Plan{Table: people})
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Values[1] != "" || rows[0].Values[2] != nil {
		t.Fatalf("with \\N marker = %#v", rows[0].Values)
	}
}

func TestQuotedFieldWithDelimiterAndNewline(t *testing.T) {
	input := "id,name\n1,\"Smith, John\nsecond line\"\n2,plain\n"
	rows, err := readAll(t, input, Settings{Delimiter: ',', Header: true}, Plan{Table: people})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Values[1] != "Smith, John\nsecond line" {
		t.Fatalf("rows = %+v", rows)
	}
	// The row after a multi-line field reports the line it really starts on.
	if rows[1].Line != 4 {
		t.Fatalf("line of the second row = %d, want 4", rows[1].Line)
	}
}

func TestShortRecordIsRefused(t *testing.T) {
	_, err := readAll(t, "id,name\n1,a\n2\n", Settings{Delimiter: ',', Header: true}, Plan{Table: people})
	if err == nil || !strings.Contains(err.Error(), "line 3: 1 fields, expected 2") {
		t.Fatalf("err = %v", err)
	}
}

func TestPreviewAppliesTypes(t *testing.T) {
	sample := []byte("id,score,born\n1,2.5,2026-01-02\nx,1,2026-01-03\n")
	p := Plan{Settings: Settings{Delimiter: ',', Header: true}, Table: people,
		Mapping: []int{0, 2, 3}}
	rows, err := Preview(sample, false, p, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[0].Err != nil || rows[0].Values[0] != int64(1) || rows[0].Values[1] != 2.5 {
		t.Fatalf("first row = %+v", rows[0])
	}
	if rows[1].Err == nil {
		t.Fatal("the mismatching row previewed without an error")
	}
}

func TestParseDelimiter(t *testing.T) {
	for in, want := range map[string]rune{",": ',', `\t`: '\t', "tab": '\t', ";": ';'} {
		if got, err := ParseDelimiter(in); err != nil || got != want {
			t.Errorf("ParseDelimiter(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", ",,", `"`} {
		if _, err := ParseDelimiter(bad); err == nil {
			t.Errorf("ParseDelimiter(%q) accepted", bad)
		}
	}
	if FormatDelimiter('\t') != `\t` {
		t.Error("tab not spelled as \\t")
	}
}
