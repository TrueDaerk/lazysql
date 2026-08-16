# Server activity

`A` on a connection in panel `[1]` asks the **connected** server what it is
doing: one row per session, the longest-running one first, with the sessions
that are stuck behind a lock marked with the id of whoever holds it.

It is the view you open when a query will not finish, a migration hangs, or
something is holding a table you need — the questions that otherwise send you
to `psql` for `pg_stat_activity` or to `mysql` for `SHOW PROCESSLIST`.

!!! note "Server engines only"

    SQLite and DuckDB run *inside* lazysql: there is no session but yours, so
    the view says so instead of showing an empty table. MySQL, MariaDB and
    PostgreSQL are supported.

## The report

It takes over the main view while panel `[1]` keeps the focus.

| Column | What it holds |
|---|---|
| `PID` | The engine's session id — MySQL's connection id, PostgreSQL's backend pid |
| `User` | The role the session connected as |
| `Database` | The database (MySQL) or the one the backend is attached to (PostgreSQL) |
| `State` | The engine's own wording: `Query: Sending data`, `active`, `idle in transaction (Lock: transactionid)` |
| `Duration` | How long the session has been doing that — a dash for an idle one |
| `Blocked by` | The sessions this one is waiting for, empty when it runs freely |
| `Query` | The statement it is running, as the server reports it |

A **blocked** session is red. Your own lazysql connection is dimmed. A dash
means the server reported nothing for that column.

An idle session deliberately has **no** duration: "idle for six hours" is not
work, and giving it one would bury the long query the view exists to surface
under a pile of pooled connections. `idle in transaction` is not idle by that
rule — it holds locks, so it keeps its duration and stays near the top.

## Keys

| Key | Action |
|---|---|
| `j` / `k` | Move between sessions |
| `g` / `G` | First / last session |
| `R` | Re-read the list now |
| `t` | Auto-refresh every 5 seconds, on or off |
| `K` | Kill the session under the cursor |
| `v` | Show that session's statement in full |
| ++esc++ | Close the report |

`A` again on an open report refreshes it without losing the cursor.

!!! tip "`K`, not `k`"

    `k` moves the cursor up here, like everywhere else in lazysql, so the kill
    key is the shifted `K`. Both are rebindable — see
    [Keybindings](../reference/keybindings.md) for the `kill-process` and
    `server-activity` action names.

## Refreshing

The list is a snapshot. `R` re-reads it, and `t` turns on a 5-second
auto-refresh — the footer always says which of the two you are looking at,
next to the time the list on screen was read at.

Auto-refresh is **off** until you ask for it, on purpose: every refresh is a
real statement, and every statement goes into the command log. A view left
open with a timer would push the connection's real history out of it.

## Killing a session

`K` ends the session under the cursor. Nothing happens on the key press
itself: a confirm popup first spells out who is being disconnected, what they
are running, and the exact statement about to be sent —
`KILL CONNECTION <id>` on MySQL/MariaDB, `pg_terminate_backend(<id>)` on
PostgreSQL. ++esc++ cancels; only ++enter++ runs it.

The statement lands in the [command log](../concepts/command-log.md) like
every other statement lazysql executes, and the list is re-read afterwards so
you can see the session go.

Three sessions cannot be killed:

- **your own** — it is the connection the list is being read through, and
  dropping it would take your staged changes with it;
- **any session on a read-only connection** — the driver refuses every write
  on one, this included, so the key comes off the options bar there;
- **anything on SQLite or DuckDB**, which have no sessions at all.

## When the list looks too short

Neither engine errors when your user may not see everything — it just answers
with less:

- **MySQL / MariaDB** — without the `PROCESS` privilege you only see your own
  sessions. Killing someone else's needs `SUPER` (MySQL) or
  `CONNECTION ADMIN` (MariaDB).
- **PostgreSQL** — without `pg_read_all_stats` (or superuser) other backends'
  statements read `<insufficient privilege>`. Terminating one needs
  `pg_signal_backend` or superuser.

Your own sessions are always visible, and always killable.

!!! warning "It is not a cancel"

    Killing a session disconnects it: its open transaction rolls back and its
    client sees a dropped connection. lazysql deliberately does not offer the
    softer `KILL QUERY` / `pg_cancel_backend` form, which would stop the
    statement but leave the transaction — and its locks — exactly where they
    were.
