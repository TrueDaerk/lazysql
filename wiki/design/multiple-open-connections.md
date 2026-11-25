---
type: Design Decision
title: Several connections open at once, each with its own session
description: issue #248 — every connected profile owns a connSession (driver, tunnel, tree, grid, editor, transaction, transfers); the one on screen is embedded by value in the root Model and the rest are parked, switching swaps the embedded value, and a reply is wrapped in a sessionMsg at the Update boundary so it is reduced into the session that issued it even while another one is on screen. Covers what is per session, shared or global, the background reducer, draining workers, the panel [1] marks, disconnect/quit/rename/remove, lost background connections and command-log attribution.
tags: [connections, architecture, tui, bubbletea]
generated:
  by: claude-code/fable-5.2
  at: 2026-11-24T00:00:00Z
---

# Several connections open at once, each with its own session

lazysql used to hold one live connection: `Model` owned a single `driver`,
`active` and `tunnel`, and every piece of browsing state hung directly off
it. Connecting to a second profile closed the first and threw its state
away — the open table, the filter, the staged changeset, the editor's
transaction. A quick look into another database cost all of that.

Now every connected profile has a **session**, and connecting to another
profile opens a session next to the ones already open. `enter` on a
profile in panel `[1]` that is already connected switches to it without
dialling or reloading anything.

## The session and where it lives

`connSession` (`internal/ui/sessions.go`) is one connection's complete
working state. Exactly one session is **on screen**: it is embedded *by
value* in the root `Model`, so every reducer and renderer keeps reading
`m.driver`, `m.grid`, `m.query` as before. The others are **parked** in
`Model.parked` (`[]*connSession`), each exactly as it was left. With
nothing connected the embedded session is a blank one.

Switching (`switchTo`) swaps the embedded value: the session on screen is
copied into the parked list, the wanted one is copied out of it. `show` is
the only function that replaces the embedded session, and it also points
`Model.panels[panelObjects]` at the session's own list state and
re-derives the bindings whose enabled state follows the session
(`syncSessionKeys`: `ctrl+c` cancel, `X` cancel export/import, the
selection copy).

### Why an embedded value and not a pointer to "the current session"

A pointer (`m.sess.grid`) would have been the textbook shape. It was
rejected for two reasons. `Model` is copied on every `Update`, and the
code relies on that: a value receiver may scribble on its copy while
rendering or computing, and only what `Update` returns counts. A shared
session pointer would turn every such copy into a write to live state.
The codebase is deliberate about which state is shared between copies
(`tree`, the changeset, the page-query handle are pointers *because* they
must be) and that property had to survive per session. And an embedded
value promotes its fields, so the several hundred existing `m.grid…` /
`m.query…` references, in code and tests alike, did not have to change
meaning or spelling.

## What is per session, shared, or global

| State | Where | Why |
| --- | --- | --- |
| driver, SSH tunnel, profile name | session | the connection itself |
| object tree and panel `[2]` list state (cursor, scroll, filter, expansion) | session | "exactly as left" |
| database, open relation or trigger, main tab, metadata | session | |
| data grid: page, filter, sort, cursor, selection, column view, **staged changeset**, FK caches and jump history, in-flight page queries | session | staged changes must never show up in, or be committed against, another connection |
| query editor: buffer, mode, running script, last result, **open transaction**, parameter memory, completion, schema and highlight caches | session | |
| EXPLAIN view, server activity view | session | both describe one server |
| table export, whole-database DDL export, CSV import (`exportsModel`) | session | they read or write through the session's driver; they keep running in the background and are cancelled when *that* session closes |
| `lastFocus` — the panel of the session the keyboard was last in | session | see "Switching" |
| query history, snippets, filter history (`recallStore`) | root, shared | each mirrors one file on disk; a statement recorded by a background run has to land in the list the `H` pane reads |
| dump / restore (`Model.backup`) | root, global | it targets a *profile*, connected or not (`B` in panel `[1]` dumps a server nobody is connected to), and mostly runs an external tool. It borrows a session's driver (SQLite, DuckDB) or tunnel only when that profile has one open — `backupState.session` names it — and is cancelled when *that* session closes, never by an unrelated disconnect. One at a time for the whole app, as before |
| schema diff (`Model.diff`) | root, global | it dials its own two connections and never touched the active one |
| command log, modal, focus, screen mode, spinner, input coalescer | root, global | they are the shell, not a connection |

Before this change `backup` was a field of `exportsModel` and every
disconnect cancelled it. Moving it out is also what fixes `B` on an
unconnected profile reading the *on-screen* connection's browsed
namespace as its dump target: `baseRequest` now asks the profile's own
session, and falls back to the profile's configured database.

## Routing a reply to its session

