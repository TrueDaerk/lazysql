---
type: Reference
title: UX audit — full application walkthrough (2026-08)
description: >-
  End-to-end intuitiveness audit of lazysql (issue #98): every flow exercised
  in a real PTY against a seeded SQLite database, judged against the CLAUDE.md
  lazygit design-language conventions. Findings ordered by severity, with a
  verified-fine list proving coverage.
tags: [ux, audit, keybindings, staged-changeset, help, options-bar]
generated:
  by: claude-code/fable-5
  at: 2026-08-10T19:45:00Z
sources:
  - resource: https://github.com/TrueDaerk/lazysql/issues/98
---

# UX audit — full application walkthrough (2026-08)

Method: `lazysql` built from `main` (branch `issue-98`), driven in a real PTY
(tmux, 120×35 and 80×20 and 45×8) against a seeded SQLite fixture containing a
5000-row table, a 22-column wide table with long text and NULLs, an empty
table, a table without a primary key, a date/time/blob types table, and a
view. All eight flow areas from issue #98 were exercised interactively.
Reference for the design language: [design/tui-shell-architecture](../design/tui-shell-architecture.md)
and the CLAUDE.md "Layout & UX conventions".

Not covered (environment limits): server-based engines (MySQL/PostgreSQL) and
mid-session disconnect behavior; long-running-query feedback (spinner /
cancellation) — all queries against the local SQLite fixture returned in
microseconds. These need a follow-up pass with a real server.

Folded-in open issues (not re-reported): #94 (column types — now shown in the
insert modal and structure tab, verified present), #95 (date picker — present,
opens from `e` on temporal cells), #96 (`ctrl+enter` — footers consistently
offer `enter/ctrl+enter`), #97 (QWERTZ aliases — `]/.`, `[/,`, `@/L` observed).

## Findings

### Blocker

**B1 — `q` quits instantly and silently discards staged changes.**
Did: staged a cell edit (status line showed "1 staged change"), pressed `q`.
Expected: a confirm modal ("1 staged change will be lost — quit?"), matching
the app's own convention that destructive steps get an explicit modal
(commit, discard, remove connection all have one).
Happened: immediate exit, no prompt; on restart the session restored the open
table but the changeset was gone. This is a data-loss hole in the staged
model — the entire point of staging is that nothing is lost or executed
without an explicit decision.
File: `internal/ui/model.go` (`k.Quit` case, ~line 1193) — no staged-changes
guard before `tea.Quit`. Follow-up: issue #103.

### Friction

**F1 — command log content overflows its border and swallows the options bar.**
Did: connected, opened tables; whenever the newest log entry wraps to
multiple lines, the log panel renders taller than its box — content appears
below the bottom border and the options bar row disappears entirely.
Expected: log clipped to its panel; options bar always visible (it is the
primary discoverability surface).
Happened: reproduced repeatedly at 120×35 (after connect, after running a
query). File: `internal/ui/view.go` (command log strip rendering, ~line 460).
Follow-up: issue #104.

