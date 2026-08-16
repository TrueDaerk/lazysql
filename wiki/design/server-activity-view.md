---
type: Design Decision
title: Server activity view (processes, lock waits, kill session)
description: Why the session list is one Driver.ListProcesses returning a dialect-agnostic []db.Process with the sort settled in the driver, why the report takes over panel [1]'s main view like the schema diff instead of becoming a fourth panel or a modal, why the kill key is `K` rather than the issue's `k`, why auto-refresh is opt-in with a visible interval, and the three guards between a key press and a terminated session.
tags: [tui, activity, processlist, locks, kill, db, keybindings, read-only]
generated:
  by: claude-code/opus-5
  at: 2026-08-16T12:00:00Z
sources:
  - resource: https://github.com/TrueDaerk/lazysql/issues/151
    title: "Issue #151 — Add server activity view: processes, locks, kill session"
---

# Server activity view (processes, lock waits, kill session)

## Decision

`A` on panel `[1] Connections` asks the live connection's server what it
is doing. The report takes over the main view — the schema diff's
pattern, not a new panel and not a modal — while panel `[1]` keeps the
focus, and claims the keys it binds before the panel sees them.

The dialect work sits behind two new Driver methods, exactly like
`Explain` does:

```go
ListProcesses(ctx context.Context) ([]Process, error)
KillProcess(ctx context.Context, id string) error
```

Everything engine-specific — which catalog, which columns, whether lock
waits are reported at all, how a session is killed — lives in
`internal/db/activity.go` plus the dialect files. See
[reference/server-activity-per-dialect](../reference/server-activity-per-dialect.md)
for what each engine actually needs. `internal/ui/activity.go` never
learns which engine it is talking to.

## Why the sort lives in the driver

`ListProcesses` sorts before it returns (`db.SortProcesses`): longest
running first, sessions with no duration last, ties by ascending id.
Doing it in each dialect's `ORDER BY` would mean four different answers
to "where does a NULL duration go", and doing it in the view would mean
the order is a rendering detail that a second caller could get wrong.

The tie-break matters more than it looks: the list is re-read whole on
every refresh, so without a total order two equal rows could swap places
between refreshes and move the row `K` is aimed at.

## Why it is not a fourth panel

The side column is three numbered panels and a `1`–`3` jump; a fourth
would renumber the shell for a view that is only meaningful while
connected to a server engine. A modal was rejected for the opposite
reason: the list is browsed, refreshed and acted on over minutes, which
is what the main view is for, and a modal swallows the keys that let you
look at anything else.

Panel `[1]` owns it because a session list is a property of the
*connection*, not of the browsed database — the same reason the schema
diff hangs there. Only one of the two reports is on screen at a time:
opening either closes the other, so panel [1]'s main view never has to
decide which of two takeovers wins.

## Why `K` and not `k`

Issue #151 asks for `k` on a row to kill it. `k` is the **Up** binding
everywhere in the shell, including in the very list `K` acts on — a
lowercase kill key would make the report unnavigable with vim keys, and
would put "delete something irreversible" on the most-repeated key in the
app. `K` (shift, for kill) is bound instead. It shadows the panel's own
`move-conn-up` for as long as the report is open, which is safe because
the report is asked for keys first and nothing in it moves a connection.

Both keys are rebindable (`kill-process`, `server-activity`,
`activity-auto` in `[keys]`), so a user who wants the issue's spelling can
have it.

## Why auto-refresh is opt-in

Every refresh is a real statement, and every statement lands in the
command log — that is the log's whole contract. A view left open with a
5-second timer would push a connection's real history out of a 500-entry
ring buffer in under an hour.

So: manual `R` refresh, and `t` toggles a 5-second auto-refresh that the
footer names while it is on (`auto-refresh 5s` / `auto-refresh off`,
next to the clock time the list was read at). A beat carries the
schedule's generation and is dropped when it does not match, which is
what stops the timer when `t` is pressed again, when `esc` closes the
report, and when the connection is replaced — there is no separate "stop"
message. A beat that arrives while a read is still out re-arms the timer
without stacking a second read.

## The three guards between `K` and a dead session

1. **The UI refuses outright** for a session it must not kill: lazysql's
   own backend (killing it would drop the connection the list is read
   through), a read-only connection, and an engine with no sessions. Each
   answers in the command log with the reason.
2. **A confirm modal** names the session, its user, its database, how long
   it has been running, the statement it is running and the exact SQL
   about to execute. Nothing runs from the key press itself: the
   statement is only issued from `onConfirm`.
3. **The driver refuses again** if the session is read-only. PostgreSQL
   spells the kill as a `SELECT`, which the statement classifier would
   wave through, so `conn.KillProcess` checks `readOnly` explicitly
   rather than relying on `ContainsWrite`.

After a kill the list is re-read, so the row is gone (or visibly still
there) without a second key press.

## What the view shows

One row per session: `PID`, `User`, `Database`, `State`, `Duration`,
`Blocked by`, `Query` — the statement column taking whatever width is
left, since it is the one that is never long enough. A blocked session is
red and names the pids it waits for; lazysql's own session is muted; the
cursor row is tinted the way the data grid tints its own, so what `K`
would act on is never in doubt. `v` opens the statement in the cell
detail popup, which is the only way to read one the table had to
truncate.

## Alternatives considered

- **A `db.ResultSet` instead of `[]Process`.** It would have made the
  view a second data grid for free, but every consumer would then have to
  know which column holds the pid, and "blocked" would be a string
  comparison rather than a field. The typed struct is what lets the kill
  flow, the sort and the row styling be about sessions rather than about
  cells.
- **Killing through `Exec` with the SQL built in the UI.** Rejected by
  the architecture rule: UI code does not write dialect SQL. It would also
  have put the read-only guard in the wrong layer.
- **Auto-refresh on by default with the log filtered.** Filtering the log
  would break the one guarantee it makes — that every statement lazysql
  ran is in it.
