---
type: Design Decision
title: Cell detail popup — content-aware rendering and raw copy
description: How `v` decides between a foldable JSON tree, a hex dump and plain text, why the choice is based on the bytes rather than the declared column type, how the JSON tree folds and is navigated, and why the modal copies the untransformed value.
tags: [tui, main-view, modal, json, tree, blob, clipboard]
generated:
  by: claude-code/opus-5
  at: 2026-08-16T00:00:00Z
---

# Cell detail popup

## Decision

`v` on the data grid opens `cellModal` (`internal/ui/modal.go`) over the
cell under the cursor: the full value, scrollable, closed by `esc`. The
grid itself never grows past `maxColWidth` (32 cells) — this is the only
way to read a long value in place.

### Classification looks at the bytes, not the schema

`classifyCell` (`internal/ui/celldetail.go`) picks the rendering from the
cell's own content, not from the column's declared SQL type:

- Valid JSON whose trimmed text starts with `{` or `[` → a foldable
  tree (see below). A bare scalar (`42`, `"hello"`) is valid JSON too
  but is not treated as a document — folding a one-token value gains
  nothing, so the check requires an object or array root. Malformed
  JSON (a trailing comma, an unterminated brace) falls through to plain
  text rather than erroring the modal.
- Otherwise, invalid UTF-8 → a `hexdump -C`-style dump
  (`hexDumpLines`): 8-digit offset, 16 bytes per line in two 8-byte hex
  groups (padded so a short last line still lines up), and an ASCII
  gutter with `.` for anything outside `0x20`–`0x7e`. This is the only
  path that keeps a BLOB from reaching the terminal as raw bytes — most
  of what a driver reports as `BLOB`/`bytea` is exactly the kind of
  content that would otherwise corrupt the screen or trigger terminal
  escape sequences.
- Anything else → the raw text as-is, split on its own newlines.

The schema is not the source of truth here because it often cannot be:
a free-form query result's `DatabaseTypeName()` can be empty, a `TEXT`
column can hold JSON (SQLite has no native JSON type), and a `bytea`
column can hold valid UTF-8. Sniffing the bytes is what stays correct
in all three cases; the declared type is used only for the title, and
only as a label.

### JSON is a tree, not a wall of indented text

