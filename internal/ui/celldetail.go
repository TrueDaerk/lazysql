package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// cellKind is how a cell's raw text renders in the detail popup (`v`).
type cellKind int

const (
	cellPlain cellKind = iota
	cellJSON
	cellBinary
)

// classifyCell decides how newCellModal renders raw. JSON wins over plain
// text because a pretty-printed document is more readable than the single
// line the grid would otherwise show; invalid UTF-8 cannot be either, so
// it falls through to a hex dump rather than corrupting the terminal.
func classifyCell(raw string) cellKind {
	trimmed := strings.TrimSpace(raw)
	if trimmed != "" && (trimmed[0] == '{' || trimmed[0] == '[') && json.Valid([]byte(trimmed)) {
		return cellJSON
	}
	if !utf8.ValidString(raw) {
		return cellBinary
	}
	return cellPlain
}

// prettyJSON re-indents s, which classifyCell has already confirmed is a
// valid JSON object or array.
func prettyJSON(s string) string {
	var buf bytes.Buffer
	// classifyCell already validated s, so the only possible error here
	// is one Indent cannot produce for valid input.
	_ = json.Indent(&buf, []byte(strings.TrimSpace(s)), "", "  ")
	return buf.String()
}

// hexDumpLines renders raw the way `hexdump -C` does: an 8-digit offset,
// 16 bytes per line split into two hex groups, and an ASCII gutter with
// `.` standing in for anything unprintable. It is what a BLOB gets in the
// cell detail popup, so raw bytes never reach the terminal directly.
func hexDumpLines(raw string) []string {
	b := []byte(raw)
	if len(b) == 0 {
		return nil
	}
	lines := make([]string, 0, (len(b)+15)/16)
	for off := 0; off < len(b); off += 16 {
		end := min(off+16, len(b))
		chunk := b[off:end]

		var hexPart strings.Builder
		for i := 0; i < 16; i++ {
			if i > 0 && i%8 == 0 {
				hexPart.WriteByte(' ')
			}
			if i < len(chunk) {
				fmt.Fprintf(&hexPart, "%02x ", chunk[i])
			} else {
				hexPart.WriteString("   ")
			}
		}

		var ascii strings.Builder
		for _, c := range chunk {
			if c >= 0x20 && c < 0x7f {
				ascii.WriteByte(c)
			} else {
				ascii.WriteByte('.')
			}
		}
		lines = append(lines, fmt.Sprintf("%08x  %s|%s|", off, hexPart.String(), ascii.String()))
	}
	return lines
}
