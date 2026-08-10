---
type: Design Decision
title: Persist screen mode across restarts via a config-vs-state split
description: screenMode (the +/_ normal/half/full cycle) survives a restart via a new state.toml next to config.toml, holding disposable UI state that config.go never touches during a hand-edit round trip.
tags: [ui, config, persistence]
generated:
  by: claude-code/sonnet-5
  at: 2026-08-10T00:00:00Z
---

# Persist screen mode across restarts via a config-vs-state split

`screenMode` (`internal/ui/model.go`'s `+`/`_` cycle between `normal`,
`half`, `full`) used to reset to `normal` on every start. It now survives a
restart via `internal/config.State`, saved to `state.toml` next to
`config.toml` in the same `AppDir`.

## Why a new file instead of `Config` or `internal/session`

`Config` is hand-edited profile data (connections, `[keys]`, `[theme]`):
every `Save()` round-trips through `forEncoding()`/`configFile` to preserve
exactly the fields a user might have written by hand, and the file carries
owner-only permissions because it can reference the keyring. Screen mode is
neither: it is written only by the app, never edited by a human, and losing
it is a non-event. Writing it into `Config` would mean every screen-mode
toggle rewrites a file the user might be mid-edit on.

`internal/session` already persists disposable-but-important state
(`Connection`/`Database`/`Table`/`Tab`/`Row`/`Col`, see
[design/session-restore](session-restore.md)) but under
`XDG_STATE_HOME`, as JSON, and only on quit when `restore_session` is
enabled — it exists to answer "where was I", not "how did the UI look".
Screen mode is UI chrome, not browsing position, and lazygit's own
`config.yml`/`state.yml` split separates exactly this way: state is
whatever isn't safe or sensible to hand-edit. `internal/config.State` keeps
that a config-directory concept (`AppDir`, same TOML format as `Config`)
rather than folding it into `internal/session`'s XDG-state/JSON file, so a
reader looking at `internal/config` sees the full "everything this app
persists about itself" picture, minus browsing position.

## Shape

- `State{ScreenMode string}` in `internal/config`, TOML-encoded, at
  `StatePath()` (`AppDir/state.toml`).
- `ScreenMode` is stored by *name* (`screenModeNames[m.screen]`), not the
  `screenMode` int — reordering the enum in `internal/ui` can never corrupt
  a saved value.
- `LoadState`/`LoadStateFrom` never return a load/parse error to the
  caller: a missing, unreadable or corrupt file yields a zero `State`,
  which `internal/ui`'s `screenModeFromName` maps to `screenNormal`. State
  is disposable — a bad file must never block startup, only degrade to the
  default.
- `Config.SaveTo` and `State.SaveTo` share `writeAtomicFile` (temp file +
  rename in the target's own directory), the same posture `Config` already
  had; `State` gets no owner-only guarantee beyond that, since it carries
  no secret.
- Saved once, on quit (`k.Quit` in `Model.Update`), not on every `+`/`_`
  toggle — mirrors `saveSession`'s placement and keeps a toggle a pure
  in-memory action.
- Kept as an open struct (see the type doc) for more disposable UI state
  later — last focused panel, last connection shown before a session
  restore — without a new file per field.
