---
type: Design Decision
title: Connection form UX redesign
description: The issue #129 UX review of the connection form and the decisions it led to — engine-first ordering, dial-order server block, engine-tracking placeholders, per-field inline validation gating submit, and the options bar taking over the form contract.
tags: [tui, forms, connections, ux, validation]
generated:
  by: claude-code/fable-5
  at: 2026-08-12T00:00:00Z
sources:
  - resource: https://github.com/TrueDaerk/lazysql/issues/129
---

# Connection form UX redesign

The structured review behind issue #129, and what each finding changed in
[design/connection-form-modal](connection-form-modal.md)'s `formModal` /
`newConnectionForm`.

## Review findings → decisions

**Engine was the second field, but decides what the form is.** Every
visibility predicate hangs off the engine select, so choosing it after
typing a name meant the form reshaped *under* text already entered.
Engine now leads; the form only ever reshapes before anything below it
is filled. Consequence: the cursor opens on a select, so a paste lands
nowhere until the user moves down one field — accepted, because "pick
the engine, then describe the server" is the flow the form wants.

**Credentials were split by the Database field.** The server block ran
host → port → user → database → password. It now runs in dial order —
host → port → user → password → ask-on-connect → database — so the
credential pair is adjacent and Database (optional) sits last.

**The port default was invisible.** The placeholder said `default`
without saying which. It now shows the actual number
(`db.DefaultPort`: 3306/5432) and *stays a placeholder* rather than
pre-filling the field: a filled field would persist `port = 3306` into
`config.toml`, freezing the default a profile never chose. The engine
select's new `onChange` hook retargets the port and user placeholders
(`e.g. root` / `e.g. postgres`) whenever the choice moves.

**Validation fired only on submit, and only at the bottom.** A bad port
was reported after `enter`, in an error line far from the field. Fields
now carry `validate func(*formModal, string) string`; a failed submit
jumps the cursor to the first offender and every invalid field is marked
`✗` in place (the one under the cursor carries the message, displacing
the help text — the field needs fixing before it needs explaining).
Two-stage reticence keeps a fresh form calm: format errors (port not a
number, bad color, malformed options) show while typing, but
required-field errors on *empty* fields only appear after the first
submit attempt (`formModal.submitted`). Validators return
self-describing messages ("host is required"), so the same string works
inline and in the bottom error line without a label prefix.
`config.Connection.Validate` still re-checks on the way to disk —
profiles also arrive by hand-editing `config.toml`.

**Form keys were invisible outside the modal's own footer.** The bottom
options bar kept showing panel verbs the open modal swallows. The bar
now switches to the form contract while any `formModal` is open
(`keyMap.formKeys`), and a form can supply its own set
(`formModal.bar`) — the connection form adds `ctrl+t`. `?` on the
Connections panel documents the same `connFormKeys()` slice, keeping the
single-source rule of
[design/keybindings-single-source](keybindings-single-source.md).
`FormChange`/`FormTest`/`FormSave` are display-only bindings without
`[keys]` slots: `formModal.update` dispatches those keys itself, so an
override could change what the bar *claims* but not what the form
answers to.

## Rejected: section headers

Grouping headers ("Server", "Credentials", "SSH") were considered and
dropped: the form already runs to ~14 visible rows for a server engine
with SSH on, and the modal must fit the 24-row tiny-terminal guard.
Ordering plus engine-driven visibility carry the grouping; the SSH
toggle already reads as its section's header.
