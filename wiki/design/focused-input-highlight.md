---
type: Design Decision
title: One shared focused-input highlight for every single-line textinput
description: Issue #182 — why a focused single-line textinput.Model gets a green-tinted background, a bold green prompt and a green cursor instead of a stacked combination of every candidate lever, why the style is built once (styles.inputStyles, internal/ui/styles.go) and applied per-render via SetStyles rather than threaded through every field constructor, the new palette.FocusInputBg theme slot, and why the quick filter's own `▌` marker bar (#180) was left as its own cue instead of being folded into this one.
tags: [tui, theme, forms, textinput, keybindings, accessibility]
generated:
  by: claude-code/sonnet-5
  at: 2026-08-20T00:00:00Z
sources:
  - resource: https://github.com/TrueDaerk/lazysql/issues/182
  - resource: https://github.com/TrueDaerk/lazysql/issues/180
---

# Focused-input highlight

## Decision

Every `textinput.Model` in the app — prompt modals, the connection form's
text/password fields, the row-insert form, the cell-edit modal — now
shares one `textinput.Styles` value, `styles.inputStyles`
(`internal/ui/styles.go`, built by `newInputStyles`). A focused field gets:

- a background tint (`palette.FocusInputBg`, config key `focus-input-bg`)
  across the whole field width, not just the cursor cell — `textinput`
  already fills unused width with the active style's background when
  `SetWidth` is set, so this reads as one highlighted rectangle even on
  an empty value or a short one padded out;
- a bold, green (`colorGreen`, the same color `BorderFocused` uses) prompt
  glyph;
- a green cursor (`Styles.Cursor.Color`).

Blurred fields fall back to a second `StyleState` with no background —
plain text, muted prompt — so only the field actually taking keystrokes
lights up; a form with ten fields does not turn into ten green boxes.

This is deliberately one lever short of "stack everything": text color
itself is left alone (`Text: lipgloss.NewStyle()`, no `Foreground`) so
whatever contrast the terminal's own foreground already has against the
tint is preserved, instead of picking a second color that might clash
with it in an arbitrary 16-color scheme.

## Why per-render `SetStyles`, not a constructor parameter

`newTextField`/`newPasswordField` (`internal/ui/form.go`) and
`newPromptModal` (`internal/ui/modal.go`) are called from a couple dozen
sites — several of them (`connections.go`, `ssh.go`, `params.go`)
building field lists before a `styles` value is in scope. Threading
`styles` through every constructor to set it once would touch all of
those call sites for a value that can already reached for free where the
field is drawn: `display()`/`view()` already take `s styles`. So each
render call does one `fl.input.SetStyles(s.inputStyles)` (or
`p.input.SetStyles(...)`) right before `.View()` — idempotent, and it
picks up a live `[theme]` reload the same way every other style already
does (see `applyPalette`/`configurable-keys-and-theme`).

## Why the quick filter line was left alone

The inline WHERE line (`internal/ui/filterinput.go`) does not call
`textinput.View()` — it draws its own SQL-highlighted clause and caret
(see `inline-where-filter`), so `styles.inputStyles` has nothing to hook
into there. It already carries its own focus cue from #180, the green
`▌` bar (`styles.filterFocus`) at the head of the line, which this issue
was scoped to roll out to the *other* inputs rather than fold into.
Redrawing the clause's own background would have meant reworking
`renderTokens`, shared with the query editor's syntax highlighting, for
a component that already reads clearly as focused.

## Rejected: theming the fill via `Text.Background` alone, no dedicated palette slot

Reusing `colorSelectionBg` (the grid's row-selection tint, itself gray)
would have kept `palette` a slot smaller, but it reads as "selected", not
"focused" — the acceptance bar was "green = focused, like a panel
border", and the existing gray tints already mean something else
on-screen. `FocusInputBg` gets its own preset value per theme (ANSI `22`
dark green for `default`, `#c3ecc9` for `light`) the same way
`SelectionBg`/`RowCursorBg`/`CellCursorBg` already do.
