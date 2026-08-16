---
type: Design Decision
title: Duplicate connection (y)
description: "`y` on a connection opens the existing form pre-filled under a unique name, saves as a plain new entry, and copies (not moves) the source's keyring secret."
tags: [connections, keyring, forms, keybindings]
generated:
  by: claude-code/sonnet-5
  at: 2026-08-16T00:00:00Z
---

# Duplicate connection (`y`)

## Decision

`y` on the `[1] Connections` panel (`keyMap.DuplicateConnection`,
`actDuplicateConnection`) duplicates the selected profile: it opens
[the connection form](connection-form-modal.md) pre-filled with every field
of the source connection, under the first unused name in the sequence
`<name> - Copy`, `<name> - Copy 2`, `<name> - Copy 3`, … (`duplicateName`,
`internal/ui/connections.go`).

The form is otherwise the ordinary create form: `oldName` is passed as `""`
to `newConnectionFormDraft`, so `enter` calls `Config.Upsert("", conn)` —
appending a new profile rather than replacing the source — and the source is
never touched. What is not ordinary is `esc`: a from-scratch `n` new
connection takes a two-step detour on cancel (form → engine picker → gone,
since the engine has not been chosen yet), but a duplicate already knows its
engine, so `newDuplicateConnectionForm` clears `onCancel` and restores the
plain "esc cancel" footer — `esc` discards the popup outright, like editing
does.

The password field is never pre-filled — `draftFrom` never reads a password
out of `config.Connection` (it isn't one of that struct's fields; passwords
live only in the keyring, see [connection-secrets](connection-secrets.md)) —
so the source's secret is never rendered or logged. Instead, on save, if the
password/SSH fields were left untouched, `duplicatePersistCmd` calls the new
`secrets.Copy(sourceName, newName)` to duplicate both the database password
and the SSH secret into the new keyring entries. `secrets.Copy` is `Rename`
without the trailing `Delete` — same two-slot (`name`, `name#ssh`) shape,
same "missing source is a no-op" behavior — so the source's own secret
survives untouched. A password typed directly into the duplicate form still
wins: `duplicatePersistCmd` runs the copy first, then applies `sec.password`
the same way `persistCmd` does, so an explicit value overwrites the copy.

The draft threading needed one new field, `connDraft.dupSource`: the source
connection's name, carried alongside the other draft state so `ctrl+e`
(change engine) and `ctrl+t` (test without saving) keep working mid-duplicate
without a second code path. `ctrl+t`'s untouched-secret fallback already read
`oldName`'s keyring entry when the user was editing; it now falls back to
`dupSource` first when `oldName` is empty, so testing a duplicate before
saving it can still dial with the copied password.

## Why

- Reusing `newConnectionFormDraft` wholesale (rather than a parallel form
  builder) keeps every field, validator, and dialect-specific section in one
  place — the same reason [connection-form-modal](connection-form-modal.md)
  exists as one reusable modal in the first place.
- Treating the save as a genuine "new connection" submit (`Upsert("", …)`)
  rather than special-casing an "insert copy" path in `Config` means the
  original profile's slice position, keyring rename logic, and validation
  are all completely unaffected by duplicating something derived from it.
- `secrets.Copy` instead of reusing `secrets.Rename` with a manual re-`Set`
  on the source: a copy-then-restore would leave a window where the source's
  password is briefly gone if the process died between the rename and the
  restore, and would race a concurrent read of the source's own secret.

## Consequences

- A duplicate of a connection with `ask_password = true` (no keyring entry
  at all) duplicates cleanly with no keyring write — `secrets.Copy` treats a
  missing source as a no-op, same as `Rename`.
- Deleting or renaming either the source or the duplicate afterwards affects
  only that profile's own keyring entry; they are independent copies, not
  linked.

## Related

- [design/connection-secrets](connection-secrets.md) — the keyring storage
  model `secrets.Copy` extends.
- [design/connection-form-modal](connection-form-modal.md) — the form this
  reuses.
- [design/connection-form-ux](connection-form-ux.md) — the two-step
  create flow whose engine-picker cancel detour duplicate deliberately
  skips.
