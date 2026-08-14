# The query editor

Panel `[3] Query` is a real editor, not a popup: it stays in the layout,
running a script never closes it or clears the buffer, and the result appears
in the main view next to it. `:` focuses it from anywhere.

It has **two vim-style modes**. The panel gains focus in normal mode.

## Normal mode

| Key | Action |
|---|---|
| `i` | Insert mode at the cursor |
| `a` | Insert mode after the cursor |
| `I` / `A` | Insert at the line's first non-blank / at its end |
| `o` / `O` | Open a line below / above and insert |
| `h` `j` `k` `l`, arrows | Move the cursor |
| `w` / `b` / `e` | Next word / previous word / word end |
| `0` / `$` | Line start / end |
| `gg` / `G` | Buffer start / end |
| `x` | Delete the character under the cursor |
| `dd` / `yy` | Delete / yank the line |
| `p` | Paste — a yanked line below, characters after the cursor |
| ++enter++ | **Run the statement the caret is in** |
| ++ctrl+r++ | Run the whole buffer |
| ++ctrl+e++ | Explain the statement under the caret |
| ++ctrl+s++ | Save the buffer as a named snippet |
| `H` (or ++backspace++) | Open the history &amp; snippets pane |
| `D` | Clear the buffer — confirms first |
| ++esc++ | Back to the previous panel, buffer kept |

There is deliberately **no undo**: the editor keeps no edit history.

`p` pastes the editor's own `x` / `dd` / `yy` register. Your terminal's paste
(⌘V, `ctrl+shift+V`, middle click) is separate and works in both modes —
pasted characters are inserted as text, never read as vim commands.

## Insert mode

Every key types into the buffer except these:

| Key | Action |
|---|---|
| ++ctrl+space++ / ++tab++ | Complete the word under the cursor |
| ++ctrl+r++ | Run the buffer — returns to normal mode |
| ++ctrl+e++ | Explain the statement under the caret — returns to normal mode |
| ++ctrl+s++ | Save the buffer as a named snippet — insert mode is kept |
| ++ctrl+c++ | Cancel the running query |
| ++esc++ | Back to normal mode, buffer kept |

## Syntax highlighting

The buffer is highlighted per dialect as you type: keywords, string literals,
numbers, comments and `?` / `:name` placeholders each get their own color.
Identifiers and operators are deliberately left uncolored, so they keep your
terminal's own foreground. All five colors are `[theme]` slots — see
[Settings](../reference/settings.md#theme).

## Autocomplete

Typing two identifier characters opens the completion popup by itself;
++ctrl+space++ opens it on any prefix, including none.

It offers:

- the dialect's SQL keywords,
- the tables and views of the current database,
- the columns of every table the buffer already mentions — and `customers.`
  narrows it to that table's columns.

| Key | While the popup is open |
|---|---|
| `↑` / `↓`, ++ctrl+p++ / ++ctrl+n++ | Move the selection |
| ++enter++ / ++tab++ | Accept, inserting at the cursor |
| ++esc++ | Close the popup only — the buffer and insert mode are untouched |

Schema metadata is fetched in the background and cached per connection and
database, so the popup never stalls the editor: it shows what is cached and
fills in when the fetch lands.

## Running

| Key | Runs |
|---|---|
| ++enter++ (normal mode) | Exactly the statement the caret is in |
| ++ctrl+r++ (either mode) | The whole buffer |

Statement boundaries come from the dialect's own lexer, so a `;` inside a
string literal or a comment cannot truncate a statement.

Several statements separated by `;` run in order. The result of the last
`SELECT` is what the Data tab shows. An **unguarded** `DELETE` or `UPDATE` —
one with neither a `WHERE` nor a `LIMIT` — asks for confirmation first;
everything else runs as written.

++ctrl+c++ cancels a run in progress. What that actually does depends on the
engine — see [Engines](../reference/engines.md#query-cancellation).

With a result on screen, the grid keys the vim layer does not claim
(`ctrl+f` / `ctrl+b` paging and `v`) work straight from the editor panel in
normal mode, so iterating on a statement never costs the editor its focus.
++tab++ into the main view for the grid's full key set.

!!! note "A query result is not a table"
    lazysql knows which table you are browsing; it cannot always know which
    table a free-form result came from. Scopes that need one — `INSERT`
    statement copies, an SQL export — are only offered when the statement
    selects from exactly one table.

## Query plans

++ctrl+e++ asks the server how it would run the statement the caret is in. The
plan replaces the editor in the main view.

| Key | In the plan view |
|---|---|
| `j` / `k`, `ctrl+f` / `ctrl+b` | Scroll |
| `y` | Copy it |
| ++esc++ | Back to the editor — buffer, cursor and mode untouched |

Each engine's own form is used: PostgreSQL's `EXPLAIN (FORMAT JSON)` and
MySQL/MariaDB's `EXPLAIN FORMAT=JSON` render as an indented tree with each
node's cost and row estimate (MySQL falls back to the tabular `EXPLAIN` on
servers without the JSON format), SQLite's `EXPLAIN QUERY PLAN` as its
id/parent tree, and DuckDB's `EXPLAIN` diagram as it comes.

`ANALYZE` is **never** added, so nothing is executed: explaining a `DELETE` is
as safe as explaining a `SELECT`. A statement with `?` / `:name` placeholders
has no values to plan with and is refused — run it with ++ctrl+r++ to bind
them. The `EXPLAIN` itself is appended to the command log like any other
statement.

## History and snippets

`H` (or ++backspace++) opens a floating pane with two sections. ++tab++
switches between them.

**History** is every statement you executed, with the engine it ran on and
when. It persists in `${XDG_STATE_HOME:-~/.local/state}/lazysql/history` and is
scoped to the connection.

**Snippets** are statements you gave a name to. ++ctrl+s++ in the editor (in
either mode) asks for one and stores the buffer in
`${XDG_STATE_HOME:-~/.local/state}/lazysql/snippets`; an existing name asks
before it is replaced. Only the SQL text is stored — never a connection, never
a password.

| Key | In the pane |
|---|---|
| ++enter++ | Load the entry into the editor |
| `r` | Run it now |
| `s` | Save it as a named snippet |
| `d` | Delete it — snippets confirm first |
| ++tab++ | Switch between History and Snippets |
| ++esc++ | Close |

++enter++ *loads* rather than runs, deliberately: running it is then one
visible ++ctrl+r++ away, with the statement in front of you.

### Placeholders

A statement containing `?` or `:name` placeholders prompts for their values
before it runs, and executes as a **prepared statement** with those values
bound as parameters. Nothing is interpolated into the SQL text.

```sql
SELECT * FROM orders WHERE customer_id = :customer AND total > :min;
```

Running that asks for `customer` and `min`, then binds both.
