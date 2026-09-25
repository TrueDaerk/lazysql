// Package csvimport reads a CSV file into rows for db.Driver.ImportRows:
// it guesses the delimiter and whether the first line is a header, maps
// CSV columns onto the target table's columns, and converts each field to
// the value its column's type calls for — refusing, never coercing, a
// field that does not fit. It knows nothing about the UI; the import modal
// in internal/ui shows its guesses and lets the user correct them.
package csvimport

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"lazysql/internal/db"
)

// SampleSize is how much of a file Sniff and the preview read. It is
// enough for a few dozen rows of any sane width, and small enough to
// re-parse on every keystroke of the settings form.
const SampleSize = 64 * 1024

// Delimiters are the candidates Sniff chooses from, in tie-break order.
var Delimiters = []rune{',', ';', '\t', '|'}

// Settings are the three things the user can correct before an import.
type Settings struct {
	Delimiter rune
	Header    bool
	// Null is the field text read as SQL NULL. The default, "", matches
	// what lazysql's own CSV export writes for NULL, so an export
	// round-trips; set it to `\N` (or anything) to keep empty strings.
	Null string
}

// newReader configures encoding/csv for s. A quoted field may hold the
// delimiter, a doubled quote or a newline; the field count is checked by
// Plan rather than by the reader, so a short row fails with a message
// naming its line and the expected count.
func newReader(r io.Reader, delim rune) *csv.Reader {
	cr := csv.NewReader(r)
	cr.Comma = delim
	cr.FieldsPerRecord = -1
	return cr
}

// stripBOM drops a UTF-8 byte-order mark, which spreadsheet exports
// often start with and which would otherwise glue itself onto the first
// header name.
func stripBOM(r *bufio.Reader) {
	if b, err := r.Peek(3); err == nil && bytes.Equal(b, []byte{0xEF, 0xBB, 0xBF}) {
		r.Discard(3)
	}
}

// sampleRecords parses up to max records of sample. A sample cut off at
// SampleSize ends in a partial record, so when truncated is set the last
// one parsed is dropped rather than trusted.
func sampleRecords(sample []byte, delim rune, max int, truncated bool) ([][]string, error) {
	br := bufio.NewReader(bytes.NewReader(sample))
	stripBOM(br)
	cr := newReader(br, delim)
	var out [][]string
	for len(out) < max+1 {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			truncated = false
			break
		}
		if err != nil {
			if truncated && len(out) > 0 {
				// The parse error is the cut, not the file.
				return out, nil
			}
			return out, err
		}
		out = append(out, rec)
	}
	if truncated && len(out) > 1 {
		out = out[:len(out)-1]
	}
	if len(out) > max {
		out = out[:max]
	}
	return out, nil
}

// Sniff guesses the settings for a file from its first bytes and the
// target table's columns. truncated says the sample is not the whole file.
//
// The delimiter is the candidate that splits every sampled record into
// the same number of fields, more than one, preferring the most fields;
// with no consistent candidate it falls back to a comma. The first line
// is a header when one of its fields names a table column, or — failing
// that — when a column that is numeric in the second record is not in
// the first.
func Sniff(sample []byte, truncated bool, cols []db.Column) Settings {
	s := Settings{Delimiter: ','}
	best := 0
	for _, d := range Delimiters {
		recs, err := sampleRecords(sample, d, 20, truncated)
		if err != nil || len(recs) == 0 {
			continue
		}
		n := len(recs[0])
		consistent := n > 1
		for _, r := range recs[1:] {
			if len(r) != n {
				consistent = false
				break
			}
		}
		if consistent && n > best {
			best, s.Delimiter = n, d
		}
	}
	recs, _ := sampleRecords(sample, s.Delimiter, 2, truncated)
	s.Header = looksLikeHeader(recs, cols)
	return s
}

func looksLikeHeader(recs [][]string, cols []db.Column) bool {
	if len(recs) == 0 {
		return false
	}
	for _, f := range recs[0] {
		if columnIndex(cols, f) >= 0 {
			return true
		}
	}
	if len(recs) < 2 {
		return false
	}
	for i, f := range recs[0] {
		if i < len(recs[1]) && !isNumber(f) && isNumber(recs[1][i]) {
			return true
		}
	}
	return false
}

