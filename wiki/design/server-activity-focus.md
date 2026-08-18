---
type: Design Decision
title: The server activity view is focused in the main panel, not overlaid on panel [1]
description: Why issue #174 moved the activity report from a key-hijacking overlay on panel [1] to a main-view citizen with its own focus — how the focus, the key routing, the options bar, `?` and the mouse follow from that — and why the schema diff was deliberately left as an overlay.
tags: [tui, activity, focus, keybindings, mouse, routing]
generated:
  by: claude-code/opus-5
  at: 2026-08-18T12:00:00Z
sources:
  - resource: https://github.com/TrueDaerk/lazysql/issues/174
    title: "Issue #174 — Give the server activity view its own focus in the main panel (keyboard + mouse)"
---

# The server activity view is focused in the main panel

## Decision

`A` opens the report **in the main view and hands it the focus there**
(`openActivity` → `setFocus(panelMain)`). It is no longer an overlay that
panel `[1]` keeps the focus for while its handler eats the panel's keys.

Concretely:

- `updateData` — the main view's key handler — dispatches
  `updateActivityKeys` before anything else, the way it already dispatches
  `updateTriggerKeys` for an open trigger definition. The branch in
  `updateFocused` that gave the report first claim on panel `[1]`'s keys
  is gone.
- `1`, `tab` and a click on a side panel move the focus away. The report
  stays on screen — it is what `mainContent` draws while
  `activityOwnsMain()` — but it consumes nothing: panel `[1]` navigates,
  connects and moves connections exactly as it does with no report open.
- The report is in the `tab` order (`cycleFocus` counts the main view when
  `m.activity != nil`, like it does for `m.data.open()` and `m.trigger`),
  and a second `A` from the panel refreshes the list *and* takes the
  keyboard back to it.
- Only one thing owns the main view at a time. Opening a relation
  (`openTable`) or a trigger definition (`openTrigger`) closes the report,
  and opening the report closes the trigger and the schema diff.

## Why the overlay had to go

The overlay had panel `[1]` nominally focused while the report answered
`j`, `k`, `g`, `G`, `R`, `K`, `t`, `v` and `esc`. The panel was therefore
focused and unusable at the same time: its cursor could not be moved and
there was no key that gave it back. "Exactly one focused panel, and the
keys of the focused panel act" is the shell's central promise; a view that
is focused *somewhere else* than where its keys go breaks it, and there is
no options bar or `?` listing that can describe the resulting state
honestly.

Focus follows what is drawn instead. The report has a cursor, a selection
and verbs that act on the selection — it is a table, and the main view is
where tables are focused.

## What follows from it

- **Options bar and `?`.** Both key off `activityFocused()` (`open && focus
  == panelMain`), so the report's set is offered only while the report has
  the keyboard; a blurred report leaves the bar to whichever panel is
  focused. `?` gets its own title ("Keybindings — Server activity") and
  lists `serverActivity()` where the grid's actions would be. The same
  slice stays documented under panel `[1]` as "In the server activity view
  (A):" — that is where `A` lives, and a key is worth finding from the
  panel that puts it on screen.
- **The footer.** A blurred report replaces its key hints with
  `tab focuses the report`, and the border title drops to the muted style,
  so "on display" and "focused" never look the same.
- **The mouse.** A click in the box focuses the report and puts the cursor
  on the clicked row in one step (`clickActivity`), mapped through `v.off`
  — the window the last frame rendered — plus the one content row
  `activityContent` spends on the column header. There is no
  focus-then-select two-step like the grid's: the report is the only thing
  the box shows, so what the click aims at is never in doubt. The wheel
  keeps walking rows and now works wherever the focus is, since
  `scrollMain` routes to the report from `activityOwnsMain()` rather than
  from `m.focus == panelConnections`.

## Why the schema diff stays an overlay

`m.diff` still renders in panel `[1]`'s main view and still claims keys
through `updateFocused` — a deliberate divergence, noted at both sites.
The diff is a scroll offset over a rendered text block, not a cursor over
selectable rows: it binds `j`/`k`/`esc` and nothing that acts on a
selection, so there is no "operate the report" mode to give focus to, and
moving it would cost the shell a second main-view owner for no gain. If
the diff ever grows per-hunk actions, it should follow the report here.

## Trade-offs

- The report now hides an open grid while it is focused, where before it
  hid panel `[1]`'s connection detail. That is the honest reading of "the
  main view shows one thing", and `esc` (or opening a relation) brings the
  grid straight back — `esc` on a report with nothing else in the box also
  returns the focus to the panel `A` came from.
- It deviates from the shell's "the main view reflects the focused side
  panel's selection" default in the same way the trigger view already
  does: a main view that has its own cursor is focused in its own right.
