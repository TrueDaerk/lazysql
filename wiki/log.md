# Update log

Chronological history of wiki changes, newest last.

## 2026-08-09

- Bundle created alongside the initial TUI shell (issue #1).
- Added [design/tui-shell-architecture](design/tui-shell-architecture.md): root
  model ownership, why side panels are cursor-over-slice rather than Bubbles
  `list`, the fixed update routing order, and the `m.modal == cur` rule that
  lets one modal replace another.
- Added [design/keybindings-single-source](design/keybindings-single-source.md):
  the `actionID` + `key.Binding` table behind key dispatch, the options bar, the
  `a` actions menu and the `?` help, plus the tests that enforce it.
- Added [reference/lipgloss-v2-sizing](reference/lipgloss-v2-sizing.md):
  discovered while debugging a side column that ended six rows short of the main
  column — lipgloss v2 `Style.Width`/`Height` are total block dimensions,
  contrary to v1 guidance.
