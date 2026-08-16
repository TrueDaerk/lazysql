package ui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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

// treeLines is the tree as it is on screen: the visible rows, flattened
// from the current fold state.
func treeLines(root *jsonNode) []string {
	rows := jsonRows(root)
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.text())
	}
	return out
}

// A document opens with its top levels expanded and everything deeper
// folded to a one-line summary.
func TestParseJSONTreeDefaultFold(t *testing.T) {
	root := parseJSONTree(`{"a":1,"b":{"c":{"d":2}},"list":[1,2,3]}`)
	if root == nil {
		t.Fatal("parseJSONTree returned nil for a valid object")
	}
	got := treeLines(root)
	want := []string{
		"▾ {",
		`    "a": 1`,
		`  ▾ "b": {`,
		`    ▸ "c": {…} 1 key`,
		`  ▾ "list": [`,
		"      1",
		"      2",
		"      3",
	}
	if len(got) != len(want) {
		t.Fatalf("rows = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// Folding a node hides its whole subtree behind a count; unfolding brings
// exactly that subtree back.
func TestJSONTreeFoldUnfold(t *testing.T) {
	root := parseJSONTree(`{"a":{"x":1,"y":2},"b":3}`)
	if root == nil {
		t.Fatal("parseJSONTree returned nil")
	}
	if n := len(jsonRows(root)); n != 5 { // root, a, x, y, b
		t.Fatalf("rows = %v, want 5", treeLines(root))
	}

	a := root.children[0]
	a.expanded = false
	got := treeLines(root)
	if len(got) != 3 {
		t.Fatalf("folded rows = %#v, want 3", got)
	}
	if got[1] != `  ▸ "a": {…} 2 keys` {
		t.Errorf("folded row = %q, want the summary line", got[1])
	}

	a.expanded = true
	if n := len(jsonRows(root)); n != 5 {
		t.Fatalf("unfolded rows = %v, want 5 again", treeLines(root))
	}
}

// Values keep their JSON spelling: strings quoted and escaped, numbers
// exactly as written (no float64 round-trip), empty containers as leaves.
func TestJSONTreeScalarRendering(t *testing.T) {
	root := parseJSONTree(`{"s":"a\"b\n","n":123456789012345678901,"f":1.50,"t":true,"z":null,"e":{},"l":[]}`)
	if root == nil {
		t.Fatal("parseJSONTree returned nil")
	}
	got := strings.Join(treeLines(root), "\n")
	for _, want := range []string{
		`"s": "a\"b\n"`,
		`"n": 123456789012345678901`,
		`"f": 1.50`,
		`"t": true`,
		`"z": null`,
		`"e": {}`,
		`"l": []`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("tree = %q, want it to contain %q", got, want)
		}
	}
	for _, r := range jsonRows(root) {
		if r.node.hasKey && (r.node.key == "e" || r.node.key == "l") && r.node.foldable() {
			t.Errorf("empty container %q is foldable, want a leaf", r.node.key)
		}
	}
}

// Object members keep the document's order — a `json` column stores its
// text verbatim, so re-sorting it would show something the row does not
// hold.
func TestJSONTreeKeepsMemberOrder(t *testing.T) {
	root := parseJSONTree(`{"z":1,"a":2,"m":3}`)
	if root == nil {
		t.Fatal("parseJSONTree returned nil")
	}
	var keys []string
	for _, c := range root.children {
		keys = append(keys, c.key)
	}
	if strings.Join(keys, ",") != "z,a,m" {
		t.Errorf("keys = %v, want the document order z,a,m", keys)
	}
}

// A deeply nested document is only walked as far as it is unfolded:
// nothing below the fold contributes a row, however deep it goes.
func TestJSONTreeDeepNesting(t *testing.T) {
	const depth = 200
	raw := strings.Repeat(`{"k":`, depth) + "1" + strings.Repeat("}", depth)
	if classifyCell(raw) != cellJSON {
		t.Fatalf("fixture is not classified as JSON")
	}
	root := parseJSONTree(raw)
	if root == nil {
		t.Fatal("parseJSONTree returned nil for a deeply nested document")
	}
	// Default fold: the top two levels are open, the 198 below them are
	// one folded row.
	if got := treeLines(root); len(got) != 3 {
		t.Fatalf("rows = %#v, want 3 visible rows at the default fold depth", got)
	}

	// Walking down one level at a time adds exactly one row per level.
	n := root.children[0] // already expanded by the default fold
	for i := 1; i <= 5; i++ {
		n = n.children[0]
		n.expanded = true
		if got, want := len(jsonRows(root)), 3+i; got != want {
			t.Fatalf("rows after %d expands = %d, want %d", i, got, want)
		}
	}

	// Opened all the way down, the tree ends on the scalar at the bottom
	// and holds one row per level — nothing was lost to the nesting.
	for n.foldable() {
		n.expanded = true
		n = n.children[0]
	}
	got := treeLines(root)
	if len(got) != depth+1 {
		t.Fatalf("fully expanded rows = %d, want %d", len(got), depth+1)
	}
	if !strings.Contains(got[len(got)-1], `"k": 1`) {
		t.Errorf("last row = %q, want the scalar at the deepest level", got[len(got)-1])
	}
}

// A very large array is not materialized: only the rows on screen come
// out of the flatten pass while the node is folded.
func TestJSONTreeLargeArrayStaysFolded(t *testing.T) {
	items := make([]string, 50_000)
	for i := range items {
		items[i] = "1"
	}
	root := parseJSONTree(`{"big":[` + strings.Join(items, ",") + `]}`)
	if root == nil {
		t.Fatal("parseJSONTree returned nil")
	}
	root.children[0].expanded = false
	got := treeLines(root)
	if len(got) != 2 {
		t.Fatalf("rows = %d, want the folded array to contribute one row", len(got))
	}
	if !strings.Contains(got[1], "[…] 50000 items") {
		t.Errorf("row = %q, want the item count", got[1])
	}
}

// j/k walk the visible nodes, enter/l open a folded one (and step into an
// open one), h closes it — the object tree's contract.
func TestCellModalTreeNavigation(t *testing.T) {
	m := sized(120, 40)
	c := newCellModal("t", "col", "json", `{"a":{"x":1},"b":2}`)
	if c.tree == nil {
		t.Fatal("newCellModal did not build a JSON tree")
	}
	// Fold "a" so the test drives it open itself.
	c.tree.children[0].expanded = false

	c.update(press('j'), &m) // onto "a"
	if n := c.nodeAt(jsonRows(c.tree)); n == nil || n.key != "a" {
		t.Fatalf("cursor = %v, want the \"a\" node", n)
	}
	c.update(press('l'), &m) // expand "a"
	if !c.tree.children[0].expanded {
		t.Error("l did not expand the folded node")
	}
	c.update(press('l'), &m) // step into "x"
	if n := c.nodeAt(jsonRows(c.tree)); n == nil || n.key != "x" {
		t.Fatalf("cursor = %v, want the \"x\" child", n)
	}
	c.update(press('h'), &m) // leaf: jump to the parent
	if n := c.nodeAt(jsonRows(c.tree)); n == nil || n.key != "a" {
		t.Fatalf("h on a leaf put the cursor on %v, want the parent", n)
	}
	c.update(press('h'), &m) // collapse "a"
	if c.tree.children[0].expanded {
		t.Error("h did not collapse the open node")
	}
	c.update(press('k'), &m)
	if c.cursor != 0 {
		t.Errorf("cursor = %d, want the root", c.cursor)
	}
	c.update(press('k'), &m)
	if c.cursor != 0 {
		t.Errorf("cursor = %d, want it clamped at the root", c.cursor)
	}
	c.update(press('G'), &m)
	if want := len(jsonRows(c.tree)) - 1; c.cursor != want {
		t.Errorf("cursor = %d, want the last row %d", c.cursor, want)
	}

	// enter navigates instead of closing, esc still closes.
	if closed, _ := c.update(special(tea.KeyEnter, 0), &m); closed {
		t.Error("enter closed the JSON popup, want it to expand instead")
	}
	if closed, _ := c.update(special(tea.KeyEscape, 0), &m); !closed {
		t.Error("esc did not close the JSON popup")
	}
}

// The tree renders the cursor row and the fold summary inside the modal,
// clipped to its width.
func TestCellModalTreeView(t *testing.T) {
	m := sized(120, 40)
	c := newCellModal("t", "col", "json", `{"a":{"x":1,"y":2},"b":2}`)
	c.tree.children[0].expanded = false

	out := c.view(m.style, 60, 14)
	if !strings.Contains(out, "{…} 2 keys") {
		t.Errorf("view = %q, want the folded summary", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if lipgloss.Width(line) > 60 {
			t.Errorf("line %q wider than the modal (60)", line)
		}
	}
	if !strings.Contains(out, "expand") || !strings.Contains(out, "collapse") {
		t.Errorf("view = %q, want the tree keys in the footer", out)
	}
}

// `y` on a JSON cell copies the raw document, not the tree rendering.
func TestCellModalTreeCopyIsRaw(t *testing.T) {
	var got string
	prev := clipboardWrite
	clipboardWrite = func(s string) error { got = s; return nil }
	t.Cleanup(func() { clipboardWrite = prev })

	m := sized(120, 40)
	raw := `{"a":{"x":1},"b":2}`
	c := newCellModal("t", "col", "json", raw)
	closed, cmd := c.update(press('y'), &m)
	if closed {
		t.Error("y closed the popup, want it to stay open")
	}
	if cmd == nil {
		t.Fatal("y produced no copy command")
	}
	cmd()
	if got != raw {
		t.Errorf("clipboard = %q, want the raw JSON %q", got, raw)
	}
}

// A JSON document the tree parser cannot walk keeps the pretty-printed
// rendering rather than showing nothing.
func TestCellModalFallsBackToPrettyPrint(t *testing.T) {
	c := newCellModal("t", "col", "text", "hello")
	if c.tree != nil {
		t.Error("plain text got a JSON tree")
	}
	if strings.Join(c.lines, "\n") != "hello" {
		t.Errorf("lines = %v, want the plain value", c.lines)
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

// A BLOB cell in the grid itself renders as a placeholder, not the raw
// bytes: control characters in the value would otherwise break the row and
// misalign the right border.
func TestGridRendersBlobAsPlaceholder(t *testing.T) {
	m := binaryBrowsing(t)
	out := m.View().Content
	if !strings.Contains(out, "<blob 6 B>") {
		t.Errorf("view = %q, want a blob placeholder for the payload cell", out)
	}
	if strings.ContainsRune(out, 0xDEAD) {
		t.Errorf("view contains a raw non-UTF-8 rune from the blob")
	}
}

// A long single-line value wraps to the modal width instead of getting
// truncated with an ellipsis, and j/k scroll through the wrapped lines.
func TestViewCellWrapsLongValue(t *testing.T) {
	m := sized(120, 40)
	long := strings.Repeat("abcde ", 100) // 600 bytes, well past any modal width or height
	c := newCellModal("t", "col", "text", long)

	out := c.view(m.style, 60, 12)
	if strings.Contains(out, "…") {
		t.Errorf("view = %q, want wrapped content, not an ellipsis-truncated line", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if lipgloss.Width(line) > 60 {
			t.Errorf("line %q wider than the modal (60)", line)
		}
	}

	before := out
	c.update(press('j'), &m)
	after := c.view(m.style, 60, 12)
	if before == after {
		t.Error("j did not scroll a wrapped multi-line cell value")
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
