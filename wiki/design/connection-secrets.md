---
type: Design Decision
title: Connection storage and password handling
description: Connections live in a plain TOML config; passwords live only in the OS keyring or a per-connect prompt.
tags: [config, security, keyring, connections]
generated:
  by: claude-code/opus-5
  at: 2026-08-09T00:00:00Z
sources:
  - resource: https://github.com/zalando/go-keyring
    note: keyring backend used for macOS Keychain, Windows credential manager and Secret Service.
---

# Connection storage and password handling

## Decision

Connection profiles are stored in
`${XDG_CONFIG_HOME:-~/.config}/lazysql/config.toml` as an array of tables
(`[[connections]]`), decoded by `internal/config`. Passwords are **never**
in that file. Each password is a separate OS keyring entry under service
`lazysql` with the connection *name* as the keyring user
(`internal/secrets`).

A profile can opt out of the keyring entirely by setting
`ask_password = true`; lazysql then opens a masked prompt on every connect
and the password never leaves memory.

## Why

- A config file gets copied into dotfile repos, backups and pastebins. A
  keyring entry does not.
- Keying by connection name (not by host/user) keeps the mapping obvious in
  the keyring UI and makes rename/delete a single operation.
- The keyring is not available everywhere (headless Linux without a Secret
  Service, CI). `secrets` maps that case to `ErrUnsupported`, and the UI
  treats it like "no password stored" rather than a hard failure, so
  passwordless and ask-on-connect profiles still work.

## Consequences

- Deleting a connection deletes its keyring entry in the same flow
  (`forgetCmd`); renaming moves it (`secrets.Rename`). Otherwise orphan
  secrets accumulate under names nothing references.
- A blank password field in the edit form means "leave the stored secret
  alone", not "clear it". Clearing requires typing something and saving,
  which is why `toConnection` reports `setPassword` separately from the
  value.
- Writing the config is atomic (temp file + `rename`) with mode `0600` and a
  `0700` directory: a crash cannot truncate a working config, and other
  users on the machine cannot read hostnames and usernames.
- `db.RedactDSN` exists because the command log prints the DSN that was
  dialled. The mask is the URL-safe literal `REDACTED`, since percent
  encoding would otherwise turn `***` into `%2A%2A%2A` and hide the fact
  that redaction happened.

## Related

- [design/db-driver-abstraction](db-driver-abstraction.md) — where `ConnParams`
  becomes an engine-specific DSN.
- [design/connection-form-modal](connection-form-modal.md) — the UI that edits
  these profiles.
