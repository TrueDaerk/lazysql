package ui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestClassifyCell(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want cellKind
	}{
		{"json object", `{"a":1,"b":[2,3]}`, cellJSON},
		{"json array", `[1,2,3]`, cellJSON},
		{"json with surrounding whitespace", "  {\"a\":1}  \n", cellJSON},
		{"invalid json falls back to plain", `{"a":1,}`, cellPlain},
		{"bare json scalar is not pretty-printed", `42`, cellPlain},
		{"quoted json string is not pretty-printed", `"hello"`, cellPlain},
		{"plain text", "hello world", cellPlain},
		{"empty string", "", cellPlain},
		{"invalid utf-8 is binary", "\xff\xfe\x00", cellBinary},
		{"invalid utf-8 inside otherwise readable text", "abc\xffdef", cellBinary},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classifyCell(c.raw); got != c.want {
				t.Errorf("classifyCell(%q) = %v, want %v", c.raw, got, c.want)
			}
		})
	}
}

func TestPrettyJSON(t *testing.T) {
	got := prettyJSON(`{"a":1,"b":[2,3]}`)
	want := "{\n  \"a\": 1,\n  \"b\": [\n    2,\n    3\n  ]\n}"
	if got != want {
		t.Errorf("prettyJSON = %q, want %q", got, want)
	}
}

func TestHexDumpLinesEmpty(t *testing.T) {
	if lines := hexDumpLines(""); lines != nil {
		t.Errorf("hexDumpLines(\"\") = %v, want nil", lines)
	}
}

func TestHexDumpLinesSingleLine(t *testing.T) {
	// "Hello, World!\n" is 14 bytes: one short line, hex column still
	// padded out to 16 slots so the ASCII gutter lines up.
	lines := hexDumpLines("Hello, World!\n")
	if len(lines) != 1 {
		t.Fatalf("lines = %v, want 1 line", lines)
	}
	line := lines[0]
	if !strings.HasPrefix(line, "00000000  ") {
		t.Errorf("line = %q, want it to start with the offset", line)
	}
	if !strings.Contains(line, "48 65 6c 6c 6f 2c 20 57") {
		t.Errorf("line = %q, want the hex bytes of \"Hello, W\"", line)
	}
	if !strings.HasSuffix(line, "|Hello, World!.|") {
		t.Errorf("line = %q, want the ASCII gutter with . for the newline", line)
	}
}

func TestHexDumpLinesMultiLineAligned(t *testing.T) {
	// 17 bytes forces a second, partial line; hexdump -C pads the hex
	// column of a short line so every line is the same width.
	raw := strings.Repeat("A", 17)
	lines := hexDumpLines(raw)
	if len(lines) != 2 {
		t.Fatalf("lines = %v, want 2 lines", lines)
	}
	if !strings.HasPrefix(lines[0], "00000000  ") {
		t.Errorf("line 0 = %q, want offset 00000000", lines[0])
	}
	if !strings.HasPrefix(lines[1], "00000010  ") {
		t.Errorf("line 1 = %q, want offset 00000010", lines[1])
	}
	// The hex column is padded so a short last line still lines up; the
	// ASCII gutter after it is not, so only the part before the first
	// `|` needs to match in width.
	hexWidth := func(line string) int { return strings.Index(line, "|") }
	if hexWidth(lines[0]) != hexWidth(lines[1]) {
		t.Errorf("hex column widths differ: %d vs %d, want the short line's hex column padded to match",
			hexWidth(lines[0]), hexWidth(lines[1]))
	}
	if !strings.HasSuffix(lines[1], "|A|") {
		t.Errorf("line 1 = %q, want a single-byte ASCII gutter", lines[1])
	}
}

func TestHexDumpLinesUnprintableBytes(t *testing.T) {
	lines := hexDumpLines("\x00\x01\x7f\xff")
	if len(lines) != 1 {
		t.Fatalf("lines = %v, want 1 line", lines)
	}
	if !strings.HasSuffix(lines[0], "|....|") {
		t.Errorf("line = %q, want unprintable bytes as . in the gutter", lines[0])
	}
}

// binaryBrowsing is a fixture table with a BLOB column holding bytes that
// are not valid UTF-8, so `v` on it must fall through to a hex dump.
func binaryBrowsing(t *testing.T) Model {
	t.Helper()
	m := browsing(t)
	ctx := context.Background()
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS blobs`,
		`CREATE TABLE blobs (id INTEGER PRIMARY KEY, payload BLOB)`,
		`INSERT INTO blobs (id, payload) VALUES (1, X'DEADBEEF00FF')`,
	} {
		if _, err := m.driver.Exec(ctx, stmt); err != nil {
			t.Fatalf("fixture %q: %v", stmt, err)
		}
	}
	m = send(t, m, press('2'), press('R'))
	if !m.panels[panelObjects].selectByName("blobs") {
		t.Fatalf("fixture table not listed: %v", m.panels[panelObjects].items)
	}
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.focus != panelMain {
		t.Fatalf("focus = %v, want the data grid", m.focus)
	}
	return m
}

// A BLOB cell renders as a hex dump, never the raw bytes: dumping
// invalid UTF-8 straight to the terminal can corrupt it.
func TestViewCellRendersBlobAsHexDump(t *testing.T) {
	m := send(t, binaryBrowsing(t), press('l'), press('v')) // payload column
	c, ok := m.modal.(*cellModal)
	if !ok {
		t.Fatalf("v opened %T, want the cell modal", m.modal)
	}
	if !strings.Contains(c.title, "binary") {
		t.Errorf("title = %q, want it to mention binary", c.title)
	}
	body := strings.Join(c.lines, "\n")
	if !strings.Contains(body, "de ad be ef 00 ff") {
		t.Errorf("cell body = %q, want the hex dump of the blob", body)
	}
	if strings.ContainsRune(body, 0xDEAD) {
		t.Errorf("cell body contains a raw non-UTF-8 rune: %q", body)
	}
	if c.rawText != "\xde\xad\xbe\xef\x00\xff" {
		t.Errorf("rawText = %q, want the untransformed bytes", c.rawText)
	}
}

// `y` inside the cell modal copies the raw value, not the hex dump or
// the pretty-printed JSON shown on screen.
func TestViewCellCopyIsRaw(t *testing.T) {
	var got string
	prev := clipboardWrite
	clipboardWrite = func(s string) error { got = s; return nil }
	t.Cleanup(func() { clipboardWrite = prev })

	m := send(t, binaryBrowsing(t), press('l'), press('v'), press('y'))
	if got != "\xde\xad\xbe\xef\x00\xff" {
		t.Errorf("clipboard = %q, want the raw blob bytes", got)
	}
	if m.modal == nil {
		t.Error("y closed the cell modal, want it to stay open")
	}
}
