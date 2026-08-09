package export

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"lazysql/internal/db"
)

func cols(names ...string) []db.Column {
	out := make([]db.Column, len(names))
	for i, n := range names {
		out[i] = db.Column{Name: n}
	}
	return out
}

func sqliteDialect(t *testing.T) db.Dialect {
	t.Helper()
	d, err := db.DialectFor(db.EngineSQLite)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestFormatForPath(t *testing.T) {
	tests := []struct {
		path string
		want Format
		ok   bool
	}{
		{"users.csv", FormatCSV, true},
		{"/tmp/USERS.CSV", FormatCSV, true},
		{"dump.json", FormatJSON, true},
		{"dump.sql", FormatSQL, true},
		{"dump.txt", "", false},
		{"dump", "", false},
	}
	for _, tt := range tests {
		got, err := FormatForPath(tt.path)
		if tt.ok != (err == nil) {
			t.Errorf("FormatForPath(%q) err = %v, want ok=%v", tt.path, err, tt.ok)
			continue
		}
		if tt.ok && got != tt.want {
			t.Errorf("FormatForPath(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

// CSV quotes whatever needs quoting and spells SQL NULL as an empty
// field — not `\N`, which nothing outside MySQL's own tooling reads.
func TestCSVQuotingAndNulls(t *testing.T) {
	rows := [][]any{
		{int64(1), "plain", true},
		{int64(2), "has,comma", false},
		{int64(3), "has\"quote", nil},
		{int64(4), "has\nnewline", nil},
		{nil, nil, nil},
	}
	got, err := Rows(FormatCSV, Options{}, cols("id", "text", "flag"), rows)
	if err != nil {
		t.Fatal(err)
	}
	want := "id,text,flag\n" +
		"1,plain,true\n" +
		"2,\"has,comma\",false\n" +
		"3,\"has\"\"quote\",\n" +
		"4,\"has\nnewline\",\n" +
		",,\n"
	if got != want {
		t.Errorf("CSV =\n%q\nwant\n%q", got, want)
	}
}

// An empty result still gets its header row, so the file describes the
// table rather than being blank.
func TestCSVEmptyResultKeepsHeader(t *testing.T) {
	got, err := Rows(FormatCSV, Options{}, cols("id", "name"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "id,name\n" {
		t.Errorf("CSV = %q", got)
	}
}

// JSON keeps types: numbers stay numbers, booleans booleans, and NULL
// becomes null rather than the string "NULL" or an empty string.
func TestJSONTypesAndNulls(t *testing.T) {
	ts := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	rows := [][]any{
		{int64(1), 2.5, true, "text", ts},
		{nil, nil, nil, nil, nil},
	}
	got, err := Rows(FormatJSON, Options{}, cols("i", "f", "b", "s", "t"), rows)
	if err != nil {
		t.Fatal(err)
	}

	var decoded []map[string]any
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, got)
	}
	if len(decoded) != 2 {
		t.Fatalf("decoded %d rows, want 2", len(decoded))
	}
	if decoded[0]["i"] != float64(1) || decoded[0]["f"] != 2.5 {
		t.Errorf("numbers did not survive: %#v", decoded[0])
	}
	if decoded[0]["b"] != true || decoded[0]["s"] != "text" {
		t.Errorf("scalars did not survive: %#v", decoded[0])
	}
	if decoded[0]["t"] != "2026-08-09T12:00:00Z" {
		t.Errorf("timestamp = %#v", decoded[0]["t"])
	}
	for k, v := range decoded[1] {
		if v != nil {
			t.Errorf("NULL column %q decoded as %#v, want nil", k, v)
		}
	}
	// The null must be present as a key, not omitted.
	if len(decoded[1]) != 5 {
		t.Errorf("NULL row has %d keys, want 5", len(decoded[1]))
	}
}

// An empty JSON export is a valid empty array, not an empty file.
func TestJSONEmptyResultIsEmptyArray(t *testing.T) {
	got, err := Rows(FormatJSON, Options{}, cols("id"), nil)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []map[string]any
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v (%q)", err, got)
	}
	if len(decoded) != 0 {
		t.Errorf("decoded %d rows, want 0", len(decoded))
	}
}

// The SQL export writes dialect-quoted INSERTs, spells NULL as NULL,
// and puts the DDL in front when one was supplied.
func TestSQLInsertsAndDDL(t *testing.T) {
	o := Options{
		Dialect:  sqliteDialect(t),
		Database: "main",
		Table:    "users",
		DDL:      "CREATE TABLE users (id INTEGER, name TEXT)",
	}
	rows := [][]any{
		{int64(1), "O'Hara"},
		{int64(2), nil},
	}
	got, err := Rows(FormatSQL, o, cols("id", "name"), rows)
	if err != nil {
		t.Fatal(err)
	}
	want := "CREATE TABLE users (id INTEGER, name TEXT);\n\n" +
		`INSERT INTO "main"."users" ("id", "name") VALUES (1, 'O''Hara');` + "\n" +
		`INSERT INTO "main"."users" ("id", "name") VALUES (2, NULL);` + "\n"
	if got != want {
		t.Errorf("SQL =\n%s\nwant\n%s", got, want)
	}

	// Without a DDL the export is INSERTs only.
	o.DDL = ""
	got, err = Rows(FormatSQL, o, cols("id", "name"), rows[:1])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "CREATE TABLE") {
		t.Errorf("SQL without DDL still has a CREATE:\n%s", got)
	}
}

// The single-row variants of the copy menu: a bare CSV line with no
// header, one JSON object rather than an array, one INSERT.
func TestRowVariants(t *testing.T) {
	c := cols("id", "name")
	values := []any{int64(7), nil}

	got, err := Row(FormatCSV, Options{}, c, values)
	if err != nil {
		t.Fatal(err)
	}
	if got != "7," {
		t.Errorf("row CSV = %q, want %q", got, "7,")
	}

	got, err = Row(FormatJSON, Options{}, c, values)
	if err != nil {
		t.Fatal(err)
	}
	if got != `{"id": 7, "name": null}` {
		t.Errorf("row JSON = %q", got)
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(got), &obj); err != nil {
		t.Fatalf("row JSON does not parse: %v", err)
	}

	got, err = Row(FormatSQL, Options{Dialect: sqliteDialect(t), Table: "users"}, c, values)
	if err != nil {
		t.Fatal(err)
	}
	if got != `INSERT INTO "users" ("id", "name") VALUES (7, NULL);` {
		t.Errorf("row SQL = %q", got)
	}
}

// pagerOf serves n synthetic rows a page at a time and records the
// largest page it was ever asked for.
func pagerOf(n int, maxSeen *int) Pager {
	return func(ctx context.Context, limit, offset int) (*db.ResultSet, error) {
		if limit > *maxSeen {
			*maxSeen = limit
		}
		rs := &db.ResultSet{Columns: cols("id", "name")}
		for i := offset; i < offset+limit && i < n; i++ {
			rs.Rows = append(rs.Rows, []any{int64(i), fmt.Sprintf("row-%d", i)})
		}
		return rs, nil
	}
}

// A 100k-row table streams through in pages: the export never holds
// more than one page of rows, whatever the table's size.
func TestStreamLargeTableStaysPaged(t *testing.T) {
	const total = 100_000
	maxPage := 0
	// io.Discard keeps the test honest about memory: nothing but the
	// current page is ever retained.
	w, err := NewWriter(io.Discard, FormatCSV, Options{})
	if err != nil {
		t.Fatal(err)
	}

	var progress []int64
	rows, truncated, err := Stream(context.Background(), w, pagerOf(total, &maxPage), StreamOptions{
		PageSize:      DefaultPageSize,
		ProgressEvery: 25_000,
		Progress:      func(n int64) { progress = append(progress, n) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if rows != total {
		t.Errorf("streamed %d rows, want %d", rows, total)
	}
	if truncated {
		t.Error("an uncapped stream reported truncation")
	}
	if maxPage != DefaultPageSize {
		t.Errorf("largest page requested = %d, want %d", maxPage, DefaultPageSize)
	}
	if len(progress) == 0 || progress[len(progress)-1] != total {
		t.Errorf("progress = %v, want it to end at %d", progress, total)
	}
}

// A table whose size is an exact multiple of the page size still ends:
// the loop reads one more page, gets nothing, and stops.
func TestStreamExactPageMultiple(t *testing.T) {
	maxPage := 0
	var b strings.Builder
	w, _ := NewWriter(&b, FormatCSV, Options{})
	rows, _, err := Stream(context.Background(), w, pagerOf(20, &maxPage), StreamOptions{PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if rows != 20 {
		t.Errorf("streamed %d rows, want 20", rows)
	}
}

// MaxRows is the clipboard cap: it stops the stream and says so,
// instead of truncating silently.
func TestStreamMaxRowsReportsTruncation(t *testing.T) {
	maxPage := 0
	var b strings.Builder
	w, _ := NewWriter(&b, FormatCSV, Options{})
	rows, truncated, err := Stream(context.Background(), w, pagerOf(5000, &maxPage), StreamOptions{
		PageSize: 1000,
		MaxRows:  2500,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rows != 2500 || !truncated {
		t.Errorf("rows = %d truncated = %v, want 2500 true", rows, truncated)
	}
	// The cap also shrinks the last page so the engine is never asked
	// for rows that would be thrown away.
	if maxPage > 1000 {
		t.Errorf("largest page = %d, want it bounded by PageSize", maxPage)
	}
}

// A table smaller than the cap is not reported as truncated.
func TestStreamUnderMaxRowsIsNotTruncated(t *testing.T) {
	maxPage := 0
	var b strings.Builder
	w, _ := NewWriter(&b, FormatCSV, Options{})
	rows, truncated, err := Stream(context.Background(), w, pagerOf(10, &maxPage), StreamOptions{
		PageSize: 1000,
		MaxRows:  5000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rows != 10 || truncated {
		t.Errorf("rows = %d truncated = %v, want 10 false", rows, truncated)
	}
}

// Cancelling the context stops the stream promptly and surfaces the
// cancellation, which is what makes `X` work mid-export.
func TestStreamCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	maxPage := 0
	page := pagerOf(1_000_000, &maxPage)
	var b strings.Builder
	w, _ := NewWriter(&b, FormatCSV, Options{})

	rows, _, err := Stream(ctx, w, func(c context.Context, limit, offset int) (*db.ResultSet, error) {
		if offset >= 2000 {
			cancel()
		}
		return page(c, limit, offset)
	}, StreamOptions{PageSize: 1000})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if rows > 4000 {
		t.Errorf("kept streaming past the cancellation: %d rows", rows)
	}
	cancel()
}

// A failing page aborts the stream and reports the driver's error.
func TestStreamPropagatesPageError(t *testing.T) {
	boom := errors.New("boom")
	var b strings.Builder
	w, _ := NewWriter(&b, FormatCSV, Options{})
	_, _, err := Stream(context.Background(), w,
		func(context.Context, int, int) (*db.ResultSet, error) { return nil, boom },
		StreamOptions{PageSize: 10})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
}

// ---------- Markdown ----------

// The header and separator rows are written even for an empty result,
// matching CSV's "header survives zero rows" rule.
func TestMarkdownHeaderAndNulls(t *testing.T) {
	rows := [][]any{
		{int64(1), "plain", true},
		{int64(2), nil, false},
	}
	got, err := Rows(FormatMarkdown, Options{}, cols("id", "text", "flag"), rows)
	if err != nil {
		t.Fatal(err)
	}
	want := "| id | text | flag |\n" +
		"| --- | --- | --- |\n" +
		"| 1 | plain | true |\n" +
		"| 2 | NULL | false |\n"
	if got != want {
		t.Errorf("Markdown =\n%q\nwant\n%q", got, want)
	}
}

// A pipe, a backslash and a newline in a cell would otherwise break the
// table's row/column structure, so all three are escaped.
func TestMarkdownEscaping(t *testing.T) {
	rows := [][]any{
		{"a|b"},
		{`a\b`},
		{"a\nb"},
	}
	got, err := Rows(FormatMarkdown, Options{}, cols("text"), rows)
	if err != nil {
		t.Fatal(err)
	}
	want := "| text |\n| --- |\n" +
		"| a\\|b |\n" +
		"| a\\\\b |\n" +
		"| a<br>b |\n"
	if got != want {
		t.Errorf("Markdown =\n%q\nwant\n%q", got, want)
	}
}

func TestMarkdownEmptyResultKeepsHeader(t *testing.T) {
	got, err := Rows(FormatMarkdown, Options{}, cols("id", "name"), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "| id | name |\n| --- | --- |\n"
	if got != want {
		t.Errorf("Markdown =\n%q\nwant\n%q", got, want)
	}
}

// ---------- StreamQuery ----------

// runnerOf is StreamQuery's fixture the way pagerOf is Stream's: it
// streams n rows in one pass, recording the largest "batch" it was asked
// to buffer — always 1, since StreamQuery holds one row at a time.
func runnerOf(n int) QueryRunner {
	return func(ctx context.Context, onRow func(cols []db.Column, row []any) error) error {
		c := cols("id", "name")
		for i := 0; i < n; i++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := onRow(c, []any{int64(i), fmt.Sprintf("row-%d", i)}); err != nil {
				return err
			}
		}
		return nil
	}
}

func TestStreamQueryWritesEveryRow(t *testing.T) {
	var b strings.Builder
	w, _ := NewWriter(&b, FormatCSV, Options{})
	var progress []int64
	rows, truncated, err := StreamQuery(context.Background(), w, runnerOf(12_000), StreamOptions{
		ProgressEvery: 5000,
		Progress:      func(n int64) { progress = append(progress, n) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if rows != 12_000 || truncated {
		t.Errorf("rows = %d truncated = %v, want 12000 false", rows, truncated)
	}
	if len(progress) == 0 || progress[len(progress)-1] != 12_000 {
		t.Errorf("progress = %v, want it to end at 12000", progress)
	}
	if got := strings.Count(b.String(), "\n"); got != 12_001 {
		t.Errorf("wrote %d lines, want header + 12000 rows", got)
	}
}

func TestStreamQueryMaxRowsReportsTruncation(t *testing.T) {
	var b strings.Builder
	w, _ := NewWriter(&b, FormatCSV, Options{})
	rows, truncated, err := StreamQuery(context.Background(), w, runnerOf(5000), StreamOptions{
		MaxRows: 2500,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rows != 2500 || !truncated {
		t.Errorf("rows = %d truncated = %v, want 2500 true", rows, truncated)
	}
}

func TestStreamQueryCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var b strings.Builder
	w, _ := NewWriter(&b, FormatCSV, Options{})

	run := func(c context.Context, onRow func(cols []db.Column, row []any) error) error {
		col := cols("id")
		for i := 0; ; i++ {
			if i == 2000 {
				cancel()
			}
			if err := c.Err(); err != nil {
				return err
			}
			if err := onRow(col, []any{int64(i)}); err != nil {
				return err
			}
		}
	}
	rows, _, err := StreamQuery(ctx, w, run, StreamOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if rows > 4000 {
		t.Errorf("kept streaming past the cancellation: %d rows", rows)
	}
}

// A failing runner aborts the stream and reports the driver's error.
func TestStreamQueryPropagatesRunnerError(t *testing.T) {
	boom := errors.New("boom")
	var b strings.Builder
	w, _ := NewWriter(&b, FormatCSV, Options{})
	_, _, err := StreamQuery(context.Background(), w,
		func(context.Context, func(cols []db.Column, row []any) error) error { return boom },
		StreamOptions{})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
}

// An empty result never reaches onRow, so the columns are never known —
// same as Stream's own empty-result case, which is why Begin(nil) is
// still valid for every writer.
func TestStreamQueryEmptyResultKeepsHeader(t *testing.T) {
	var b strings.Builder
	w, _ := NewWriter(&b, FormatCSV, Options{})
	rows, _, err := StreamQuery(context.Background(), w, runnerOf(0), StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Errorf("rows = %d, want 0", rows)
	}
	if b.String() != "\n" {
		t.Errorf("wrote %q, want a bare newline from Begin(nil)", b.String())
	}
}
