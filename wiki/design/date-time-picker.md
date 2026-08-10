---
type: Design Decision
title: Date/time picker for temporal columns
description: Why type classification lives in internal/db as db.ClassifyType, why the picker is a modal that swaps with the plain text editor rather than embedding it, the h/l · j/k · [/] key layout across the calendar and the clock, and why the raw text fallback is mandatory rather than a nicety.
tags: [tui, modal, staged-changeset, keybindings, dialects]
generated:
  by: claude-code/opus-5
  at: 2026-08-10T00:00:00Z
---

# Date/time picker for temporal columns

## Decision

Editing or inserting a value into a column whose declared type is
temporal opens a calendar-and-clock modal (`internal/ui/datepicker.go`)
instead of a bare `textinput`. Which columns those are is decided by
`db.ClassifyType` (`internal/db/coltype.go`), not by UI code.

The picker produces a value string and nothing else. The cell edit path
runs it through the existing `convertInput` → `Model.stageChange` →
[staged-changeset](staged-changeset.md); the insert path writes it into
the form field it was opened from. No SQL is built, executed or logged by
the picker itself (issue #95).

## Classification belongs in `internal/db`

`TypeKind` is `KindDate` / `KindTime` / `KindDateTime` / `KindOther`, and
`ClassifyType` maps a driver-reported `Column.DataType` onto it. It sits
next to the dialects because the spellings are a dialect fact, not a UI
one — `DATETIME(6)` (MySQL), `timestamp without time zone` (PostgreSQL),
`TIMESTAMP_NS` (DuckDB) and whatever text a SQLite `CREATE TABLE`
happened to declare all mean the same thing to the picker. This follows
[db-driver-abstraction](db-driver-abstraction.md): UI code asks a
question, the `db` package owns the engine-specific answer.

Normalization strips precision (`timestamp(6) with time zone`), array
markers (`date[]`), case and repeated whitespace, then matches the result
against an **exact** list. Prefix matching was rejected: `datetime_text`,
`timestamp_range` and a user column called `mydate` would all have been
misread, and swapping a working text field for a calendar that cannot
express the column's real values is a worse failure than showing the text
field on a column that could have had a calendar. Unknown ⇒ `KindOther`
⇒ nothing changes.

`ParseDateTimeIn` is the matching lenient reader, used to prefill the
picker from the current cell. SQLite and DuckDB hand temporal columns
back as whatever text the row holds, so it tries a list of layouts
longest-first; a value it cannot read (`now()`, `CURRENT_TIMESTAMP`,
NULL) falls back to today rather than refusing to open. It parses **in a
caller-supplied location** — `convertInput` passes the old value's zone,
because the picker spells a wall-clock time with no offset and re-reading
that as UTC would shift a value the user never touched.

## The picker is a peer modal, not a nested widget

`m.modal` holds one modal. Rather than embedding the picker inside the
edit modal and the insert form, `datePickerModal` carries a `back modal`
field and uses the shell's existing swap rule (see
[tui-shell-architecture](tui-shell-architecture.md)): a handler that
installs a replacement in `m.modal` and returns `close = true` is not
cleared, because the router only nils `m.modal` when it still points at
the modal it dispatched to.

That gives all four transitions for free:

| From | Key | To |
| --- | --- | --- |
| data grid, temporal column | `e` | picker |
| picker | `e` | plain text editor (`editCellModal`) |
| plain text editor, temporal column | `ctrl+t` | picker |
| insert form field, temporal column | `ctrl+t` | picker over the form (`back`) |

`esc` from a picker with a `back` restores it; `esc` from a picker
without one closes to the grid. Neither stages anything.

## The raw text fallback is mandatory

A calendar cannot express `now()`, `CURRENT_TIMESTAMP`, `DEFAULT` or
NULL. Making the picker the *only* editor for a `TIMESTAMP` column would
therefore have made those columns strictly harder to edit than before —
a regression dressed as a feature. `e` inside the picker is the way out,
and lands in exactly the modal that used to open for the column, with its
`ctrl+n` NULL toggle intact. The insert form keeps NULL and DEFAULT on
its own `ctrl+n` / `ctrl+d`, so its picker's `e` simply returns to it.

## Key layout

Every key is a `keyMap` binding — see
[keybindings-single-source](keybindings-single-source.md) — dispatched
inside the modal like the history pane's, exposed through
`keyMap.datePicker()`, listed in `?` under the data grid, rendered into
the picker's own footer, offered by the options bar while the picker is
open (the only modal that rebinds the bar; its keys are motions, not a
footer's worth of verbs), and rebindable through `[keys]` under
`pick-prev`, `pick-next`, `pick-up`, `pick-down`, `pick-month-prev`,
`pick-month-next`, `pick-today`, `pick-section`, `pick-raw` and
`open-picker`.

`h`/`l` mean *sideways within whatever the cursor is on* and `j`/`k`
mean *the bigger unit*, in both halves:

| Key | Calendar | Clock |
| --- | --- | --- |
| `h` / `l`, `←` / `→` | ∓1 day | previous / next spinner (hh · mm · ss) |
| `k` / `j`, `↑` / `↓` | ∓1 week | +1 / −1 on the spinner under the cursor |
| `[` / `]`, `H` / `L`, `,` / `.` | ∓1 month | — |
| `tab` | → clock | → calendar (date+time columns only) |
| `t` | now | now |

Two deliberate asymmetries:

- **`j`/`k` invert in the clock.** Down is *later* in a calendar but a
  *smaller* number on a stepper. Following the screen direction in both
  places would make one of them read backwards; the stepper convention
  wins on the clock.
- **The clock wraps per field.** 59 minutes + 1 becomes 00 minutes and
  leaves the hour and the day alone. Carrying would let a nudge on the
  minute spinner silently move the date the user just picked.

`H`/`L` are bound alongside `[`/`]` for the same QWERTZ/AZERTY reason
`PrevMainTab`/`NextMainTab` carry `,`/`.` — see
[keyboard-layout-portability](../reference/keyboard-layout-portability.md).
Inside the picker they are free, since the modal claims every key.

Month arithmetic clamps the day to the shorter month (31 January back
one month is 31 December, not the 2nd or 3rd of March that
`time.AddDate`'s own normalization produces).

## Output format

`TypeKind.Layout()` is the single place the ISO renderings live:
`2006-01-02`, `15:04:05`, `2006-01-02 15:04:05`. Every supported engine
accepts all three as literals, and `convertInput` converts back to a
`time.Time` when the cell already held one, so a driver that returned a
typed value keeps getting one.

## Alternatives rejected

- **Auto-opening the picker for every temporal field of the insert
  form.** The form has one row per column; popping a modal while the user
  tabs through twelve fields would fight the form rather than help it.
  `ctrl+t` opens it on request, and the footer advertises the key only
  while the cursor is on a field that has one.
- **A year spinner / `{`/`}` for years.** `[`/`]` held twelve times
  reaches any year, and `t` plus a typed value covers the rest. More keys
  for less.
- **Classifying by the driver's Go type (`time.Time`) instead of the
  declared type.** SQLite would then offer a calendar or not depending on
  whether the stored text happened to parse, which is exactly the
  unpredictability the declared-type rule avoids.