**F2 — autocomplete popup is invisible when editing in side panel `[3] Query`.**
Did: typed `select * from big` in the side panel, pressed `ctrl+space`.
Expected: a suggestion list, as the options bar promises ("↓/ctrl+n next
suggestion · enter/tab accept").
Happened: no popup rendered anywhere; cycling and accepting works blind
(enter completed to `big_table`). The same completion in the main-view query
editor renders a proper popup with kinds (table/view/kw). A first-time user
in the side panel concludes completion is broken.
Files: `internal/ui/complete.go`, `internal/ui/query.go` (popup placement).
Follow-up: issue #105.

**F3 — `?` help merges sub-context bindings into panel help without headers,
creating duplicate/conflicting entries.**
Did: pressed `?` in the Data view and in Connections.
Expected: help that matches the current context, one meaning per key.
Happened: Data help lists the date-picker keymap inline — `h/←` appears as
both "prev column" and "prev day / time field", `e` as both "edit cell" and
"raw text (NULL, now())", `]/L next month` collides with `@/L expand command
log`. Connections help lists form-modal keys — `tab` has three meanings
(complete path / next field / next panel). Violates "every binding appears in
`?`" in spirit: they appear, but as noise that teaches wrong lessons.
File: `internal/ui/keys.go` (help assembly). Follow-up: issue #106.

**F4 — cell viewer (`v`) truncates long text; content unreadable.**
Did: `v` on a 167-byte text cell.
Expected: wrapped or scrollable full value — the viewer exists precisely for
values the grid truncates.
Happened: single line ending in `…`; `j`/`k` do not scroll; no way to read
the value anywhere in the app (row detail `x` reuses the same rendering).
File: `internal/ui/celldetail.go`. Follow-up: issue #107.

**F5 — BLOB cells render raw bytes and corrupt the grid.**
Did: opened a table with a 4-byte BLOB (`x'DEADBEEF'`).
Expected: a placeholder (`<blob 4 B>` or hex), as the cell-detail popup
already classifies binary for its hex dump.
Happened: control characters printed into the row; the row's right border and
following cell misaligned. File: `internal/ui/datagrid.go` (cell text
sanitization). Follow-up: issue #108.

**F6 — empty table reports "no rows match".**
Did: opened a genuinely empty table, no filter active.
Expected: "table is empty" (with "no rows match" reserved for an active
filter — the status line already knows whether a where-clause is applied).
Happened: "no rows match" implies a filter the user never set; first-run
users will hunt for one. File: `internal/ui/datagrid.go:275`.
Follow-up: issue #108 (grouped with F5 — both grid rendering).

### Polish

**P1 — options bar truncates with `…` even at 120 columns, hiding most
bindings; the first-run bar never mentions `?`.** The bar shows 4–5 bindings
then `…`; keys like `e`, `d`, `n`, `c` (the whole editing lifecycle) are
hidden in the Data context. lazygit keeps the bar terse but always shows
"? help" as the escape hatch; here a first-time user has no visible route to
the full keymap. File: `internal/ui/view.go` (options bar).

**P2 — actions menu (`a`) is not centered and truncates labels despite free
space.** "remove connecti…" in a narrow fixed-width box, positioned top-left
of the main view; every other modal is centered. File: `internal/ui/modal.go`.

**P3 — DATE columns display as full timestamps.** `2026-08-01` renders as
`2026-08-01T00:00:00Z` in the grid; the structure tab knows the declared type
is `date`. File: `internal/ui/datagrid.go` (temporal formatting).

**P4 — query side panel placeholder goes stale.** After `:` the panel title
becomes "[3] Query insert" but the body still reads "(empty — press : to
write a query)" until the first keystroke. File: `internal/ui/query.go`.

**P5 — occasional stray tab characters in rendered lines** (seen in main-view
filler and wrapped command-log lines), which misalign the right border in
some terminals. Likely unexpanded `\t` in logged SQL / padding. Files:
`internal/ui/view.go` / log entry rendering.

## Verified fine

- **First run**: empty states teach the next step in every panel ("press n to
  add one", "press : to write a query"); connection form is engine-adaptive
  (fields change per engine), placeholders documented, footer lists all keys;
  save is logged to the command log.
- **Navigation**: `1`–`3` jump, `tab` cycles, exactly one green focused panel
  at all times; tree drill `enter`/`l`/`h` consistent; `esc` chain is
  coherent everywhere tested (popup → normal mode → results → objects panel;
  modal `esc` always cancels).
- **Table browsing**: pagination status ("rows 101–200 of ~5000 (page
  2/50)"), `ctrl+f`/`ctrl+b` paging, column scrolling with "columns 1–9 of 22
  — h/l scrolls" hint, ellipsis truncation, dim NULL styling, sticky main-view
  tabs (lazygit-consistent), structured filter modal with parameterized
  values and the active where-clause echoed in the status line, `F` clears.
- **Editing lifecycle**: staged cell is yellow/bold with "N staged changes"
  in the status line; staged insert appears as a phantom row with `DEFAULT`
  markers; staged delete renders red strikethrough; commit modal previews the
  exact SQL with args and states the one-transaction guarantee; discard has
  its own confirm; commit result confirmed in the command log ("commit ok: 3
  staged changes applied") and the grid refreshes. Excellent — the strongest
  flow in the app (modulo B1).
- **Query editor**: side-panel and main-view editors; vim normal/insert with
  visible mode badge; non-vim users survive (arrows, `enter` to edit, footer
  hints); `ctrl+r` run with row-count + timing feedback; history & snippets
  pane (`H`) with load/run/save/delete; main-view completion popup shows
  candidate kinds; results reuse the grid.
- **Feedback**: command log records every statement with timings plus
  lifecycle comments (`-- connecting`, `-- stage:`, `-- commit ok`); errors
  not deeply exercised (SQLite fixture) — see "not covered".
- **Edge conditions**: tiny-terminal guard ("45x8 — need at least 60x18");
  resize mid-modal reflows cleanly at 80×20; session restore reconnects and
  reopens the last table.
- **Date picker** (#95): calendar + clock modal from `e` on temporal cells,
  month navigation, raw-text escape hatch.
