---
type: Design Decision
title: Cell detail popup — content-aware rendering and raw copy
description: How `v` decides between pretty-printed JSON, a hex dump and plain text, why the choice is based on the bytes rather than the declared column type, and why the modal copies the untransformed value.
tags: [tui, main-view, modal, json, blob, clipboard]
generated:
  by: claude-code/sonnet-5
  at: 2026-08-09T00:00:00Z
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

- Valid JSON whose trimmed text starts with `{` or `[` → pretty-printed
  with `encoding/json.Indent`. A bare scalar (`42`, `"hello"`) is valid
  JSON too but is not pretty-printed — re-indenting a one-token value
  gains nothing, so the check requires an object or array root.
  Malformed JSON (a trailing comma, an unterminated brace) falls through
  to plain text rather than erroring the modal.
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
`helpModal`).

## Consequences

- The hex dump line width (~80 columns before the ASCII gutter) needs
  more horizontal room than the modal's other content; `cellModal.view`
  raises its width cap from 80 to 100 columns (still bounded by the
  terminal) rather than adding a second, narrower rendering path for
  binary content.
- A JSON value large enough to need scrolling is still pretty-printed in
  full before display — there is no streaming or partial indent. This
  matches every other in-memory formatting path in the UI (the grid
  itself, the row copy formats) and is bounded by the same page size the
  grid already fetches.
- No syntax colouring was added for the pretty-printed JSON: the
  existing `sqlString`/`sqlNumber` styles could be reused for a cheap
  token colourer, but plain indentation already carries most of the
  readability gain over the grid's single truncated line, and a second
  tokenizer for a feature this size was not worth the surface area.

## See also

- [data-grid](data-grid.md) — the grid `v` is opened from, its column
  truncation, and how the cell cursor is tracked.
- [copy-and-export](copy-and-export.md) — the clipboard seam
  (`clipboardWrite`/`copyOut`) the modal's `y` reuses.
