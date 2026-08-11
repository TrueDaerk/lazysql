---
type: Design Decision
title: The reusable multi-field form modal
description: One formModal with dynamically visible fields replaces a chain of prompts; the engine select drives which fields exist.
tags: [tui, modal, forms, connections]
generated:
  by: claude-code/opus-5
  at: 2026-08-09T00:00:00Z
---

# The reusable multi-field form modal

## Decision

`internal/ui/form.go` provides one `formModal`: a labelled field stack with a
single cursor, four field kinds (`fieldText`, `fieldPassword`, `fieldSelect`,
`fieldBool`), an inline error line and the standard `enter` submit / `esc`
cancel contract. The connection editor is built from it; row insert and the
filter builder will reuse it.

The alternative — chaining `promptModal`s, one field at a time — was rejected:
you cannot go back to fix field 2 without restarting, and engine-dependent
fields would need a branch in the middle of the chain.

## Dynamic visibility

Each field carries `visible func(*formModal) bool`. `visibleFields()` is
recomputed on every keystroke and render, so changing the `engine` select
immediately swaps host/port/user/database/password for a single file path —
no modal rebuild, no lost input. Fields that are hidden keep their state, so
switching to SQLite and back does not erase a typed hostname.

Two consequences fall out of that:

- The cursor indexes into the *visible* list, so `current()` clamps after a
  visibility change and `syncFocus()` re-focuses exactly one text input.
- `value(name)` returns `""` for a hidden field (it does not apply), while
  `rawValue(name)` ignores visibility. The engine select itself must be read
  with `rawValue`, because the visibility predicates depend on it.

## Validation

`enter` calls `onSubmit`, which returns `close=false` after setting `f.err`
when validation fails, so the popup stays open with the user's input intact.
Ports are parsed and range-checked in the form; `config.Connection.Validate`
re-checks required fields on the way to disk, since profiles also arrive by
hand-editing `config.toml`.

## Password fields

`formField.value()` trims whitespace for text fields but **not** for
passwords: leading and trailing spaces can be significant in a secret. The
password field is also excluded from file-based engines entirely, since
SQLite and DuckDB have nothing to authenticate against.

## Test from inside the form (`ctrl+t`)

The connection form can dial what it currently shows without saving:
`ctrl+t` runs `toConnection()` for validation only, builds a
`dialRequest` and fires the same `testConnCmd` the panel's `t` uses.
Two mechanisms carry it:

- `formModal.onKey` — an optional hook that sees every key the form's
  own contract (esc/tab/enter/↑↓) does not claim, checked before the
  key reaches the field under the cursor. It keeps `formModal` generic;
  only the connection form installs a handler.
- `dialRequest.form` — a pointer to the originating form. The
  `connTestedMsg` reducer routes a reply with `form != nil` into that
  form's `info` (success, green) or `err` (failure) line instead of the
  panel row status — the profile may be unsaved, so there is no row to
  color — and drops the reply when that form is no longer the open
  modal. SSH failure prompts (host key, passphrase) are *not* opened
  from a form test: they would replace the form; the error text is
  shown inline instead.

Untouched secret fields fall back to the keyring entries of the profile
being edited, looked up under `oldName` because the form may be renaming
it. A typed password always wins, which also lets an `ask on connect`
profile be tested by typing the password into the form.

## Related

- [design/tui-shell-architecture](tui-shell-architecture.md) — the modal
  interface and update routing this plugs into.
- [design/connection-secrets](connection-secrets.md) — where the typed
  password goes.
- [design/path-completion-in-forms](path-completion-in-forms.md) — filesystem
  completion on the `File` field, and why it takes `tab` away from field
  navigation while candidates are up.
