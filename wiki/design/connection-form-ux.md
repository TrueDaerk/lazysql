---
type: Design Decision
title: Connection flow — engine-first two-step design
description: The issue #144 ground-up redesign of the connection create/edit flow — a one-keystroke engine picker followed by an engine-specific sectioned form, its keystroke economics, and the alternatives that were rejected (including the #129 flat list this supersedes).
tags: [tui, forms, connections, ux, wizard, validation]
generated:
  by: claude-code/fable-5
  at: 2026-08-13T00:00:00Z
sources:
  - resource: https://github.com/TrueDaerk/lazysql/issues/144
  - resource: https://github.com/TrueDaerk/lazysql/issues/129
---

# Connection flow — engine-first two-step design

Issue #129 kept the connection editor as one flat field list and polished
it (field order, placeholders, inline validation). Issue #144 judged that
insufficient and asked for a structural redesign. This document is the
new design's rationale; it supersedes the #129 write-up, whose surviving
decisions (validation reticence, placeholder semantics, options-bar
contract) are folded in below.

## The chosen structure: pick the engine, then get *that engine's* form

The engine is not one field among fourteen — it is the input that decides
what every other field means and whether it exists. So it is asked alone,
first, in its own modal:

```
  New connection
  Pick an engine — it decides the fields that follow.
  ▸ 1  PostgreSQL  server · default port 5432
    2  MySQL       server · default port 3306
    3  MariaDB     server · default port 3306
    4  SQLite      single file on disk
    5  DuckDB      local file — or in-memory
```

Each engine carries a digit, so step one is a single keystroke; j/k +
enter work like every list in the app. The order is curated (daily-driver
servers first, embedded files after), not alphabetical — unregistered
future engines append rather than vanish (`enginePickOrder`).

Step two is a form built for exactly one engine (`newConnectionFormDraft`).
A SQLite form never contains a host, a password or an SSH toggle — the
fields are absent, not hidden. The fields are grouped under styled section
headers with rules:

- **Profile** — Name, Color tag
- **Server** — Host, Port, Database *(server engines)*
- **Credentials** — User, Password, Ask on connect *(server engines)*
- **SSH tunnel** — Enabled + the jump-host fields *(server engines)*
- **Storage** — File *(file engines)*
- **Advanced** — Options, Read-only

The engine itself rides in a hidden carrier field, so every reader that
asks `rawValue("engine")` — the SSH visibility predicates, `toConnection`
— is unchanged.

### Moving between the steps

- `esc` retreats one level, the app's universal reading of it: from the
  create form back to the picker (typed values kept), from the picker out
  of the flow. Editing has no step one, so esc closes the edit form
  directly.
- `ctrl+e` reopens the picker from inside the form — the way to change
  the engine, since it is no longer a walkable field. esc in *that*
  picker returns to the form.
- Values survive every transition through a `connDraft`: the profile plus
  the raw mid-edit texts that do not round-trip through
  `config.Connection` (a half-typed port, an invalid options string) and
  the typed secrets. Snapshots overlay a base draft, so a host typed on a
  server form survives even a server → file → server excursion.

## Keystroke economics

The redesign is measured in keystrokes for the two commonest profiles
(counts include opening the form with `n` and the final enter):

- **Local PostgreSQL dev DB named `dev`** — before: `n`, cycle the engine
  select ×3, ↓, `dev`, ↓, `localhost`, enter = **19**. After: `n`, `1`,
  `dev`, enter = **6**. Host is prefilled `localhost` as a real value
  (host is required anyway; anything else overtypes it), port/user fall
  back to engine defaults, and the cursor opens on Name — a text field,
  so a paste lands immediately.
- **SQLite file `app.db`** — before: `n`, cycle ×4, ↓, name, ↓, path,
  enter = **17**. After: `n`, `4`, path, enter = **9** — the cursor opens
  on File (the one field a file profile needs) and the profile name
  derives from the file's basename when left empty (`toConnection`),
  which the Name placeholder promises. DuckDB keeps the in-memory case:
  no file means a name is required again.

## Small terminals: scroll, don't overflow

#129 rejected section headers because the flat modal already brushed the
tiny-terminal guard. The redesign removes the constraint instead of the
headers: `formModal.view` clips the field block to the terminal height
and scrolls it under the cursor, marking clipped edges with `⋮`. Every
line is also clamped horizontally to the modal's width budget. A server
form with SSH open is fully usable at 62×19; below the 60×18 guard the
app shows "terminal too small" as before.

## Validation (carried over from #129, unchanged in spirit)

Per-field `validate` hooks gate the submit; a failed submit jumps the
cursor to the first offender and marks every invalid field `✗` in place,
the one under the cursor carrying the message. Format errors show while
typing; required-field errors on empty fields wait for the first submit
attempt (`formModal.submitted`), so a fresh form starts calm.
`config.Connection.Validate` still re-checks on the way to disk.

## Keys are documented in one place

The picker's contract (`enginePickerKeys`: digits, ↑↓, enter, esc) and
the form's (`connFormKeys`, now including `ctrl+e` next to `ctrl+t`) are
keyMap slices: the options bar renders them while the respective modal is
open, and `?` on the Connections panel documents both groups. They are
display-only bindings without `[keys]` slots — the modals dispatch those
keys themselves, so an override could change the claim but not the
behavior (same rule as the rest of the form contract).

## Rejected alternatives

- **The flat list with show/hide (the #129 shape).** Structurally the
  form stays "everything, minus what the predicates hide": ~14 rows for a
  server engine, engine buried as field one of them, no visual grouping,
  and the modal reshaping under the cursor as the select cycles. This is
  the baseline the issue asked to depart from.
- **A full wizard (one section per screen).** Best keystroke story for
  the happy path, but editing becomes paging: you cannot see the whole
  profile at once, fixing field 2 from step 5 is four escs, and the
  `formModal` engine would have to become a multi-page state machine that
  dump/restore and schema-diff do not want. Two steps — one decision,
  then one visible form — keeps the whole profile on screen.
- **Engine tabs across the top of one modal.** Preserves single-screen
  editing but spends a header row on four engines you are *not*
  configuring, and tab-switching with preserved cross-engine state is
  exactly the hidden-field soup the redesign removes. The picker gives
  the same reachability with one digit and no permanent chrome.
- **Auto-connect after save.** Considered for "opened form → connected";
  dropped: saving a profile and dialing it are separate intents (`enter`
  in the panel connects), and a failed dial inside the save path would
  need its own error surface. `ctrl+t` already answers "will it
  connect?" from inside the form.

## Related

- [design/connection-form-modal](connection-form-modal.md) — the generic
  form engine this builds on (sections, scrolling, drafts, ctrl+t).
- [design/path-completion-in-forms](path-completion-in-forms.md) — the
  File field's tab completion, unchanged.
- [design/connection-secrets](connection-secrets.md) — where the typed
  password goes.
