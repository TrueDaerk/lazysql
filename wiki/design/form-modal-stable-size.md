---
type: Design Decision
title: The form modal's size is pinned per field set
description: A formModal computes its width and reserved rows once from the worst case (issue #159); errors, info lines and help text render into reserved space, so no keystroke inside one field set moves the box.
tags: [tui, modal, forms, layout]
generated:
  by: claude-code/opus-5
  at: 2026-08-16T00:00:00Z
---

# The form modal's size is pinned per field set

## Decision

`formModal.view` derives its geometry from a `formLayout` that is computed
once per *field set* and reused on every later draw:

- `contentW` — the cells every rendered line is clipped **and** padded to.
- `bodyRows` — the rows reserved for the optional body block.
- `fieldRows` — the rows reserved for the field block (the scroll window).

The cache is keyed by `layoutKey`: terminal size, title, footer and the
names/labels of the currently visible fields. Nothing a keystroke does
*inside* a field can change that key, so typing, a validation error
appearing, a test result landing or path candidates popping up all render
into the frame that is already there. A field set change — the SSH section
unfolding, `custom` selected on the colour tag, an engine swap — does
recompute it: that is a deliberate content change, and the box may grow.

Before #159 the box was as wide as its widest current line and as tall as
its current content, so the centred modal shifted left and right (and up
and down) while the user typed: an inline `✗ port out of range 1-65535`
widened a 120-column form from 72 to 96 cells, and the completion footer
did the same in every path field.

## The worst case

`computeLayout` measures what the form could ever need, not what it shows:

- the title, and the *widest* footer — the completion contract swaps in a
  longer one whenever candidates come up, which is one keystroke away in
  any path field, so all spellings count;
- per visible field, `indent + marker + labelW + gap + valueWidth + gap +
  formMsgWidth`. `valueWidth` is a worst case too: a select is as wide as
  its longest choice, so stepping through the choices cannot move the box;
- `formBodyWidth` (100) when the form has a body block. That block is
  free-form text tracking what is typed (the dump/restore command
  preview), so it cannot be measured once — pinning the box to its first
  draw would clip every later, longer command.

`formMsgWidth` (32) is the column reserved next to the cursor's field for
its help text or its inline validation message. Both are clipped to it:
the column is what a message costs, whatever it says. A help text is a
hint, and the message that actually blocks a submit is repeated in full in
the status line — which is why letting the longest help text (60 cells on
the SSH secret field) set the width was rejected: it pushed the form to
nearly the whole terminal.

The result is capped by `maxW-6` (border and padding), so on a narrow
terminal the box simply fills the width and lines are clipped as before.

## The reserved status line

`err` and `info` used to be written only when set, adding two rows each and
stealing them from the scroll window's budget. There is now exactly one
status row (plus its blank line), always: `err` if set, otherwise `info`,
otherwise blank. The chrome outside the field block is therefore constant —
two border rows, title + blank, the body block + blank, status + blank,
footer + blank — which is the `maxH - 8` the scroll budget starts from. The
tiny-terminal guard is unchanged: the budget floors at three rows and the
block clips with `⋮`.

Path candidates cost no rows at all — they float over the box as a
compositor layer, see
[design/path-completion-in-forms](path-completion-in-forms.md).

## Related

- [design/connection-form-modal](connection-form-modal.md) — the modal this
  layout belongs to.
- [design/path-completion-in-forms](path-completion-in-forms.md) — the
  floating candidate list that removed the largest variable block.
