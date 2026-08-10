---
type: Design Decision
title: Query editor UX rework — audit and redesigned interaction model
description: >-
  UX audit of panel [3] (modal editing, keymap, run/result story, history
  pane, autocomplete, EXPLAIN) and the redesign it led to: an in-content
  status line for mode and run state, a rationalized mnemonic keymap, a
  history pane whose enter drills in instead of executing, and a help
  modal that packs columns instead of truncating them.
tags: [ux, query-editor, keybindings, history, vim]
generated:
  by: claude-code/fable-5
  at: 2026-08-10T15:10:00+02:00
sources:
  - resource: "lazysql issue #88 — Rework query editor UX"
---

# Query editor UX rework

Issue #88 asked for a UI/UX review of the query editor as a whole: the
pieces (vim modes, autocomplete, EXPLAIN, history/snippets pane,
placeholder prompts, results in the Data tab) worked, but did not form a
discoverable whole. This concept records the audit, the chosen
interaction model and the alternatives that were rejected. It supersedes
nothing structurally — [query-editor-panel](query-editor-panel.md),
[vim-mode-query-editor](vim-mode-query-editor.md),
[history-pane-and-placeholders](history-pane-and-placeholders.md) and
[schema-aware-autocomplete](schema-aware-autocomplete.md) still describe
the machinery — but where those concepts name keys or hint texts, this
one is the current word.

## Audit findings

A code walkthrough plus a scripted PTY session (120×35 and small sizes,
SQLite fixture) against the pre-rework build found:

1. **Mode was visible only in border titles.** "insert"/"normal" appeared
   as a muted suffix in panel [3]'s border and the main view's title —
   exactly the place a first-time user does not look. Nothing in the
   content area said which keys would type and which would act.
2. **Run state and result state were diffuse.** A running query showed a
   spinner in the options bar and the border title; a finished one
   reported only to the command log. Nothing near the buffer said "this
   result below came from that buffer, 3 rows in 237µs".
3. **Hidden, unguessable entry points.** `backspace` opened the
   history/snippets pane — a key that elsewhere means "delete leftwards"
   and appears in no other panel as an opener. `ctrl+enter` ran the
   buffer as an undocumented alias living only in a `msg.String()`
   comparison.
4. **The options bar overflowed and truncated.** The query panel binds
   more actions than any side panel, and long help descriptions ("run
   the buffer", "explain the statement") pushed `D`, `backspace` and
   `ctrl+s` off a 120-column bar entirely — the keys most in need of
   discovery were the ones cut.
5. **The `?` help modal truncated too.** Bubbles' `FullHelpView` joins
   one column per group horizontally; the query panel's five groups
   already overflowed 120 columns and were cut with an ellipsis, so
   "every binding appears in `?`" silently did not hold.
6. **The history pane inverted the app's enter convention.** Everywhere
   else `enter` drills in; in the pane it *executed* the selected
   statement immediately (`e` loaded it into the editor). Running SQL is
   the most consequential action in the app and it sat on the least
   surprising key. The pane's keys existed only as raw strings in its
   own update function and its footer — invisible to `?` and immune to
   `[keys]` overrides.
7. **The vim fall-through to the result grid was mostly shadow.** With a
   result on screen, unclaimed keys fell through to the data grid — but
   the vim layer claims `h l x y d s g G`, so of the grid's keys only
   `ctrl+f`/`ctrl+b`, `v` and `[`/`]` actually arrived. Nothing
   documented which.
8. **Stale panel numbers in prose.** Comments and hints still said panel
   [5]/[4] from before the #79 panel merge; the editor is panel [3].

What the audit found *sound*, and the rework deliberately preserves: the
persistent editor with the buffer surviving every focus change; normal
mode as the resting state (a focused panel must not swallow `q`, `?`,
digits, `tab` — the lazygit language depends on it); autocomplete that
auto-opens at a two-character prefix; the run pipeline (split → read-only
vet → placeholder prompt → unguarded-write confirm → cancellable worker
→ command log); EXPLAIN as a main-view overlay; results landing in the
in-memory Data tab below the buffer.

## The redesigned interaction model

**A status line inside the editor, not only a border tag.** The line
under the buffer (previously a muted key list) is now the editor's
cockpit: a reverse-video mode badge — `INSERT` (green) or `NORMAL`
(cyan) — followed by the run state (spinner + elapsed + "ctrl+c cancel"
while running; "n rows in t", "n rows affected" or a red "failed" after;
nothing when idle with no result) and then the two or three keys that
matter in exactly this state. The badge is the answer to "why is my
typing not appearing", readable without knowing vim exists.

**A mnemonic, visible keymap.** `H` opens history & snippets (backspace
stays as a compatibility alias on the same binding); `ctrl+enter` joined
`ctrl+r` on the RunEditor binding so the alias is documented; help
descriptions shrank to their essence ("run", "explain", "clear buffer",
"history & snippets", "save snippet") so the options bar fits its
actions again. Everything else keeps its key: `i`/`enter` edit, `D`
clear (confirmed), `ctrl+e` explain, `ctrl+s` snippet, `esc` back.

**The history pane speaks the app's dialect.** `enter` now loads the
selected statement into the editor — the drill-in reading — leaving the
run one visible `ctrl+r` away, behind every guard the editor itself has.
`r` runs immediately for the muscle-memory path (same submitQuery
pipeline: placeholder prompt, unguarded-write confirm, read-only vet).
`s` saves as snippet, `d` deletes (snippets confirm), `tab` switches
section, `esc`/`q` close. All six actions are `key.Binding`s in the
keyMap — rebindable via `[keys]`, listed in `?` under the query panel,
and the pane's footer renders from those bindings, so there is exactly
one source of truth again.

**The result fall-through is documented as what it is.** The `?` groups
for the query panel gained the pane bindings and the small "result
below" set that genuinely survives the vim layer (`ctrl+f`/`ctrl+b`
page, `v` cell detail); the status line names them when a result is on
screen, plus `tab` to focus the grid for everything else (sort, copy,
export, row detail).

