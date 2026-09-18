---
type: Design Decision
title: Implicit autocomplete triggers on text change, not caret movement
description: Issue #202 — updateEditor compares the buffer's text before and after the textarea's own Update instead of refreshing the popup unconditionally, so cursor navigation into an existing word never opens it; an open popup closes when a caret-only key moves the caret away from the word it was built over.
tags: [tui, query, editor, completion, autocomplete, keybindings]
generated:
  by: claude-code/sonnet-5
  at: 2026-09-18T00:00:00Z
sources:
  - resource: https://github.com/TrueDaerk/lazysql/issues/202
    title: "Issue #202 — Query editor: do not open autocomplete on caret nav through existing SQL"
---

# Implicit autocomplete triggers on text change, not caret movement

## Problem

[schema-aware-autocomplete](schema-aware-autocomplete.md) re-derived the
popup unconditionally after every key `updateEditor` forwarded to the
textarea. That includes pure caret motion: arrows, home/end, word jumps,
paging. Landing the caret inside an already-written word with at least
`minCompletionPrefix` characters before it opened the popup, and once
open it claimed `up`/`down`/`tab`/`enter`/`esc` — the very keys plain
navigation needs next. Moving around finished SQL meant fighting the
popup at every word boundary.

## Decision

`updateEditor`'s fallthrough now reads `m.script()` before and after
`m.editor.area.Update(msg)` and compares the two:

- **Text changed** (typing, backspace/delete, paste) → `refreshCompletion(false)`
  runs exactly as before: narrow, open or close the popup from the new
  word under the caret.
- **Text unchanged** (any caret-only key not claimed earlier in
  `updateEditor`) → the popup is left alone if already closed, and
  **closed** if it was open. An open popup is anchored on a word; a key
  the popup does not own that still moved the caret has, by definition,
  left that word, so keeping the popup up would show suggestions for
  text the caret is no longer in.

Explicit completion (`ctrl+space`, and `tab` on a word) is unaffected —
both go through the `k.Complete` branch earlier in the switch, which
returns before reaching this comparison, so they still open the popup
regardless of how the caret got there.

This makes the buffer-text diff the single discriminator between "the
user is typing" and "the user is moving around" for every key that falls
through to the textarea. It does not touch normal mode's vim motions
([vim-mode-query-editor](vim-mode-query-editor.md)) — normal mode has no
completion site, so the popup is never at stake there — and it does not
touch the paste path (`internal/ui/paste.go`), which already always
refreshes unconditionally because a paste always changes the text.

## Rejected alternative

Special-casing every caret-motion key binding (arrows, home/end,
ctrl+a/e, alt+left/right, ctrl+left/right, pgup/pgdown) to skip the
refresh was rejected: the list is long, textarea's own keymap can grow it
further, and missing one silently regresses the issue. A text-content
diff is exhaustive by construction — it does not need to know which keys
exist, only whether the buffer moved.
