---
type: Design Decision
title: Restore last session (connection, table, tab) on startup
description: A tiny session.json under XDG_STATE_HOME names the last connection/database/table/tab/cursor; startup redials it through the existing connect path and unwinds if any stage no longer matches reality.
tags: [ui, config, startup, persistence]
generated:
  by: claude-code/sonnet-5
  at: 2026-08-10T00:00:00Z
---

# Restore last session (connection, table, tab) on startup

`internal/session` persists `{Connection, Database, Table, Tab, Row, Col}`
to `${XDG_STATE_HOME:-~/.local/state}/lazysql/session.json` — same
directory family as `internal/history`, same atomic-write/owner-only
posture. Only a connection *name* is stored, never a secret: a restored
`AskPassword` connection prompts exactly like an interactive one.

## Why the chain lives on `Model.restoreSess`, not closures

The rest of the connect flow (`dialSelected`) is entirely closure-based —
the open password-prompt modal *is* the pending state, per
[design/tui-shell-architecture](tui-shell-architecture.md). Restore can't
use that trick end to end: it has to survive four independent async
replies (`connectedMsg` → `relationsLoadedMsg` → `pageLoadedMsg`), and a
tea.Cmd closure captured before the first of those lands has nowhere to
carry state into the next one. So `Model.restoreSess *session.Session`
holds the target across the whole chain, consumed (set nil) the moment
either the next stage succeeds terminally or fails:

- **`connectedMsg`**: connection missing after `restoreStartMsg` fired →
  never reached this handler. Dial itself fails → logged, `restoreSess`
  cleared. Dial succeeds → `findDatabaseDisplayName` checks the saved
  database is still among what the server just listed (comparing through
  `databaseArg`, because the stored value is the *driver* argument, e.g.
  `""` for a single-namespace engine, while panel `[2]` shows the
  `pseudoDatabase` display form) and opens it, or degrades to the normal
  post-connect navigation (single-namespace auto-open / multi-namespace
  panel focus) with a log line.
- **`relationsLoadedMsg`**: the saved table is looked up in
  `db.RelationNames(m.relations)`. Missing (or never set) → `restoreSess`
  cleared here, database stays open. Found → `openTable` fires and
  `restoreSess` stays set one more hop.
- **`pageLoadedMsg`**: the terminal stage. The saved `(row, col)` is
  written into `dataView` *before* `Model.clampCursor()` runs, so a stale
  cursor from a table that shrank (or emptied) clamps through the exact
  same path a live resize would — no separate restore-specific clamping
  logic. `setMainTab` runs last so switching to Structure/Indexes/DDL
  triggers its normal metadata fetch.

Each stage's failure path names what went wrong (`FAILED: %v`, `table %q
not found in %s`, …) through the same `logCmd` the command log strip
already renders — restore has no status widget of its own, which is also
what "restoring session…" in the acceptance criteria turns out to mean in
practice: the always-visible log strip, not a dedicated banner.

## `dialRequest.restore` vs. `Model.restoreSess`

`connectCmd`/`testConnCmd` are shared with every interactive dial, so the
one restore-triggered `dialRequest` carries `restore: true` through to its
`connectedMsg` reply. The handler treats a reply as "the restore's" only
when `msg.req.restore && m.restoreSess != nil` — the second half matters
because `esc` (or cancelling the `AskPassword` prompt) clears
`restoreSess` *before* the in-flight dial can reply. When that reply lands
anyway, `restore: true` but `restoreSess == nil` means "cancelled, not
adopted": a successful dial in that state is closed via
`closeSessionCmd` rather than silently becoming the active connection —
cancelling has to actually leave the connections panel disconnected, not
just skip the auto-navigation.

## Cancelling the `AskPassword` path

`formModal` gained an `onCancel func(*Model)` hook fired from its own
`esc` case, alongside the existing `onSubmit`. Restore is the first (and
so far only) caller: `esc` on the prompt clears `restoreSess` the same as
`esc` during a plain dial. Without it, cancelling the prompt would leave
`restoreSess` non-nil with no dial ever in flight to eventually clear it —
harmless in itself, but the *next*, unrelated `esc` press would be
swallowed by the "abort a pending restore" branch in `updateGlobal`
instead of doing whatever it was actually pressed for.

## Why `RestoreSession` is `*bool`, not `bool`

`omitempty` on a plain `bool` can't distinguish "absent" from "explicitly
false," and the default has to be *true* (§ acceptance criteria) — the
opposite of what a zero-value `bool` gives for free. `Config.RestoreSession
*bool` follows the same pointer trick `connectionFile`/`sshFile` already
use for `Port` (nil = "not written," not "zero"), read through
`RestoreSessionEnabled()` (`nil` or `true` → enabled). `--no-restore` is a
separate, per-run `ui.New(noRestore bool)` argument: it never touches the
config or the session file, so a config with `restore_session` left
enabled restores again next run without the flag.

## What `saveSession` writes, and when

Only on `q` (`updateGlobal`'s `k.Quit` case), and only when
`m.active != ""` — a quit from the bare connections panel must not
overwrite a real saved session with an empty one just because this run
never got around to connecting. There is no debounce and no autosave on
navigation: the issue explicitly allows either, and a clean-shutdown-only
write is the simpler of the two with no risk of a mid-browse disk write
racing a resize.
