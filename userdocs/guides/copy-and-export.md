# Copy and export

Two keys, one serializer behind both: `y` puts text on the clipboard, `E`
streams it to a file.

## `y` — the copy menu

`y` on the Data tab opens a menu whose entries depend on where the cursor is
and what is selected.

**With no selection, on a browsed table:**

| Key | Copies |
|---|---|
| `c` | cell — the raw value |
| `r` | row — CSV line |
| `o` | row — JSON object |
| `i` | row — `INSERT` statement |
| `C` | table — CSV |
| `O` | table — JSON array |
| `I` | table — `INSERT` statements |
| `A` | table — `CREATE TABLE` + `INSERT`s |
| `d` | the DDL statement |

**With rows selected**, the selection scopes replace the row ones and take
their keys:

| Key | Copies |
|---|---|
| `r` | *N* selected rows — CSV |
| `o` | *N* selected rows — JSON array |
| `i` | *N* selected rows — `INSERT` statements |
| `c` | the cursor column's value in every selected row, one per line |

That last one is the scope only a selection has — a list of ids to paste into
the next query.

With a [column block](editing-data.md#narrowing-to-a-block-of-columns) up, the
entries say so (`3 selected rows × 2 columns`) and carry only those columns:
the CSV header, the JSON keys and the `INSERT` column list all shrink with it.

**On a query result**, the table scopes become page scopes — `C` for the
loaded page as CSV, `O` as a JSON array — because a free-form result has no
relation behind it to re-read. `E` is the way to get all of it. `INSERT`
scopes are only offered when the statement selects from exactly one table,
since an `INSERT` needs somewhere to insert into.

`ctrl+c` opens the same menu directly, scoped to the selection, while one is
up.

### Limits

A whole-table clipboard copy **streams pages** like the export does, but is
capped at **5000 rows** and two minutes, because a clipboard has to hold the
whole thing at once. Past the cap the copy is truncated and the command log
says so — `E` writes the rest without ever materializing it.

### Where the text actually goes

The native clipboard (`pbcopy`, `xclip`, `xsel`) when there is one; an OSC 52
escape sequence when there is not, so a copy over SSH lands on the clipboard
of the terminal in front of you; and a temp file as the last resort. The
command log always names which of the three happened. Details in
[Terminal setup](../getting-started/terminal-setup.md#clipboard).

## `E` — export to a file

`E` on the Data tab prompts for a path, defaulting to `<relation>.csv` in the
working directory. `~` and environment variables are expanded.

**The extension picks the format:**

| Extension | Format |
|---|---|
| `.csv` | CSV, with a header row |
| `.json` | One JSON array |
| `.sql` | `INSERT` statements |
| `.md`, `.markdown` | A Markdown table |

Any other extension — or none — is refused with a message naming the four.

The export **streams**: rows are written page by page and never all held in
memory, so table size is not a limit. Progress goes to the command log and `X`
cancels a run in flight.

The grid's filter and sort apply. A query result exports by re-running its
statement rather than re-paging what is on screen, so `E` on a result gives
you all of it, not the loaded page.

### A table's DDL

`E` on the **DDL** tab writes that relation's `CREATE` statement to a `.sql`
file (defaulting to `<relation>-ddl.sql`).

### A whole database's DDL

`E` on panel `[2] Objects` exports **every** relation of the selected database
into one `.sql` file, ordered by foreign-key dependency so the file replays in
an order that works. A dependency cycle falls back to alphabetical order.

The target is whatever the tree's cursor points into — a category can be
expanded without its database ever having been browsed — or the browsed
database when the panel is not focused.

This is a schema dump, not a data dump. For data, see
[Dump and restore](dump-and-restore.md).

## `I` — import a CSV into a table

`I` on a table in panel `[2] Objects` loads a CSV file into it — the way back
in for a CSV exported with `E` and edited elsewhere. The table must already
exist.

1. Enter the file path (`tab` completes it).
2. Check what lazysql read, and correct it if needed:
   - **Delimiter** — detected from `,` `;` tab (`\t`) `|`.
   - **First line is a header** — `space` toggles it.
   - **Columns** — the table column each CSV column goes into, in file order;
     `-` skips a column. With a header they are matched by name, otherwise
     by position.
   - **NULL marker** — the field text read as `NULL`. Empty (the default)
     means an empty field is `NULL`, matching how `E` writes it; set `\N`
     to keep empty strings as empty strings.

   Above the fields, a preview shows the first rows converted to the
   columns' types. A value that does not fit its column — `n/a` in an
   integer column — shows there as an error, and would stop the import:
   lazysql never converts it silently.
3. `enter` runs the import.

The whole import is **one transaction**. If any row fails — a type
mismatch, a constraint, a row with the wrong number of fields — everything
is rolled back and the command log names the row, its line in the file and
the reason. Progress is logged every 5000 rows; `X` cancels, which also
rolls back. A read-only connection refuses the import.

## How `NULL` is spelled

| Format | `NULL` becomes |
|---|---|
| CSV | an empty field — the only spelling that round-trips through a spreadsheet |
| JSON | `null` |
| SQL | `NULL` |
| Markdown | the word `NULL` — a blank cell would read as an empty value |

A `time.Time` value is written as RFC 3339 in every format.