func isNumber(s string) bool {
	_, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return err == nil
}

// columnIndex finds a table column by name, case-insensitively and
// ignoring surrounding space, or -1.
func columnIndex(cols []db.Column, name string) int {
	name = strings.TrimSpace(name)
	for i, c := range cols {
		if c.Name == name {
			return i
		}
	}
	for i, c := range cols {
		if strings.EqualFold(c.Name, name) {
			return i
		}
	}
	return -1
}

// Skip marks a CSV column that goes nowhere.
const Skip = -1

// DefaultMapping maps each of a file's columns to a table column index,
// or Skip. With a header the names decide (case-insensitively); without
// one the positions do.
func DefaultMapping(header []string, width int, useHeader bool, cols []db.Column) []int {
	out := make([]int, width)
	used := map[int]bool{}
	for i := range out {
		out[i] = Skip
		switch {
		case useHeader && i < len(header):
			if j := columnIndex(cols, header[i]); j >= 0 && !used[j] {
				out[i] = j
			}
		case !useHeader && i < len(cols):
			out[i] = i
		}
		if out[i] != Skip {
			used[out[i]] = true
		}
	}
	return out
}

// FormatMapping spells a mapping the way the settings form edits it: one
// entry per CSV column, the table column's name or `-` for a skipped one.
func FormatMapping(m []int, cols []db.Column) string {
	parts := make([]string, len(m))
	for i, j := range m {
		if j == Skip || j >= len(cols) {
			parts[i] = "-"
		} else {
			parts[i] = cols[j].Name
		}
	}
	return strings.Join(parts, ", ")
}

// ParseMapping reads FormatMapping's spelling back for a file of width
// columns. Missing trailing entries are skipped columns; an unknown name,
// a column named twice, more entries than the file has columns, or a
// mapping that imports nothing at all are errors.
func ParseMapping(text string, width int, cols []db.Column) ([]int, error) {
	out := make([]int, width)
	for i := range out {
		out[i] = Skip
	}
	var entries []string
	if strings.TrimSpace(text) != "" {
		entries = strings.Split(text, ",")
	}
	if len(entries) > width {
		return nil, fmt.Errorf("mapping names %d columns, the file has %d", len(entries), width)
	}
	used := map[int]bool{}
	mapped := false
	for i, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" || e == "-" {
			continue
		}
		j := columnIndex(cols, e)
		if j < 0 {
			return nil, fmt.Errorf("mapping: no column %q in the table", e)
		}
		if used[j] {
			return nil, fmt.Errorf("mapping: column %q is mapped twice", cols[j].Name)
		}
		used[j], out[i], mapped = true, j, true
	}
	if !mapped {
		return nil, errors.New("mapping imports no column")
	}
	return out, nil
}

// Plan is a validated import: settings, the mapping and the target
// columns. Its Row turns one CSV record into the values ImportRows binds.
type Plan struct {
	Settings Settings
	Mapping  []int
	Table    []db.Column
}

// Columns names the target columns in the order Row yields values.
func (p Plan) Columns() []string {
	var out []string
	for _, j := range p.Mapping {
		if j != Skip {
			out = append(out, p.Table[j].Name)
		}
	}
	return out
}

// FieldError is a field that could not be converted, or a record of the
// wrong width, with the line it sits on.
type FieldError struct {
	Line   int
	Column string // empty for a whole-record problem
	Type   string
	Err    error
}

func (e *FieldError) Error() string {
	if e.Column == "" {
		return fmt.Sprintf("line %d: %v", e.Line, e.Err)
	}
	return fmt.Sprintf("line %d, column %s (%s): %v", e.Line, e.Column, e.Type, e.Err)
}

func (e *FieldError) Unwrap() error { return e.Err }

