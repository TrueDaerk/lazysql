# Focus and navigation

## Exactly one thing is focused

One panel at a time has the keyboard. It is the one with the highlighted
border and bold title, and it is what the options bar at the bottom describes.
The main view reflects the focused panel's selection.

| Key | Moves focus |
|---|---|
| `1` `2` `3` | Straight to that side panel |
| ++tab++ | Next panel, wrapping |
| ++shift+tab++ | Previous panel |
| `:` | The query editor, panel `[3]` |

The **main view** joins the cycle as soon as something is open in it. It has no
digit of its own — ++tab++ is the way in, and that is where the data grid's
keys (`h`/`l`, `s`, `/`, `e`, `y`, `g`…) live.

## The same four keys everywhere

Every panel is a list, and every list moves the same way:

| Key | Action |
|---|---|
| `j` / `↓` | Down |
| `k` / `↑` | Up |
| ++enter++ | Drill in — connect, open, toggle a branch |
| ++esc++ | Back out one step |

The object tree adds `l` / `→` to expand and `h` / `←` to collapse, lazygit
style: `l` on an open branch steps into it, `h` on a closed one steps out to
the parent.

The query editor is the one panel where ++enter++ is not "drill in" — in
normal mode it runs the statement the caret is in. Its own action list says so,
and `?` documents it there rather than twice.

## What ++esc++ unwinds

++esc++ always means *one step back*, and the step it takes depends on what is
in front of it, in this order:

1. an open modal — the popup closes and nothing is applied;
2. an inline input — the grid's `/` filter line, the editor's insert mode;
3. a foreign-key jump — in the data grid, ++esc++ pops the jump history before
   anything else, so it walks back through the tables you followed;
4. an auto-connect in progress — the dial is cancelled;
5. otherwise, focus returns to the previous panel.

## Modals swallow everything

Every interactive flow — a confirm, a menu, a prompt, a form, the date picker,
the history pane, the copy menu — is a centered popup, and while one is open it
receives **every** key. Global keys do not fire underneath it: `q` does not
quit out of a confirm dialog, and `?` does not open help over a form.

++esc++ always cancels a modal without applying anything.

## Context is a menu too

Every panel's context actions are also reachable without remembering their
keys: `a` opens the actions menu for the focused panel, listing exactly the
actions its keys dispatch. (The query editor is the exception — there `a` is
vim's *append*.) `?` shows the same set as a help overlay, grouped by
the sub-context each key belongs to — "In vim normal mode", "In the filter
input", "In the connection form" — so a key that means one thing in the grid
and another in a modal reads as scoped rather than as a conflict.

## The mouse is a shortcut, never the only way

Nothing in lazysql is mouse-only. Clicking a panel focuses it (like its
number), clicking a row moves the cursor onto it, and clicking the row the
cursor is already on is ++enter++ — so a second click drills in, which on a
tree branch means expand or collapse. Clicking a main-view tab header switches
to that tab.

The wheel scrolls whatever is *under the pointer*, focused or not: a side
panel's list, the data grid, the query editor, and an open popup. In the grid
it stops at the page boundary, because turning a page is a query — `ctrl+f` and
`ctrl+b` do that.