Pretty-printing a 5 MB `jsonb` document produced a hundred thousand
lines with no way to get an overview, so `cellJSON` renders as a
collapsible tree instead (issue #150). The fold model is the object
tree's, deliberately: `jsonNode` carries its own `expanded` flag and
`jsonRows` flattens **only the expanded nodes** into `jsonRow{node,
depth}` values — the same `treeRow` shape, the same `treeIndent` and the
same `▾`/`▸` markers `internal/ui/objtree.go` uses. The fully expanded
rendering is therefore never materialized: per keystroke the popup pays
for the rows that are on screen, not for the document.

- **Parsing** (`parseJSONTree`) walks `json.Decoder` tokens rather than
  unmarshalling into `map[string]any`, because a map loses member
  order — a `json` column stores its text verbatim and re-sorting it
  would show something the row does not hold. `dec.UseNumber()` keeps
  numeric literals exactly as written, so a bigint id is not rounded
  through `float64` on screen. Nesting depth needs no guard of its own:
  `json.Valid` in `classifyCell` already enforces `encoding/json`'s
  10 000-level limit, so the recursion is bounded before it starts.
- **Rows**: an expanded container shows its opening bracket (`▾ "b": {`),
  a folded one a summary with the child count (`▸ "b": {…} 12 keys`),
  and an empty one is a leaf (`"b": {}`) — folding `{}` would hide
  nothing. There are no closing-bracket rows: one node is one row, which
  is what keeps the cursor meaning exactly what it points at.
- **Keys** are the tree conventions, dispatched inside the modal against
  `m.keys` so `[keys]` overrides of `up`/`down`/`expand-node`/
  `collapse-node` apply here too: `j`/`k` walk visible nodes, `enter`/`l`
  open a folded node or step onto the first child of an open one, `h`
  closes it or — on a leaf — jumps to its parent. `enter` therefore does
  **not** close the popup for a JSON cell, which is the one deliberate
  divergence from the other read-only modals; `esc`, `q` and `v` still
  close it, and `keyMap.jsonCellKeys` is what documents the set in `?`
  and the options bar.
- **Fold depth** starts at `jsonExpandDepth = 2`: the root and its
  members are open, everything below them folded. That fits the common
  shape (a config or payload object of a dozen keys) on one screen while
  a deep document still opens as an overview.
- Rows are **truncated, not wrapped**, unlike the plain-text rendering:
  wrapping would break the one-row-per-node mapping the cursor depends
  on. The whole value is one `y` away.
- A document the parser cannot walk falls back to `prettyJSON`
  (`cellModal.tree == nil`), so a rendering exists even if the tree
  builder is ever wrong about a shape `json.Valid` accepted.

### Why `[]byte` cells are already plain Go strings here

`internal/db/scan.go`'s `normalizeValue` copies every `[]byte` the
driver returns into a `string` (`string(x)`), because drivers may reuse
the scan buffer. A Go string is just bytes with no encoding
requirement, so a BLOB survives that conversion losing nothing — it is
exactly the byte sequence `classifyCell` and `hexDumpLines` see, and
exactly what `db.FormatValue` renders raw. No separate binary type or
code path was needed in the UI layer; `any(string)` already carries the
BLOB intact from driver to popup.

### The title states type and size, not just a name

`newCellModal(subject, column, colType string, value any)` builds a
title like `orders.notes — text, 812 bytes`, with `(json)` or `(binary)`
appended when the detected kind adds information the declared type
didn't already give. `subject` is `Model.dataSubject()` — the table
name, or `"query"` for a free-form result — the same label the `y` copy
menu's log lines already use, so the two features read consistently.
`colType` falls back to the detected kind (`json`/`binary`/`text`) when
the driver reported no type name at all.

NULL is its own early return: title `<label> — NULL`, one line
(`nullText`, the same dim `NULL` the grid shows), no byte count — there
is no value to count.

### Copy is a modal-local key, and it never copies the rendering

`y` inside the modal copies `cellModal.rawText` — the exact
`db.FormatValue` output, before pretty-printing or hex-dumping — through
`copyTextCmd`/`clipboardWrite`, the same seam [copy-and-export](copy-and-export.md)
uses everywhere else. It does not close the modal (`update` returns
`false, cmd`): copying and then continuing to scroll is the common case,
and closing on every copy would make comparing the clipboard against the
still-open value impossible. `esc`/`q`/`enter`/`v` close it, matching
every other read-only modal in the app (`commandLogModal`, the old
`helpModal`) — except on a JSON cell, where `enter` expands the node
under the cursor and only `esc`/`q`/`v` close.

## Consequences

- The hex dump line width (~80 columns before the ASCII gutter) needs
  more horizontal room than the modal's other content; `cellModal.view`
  raises its width cap from 80 to 100 columns (still bounded by the
  terminal) rather than adding a second, narrower rendering path for
  binary content.
- A JSON value is parsed in full before display (one `jsonNode` per
  value); only the *rendering* is lazy. That is bounded by the same page
  size the grid already fetches, and it is what makes the child counts on
  a folded row exact.
- No syntax colouring was added for the JSON tree: the
  existing `sqlString`/`sqlNumber` styles could be reused for a cheap
  token colourer, but the fold markers and indentation already carry
  most of the readability gain over the grid's single truncated line,
  and a second tokenizer for a feature this size was not worth the
  surface area.

## See also

- [data-grid](data-grid.md) — the grid `v` is opened from, its column
  truncation, and how the cell cursor is tracked.
- [object-tree-panel](object-tree-panel.md) — the fold state, flatten
  pass and `l`/`h` contract the JSON tree reuses.
- [copy-and-export](copy-and-export.md) — the clipboard seam
  (`clipboardWrite`/`copyOut`) the modal's `y` reuses.
