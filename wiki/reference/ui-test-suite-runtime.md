---
type: Reference
title: Where internal/ui's test time goes
description: Issue #228's measurement of the internal/ui test suite — 341s of which almost all was wall-clock waiting on Bubbles' 530ms cursor-blink timer, run synchronously by the test helper `send`/`drain` — and the fix, which turned off the virtual cursor on the two inputs whose cursor lazysql draws itself.
tags: [testing, performance, bubbletea, bubbles, cursor]
generated:
  by: claude-code/opus-5.5
  at: 2026-09-25T08:30:00Z
sources:
  - resource: charm.land/bubbles/v2@v2.1.1 cursor/cursor.go (defaultBlinkSpeed, Model.Blink)
  - resource: charm.land/bubbles/v2@v2.1.1 textinput/textinput.go and textarea/textarea.go (SetVirtualCursor, updateVirtualCursorStyle)
  - resource: go test ./internal/ui -json, -blockprofile, -cpuprofile runs on 2026-09-25 (issue #228)
---

# Where internal/ui's test time goes

## The measurement

`go test ./internal/ui` took **341s** while every other package finished in
under 9s. The run used 4% CPU (13.6s user over 5:41 wall): the suite was
*waiting*, not computing, so the on-disk SQLite fixtures, `perf_test.go` and
DuckDB's cgo — the suspects the issue listed — were not it.

Per-test timings (`go test -json`) showed the cost concentrated in about 120
of 691 tests, and every one of them a multiple of ~0.53s: the filter-line
tests (`TestFilterInputBindsNullTestsAndAndedTerms` 24.5s,
`TestFilterInputBindsQuotedLiterals` 21.8s, the filter history and completion
tests at 5–14s each) and the query-editor tests (snippets, completion,
placeholders).

A `-blockprofile` of the slowest test attributes **100%** of its blocked time
to `charm.land/bubbles/v2/cursor.(*Model).Blink.func1`, called from the test
helper `drain`.

## The mechanism

- A focused Bubbles `textinput`/`textarea` in the default `CursorBlink` mode
  returns `cursor.Blink()` from `Update` on every keystroke that moves the
  caret. That command blocks on a `context.WithTimeout` of
  `defaultBlinkSpeed` = **530ms** and then yields a `BlinkMsg`.
- In the real program Bubble Tea runs commands on goroutines, so the timer
  costs nothing. The tests' `send` helper (`internal/ui/model_test.go`) instead
  runs every command synchronously to a standstill — `drain` calls `cmd()` —
  so each typed character stood still for 530ms. `typeKeys(t, m, "id IS NULL
  AND name = 'x'")` is ~25 keystrokes, ~13s.

## The fix

Two of the inputs never let Bubbles draw their cursor at all:

- `newFilterInput` (`internal/ui/filterinput.go`) — the inline `WHERE` line
  draws its own caret and scroll window (see
  [design/inline-where-filter](../design/inline-where-filter.md)).
- `newQueryEditor` (`internal/ui/query.go`) — `highlight.go` draws the buffer,
  gutter and caret; `textarea.View()` is never called.

For both, the blink timer was pure waste *in production too*: one real timer
armed per keystroke for a cursor cell nobody renders. Both now call
`SetVirtualCursor(false)`, which puts the Bubbles cursor in `CursorHide` mode so
`Blink()` returns nil — the same thing `newPanelFilterInput`
(`internal/ui/panel.go`) already did for the side-panel filter. The caret
position, key handling and rendering are unchanged.

Result on the same machine: `go test ./internal/ui` **341s → 48s** (7×);
`go test ./...` ≈ 350s → 54s. No test was changed, deleted or skipped; the
statement coverage of `internal/ui` stayed at 84.17% (7315/8691 before,
7317/8693 after — the two added statements are covered).

## What still waits, and why it stays

The remaining ~48s is still real timers run synchronously, but they belong to
behavior that is actually visible:

- **Modal text inputs** (prompt, edit-cell, connection form, insert-row) are
  rendered with `textinput.View()`, so their cursor really blinks. Tests that
  type into them (`TestDuplicateNamePromptsBeforeOverwrite`,
  `TestOverwriteCancelKeepsTheOldStatement`, the snippet-save tests) still pay
  530ms per keystroke. Making the interval a test seam is not safe: a blink
  message that is accepted chains another `Blink()`, and with a near-zero
  interval `send`'s standstill loop would spin until its 10 000-round guard.
- **The input coalescer's 16ms flush tick**
  ([design/input-coalescing](../design/input-coalescing.md)): every `j`/`k` in
  the grid or a side panel arms it, so a test that walks 99 rows at four
  terminal sizes (`TestClickKeepsTheHighlightOnTheClickedLine`, ~9s) pays
  ~18ms per step.

## Rule of thumb

When a test in `internal/ui` is slow, block-profile it first
(`go test -run X -blockprofile=b.out -o ui.test` then
`go tool pprof -top -cum ui.test b.out`): time spent in `drain` under a
`tea.Tick` or `cursor.Blink` closure is a timer, not work. And an input whose
view lazysql draws itself should always get `SetVirtualCursor(false)`.
