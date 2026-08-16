# Keybindings

Every binding lazysql knows about, grouped by the context it acts in — the
same grouping `?` uses inside the app.

Three things worth knowing before reading the tables:

- **Keys are case-sensitive.** `d` stages a row delete; `D` duplicates the row.
- **A binding can have several spellings**, and all of them stay live. The
  extras exist for keyboards where the primary key needs AltGr, or for
  terminals that cannot report a chord — see
  [Terminal setup](../getting-started/terminal-setup.md).
- **The *action name* column is what `[keys]` overrides.** A dash means the
  binding is dispatched inside a modal that claims every key, so overriding it
  could not change what the modal answers to — only what the options bar
  claims. Those few are fixed.

Key names in `[keys]` are the terminal's own spellings: `up`, `down`, `left`,
`right`, `enter`, `esc`, `tab`, `shift+tab`, `space`, `backspace`, `pgup`,
`pgdown`, `ctrl+f`, `ctrl+space`, and so on.

## Global

Handled by the root model, so they act from any panel.

| Keys | Action | Action name |
|---|---|---|
| `1` `2` `3` | Jump to panel | `jump` |
| `tab` | Next panel | `next-panel` |
| `shift+tab` | Previous panel | `prev-panel` |
| `+` | Next screen mode | `screen-next` |
| `_` | Previous screen mode | `screen-prev` |
| `:` | Focus the query editor | `open-editor` |
| `ctrl+c` | Cancel the running query (only while one runs) | `cancel-query` |
| `@` · `L` | Expand the command log | `command-log` |
| `?` | Help | `help` |
| `q` · `ctrl+c` | Quit | `quit` |

## Navigation

Valid in every panel.

| Keys | Action | Action name |
|---|---|---|
| `k` · `↑` | Up | `up` |
| `j` · `↓` | Down | `down` |
| `enter` | Drill in | `enter` |
| `esc` | Back | `back` |
| `ctrl+enter` · `cmd+enter` | Accept — an alias for `enter` wherever it submits, confirms or commits. Needs terminal support | `accept-changes` |

The query editor is the one panel where `enter` is not "drill in": in normal
mode it runs the statement the caret is in.

## `[1] Connections`

| Keys | Action | Action name |
|---|---|---|
| `enter` · `space` | Connect | `connect` |
| `n` | New connection | `new-connection` |
| `e` | Edit connection | `edit-connection` |
| `d` | Remove connection | `drop-connection` |
| `y` | Duplicate connection | `duplicate-connection` |
| `t` | Test connection | `test-connection` |
| `D` | Schema diff vs… | `schema-diff` |
| `K` | Move up | `move-conn-up` |
| `J` | Move down | `move-conn-down` |
| `B` | Dump / restore… | `backup` |
| `X` | Cancel dump/restore (only while one runs) | `cancel-backup` |
| `a` | Actions menu | `actions` |

### In the engine picker

| Keys | Action | Action name |
|---|---|---|
| `1`–`9` | Pick engine | — |
| `k` · `↑` / `j` · `↓` | Move | `up` / `down` |
| `enter` | Choose | — |
| `esc` | Back | `back` |

### In the connection form

| Keys | Action | Action name |
|---|---|---|
| `↓` · `tab` | Next field | — |
| `↑` · `shift+tab` | Previous field | — |
| `←` / `→` | Change value (select and checkbox fields) | — |
| `ctrl+t` | Test without saving | — |
| `ctrl+e` | Change engine | — |
| `enter` · `ctrl+enter` | Save | — |
| `esc` | Back | `back` |

### While path suggestions are up

On the `File` field of a SQLite or DuckDB profile.

| Keys | Action | Action name |
|---|---|---|
| `tab` · `shift+tab` | Complete the path, then cycle candidates | — |
| `↓` · `↑` | Select a candidate | — |
| `enter` | Accept the selected candidate | — |
| `ctrl+enter` | Save (bypasses the candidate list) | — |
| `esc` | Dismiss the list, then back out of the form | `back` |

## `[2] Objects`

| Keys | Action | Action name |
|---|---|---|
| `l` · `→` | Expand | `expand-node` |
| `h` · `←` | Collapse | `collapse-node` |
| `R` · `r` | Reload from server | `refresh` |
| `/` | Fuzzy filter | `filter` |
| `E` | Export the database's DDL | `export-database-ddl` |
| `B` | Dump / restore… | `backup` |
| `X` | Cancel dump/restore (only while one runs) | `cancel-backup` |
| `a` | Actions menu | `actions` |

## `[3] Query`

