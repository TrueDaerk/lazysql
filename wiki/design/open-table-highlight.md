---
type: Design Decision
title: Marking the currently open relation in the [2] Objects tree
description: issue #204 — a persistent, cursor- and focus-independent "open" tint on the tree row of the relation the main view is showing, matched by connection+database+name against m.data rather than by treeNode identity, riding the data grid's row-cursor tint (styles.openRow) so it stays clearly weaker than the cursor's selection style.
tags: [tui, panels, tree, styling, theme]
generated:
  by: claude-code/sonnet-5
  at: 2026-09-21T00:00:00Z
---

# Marking the currently open relation in the `[2]` Objects tree

## Problem

The tree's only highlight was the cursor row, live only while `[2]` was
focused. Once focus moved to the main view — the common case, since
opening a table hands focus there — or the cursor wandered elsewhere in
the tree, nothing in `[2]` said which row the grid on the right belonged
to.

## Decision

`panel.render` (`internal/ui/panel.go`) now takes an `isOpen func(*treeNode)
bool` predicate alongside the existing `focused`/`cursor` state. When a
tree row's node satisfies it and the row is not also under the cursor, it
renders with a new `styles.openRow` background instead of the plain
style. The cursor's `s.selected` still wins outright when both coincide —
`open` is checked after `selected` in the row's style `switch`, so a row
that is both open and under the cursor reads exactly as it did before
this change.

`styles.openRow` is `Background(colorRowCursorBg)` — the data grid's row
tint, already themed as "highlighted but weaker than the primary
selection" (see the grid's own `rowCursor`/`cellCursor` split). Reusing it
rather than adding a new palette slot keeps the "weaker than the cursor"
rule enforced by construction: as long as a theme keeps its row-cursor
color visibly weaker than its selection color, the tree's open marking
and its cursor marking can never compete for attention. No new
`[theme]` key was needed.

## What counts as "open"

`Model.isOpenNode` (`internal/ui/objtree.go`) answers the predicate. It
matches a `nodeObject` node whose `cat` is relational (a table or a view,
never a trigger) against `m.data.database`/`m.data.table`, gated on
`m.data.conn == m.active`. It deliberately does **not** compare `*treeNode`
pointers: the tree is rebuilt — its nodes replaced — on every relation
listing reply (`applyRelations`), so a pointer captured at open time would
go stale across a `R` reload of the category, silently dropping the mark.
Matching by value instead means the mark is automatically:

- **Reattached** after a category reload, once the listing lands again.
- **Moved** when a different table opens, since every relation-opening path
  (`openTable`, `walkRelation`, `jumpTo`) overwrites `m.data` wholesale.
- **Cleared** on disconnect or a database switch, since both paths reset
  `m.data = dataView{}` (`model.go`), which zeroes `m.data.table` and trips
  the guard.
- **Cleared** for a plain query result or error notice, since
  `showQueryResult`/`showQueryError`/`showQueryNotice` all set
  `m.data.table = ""` — a query result is not a relation and never wants a
  tree row marked as "open" under it.

Comparing against `m.data.database` (not `m.database`, the panel's
*browsed* namespace) is what lets the mark follow a relation opened by a
foreign-key jump into a database other than the one `[2]` is currently
showing — `walkRelation`/`jumpTo` only re-point `[2]`'s selection when the
target's database matches the browsed one, but the open mark tracks the
main view regardless.

## Rejected alternatives

- **A pointer on `objectTree`/`sidePanel` set at open time.** Simpler at
  first glance, but wrong the moment a category reloads and replaces its
  children — the exact case `R` and a stats/relations refresh already
  produce routinely. Recomputing the match at render time from `m.data`
  costs one string comparison per visible tree row and needs no
  invalidation logic anywhere.
- **A distinct foreground/bold instead of a background tint.** Would have
  needed to compose with the marker's own foreground override (color tag)
  and the row's status color (`statusColor`), both of which already ride
  on top of the row style; a background tint composes with both for free
  since neither touches `Background`.
