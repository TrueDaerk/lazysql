// Package export serializes result rows to CSV, JSON and SQL, and
// streams whole tables through those serializers a page at a time.
//
// Nothing here holds more than one page of rows: a writer is fed row by
// row and flushes into the io.Writer it was built on, so exporting a
// 100k-row table costs the same memory as exporting a 100-row one.
package export

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"lazysql/internal/db"
)

// Format is one serialization the copy menu and the file export share.
type Format string

const (
	FormatCSV      Format = "csv"
	FormatJSON     Format = "json"
	FormatSQL      Format = "sql"
	FormatMarkdown Format = "markdown"
)

// FormatForPath infers the format from a file extension, which is how
// `E` decides what to write.
func FormatForPath(path string) (Format, error) {
	switch ext := strings.ToLower(filepath.Ext(path)); ext {
	case ".csv":
		return FormatCSV, nil
	case ".json":
		return FormatJSON, nil
	case ".sql":
		return FormatSQL, nil
	case ".md", ".markdown":
		return FormatMarkdown, nil
	case "":
		return "", errors.New("no file extension — use .csv, .json, .md or .sql")
	default:
		return "", fmt.Errorf("unsupported extension %q — use .csv, .json, .md or .sql", ext)
	}
}

// Options carries what a format needs beyond the rows themselves. Only
// the SQL writer reads any of it.
type Options struct {
	// Dialect quotes identifiers and escapes literals. A nil dialect is
	// allowed: identifiers are then left unquoted, which is what a
	// preview built without a connection wants.
	Dialect  db.Dialect
	Database string
	Table    string

	// DDL, when set, is written ahead of the INSERTs by the SQL writer.
	// It is the "CREATE TABLE + INSERTs" copy-menu variant.
	DDL string
}

// Writer serializes a result set incrementally. Begin is called once
// with the columns — before any row, and even when there are no rows,
// so a CSV still gets its header and a JSON still gets its brackets.
type Writer interface {
	Begin(cols []db.Column) error
	Row(values []any) error
	End() error
}

// NewWriter builds the writer for a format.
func NewWriter(w io.Writer, f Format, o Options) (Writer, error) {
	switch f {
	case FormatCSV:
		return &csvWriter{w: csv.NewWriter(w)}, nil
	case FormatJSON:
		return &jsonWriter{w: w}, nil
	case FormatSQL:
		return &sqlWriter{w: w, opt: o}, nil
	case FormatMarkdown:
		return &markdownWriter{w: w}, nil
	default:
		return nil, fmt.Errorf("export: unknown format %q", f)
	}
}

// ---------- CSV ----------

type csvWriter struct {
	w    *csv.Writer
	cols int
	buf  []string
}

func (c *csvWriter) Begin(cols []db.Column) error {
	c.cols = len(cols)
	c.buf = make([]string, len(cols))
	names := make([]string, len(cols))
	for i, col := range cols {
		names[i] = col.Name
	}
	return c.w.Write(names)
}

func (c *csvWriter) Row(values []any) error {
	for i := range c.buf {
		var v any
		if i < len(values) {
			v = values[i]
		}
		c.buf[i] = csvValue(v)
	}
	return c.w.Write(c.buf)
}

func (c *csvWriter) End() error {
	c.w.Flush()
	return c.w.Error()
}

// csvValue renders one cell. SQL NULL becomes an empty field: CSV has
// no null, and the `\N` convention MySQL's own tooling uses is not
// readable by anything else — an empty field at least round-trips
// through every spreadsheet. encoding/csv quotes whatever needs it, so
// a value containing a comma, a quote or a newline stays one field.
func csvValue(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case time.Time:
		return x.Format(time.RFC3339Nano)
	default:
		return db.FormatValue(x, "")
	}
}

// ---------- JSON ----------

// jsonWriter streams a JSON array of objects. It is written by hand
// rather than with an Encoder over a slice because the array must be
// emitted incrementally, and because a Go map would lose the column
// order the user is looking at.
type jsonWriter struct {
	w     io.Writer
	keys  []string
	nth   int
	begun bool
}