The panel's own actions, live in vim **normal** mode.

| Keys | Action | Action name |
|---|---|---|
| `i` | Edit — enter insert mode | `edit-query` |
| `enter` | Run the statement at the cursor | `run-statement` |
| `ctrl+r` · `ctrl+enter` | Run the whole script | `run-editor` |
| `ctrl+e` | Explain | `explain-query` |
| `D` | Clear the buffer | `clear-query` |
| `H` · `backspace` | History &amp; snippets | `history` |
| `ctrl+s` | Save snippet | `save-snippet` |

!!! note "`a` is not the actions menu here"
    In the query editor `a` is vim's *append*, which the editor claims before
    the panel's actions are consulted. The actions menu is reachable from
    panels `[1]`, `[2]` and the main view.

### In vim normal mode

| Keys | Action | Action name |
|---|---|---|
| `h` · `←` | Left | `vim-left` |
| `l` · `→` | Right | `vim-right` |
| `w` | Next word | `vim-word-fwd` |
| `b` | Previous word | `vim-word-back` |
| `e` | Word end | `vim-word-end` |
| `0` | Line start | `vim-line-start` |
| `$` | Line end | `vim-line-end` |
| `gg` | Buffer start | `vim-top` |
| `G` | Buffer end | `vim-bottom` |
| `a` | Append (insert) | `vim-append` |
| `I` | Insert at line start | `vim-insert-start` |
| `A` | Append at line end | `vim-append-eol` |
| `o` | Open line below | `vim-open-below` |
| `O` | Open line above | `vim-open-above` |
| `x` | Delete character | `vim-delete-char` |
| `dd` | Delete line | `vim-delete-line` |
| `yy` | Yank line | `vim-yank-line` |
| `p` | Paste | `vim-paste` |

`gg`, `dd` and `yy` are two-key chords: the binding names the first key, and
the editor completes it. Leaving the panel resets a half-typed chord.

### In insert mode

Every other key types into the buffer.

| Keys | Action | Action name |
|---|---|---|
| `ctrl+space` · `tab` | Complete | `complete` |
| `ctrl+r` · `ctrl+enter` | Run the script | `run-editor` |
| `ctrl+e` | Explain | `explain-query` |
| `ctrl+s` | Save snippet | `save-snippet` |
| `ctrl+c` | Cancel the running query | `cancel-query` |
| `esc` | Back to normal mode | `leave-insert` |

### In the completion popup

| Keys | Action | Action name |
|---|---|---|
| `↓` · `ctrl+n` | Next suggestion | `complete-next` |
| `↑` · `ctrl+p` | Previous suggestion | `complete-prev` |
| `enter` · `tab` | Accept the suggestion | `accept-completion` |
| `esc` | Close the popup only | `close-completion` |

### In history &amp; snippets

| Keys | Action | Action name |
|---|---|---|
| `enter` | Load into the editor | `hist-load` |
| `r` | Run now | `hist-run` |
| `s` | Save as a snippet | `hist-snippet` |
| `d` | Delete the entry | `hist-delete` |
| `tab` · `shift+tab` | Switch section | `hist-section` |

### Result grid, under the editor

The grid keys that survive the vim layer while a result sits under the buffer.
Everything else the grid binds is claimed by a motion first — ++tab++ into the
main view for the full set.

| Keys | Action | Action name |
|---|---|---|
| `ctrl+f` · `pgdn` | Next page | `next-page` |
| `ctrl+b` · `pgup` | Previous page | `prev-page` |
| `v` | View cell | `view-cell` |

## The main view (data grid)

Live while the main view has the focus.

