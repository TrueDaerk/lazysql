---
type: Design Decision
title: DDL destinations — file or clipboard, and the `[2]` copy menu
description: Why `E` on the Objects panel grew a destination menu instead of a second top-level key, how the clipboard and the file export are guaranteed to produce the same bytes, and why `y` on `[2]` opens a node-scoped copy menu that mirrors the grid's.
tags: [tui, ddl, clipboard, export, keybindings, object-tree]
generated:
  by: claude-code/opus-5
  at: 2026-09-23T00:00:00Z
sources:
  - resource: "GitHub issue TrueDaerk/lazysql#212"
---

# DDL destinations

Before issue #212 the DDL of a relation could reach the clipboard from
exactly one place — the grid's `y` → `d`, which needs the relation open —
and the DDL of a whole database could reach a file and nowhere else.
Both detours are annoying in the one workflow that wants DDL most: a
schema review, where nothing is being browsed and the destination is a
chat window or a migration file.

This concept records how the missing halves were added. The mechanics of
the scan itself (dependency ordering, the cycle fallback, why the DDL
tab's `E` skips the streaming worker) stay in
[design/ddl-export](ddl-export.md); the three-step clipboard fallback
stays in [design/clipboard-strategy](clipboard-strategy.md).

## Decision — the destination is a menu, not a second key

`E` on `[2] Objects` now opens a two-entry menu modal (`f` file, `c`
clipboard, `esc` cancel) before anything happens. `f` continues into the
`.sql` path prompt `E` used to open directly; `c` runs the same scan and
hands the result to `copyOut`.

A second top-level key was rejected. The Objects panel's letter space is
already crowded (`E`, `B`, `X`, `R`, `/`, `y`), and the choice here is a
*destination for one operation*, not two operations — the same shape
`B` (dump / restore) already uses. One extra keystroke buys a key back
and keeps the options bar honest: `E` still reads "export database DDL",
because that is still what it does.

Both destinations are reachable as their own `actionID`s
(`actExportDatabaseDDLFile`, `actExportDatabaseDDLClipboard`) dispatched
through `runAction`, so the menu entries, the `a` actions menu and a
future config binding all reach the same code. Neither is bound to a key
of its own.

## Decision — one text assembly, two sinks

`runDatabaseDDLScan` used to build the document and write the file in one
function. It is now split: `buildDatabaseDDL` returns the combined text
plus the tally (order, `acyclic`, `failed`), and the two workers —
`runDatabaseDDLScan` (file) and `runDatabaseDDLCopy` (clipboard) — do
nothing but choose a sink for it.

This is the only way the acceptance criterion "byte-identical" can hold
over time. A second copy of the header/separator logic would drift the
first time either side is touched;
`TestExportDatabaseDDLToClipboardMatchesTheFile` compares an actual file
against an actual clipboard write, so the split is also checked rather
than just intended.

The outcome line is shared the same way: `ddlExportFootnotes` renders the
cycle notice and the "N relation(s) had no DDL" tally, and both
destinations append it to their own first clause.

## Decision — the clipboard write rides back through `Update`

The clipboard destination reuses the whole in-flight machinery of the
file export — `dbDDLExportState`, the run `id` that makes a stale reply
harmless, the `cancel` that `resetBrowse` calls on disconnect — because
the expensive part (dozens of round trips) is identical. What differs is
the last step, and it has two constraints that pull in opposite
directions:

- `clipboardWrite` shells out to `pbcopy`/`xclip`, so it may not run in
  `Update`. It therefore runs on the worker goroutine, inside
  `runDatabaseDDLCopy`.
- An OSC 52 copy has to leave through the program's own tty, which only
  the program may write to. It therefore has to end up back in `Update`.

`databaseDDLExportedMsg` gained a `copied *copiedMsg` field that carries
copyOut's already-rendered outcome (and its OSC 52 payload, if any) back.
`finishDatabaseDDLExport` clears the in-flight state and re-emits that
`copiedMsg`, which the root's existing `case copiedMsg` turns into a log
line plus, when needed, `tea.SetClipboard`. Nothing about the clipboard
fallback chain is reimplemented here — oversized output spills to a temp
file through `writeSpillFile` exactly like a whole-table copy.

## Decision — `y` on `[2]` is a node-scoped copy menu

`y` was unbound on the Objects panel, so there was no conflict to
resolve: it takes the same `CopyMenu` binding the grid uses, and
`actCopyMenu` branches on `m.focus` inside `copyActions`. One key, one
action name (`copy-menu`), two menus — which is what keeps `?`, the
options bar and the `[keys]` config section describing one thing.

Which entries a node gets follows from what actually has DDL behind it:

| Node | Entries |
|---|---|
| relation (table or view) | `d` DDL statement, `D` database DDL |
| database | `D` database DDL |
| category header, trigger | none — one skip line in the command log |

`D` on a relation node is not redundant with `D` on its database node:
the tree cursor is usually parked on a table, and `ddlExportTarget()`
already resolves a node of any kind to its owning namespace, so offering
it costs nothing and saves a `h`-then-`y`.

Views count as relations here for the same reason they do everywhere
else in the tree — `objectCategory.relational()` is the single test.
Triggers are deliberately out of scope: their definition has its own
read-only view (see
[design/object-tree-panel](object-tree-panel.md)) and no
`TableDDL`-shaped driver call behind it.

## Decision — the single-relation copy asks the driver directly

`copyNodeDDL` reuses `m.meta.ddl` only when the metadata cache happens to
describe that exact relation (same database, same table, non-empty DDL) —
the user opened it and walked back up to `[2]`. Otherwise it calls
`Driver.TableDDL` in a `tea.Cmd` of its own.

It deliberately does **not** go through `deferUntilMeta` the way the
grid's `actCopyDDL` does. That path parks an action until a *full*
metadata fetch lands — columns, indexes, foreign keys and DDL — and then
replays it against the open relation. Here there is no open relation and
three of those four reads would be thrown away; worse, starting a meta
load for a node the user merely has the cursor on would overwrite the
cache describing whatever *is* open in the grid. One targeted round trip
is both cheaper and side-effect free.

## Consequences

- `E` on `[2]` is one keystroke longer for a file export. Two existing
  tests had to gain a `press('f')`, which is the whole cost.
- The clipboard destination shares the "only one export at a time" guard
  with the file destination — they read through the same driver, and a
  disconnect cancels either.
- `databaseDDLPrecheck` exists so the menu never opens on an export that
  cannot run *and* each destination still refuses on its own: both are
  reachable without passing through the menu.
