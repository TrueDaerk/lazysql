---
type: Design Decision
title: Configurable keybindings and theme fail startup, not silently
description: Why `[keys]`/`[theme]` overrides in config.toml are validated once in ui.New() and turned into a hard startup error, unlike a broken connections list — plus the palette/action-name registries that make "list every valid name" possible without hand-written tables.
tags: [config, keybindings, theme, startup]
generated:
  by: claude-code/sonnet-5
  at: 2026-08-09T00:00:00Z
---

# Configurable keybindings and theme fail startup, not silently

Issue #13. `config.toml` already tolerates a broken `connections` list —
`config.Load` returns the error, `ui.New` swallows it into the command log,
and the app starts with an empty connection list (see
[connection-secrets](connection-secrets.md)). `[keys]`/`[theme]` do not get
that treatment: they change what the app *does* when a key is pressed or
what a color *means*, so a typo there should stop the program with a clear
message, not run with half a keymap.

## Where validation lives, and why not `internal/config`

`internal/config` owns the TOML shape (`Config.Keys`, `Config.Theme`, both
`map[string]string`) but not validation — the set of valid action names is
the field list of `internal/ui`'s `keyMap`, and the set of valid color names
is `internal/ui`'s `palette`. Making `internal/config` validate would mean
either importing `internal/ui` (backwards — UI already depends on config) or
duplicating the name lists and letting them drift. So `config.Load` just
parses; `ui.New` calls `applyKeyOverrides`/`resolvePalette`, and returns the
error instead of starting the model:

```go
km := newKeyMap()
if err := applyKeyOverrides(&km, cfg.Keys); err != nil {
    return Model{}, fmt.Errorf("config: %w", err)
}
pal, err := resolvePalette(cfg.Theme)
...
```

`New()`'s signature changed from `Model` to `(Model, error)` for this —
`main.go` prints the error to stderr and exits 1 before a `tea.Program`
ever starts, so there is no half-drawn TUI and no panic path.

## Single-source name registries, not reflection

Both `keyMap.slots()` (in `keys.go`) and `palette.slots()` (in `theme.go`)
return `[]struct{ name string; ptr *T }` — a hand-written pairing of a
kebab-case config name to the field it controls. `applyKeyOverrides` and
`resolvePalette` build a `map[string]*T` from that slice, so an unknown name
in the config produces an error listing every valid one
(`strings.Join(actionNames(), ", ")` / `paletteNames()`), sourced from the
same slice — there is no second hand-maintained list to drift out of sync.

Reflection over the struct fields would auto-derive the same kebab-case
names and remove the hand-written pairing, but `key.Binding` and
`color.Color` fields both need their *address* to mutate in place, and a
few field names (e.g. `NextMainTab` → `next-main-tab`) are not a mechanical
`unicode.ToLower` away from their config name without a small conversion
table anyway. The explicit slice keeps the mapping grep-able.

## Keys: override in place, keep the existing help description

`applyKeyOverrides` looks up each `[keys]` entry, splits the value on `,`
into a key list (`down = "j, down, ctrl+n"`), and calls
`binding.SetKeys(keys...)` / `binding.SetHelp(strings.Join(keys, "/"),
binding.Help().Desc)` — the description is never user-supplied, only the
keys and the help's key-hint change. Because it mutates the same `keyMap`
struct instance the whole shell already reads from (see
[keybindings-single-source](keybindings-single-source.md)), the options bar,
the actions menu and `?` all reflect an override with no extra plumbing —
`TestKeyOverridePropagatesToOptionsBarAndHelp` checks this directly rather
than trusting it by construction.

## Theme: a preset base plus named overrides, applied through package vars

`[theme]` first resolves a preset (`theme = "light"`, default `"default"`)
from `presets: map[string]palette`, then applies every other key as a color
override on top of it. `default` reuses the low ANSI indexes the shell
always used (`"2"`, `"8"`, `"236"`, …) so it still respects the user's
terminal scheme; `light` hardcodes hex/256 values tuned for a white
background, since those low indexes are whatever the terminal defines them
as and cannot be trusted to stay readable there.

The resolved `palette` is applied via `applyPalette`, which assigns into the
same package-level `color.Color` vars (`colorGreen`, `colorMuted`,
`colorDeleted`, `colorError`, …) that `newStyles()` and a few direct call
sites (the data grid's deleted-row strikethrough, the danger-confirm modal
border) already read — the vars existed before this issue for exactly this
reason (see `styles.go`'s original comment about ANSI colors respecting the
user's terminal theme). `deleted` and `error` are separate palette slots
even though they shared one `colorRed` before: a dropped/staged-for-delete
row and an actual error message are different signals, and now they can be
retinted independently.

`resolveColor` accepts an ANSI name (`"green"`, `"bright-red"`, `"gray"` as
an alias for `bright-black`), a bare 256-color index (`"237"`), or
`#rgb`/`#rrggbb` hex — `lipgloss.Color(s)` itself only parses the latter two,
so the ANSI name table is this package's addition.

## Round-tripping through Save

`config.Config.Clone()` previously only deep-copied `Connections`; `Keys`
and `Theme` are maps, so a shallow copy shared them with the original. Every
connection add/edit calls `Save()` on a clone (see `connections.go`), which
re-encodes the *whole* file — a shared-then-mutated map wouldn't have caused
visible corruption itself, but the real bug it masked is that `configFile`
(the TOML-encoding shadow struct) didn't carry `Keys`/`Theme` at all, so
saving a connection would silently drop a hand-edited `[keys]`/`[theme]`
section from disk. Both are fixed together:
`TestSavingConnectionsPreservesKeysAndTheme` covers the round trip.

See also [keybindings-single-source](keybindings-single-source.md) and
[connection-secrets](connection-secrets.md).