func (j *jsonWriter) Begin(cols []db.Column) error {
	j.keys = jsonKeys(cols)
	j.begun = true
	_, err := io.WriteString(j.w, "[")
	return err
}

func (j *jsonWriter) Row(values []any) error {
	var b strings.Builder
	if j.nth > 0 {
		b.WriteString(",")
	}
	b.WriteString("\n  ")
	b.WriteString(jsonObject(j.keys, values))
	j.nth++
	_, err := io.WriteString(j.w, b.String())
	return err
}

// jsonObject renders one row as a single-line JSON object. keys are
// already-quoted column names, in the order the grid shows them.
func jsonObject(keys []string, values []any) string {
	var b strings.Builder
	b.WriteString("{")
	for i, key := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		var v any
		if i < len(values) {
			v = values[i]
		}
		b.WriteString(key)
		b.WriteString(": ")
		b.WriteString(jsonValue(v))
	}
	b.WriteString("}")
	return b.String()
}

// jsonKeys quotes the column names once for reuse across rows.
func jsonKeys(cols []db.Column) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = quoteJSON(c.Name)
	}
	return out
}

func (j *jsonWriter) End() error {
	if !j.begun {
		_, err := io.WriteString(j.w, "[]\n")
		return err
	}
	if j.nth == 0 {
		_, err := io.WriteString(j.w, "]\n")
		return err
	}
	_, err := io.WriteString(j.w, "\n]\n")
	return err
}

// jsonValue renders one cell as a JSON value. SQL NULL becomes JSON
// null — the one format of the three that can say so exactly — and the
// numeric types stay numbers instead of being stringified. Infinity and
// NaN have no JSON spelling at all, so they degrade to null.
func jsonValue(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return "null"
		}
		return strconv.FormatFloat(x, 'g', -1, 64)
	case time.Time:
		return quoteJSON(x.Format(time.RFC3339Nano))
	case string:
		return quoteJSON(x)
	default:
		return quoteJSON(db.FormatValue(x, ""))
	}
}

func quoteJSON(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		// json.Marshal of a string only fails on invalid UTF-8, which it
		// replaces rather than rejects; this branch is unreachable in
		// practice and a quoted empty string is the harmless reading.
		return `""`
	}
	return string(b)
}

// ---------- SQL ----------

// sqlWriter emits one INSERT per row, optionally preceded by the
// table's DDL. Values are rendered as dialect-escaped literals: this is
// text handed to the user, not a statement lazysql executes.
type sqlWriter struct {
	w    io.Writer
	opt  Options
	cols []db.Column
}

func (s *sqlWriter) Begin(cols []db.Column) error {
	s.cols = cols
	if s.opt.DDL == "" {
		return nil
	}
	ddl := strings.TrimRight(s.opt.DDL, " \t\n")
	if !strings.HasSuffix(ddl, ";") {
		ddl += ";"
	}
	_, err := io.WriteString(s.w, ddl+"\n\n")
	return err
}

func (s *sqlWriter) Row(values []any) error {
	stmt := db.InsertStatement(s.opt.Dialect, s.opt.Database, s.opt.Table, s.cols, values)
	_, err := io.WriteString(s.w, stmt+"\n")
	return err
}

func (s *sqlWriter) End() error { return nil }

// ---------- Markdown ----------

// markdownWriter renders a GitHub-flavored Markdown table: a header row,
// the `---` separator row Markdown requires to recognize a table at all,
// and one row per data row. It streams the same way the CSV writer does —
// the separator only needs the column count, not any row's values.
type markdownWriter struct {
	w    io.Writer
	cols int
}

func (m *markdownWriter) Begin(cols []db.Column) error {
	m.cols = len(cols)
	var b strings.Builder
	b.WriteString("|")
	for _, c := range cols {
		b.WriteString(" ")
		b.WriteString(markdownEscape(c.Name))
		b.WriteString(" |")
	}
	b.WriteString("\n|")
	for range cols {
		b.WriteString(" --- |")
	}
	b.WriteString("\n")
	_, err := io.WriteString(m.w, b.String())
	return err
}