**The help modal packs instead of truncating.** `helpModal.view` now
measures each group's column and greedily packs columns into rows that
fit the modal's width, stacking rows vertically — every binding of every
group is visible at any terminal width the app itself accepts.

## Rejected alternatives

- **Insert mode as the resting state** (non-modal editor, "just type").
  Rejected: a focused editor that types by default swallows `q`, `?`,
  `1`–`3` and `tab`, which breaks the app-wide navigation contract the
  moment focus lands on panel [3] — the exact trap the two-mode split
  was introduced to avoid. The fix chosen instead is to make the mode
  loudly visible and its exit keys always on screen.
- **Auto-entering insert on any unbound printable key** in normal mode.
  Rejected: it makes the vim layer's chords ambiguous (`d`, `y`, `g`
  are prefixes), and a stray keystroke silently editing SQL that is
  about to be run is worse than a visibly ignored one.
- **A dedicated result pane or result tab** separate from the Data tab.
  Rejected: it duplicates the grid (paging, cell detail, copy, export)
  for no new capability, and the existing "editor above, result below"
  stacking already keeps buffer and result in one glance.
- **Running the query from a modal** (prompt-style, like the filter).
  Rejected: a modal blocks browsing while a long query runs; the
  worker + cancellable context + spinner already give a better story.
- **Replacing `enter`-runs with nothing** (load-only history pane).
  Rejected: recalling-and-rerunning is the pane's core loop; it moved
  to `r` rather than disappearing.
- **Removing the vim layer** and going textarea-native. Rejected: the
  layer is pure, tested, and invisible to non-vim users now that insert
  mode is one visible `i` away; deleting it would only remove value.

## Consequences

- `keyMap` gained `HistLoad`, `HistRun`, `HistSnippet`, `HistDelete`,
  `HistSection` (override names `hist-load`, `hist-run`,
  `hist-snippet`, `hist-delete`, `hist-section`) and the `History`
  binding is `H`+`backspace`.
- `queryRun` carries a human-readable `outcome` the status line shows
  after a run; it is set where the statement messages are reduced, so
  the line and the command log can never disagree.
- The old `editorHint` is gone; `queryStatusLine` renders badge + state
  + hints and is covered by tests, as is "every pane key is documented".