Most async replies used to be "for the one connection", validated by a
name check (`msg.conn != m.active`) and request counters. With several
sessions a reply has to say which one it is for, and has to be reduced
into it even when it is not on screen.

Replies are **scoped at the `Update` boundary**, not in the command
constructors:

- `sessionScoped(msg)` is the one list of message types that are the
  reply of work done on a connection (listings, pages, counts, metadata,
  foreign keys, schema columns, commits, query statements and outcomes,
  transaction replies, EXPLAIN, activity, export/import progress and
  outcomes).
- `Update` wraps the command it returns in `scopeCmd(id, …)`. When the
  command later produces a session-scoped message, it comes back as
  `sessionMsg{id, msg}`; a `tea.BatchMsg` is scoped command by command.
  Everything else — `tea.Quit`, ticks, global replies — passes through.
- `Update` unwraps a `sessionMsg` first (`updateSession`): for the session
  on screen it is the ordinary reducer; for a parked one see below; for
  an id that no longer exists the reply is dropped (a transaction handle
  it carries is rolled back).

The id is the on-screen session's **at the end of the update**, which is
right for every path that changes the session inside one update (a
connect, a switch): the commands those return belong to the session that
is on screen afterwards. The one path that issues commands for a session
it does not put on screen — a connection opened in the background, see
below — scopes them itself, and the outer wrap leaves an already wrapped
message alone.

Tagging every message in its constructor was the alternative. It would
have touched ~35 constructors and call sites, and a forgotten one would
have silently been reduced into whatever is on screen. With the boundary
wrap a message type is either in `sessionScoped` or it is not; the
existing constructors, and tests that hand a bare message to `Update`
(which is reduced on screen, as before), are unchanged. The old name and
counter checks stay as they are: inside the right session they still
reject a reply for a relation the user has left.

Streaming workers (script runs, exports, imports) re-issue their
`wait…Cmd` from the reducer, so the chain is re-scoped on every hop.

## Reducing into a background session

`inBackground(s, fn)` puts the parked session in the on-screen slot, runs
`fn` — for a reply, the ordinary `reduce` — and puts everything back. The
reducers cannot tell the difference, which is the point: there is no
second set of "background" reducers to keep in sync.

What a reducer does to the *shell* rather than to the session is undone,
because a session nobody is looking at must not take the keyboard:

- `modal`, `focus` and the focus stack are restored. A background reply
  that wanted to open a dialog (a failed COMMIT, the import settings)
  does not get one; its outcome is in the session and in the log.
- A `focusPanelMsg` issued from there is dropped by `scopeCmd`.
- The session-following bindings are re-derived for the session on screen.
- `Model.background` is set for the duration, for the one behaviour that
  has to differ: the activity report's auto-refresh beat is dropped
  rather than chained, so a report nobody watches stops polling its
  server. `switchTo` arms a new beat.

`park` also clears what was waiting on the user's next key — a deferred
action (`meta.afterLoad`, `grid.fkAfter`), the completion popup, a
half-typed `/` clause.

### Spinner and ticks

The spinner and the coalescer tick are global and only ever animate the
session on screen. The spinner's tick handler drops a tick while the
on-screen session has no run, exactly as before, so switching away from
a running query lets the chain die instead of ticking for a run that is
not shown; `switchTo` starts it again when the session it shows is
running. Panel `[1]` marks a busy background session with a static
`busy` note, so nothing has to tick for it.

### Draining workers

The streaming workers send on unbuffered channels until their final
message is read. A session whose connection was closed while such a
worker is still winding down is therefore **kept** — on screen as the
blank state (as before), or in the parked list with no connection —
until `draining()` is false; `inBackground` removes it then. Dropping it
earlier would have left the worker blocked on its channel forever.

## Switching

- Only through panel `[1]`: `enter` on a connected profile. There is
  deliberately no quick-switch key.
- No dial, no query: the parked value is put back as it is. A test
  asserts the driver's statement count does not move.
- The keyboard goes to the session's `lastFocus` (the object tree when
  that panel has nothing to show any more). With the focus left on panel
  `[1]` the main view would show the profile's detail page instead of
  the session — the grid with its filter and cursor would be one more
  key away, which is not "exactly as left". `enter` on the profile that
  is already on screen does the same and dials nothing.
- A blank on-screen state is discarded by a switch. A *connect* from the
  blank state adopts it instead, so a query typed before connecting, or
  left in the editor after a disconnect, is still there — the behaviour
  the single-connection shell had.

### A connection that lands while a dialog is open

"The new connection becomes the one on screen" has one exception. A
dialog was opened for the session behind it and its answer acts on
whatever is on screen when it is given: an edit staged, rows deleted, a
commit confirmed. If a dial finishes while one is up, the new session is
opened **in the background** (`inBackground` on a fresh session) and the
log says so; `enter` on it shows it. Without this a slow connect could
swap the grid under an open edit dialog and stage the edit against
another database.