func (m *markdownWriter) Row(values []any) error {
	var b strings.Builder
	b.WriteString("|")
	for i := 0; i < m.cols; i++ {
		var v any
		if i < len(values) {
			v = values[i]
		}
		b.WriteString(" ")
		b.WriteString(markdownValue(v))
		b.WriteString(" |")
	}
	b.WriteString("\n")
	_, err := io.WriteString(m.w, b.String())
	return err
}

func (m *markdownWriter) End() error { return nil }

// markdownValue renders one cell. SQL NULL becomes the literal text
// "NULL": a Markdown table cell has no equivalent of CSV's empty field —
// a blank cell there just reads as an empty value, not as "no value at
// all" — so the word is the only way to say it unambiguously.
func markdownValue(v any) string {
	switch x := v.(type) {
	case nil:
		return "NULL"
	case string:
		return markdownEscape(x)
	case bool:
		return strconv.FormatBool(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case time.Time:
		return x.Format(time.RFC3339Nano)
	default:
		return markdownEscape(db.FormatValue(x, ""))
	}
}

// markdownEscape keeps a cell from spilling into its neighbors or
// breaking the row: an unescaped `|` reads as a new column boundary and a
// newline ends the row outright, so both are neutralized before a
// backslash of their own could be mistaken for an escape.
func markdownEscape(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\r\n", "<br>")
	s = strings.ReplaceAll(s, "\n", "<br>")
	s = strings.ReplaceAll(s, "\r", "<br>")
	return s
}

// ---------- streaming ----------

// Pager reads one page of the exported result. It is the seam between
// this package and the driver: the UI closes over the table, filter and
// sort, so the stream inherits exactly what the grid is showing.
type Pager func(ctx context.Context, limit, offset int) (*db.ResultSet, error)

// StreamOptions tunes one Stream run.
type StreamOptions struct {
	// PageSize is how many rows one round trip reads. It is the whole
	// memory bound of an export.
	PageSize int
	// MaxRows caps the export; 0 means no cap. The clipboard variants
	// set it, because a clipboard copy has to be materialized in full.
	MaxRows int64
	// Progress is called with the running row count, at most once per
	// ProgressEvery rows plus once at the end.
	Progress      func(rows int64)
	ProgressEvery int64
}

// DefaultPageSize is the streaming page size. It is larger than the
// grid's page because an export has no rendering cost per row and
// fewer, bigger round trips finish sooner.
const DefaultPageSize = 1000

// Stream reads pages until one comes back short and feeds every row to
// the writer. It returns how many rows were written and whether it
// stopped early because MaxRows was reached. A cancelled context aborts
// between pages and between rows, so `X` interrupts a long export
// without waiting for it to finish.
func Stream(ctx context.Context, w Writer, page Pager, o StreamOptions) (rows int64, truncated bool, err error) {
	pageSize := o.PageSize
	if pageSize <= 0 {
		pageSize = DefaultPageSize
	}
	every := o.ProgressEvery
	if every <= 0 {
		every = 5000
	}

	var (
		offset     int
		reportedAt int64
		begun      bool
	)
	for {
		if err := ctx.Err(); err != nil {
			return rows, truncated, err
		}
		limit := pageSize
		if o.MaxRows > 0 {
			if left := o.MaxRows - rows; int64(limit) > left {
				limit = int(left)
			}
		}
		if limit <= 0 {
			truncated = true
			break
		}

		rs, err := page(ctx, limit, offset)
		if err != nil {
			return rows, truncated, err
		}
		if !begun {
			if err := w.Begin(rs.Columns); err != nil {
				return rows, truncated, err
			}
			begun = true
		}
		for _, r := range rs.Rows {
			if err := ctx.Err(); err != nil {
				return rows, truncated, err
			}
			if err := w.Row(r); err != nil {
				return rows, truncated, err
			}
			rows++
		}
		offset += len(rs.Rows)
		if o.Progress != nil && rows-reportedAt >= every {
			reportedAt = rows
			o.Progress(rows)
		}
		// A page shorter than what was asked for is the last one. The
		// engine has no other way to say "that was all".
		if len(rs.Rows) < limit {
			break
		}
		if o.MaxRows > 0 && rows >= o.MaxRows {
			truncated = true
			break
		}
	}

	// An empty result never reached the Begin above, but a CSV still
	// owes its header and a JSON its brackets.
	if !begun {
		if err := w.Begin(nil); err != nil {
			return rows, truncated, err
		}
	}
	if o.Progress != nil && rows != reportedAt {
		o.Progress(rows)
	}
	return rows, truncated, w.End()
}

// QueryRunner executes an arbitrary statement exactly once and streams
// its rows to onRow, one at a time. It is the query-result counterpart
// of Pager: a browsed table can be re-read page by page by OFFSET, but
// rewriting an arbitrary user statement to page it the same way would
// risk changing what it selects — see
// wiki/design/query-editor-and-history.md — so a query result is instead
// read a single time, streaming straight through with no more than one
// row held in memory at once.
type QueryRunner func(ctx context.Context, onRow func(cols []db.Column, row []any) error) error

// errStreamMaxRows is StreamQuery's internal signal to stop reading once
// MaxRows is reached. It is translated back into the truncated return
// value and never escapes StreamQuery itself.
var errStreamMaxRows = errors.New("export: MaxRows reached")

// StreamQuery is Stream for a query result: same Writer contract, same
// progress and cancellation behaviour, same MaxRows truncation, but fed
// by a QueryRunner's single pass instead of Pager's repeated offset
// reads.
func StreamQuery(ctx context.Context, w Writer, run QueryRunner, o StreamOptions) (rows int64, truncated bool, err error) {
	every := o.ProgressEvery
	if every <= 0 {
		every = 5000
	}

	var (
		reportedAt int64
		begun      bool
	)
	runErr := run(ctx, func(cols []db.Column, row []any) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !begun {
			if err := w.Begin(cols); err != nil {
				return err
			}
			begun = true
		}
		if o.MaxRows > 0 && rows >= o.MaxRows {
			truncated = true
			return errStreamMaxRows
		}
		if err := w.Row(row); err != nil {
			return err
		}
		rows++
		if o.Progress != nil && rows-reportedAt >= every {
			reportedAt = rows
			o.Progress(rows)
		}
		return nil
	})
	if errors.Is(runErr, errStreamMaxRows) {
		runErr = nil
	}
	if runErr != nil {
		return rows, truncated, runErr
	}

	if !begun {
		if err := w.Begin(nil); err != nil {
			return rows, truncated, err
		}
	}
	if o.Progress != nil && rows != reportedAt {
		o.Progress(rows)
	}
	return rows, truncated, w.End()
}