// Row converts one record. A record whose width differs from the
// mapping's is refused whole: a missing or extra field means the columns
// have shifted, and every value after it would land in the wrong place.
func (p Plan) Row(rec []string, line int) ([]any, error) {
	if len(rec) != len(p.Mapping) {
		return nil, &FieldError{Line: line,
			Err: fmt.Errorf("%d fields, expected %d", len(rec), len(p.Mapping))}
	}
	out := make([]any, 0, len(p.Mapping))
	for i, j := range p.Mapping {
		if j == Skip {
			continue
		}
		col := p.Table[j]
		if rec[i] == p.Settings.Null {
			out = append(out, nil)
			continue
		}
		v, err := db.ConvertText(col.DataType, rec[i])
		if err != nil {
			return nil, &FieldError{Line: line, Column: col.Name, Type: col.DataType, Err: err}
		}
		out = append(out, v)
	}
	return out, nil
}

// Reader streams a file's records. Header, when the settings say there
// is one, has already been consumed.
type Reader struct {
	cr     *csv.Reader
	Header []string
	// Width is the number of fields of the first record (the header or
	// the first data row); 0 for an empty file.
	Width int
	first []string
	line  int
}

// NewReader starts reading r with s. The first record is read eagerly so
// Width is known before any row is converted.
func NewReader(r io.Reader, s Settings) (*Reader, error) {
	br := bufio.NewReaderSize(r, 64*1024)
	stripBOM(br)
	rd := &Reader{cr: newReader(br, s.Delimiter)}
	rec, err := rd.cr.Read()
	if errors.Is(err, io.EOF) {
		return rd, nil
	}
	if err != nil {
		return nil, err
	}
	rd.Width = len(rec)
	if s.Header {
		rd.Header = rec
	} else {
		rd.first = rec
		rd.line, _ = rd.cr.FieldPos(0)
	}
	return rd, nil
}

// Next returns the next data record and the line it starts on, or io.EOF.
func (r *Reader) Next() ([]string, int, error) {
	if r.first != nil {
		rec := r.first
		r.first = nil
		return rec, r.line, nil
	}
	rec, err := r.cr.Read()
	if err != nil {
		return nil, 0, err
	}
	line, _ := r.cr.FieldPos(0)
	return rec, line, nil
}

// Source adapts a Reader and a Plan to db.ImportRequest.Next.
func Source(r *Reader, p Plan) func() (db.ImportRow, error) {
	return func() (db.ImportRow, error) {
		rec, line, err := r.Next()
		if err != nil {
			return db.ImportRow{}, err
		}
		vals, err := p.Row(rec, line)
		if err != nil {
			return db.ImportRow{}, err
		}
		return db.ImportRow{Values: vals, Line: line}, nil
	}
}

// PreviewRow is one row of the settings form's preview: the converted
// values of a record, or the reason it would stop the import.
type PreviewRow struct {
	Line   int
	Values []any
	Err    error
}

// Preview converts the first max data records of sample under p.
func Preview(sample []byte, truncated bool, p Plan, max int) ([]PreviewRow, error) {
	rd, err := NewReader(bytes.NewReader(sample), p.Settings)
	if err != nil {
		return nil, err
	}
	var out []PreviewRow
	for len(out) < max {
		rec, line, err := rd.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if truncated {
				break
			}
			return out, err
		}
		vals, err := p.Row(rec, line)
		out = append(out, PreviewRow{Line: line, Values: vals, Err: err})
	}
	return out, nil
}

// FormatDelimiter spells a delimiter for the settings form; `\t` stands
// for a tab, which is otherwise invisible.
func FormatDelimiter(d rune) string {
	if d == '\t' {
		return `\t`
	}
	return string(d)
}

// ParseDelimiter reads the form's spelling back: one character, or `\t`
// / `tab` for a tab. A quote or a newline cannot delimit anything.
func ParseDelimiter(s string) (rune, error) {
	switch strings.ToLower(s) {
	case `\t`, "tab":
		return '\t', nil
	}
	r := []rune(s)
	if len(r) != 1 || r[0] == '"' || r[0] == '\r' || r[0] == '\n' {
		return 0, fmt.Errorf("delimiter must be one character (or \\t), got %q", s)
	}
	return r[0], nil
}