A second dial of a profile that is already open (enter pressed twice
before the first reply) is closed again on arrival: one row, one session.

## Panel [1]

`refreshConnections` derives a row's state from the sessions, ahead of
the last dial or test outcome kept in `connState`:

| State | Mark | Tint | Detail view |
| --- | --- | --- | --- |
| connected, on screen | `◆` | green | `connected · on screen` |
| connected, in background | `◇` | accent | `connected · in background — enter switches to it` |
| in background and busy | `◇` … `busy` | yellow | `connected · in background, busy — …` |

The marks are glyphs, not only colors, so the states survive a
monochrome terminal and can be asserted in a PTY dump. `busy()` is true
for a running script, a page load, an analyzed plan, a transaction
statement in flight or a file transfer. The panel is refreshed after
every background reduction, which is the only time a background
session's busy state can change.

`enter` reads `connect / switch to` and `x` reads `disconnect selected`
in the options bar and in `?` — both from the one `key.Binding` each in
`keys.go`.

## Closing sessions

- **`x`** acts on the selected row when that profile is connected,
  wherever its session is. `closeShown` is the one teardown (drop the
  transaction, cancel what runs, `resetBrowse`, close driver then
  tunnel); for a background session it runs under `inBackground`.
  Disconnecting the session on screen leaves the blank state there; the
  others stay open. The confirm appears only when there is something to
  lose and names it: an open transaction and/or staged changes. Staged
  changes used to be dropped by a disconnect without a word; that is now
  part of the same question. See [disconnect-action](disconnect-action.md).
- **Connecting no longer asks anything**: it closes nothing, so the
  "Connect and roll back the open transaction?" prompt is gone. See
  [editor-transactions](editor-transactions.md).
- **Quit** closes every session synchronously (`closeSessions`).
  `quitLosses` keeps its short wording while one connection is open and
  otherwise lists every session that loses something, by name.
- **Removing** a profile closes its session wherever it is, with the same
  loss note in the confirmation. **Renaming** one renames its session, so
  the row keeps its state and its mark. Editing other fields leaves the
  live session alone, as it always did for the active connection.
- **Ephemeral files** get an ordinary session and can sit next to saved
  profiles. There is still one ephemeral row: opening a second file
  closes the first one's session — only that one. See
  [ephemeral-file-connections](ephemeral-file-connections.md).

## A connection that dies in the background

There is no keepalive and no limit on open connections. A dead
connection is found by the reply that needed it: `lostConnection` looks
at the error of a background reply and `db.ConnectionLost`
(`internal/db/connlost.go`) decides whether it means the connection is
gone — `driver.ErrBadConn`, `sql.ErrConnDone`, `mysql.ErrInvalidConn`,
EOF, a closed or reset transport, a refused or failed redial. Timeouts
and cancellations are deliberately not on the list: the query was
stopped, the connection was not.

Such a session is closed, the profile's row turns red with the error as
its `last error`, and the log gets one line naming the connection, the
error and what was lost with it. The session on screen is not touched —
no modal, no focus change. For the session *on screen* nothing changed:
it shows its own errors and the user decides.

## Command log

The log stays one shared log. `commandLogEntries` merges the notes with
the `Logger` of **every** open driver, and a line carries its connection
(`logLine.conn`): statements from the session whose Logger recorded them,
notes from `scopeCmd`, which attributes what a session's reply logged —
and what a key pressed in panels `[2]`, `[3]` or the main view logged —
to that session. Notes from panel `[1]` name their connection in their
own text and stay unattributed.

A line is prefixed `[name]` only when that is not obvious: while more
than one connection is open, or when the line's connection is not the
one on screen. With a single connection the log reads exactly as before.

## Session restore

Unchanged: `session.json` holds one connection — the one on screen at
quit — and startup restores exactly that. Background sessions are not
saved. The restore chain's stages now check `restoring()` (the reply's
session is the one being restored) so a second connection dialled during
a restore cannot consume it. See [session-restore](session-restore.md).

## Tests

`internal/ui/sessions_test.go` drives two SQLite files through the real
connect path: the round trip (table, filter, sort, cursor, tab, tree
expansion, no statement on return), staged changes and an open
transaction surviving and committing afterwards, per-session editor
buffer and result, a query and a page load whose replies are held back
and delivered while the other session is on screen, `x` on a background
row with and without losses, the quit question across sessions and
every driver closed, the panel marks, a lost background connection, a
connect landing under an open dialog, an ephemeral file next to saved
profiles, rename/remove with a background session, and the log prefixes.