// Row serializes a single row the way the copy menu offers it: a bare
// CSV line with no header, a single JSON object rather than a
// one-element array, and one INSERT statement. The three go through the
// same value renderers as Stream, so a row and the table it came from
// can never disagree about quoting.
func Row(f Format, o Options, cols []db.Column, values []any) (string, error) {
	switch f {
	case FormatCSV:
		var b strings.Builder
		w := &csvWriter{w: csv.NewWriter(&b), cols: len(cols), buf: make([]string, len(cols))}
		if err := w.Row(values); err != nil {
			return "", err
		}
		if err := w.End(); err != nil {
			return "", err
		}
		return strings.TrimRight(b.String(), "\r\n"), nil
	case FormatJSON:
		return jsonObject(jsonKeys(cols), values), nil
	case FormatSQL:
		return db.InsertStatement(o.Dialect, o.Database, o.Table, cols, values), nil
	default:
		return "", fmt.Errorf("export: unknown format %q", f)
	}
}

// Rows serializes an already-materialized set of rows — one cell, one
// row, or the visible page. It shares the writers with Stream so a
// single row and a whole table can never disagree about quoting.
func Rows(f Format, o Options, cols []db.Column, rows [][]any) (string, error) {
	var b strings.Builder
	w, err := NewWriter(&b, f, o)
	if err != nil {
		return "", err
	}
	if err := w.Begin(cols); err != nil {
		return "", err
	}
	for _, r := range rows {
		if err := w.Row(r); err != nil {
			return "", err
		}
	}
	if err := w.End(); err != nil {
		return "", err
	}
	return b.String(), nil
}
