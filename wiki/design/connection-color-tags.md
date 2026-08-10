---
type: Design Decision
title: Per-connection color tags for environment safety
description: An optional color tag rides the main view's top border and a panel marker rather than replacing the focus color, so the two stay distinguishable.
tags: [ui, config, safety, theme]
generated:
  by: claude-code/sonnet-5
  at: 2026-08-10T00:00:00Z
---

# Per-connection color tags for environment safety

`color = "red"` on a `[[connections]]` profile (`internal/config.Connection.Color`)
tags it with an environment color: a named ANSI color, a 256-color index,
or a hex value — the same three formats `[theme]` accepts, resolved by
the same `resolveColor` (`internal/ui/theme.go`). An absent key means no
tag, so configs written before the field existed load unchanged.

## Why the tag rides the top border, not the whole one

The focused-panel border is green (`colorGreen`/`BorderFocused`) and that
signal has to stay unambiguous: a green box is "this is where your keys
go." Painting a tagged connection's whole border in its tag color would
compete with that, and a connection is very often the *unfocused* main
view (browsing a table, say) while its tag still matters.

lipgloss v2 styles a border per side (`BorderTopForeground`,
`BorderRightForeground`, …), so `renderMainColumn` (`internal/ui/view.go`)
keeps the ordinary focused/blurred style for every side and only
overrides the top segment's foreground with the active connection's tag
color, via `Model.activeTagColor`. The left/right/bottom sides — and
every other panel's border — are completely untouched by a color tag.
Green-for-focus and the tag are visually orthogonal: either, both or
neither can be on screen at once without one reading as the other.

## Where else the tag shows

- **Panel `[1]`**: every connection with a valid tag gets a `●`
  (`tagMarker`) in its color ahead of its name, regardless of whether
  that connection is the live one — same idea as the 🔒 read-only marker,
  but rendered separately (`sidePanel.tagColor`, `panel.go`) rather than
  folded into `decor`, because the marker's color must survive the row's
  status/selection styling instead of being overridden by it.
- **Main view**: `Model.tagMarkerFor` prepends the same `●` to the
  connection name wherever it appears in the main view's own text — the
  generic "connection: …" line and the `[1]` detail's "name" line — so
  the marker is visible even when the top-border tint alone might be
  missed at a glance.
- **Destructive confirm modals**: the changeset commit modal
  (`edit.go:openCommitModal`) and the unguarded DELETE/UPDATE warning
  (`query.go:vetQuery`, the #31 guard) both name the connection through
  `Model.taggedConnName`, which bolds and colors it when tagged. This is
  the moment the tag earns its keep: the connection name sits right next
  to "this runs immediately" or "commit N staged changes," in a color
  that (by construction, since the operator picked it) reads as
  "production" or whatever it was made to mean.

## Invalid colors never fail config load

Unlike `[keys]`/`[theme]` — which fail `ui.New()` outright on an unknown
name, because they change what a keypress *does* — an invalid `color` on
one connection only affects that connection's own decoration. `New()`
runs `validateConnectionColors` over every profile and stores the
resulting warnings on the model; `Init()` logs one command-log line per
offending connection, naming it, and the app starts normally. Every
render-time call site (`connTagColor`) treats an invalid or empty value
identically — "no tag" — so there is exactly one place that decides
validity and every consumer just asks it.

## The form's picker vs. the raw config value

The connection form's "Color tag" field is a `fieldSelect` over
`none`/the six named colors from the issue (`red`, `yellow`, `green`,
`blue`, `magenta`, `cyan`)/`custom…`, backed by a second text field
(`color_hex`) that only shows for `custom` and carries *any* string
`resolveColor` accepts — another ANSI name, a 256-color index, or hex —
not just hex despite the field's placeholder. Editing a profile whose
stored `color` isn't one of the six named choices (a hex value, or a
config hand-edited to something like `"9"`) opens the form with `custom…`
preselected and that raw value in the text field, so nothing entered
outside the picker is silently discarded on the next save. A `custom`
value that fails `resolveColor` blocks the form submit with an inline
error — unlike a bad value arriving from outside the form (a hand-edited
`config.toml`), which degrades gracefully instead, per above.