| Keys | Action | Action name |
|---|---|---|
| `<` · `[` · `,` | Previous tab | `prev-main-tab` |
| `>` · `]` · `.` | Next tab | `next-main-tab` |
| `h` · `←` | Previous column | `col-left` |
| `l` · `→` | Next column | `col-right` |
| `ctrl+f` · `pgdn` | Next page | `next-page` |
| `ctrl+b` · `pgup` | Previous page | `prev-page` |
| `s` | Sort the cursor column | `sort-column` |
| `ctrl+v` · `V` | Select rows | `select-rows` |
| `C` | Select columns (`h`/`l` extend) | `select-columns` |
| `shift+↑` · `K` | Extend the selection up | `shift-up` |
| `shift+↓` · `J` | Extend the selection down | `shift-down` |
| `shift+←` | Extend the selection a column left | `shift-left` |
| `shift+→` | Extend the selection a column right | `shift-right` |
| `ctrl+c` | Copy the selection… (only while one is up) | `copy-selection` |
| `/` · `f` | Filter rows | `where-filter` |
| `F` | Clear the filter | `clear-filter` |
| `v` | View cell | `view-cell` |
| `x` | Row detail | `row-detail` |
| `g` | Follow the foreign key | `follow-fk` |
| `G` | Rows referencing this one | `incoming-refs` |
| `ctrl+o` | Back to the previous table | `browse-back` |
| `e` | Edit cell | `edit-cell` |
| `d` | Stage a row delete | `delete-row` |
| `n` | Insert a row | `insert-row` |
| `D` | Duplicate the row | `duplicate-row` |
| `c` · `ctrl+enter` | Commit the staged changes | `commit-changes` |
| `u` | Unstage | `unstage-cell` |
| `U` | Discard the staged changes | `discard-changes` |
| `y` | Copy… | `copy-menu` |
| `E` | Export to a file | `export-table` |
| `X` | Cancel the export (only while one runs) | `cancel-export` |
| `R` · `r` | Reload from server | `refresh` |
| `a` | Actions menu | `actions` |

On a [read-only connection](../guides/configuration.md#read-only-connections)
the five write keys — `e`, `d`, `n`, `D`, `c` — drop out of the options bar.
They are still listed by `?`, so every binding stays documented in exactly one
place.

### In the filter input (`/`)

Every other key types into the clause.

| Keys | Action | Action name |
|---|---|---|
| `↑` · `ctrl+p` | Previous filter for this table | `filter-hist-prev` |
| `↓` · `ctrl+n` | Next filter | `filter-hist-next` |
| `enter` · `ctrl+enter` | Apply the filter | `apply-filter` |
| `esc` | Cancel the filter | `cancel-filter` |

### In the cell detail popup (`v`)

| Keys | Action |
|---|---|
| `j` · `↓` / `k` · `↑` | Scroll a line |
| `ctrl+d` · `pgdn` · `ctrl+f` / `ctrl+u` · `pgup` · `ctrl+b` | Scroll ten lines |
| `g` · `home` / `G` · `end` | Top / bottom |
| `y` | Copy the raw value — never the rendering |
| `esc` · `q` · `enter` · `v` | Close |

### In a JSON cell popup (`v`)

A JSON value opens as a collapsible tree, so the keys are the object
tree's — including `enter`, which expands a node here instead of closing
the popup.

| Keys | Action | Action name |
|---|---|---|
| `j` · `↓` / `k` · `↑` | Next / previous visible node | `down` / `up` |
| `enter` · `l` · `→` | Expand, or step into an open node | `enter` / `expand-node` |
| `h` · `←` | Collapse, or step out to the parent | `collapse-node` |
| `ctrl+d` · `pgdn` · `ctrl+f` / `ctrl+u` · `pgup` · `ctrl+b` | Ten nodes down / up |
| `g` · `home` / `G` · `end` | First / last visible node |
| `y` | Copy the raw JSON — never the tree |
| `esc` · `q` · `v` | Close |

### In the date picker

| Keys | Action | Action name |
|---|---|---|
| `ctrl+t` | Open the picker from a text field | `open-picker` |
| `h` · `←` | Previous day / time field | `pick-prev` |
| `l` · `→` | Next day / time field | `pick-next` |
| `k` · `↑` | Previous week / +1 unit | `pick-up` |
| `j` · `↓` | Next week / −1 unit | `pick-down` |
| `[` · `H` · `,` | Previous month | `pick-month-prev` |
| `]` · `L` · `.` | Next month | `pick-month-next` |
| `t` | Jump to now | `pick-today` |
| `tab` · `shift+tab` | Date / time half | `pick-section` |
| `e` | Raw text (`NULL`, `now()`) | `pick-raw` |
| `enter` | Stage the value | `enter` |
| `esc` | Cancel | `back` |

### In the edit and insert modals

| Keys | Action |
|---|---|
| `enter` · `ctrl+enter` | Stage the value |
| `ctrl+n` | Toggle SQL `NULL` |
| `ctrl+d` | Toggle `DEFAULT` (insert form only) |
| `ctrl+t` | Open the date picker (temporal columns) |
| `tab` · `↓` / `shift+tab` · `↑` | Next / previous field (insert form) |
| `esc` | Cancel |

## Generic form popups

Any form without a bar of its own — the schema-diff picker, the dump/restore
form, a password prompt.

| Keys | Action |
|---|---|
| `↓` · `tab` | Next field |
| `↑` · `shift+tab` | Previous field |
| `←` / `→` | Change the value |
| `enter` · `ctrl+enter` | Save |
| `esc` | Back |
