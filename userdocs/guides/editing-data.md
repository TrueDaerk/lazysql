# Editing data

Everything on this page **stages** a change. Nothing here sends SQL — read
[Staged mutations](../concepts/staged-mutations.md) first if you have not.

All of it happens on the **Data** tab with the main view focused.

| Key | Stages |
|---|---|
| `e` | An `UPDATE` of the cursor cell |
| `n` | An `INSERT` |
| `D` | An `INSERT` prefilled from the cursor row |
| `d` | A `DELETE` of the cursor row |
| `u` | *Un*stages what is under the cursor |
| `U` | Discards the whole changeset |
| `c` | Commits everything, in one transaction |

!!! warning "A table needs a declared primary key"
    Cell edits and row deletes identify their row by its full primary key.
    Without one there is no safe statement to write, so both refuse. Inserts
    do not need a key and work regardless.

## Editing a cell

`e` opens a one-field modal on the cursor cell.

| Key | In the edit modal |
|---|---|
| ++enter++ (or ++ctrl+enter++) | Stage the value |
| ++ctrl+n++ | Toggle SQL `NULL` |
| ++ctrl+t++ | Open the date picker (temporal columns only) |
| ++esc++ | Cancel, staging nothing |

Typing while the field is on `NULL` switches it back to a value.

Editing a cell back to its original value **unstages** it rather than staging
a no-op.

## Inserting and duplicating a row

`n` opens a form with one field per column; `D` opens the same form prefilled
from the row under the cursor — as the grid shows it, staged edits included —
with its primary key cleared, since reusing it would collide.

Each field is in one of three modes:

| Mode | Meaning |
|---|---|
| **value** | Bind what is typed |
| **NULL** | Bind an explicit SQL `NULL` |
| **DEFAULT** | Leave the column out of the statement, so the engine fills it in |

The form starts each field on the mode that is usually right: `DEFAULT` for an
auto-assigned column (a `DEFAULT` clause, auto-increment / identity / serial,
or SQLite's `INTEGER PRIMARY KEY` rowid alias), `NULL` for a nullable column,
and a value for everything else.

| Key | In the insert form |
|---|---|
| ++tab++ / `↓` | Next field |
| ++shift+tab++ / `↑` | Previous field |
| ++ctrl+n++ | Toggle the field between `NULL` and a value |
| ++ctrl+d++ | Toggle the field between `DEFAULT` and a value |
| ++ctrl+t++ | Open the date picker for a temporal field |
| ++enter++ | Stage the `INSERT` |
| ++esc++ | Cancel |

Typing into a field switches it to the value mode, whatever it was on.

The staged insert appears as a green **phantom row** after the last row of the
page, with `DEFAULT` in every column left out.

## Dates and times

`e` on a `DATE`, `DATETIME`, `TIMESTAMP`, `TIMESTAMPTZ` or `TIME` column — in
every dialect's spelling, precision and time-zone qualifier included — opens a
calendar and clock instead of a bare text field. ++ctrl+t++ opens the same
picker for the field under the cursor in the insert form.

| Key | In the picker |
|---|---|
| `h` / `l`, `←` / `→` | Previous / next day — or previous / next time field on the clock |
| `k` / `j`, `↑` / `↓` | Previous / next week — or adjust the time field under the cursor |
| `[` / `]` (or `H` / `L`, `,` / `.`) | Previous / next month |
| ++tab++ | Switch between the date and the time half |
| `t` | Jump to now |
| `e` | Raw text entry — for `NULL`, `now()`, `CURRENT_TIMESTAMP` and anything else a calendar cannot spell (++ctrl+t++ comes back) |
| ++enter++ | Stage the value, ISO-formatted |
| ++esc++ | Cancel, staging nothing |

The picker only produces a value. It is staged like any other edit and executes
nothing until `c`.

## Editing a column across rows

`ctrl+v` (or `V`) opens a vim-style row selection anchored at the cursor row;
every vertical move extends it, and ++esc++ — or a second `ctrl+v` — clears it.

With a selection up, `e` opens the usual edit modal for the **cursor column**
and stages the confirmed value in that column of every selected row: one
pending change each, all visible in the grid, none executed before `c`.

Rows that cannot be identified safely — one already staged for deletion, one
whose primary key the result set does not carry — drop out and are reported in
the command log.

The selection is dropped by anything that replaces the rows under it (a
reload, a sort, a filter, a page turn), so a staged edit can never land on a
row that was never picked.

### Narrowing to a block of columns

`shift+←` / `shift+→` — or `C`, after which plain `h` / `l` move the open edge
— narrow the selection to a block. The first press anchors the column span at
the cursor column; each further press moves the other edge. Until one of them
is pressed the selection means whole rows.

With a block up the status line reads `N rows × M columns selected`, only the
block is tinted, and the [copy scopes](copy-and-export.md) carry only those
columns. The bulk `e` edit stays aimed at the cursor column.

## Deleting a row

`d` stages a `DELETE` of the cursor row, which is struck through in red until
you commit or unstage it. Staging the delete drops that row's pending cell
edits, and `e` on a row already staged for deletion is refused with a note in
the command log.

`d` on a phantom row is refused too — a staged insert is not a row yet; `u`
removes the whole insert.

## Committing

`c` opens a confirm modal listing what is about to run, and only then does
lazysql execute: every staged statement, in staging order, inside **one
transaction**.

- Success clears the changeset and reloads the page.
- Failure rolls the transaction back — nothing is applied — and **keeps** the
  changeset, so you can fix the cause and commit again.

Every statement the commit runs lands in the
[command log](../concepts/command-log.md).

++ctrl+enter++ is an alias for `c` where the terminal reports it. It opens the
same confirm modal; there is no way to bypass it.

!!! note "Quitting with changes staged"
    `q` on a dirty changeset asks first, naming how many changes would be lost.
    Staged changes are not saved on exit.
