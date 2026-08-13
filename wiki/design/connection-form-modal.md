---
type: Design Decision
title: The reusable multi-field form modal
description: One formModal with dynamically visible fields, optional section headers and a cursor-following scroll window; the connection editor, dump/restore and schema-diff build on it.
tags: [tui, modal, forms, connections]
generated:
  by: claude-code/fable-5
  at: 2026-08-13T00:00:00Z
---

# The reusable multi-field form modal

## Decision

`internal/ui/form.go` provides one `formModal`: a labelled field stack with a
single cursor, four field kinds (`fieldText`, `fieldPassword`, `fieldSelect`,
`fieldBool`), an inline error line and the standard `enter` submit / `esc`
cancel contract. The connection editor is built from it, as are the
dump/restore and schema-diff forms and the dial-time secret prompts.

The alternative — chaining `promptModal`s, one field at a time — was rejected:
you cannot go back to fix field 2 without restarting, and engine-dependent
fields would need a branch in the middle of the chain.

## Sections and scrolling (issue #144)

Fields may carry a `section` (`withSection`). Consecutive visible fields
sharing one render under a styled, rule-underlined header with a blank
line between groups, and the whole form indents its fields two cells —
the connection editor's grouped look. Forms without sections render as
the original flat list, so the other `formModal` users are untouched.

The field block scrolls: `view` clips it to the terminal height, keeps
the cursor's line inside the window (`formModal.offset`) and replaces
clipped edge rows with `⋮`. Every rendered line is also clamped to the
modal's width budget, so a long help text cannot push the box past a
narrow terminal. The mouse wheel walks the cursor (`scroll`), clamped
rather than wrapping.

## Dynamic visibility

Each field carries `visible func(*formModal) bool`. `visibleFields()` is
recomputed on every keystroke and render — the SSH section unfolds under its
toggle this way, and the auth select decides between key file and secret.
Since #144 the *engine* is no longer a select driving visibility: the
connection flow builds one form per engine (see
[design/connection-form-ux](connection-form-ux.md)), and a never-visible
carrier field (`newHiddenField`) holds the engine value so predicates and
readers keep asking `rawValue("engine")`.

Two consequences of visibility remain:

- The cursor indexes into the *visible* list, so `current()` clamps after a
  visibility change and `syncFocus()` re-focuses exactly one text input.
  `focusField(name)` parks the cursor on a named field — the file engines
  open on their path.
- `value(name)` returns `""` for a hidden field (it does not apply), while
  `rawValue(name)` ignores visibility — required for the hidden engine and
  for fields that keep state while folded away.

## Validation

Fields can carry a `validate` hook, run on every draw and again as a gate
before `onSubmit`: a failed submit keeps the popup open, jumps the cursor to
the first offender and marks every invalid field inline — see
[design/connection-form-ux](connection-form-ux.md) for the reticence rules.
`onSubmit` can still return `close=false` after setting `f.err` for checks
only the whole profile can answer. `config.Connection.Validate` re-checks
required fields on the way to disk, since profiles also arrive by
hand-editing `config.toml`.

## Password fields

`formField.value()` trims whitespace for text fields but **not** for
passwords: leading and trailing spaces can be significant in a secret. The
password field takes an initial value so a typed-but-unsaved secret survives
the connection flow's form rebuilds (the `connDraft` round trip); file-based
engines never build the field at all.

## Escape and modal handoff

`esc` runs `onCancel` before the root closes the modal. Because the root
only clears `m.modal` when the closing modal is still the open one, an
`onCancel` (or an `onKey` handler) that installs a *different* modal
performs a handoff instead of a close — this is how the create-mode
connection form retreats to the engine picker and how `ctrl+e` swaps form
for picker without ending the flow.

## Test from inside the form (`ctrl+t`)

The connection form can dial what it currently shows without saving:
`ctrl+t` runs `toConnection()` for validation only, builds a
`dialRequest` and fires the same `testConnCmd` the panel's `t` uses.
Two mechanisms carry it:

- `formModal.onKey` — an optional hook that sees every key the form's
  own contract (esc/tab/enter/↑↓) does not claim, checked before the
  key reaches the field under the cursor. It keeps `formModal` generic;
  only the connection form installs a handler (ctrl+t, ctrl+e).
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

- [design/connection-form-ux](connection-form-ux.md) — the engine-first
  two-step connection flow built on this modal.
- [design/tui-shell-architecture](tui-shell-architecture.md) — the modal
  interface and update routing this plugs into.
- [design/connection-secrets](connection-secrets.md) — where the typed
  password goes.
- [design/path-completion-in-forms](path-completion-in-forms.md) — filesystem
  completion on the `File` field, and why it takes `tab` away from field
  navigation while candidates are up.
