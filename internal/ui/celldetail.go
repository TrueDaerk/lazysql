package ui

import (
	"bytes"
	"encoding/json"
	"errors"
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

// ---------- JSON tree ----------

// A JSON cell is not shown as a wall of pretty-printed text but as a
// foldable tree, the same shape the [2] Objects panel uses: nodes carry
// their own fold state and a flatten pass turns the *expanded* ones into
// the rows the popup renders. A 5 MB document therefore costs one parse
// and, per keystroke, only as many rows as are actually on screen — the
// fully expanded rendering is never materialized. See
// wiki/design/json-cell-tree.md.

// jsonKind is the shape of one parsed node.
type jsonKind int

const (
	jsonScalar jsonKind = iota
	jsonObject
	jsonArray
)

// jsonExpandDepth is how many levels a document opens with. Two gives the
// top-level members and one level under them — enough to see what the
// document is about — while deeper levels stay folded, which is the whole
// point of the tree for a large payload.
const jsonExpandDepth = 2

// jsonNode is one value of the parsed document. Members keep the order
// they were written in: a `json` column stores its text verbatim, and
// re-sorting it in the viewer would show something the row does not hold.
type jsonNode struct {
	// key is the object member name this value hangs under; hasKey tells
	// an absent key apart from the empty one ({"": 1} is legal JSON).
	key    string
	hasKey bool
	kind   jsonKind
	// text is the scalar's literal, already in JSON form ("a\nb", 42,
	// true, null). Empty for objects and arrays.
	text     string
	children []*jsonNode
	parent   *jsonNode
	expanded bool
}

// foldable reports whether the node has something to open. An empty
// object or array is a leaf: folding `{}` would hide nothing.
func (n *jsonNode) foldable() bool { return n.kind != jsonScalar && len(n.children) > 0 }

// parseJSONTree parses s — which classifyCell has already confirmed is a
// valid JSON object or array — into a node tree. It returns nil when the
// document cannot be walked as a tree, so the caller can fall back to the
// pretty-printed text rather than showing nothing.
func parseJSONTree(s string) *jsonNode {
	dec := json.NewDecoder(strings.NewReader(strings.TrimSpace(s)))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return nil
	}
	root, err := parseJSONValue(dec, tok)
	if err != nil || root.kind == jsonScalar {
		return nil
	}
	root.expandTo(jsonExpandDepth)
	return root
}

// errJSONShape is the one parse failure that is not the decoder's: a
// token where a value belongs. It cannot happen for input classifyCell
// accepted, but parseJSONValue is written not to trust that.
var errJSONShape = errors.New("unexpected JSON token")

// parseJSONValue builds the node for the value tok opens, consuming the
// rest of it from dec. Nesting is bounded by encoding/json's own depth
// limit, which json.Valid already enforced, so the recursion is safe.
func parseJSONValue(dec *json.Decoder, tok json.Token) (*jsonNode, error) {
	delim, ok := tok.(json.Delim)
	if !ok {
		return &jsonNode{kind: jsonScalar, text: jsonLiteral(tok)}, nil
	}
	switch delim {
	case '{':
		n := &jsonNode{kind: jsonObject}
		for {
			kt, err := dec.Token()
			if err != nil {
				return nil, err
			}
			if d, ok := kt.(json.Delim); ok && d == '}' {
				return n, nil
			}
			key, ok := kt.(string)
			if !ok {
				return nil, errJSONShape
			}
			vt, err := dec.Token()
			if err != nil {
				return nil, err
			}
			child, err := parseJSONValue(dec, vt)
			if err != nil {
				return nil, err
			}
			child.key, child.hasKey, child.parent = key, true, n
			n.children = append(n.children, child)
		}
	case '[':
		n := &jsonNode{kind: jsonArray}
		for {
			vt, err := dec.Token()
			if err != nil {
				return nil, err
			}
			if d, ok := vt.(json.Delim); ok && d == ']' {
				return n, nil
			}
			child, err := parseJSONValue(dec, vt)
			if err != nil {
				return nil, err
			}
			child.parent = n
			n.children = append(n.children, child)
		}
	}
	return nil, errJSONShape
}

// jsonLiteral renders a scalar token the way the document spelled it:
// numbers keep their exact digits (json.Number, not float64, so a bigint
// primary key is not rounded on screen) and strings keep JSON escaping.
func jsonLiteral(tok json.Token) string {
	switch v := tok.(type) {
	case nil:
		return "null"
	case bool:
		if v {
			return "true"
		}
		return "false"
	case json.Number:
		return v.String()
	case string:
		return jsonQuote(v)
	}
	return fmt.Sprint(tok)
}

// jsonQuote is a JSON string literal for s, used for both scalars and
// member names.
func jsonQuote(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `"` + s + `"`
	}
	return string(b)
}

// expandTo opens the node and everything above depth levels below it,
// leaving the rest folded.
func (n *jsonNode) expandTo(depth int) {
	if n == nil || n.kind == jsonScalar {
		return
	}
	n.expanded = depth > 0
	for _, c := range n.children {
		c.expandTo(depth - 1)
	}
}

// jsonRow is one visible line of the tree: the node and how deep it sits,
// exactly like the object tree's treeRow.
type jsonRow struct {
	node  *jsonNode
	depth int
}

// jsonRows flattens the expanded nodes into the rows the popup draws. A
// folded subtree is never walked, so the cost is the number of visible
// rows and not the size of the document.
func jsonRows(root *jsonNode) []jsonRow {
	if root == nil {
		return nil
	}
	var out []jsonRow
	var walk func(n *jsonNode, depth int)
	walk = func(n *jsonNode, depth int) {
		out = append(out, jsonRow{node: n, depth: depth})
		if !n.expanded {
			return
		}
		for _, c := range n.children {
			walk(c, depth+1)
		}
	}
	walk(root, 0)
	return out
}

// text is the line the row renders as: the indent and fold marker of the
// object tree, the member name, and the value — folded to a one-line
// summary (`{…} 12 keys`) when the node is closed.
func (r jsonRow) text() string {
	var b strings.Builder
	b.WriteString(strings.Repeat(" ", treeIndent*r.depth))
	switch {
	case !r.node.foldable():
		b.WriteString("  ")
	case r.node.expanded:
		b.WriteString("▾ ")
	default:
		b.WriteString("▸ ")
	}
	if r.node.hasKey {
		b.WriteString(jsonQuote(r.node.key) + ": ")
	}
	b.WriteString(r.node.value())
	return b.String()
}

// value is the right-hand side of a row: the scalar literal, or the
// container's bracket — open when the node is expanded, a summary with
// the child count when it is folded.
func (n *jsonNode) value() string {
	if n.kind == jsonScalar {
		return n.text
	}
	open, closing := "{", "}"
	if n.kind == jsonArray {
		open, closing = "[", "]"
	}
	switch {
	case len(n.children) == 0:
		return open + closing
	case n.expanded:
		return open
	default:
		return fmt.Sprintf("%s…%s %s", open, closing, n.countLabel())
	}
}

// countLabel says how much a folded node hides: "12 keys", "1 item".
func (n *jsonNode) countLabel() string {
	unit := "items"
	if n.kind == jsonObject {
		unit = "keys"
	}
	if len(n.children) == 1 {
		unit = strings.TrimSuffix(unit, "s")
	}
	return fmt.Sprintf("%d %s", len(n.children), unit)
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
