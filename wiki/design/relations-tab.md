---
type: Design Decision
title: Relations tab — per-table foreign-key hub
description: Why the Relations tab is a per-table hub instead of a full ERD, how the incoming half reuses the namespace foreign-key scan and its cache, and why `enter` walks without a filter.
tags: [ui, main-view, tabs, foreign-keys, navigation, introspection]
generated:
  by: claude-code/opus-5
  at: 2026-08-09T00:00:00Z
---

# Relations tab — per-table foreign-key hub

`Relations` is the fifth main-view tab
([design/main-view-tabs](main-view-tabs.md)). It shows how the open
relation connects to the rest of its namespace: the constraints it
declares above it, the constraints that point at it below it, and the
relation itself in a box between them.

```
Outgoing — orders references
  └─ customer_id → customers.id  fk_0
                     ▲
               ┌──────────┐
               │  orders  │
               └──────────┘
                     ▼
Incoming — referenced by
  ├─ order_items.order_id → id  fk_0
  └─ shipments.order_id → id  fk_0
```

Both halves are read outwards from the open relation, in
`column → table.column` notation: an outgoing edge starts at the local
columns, an incoming one at the referencing table's. A composite key
renders as `table.(a, b)` so it reads as one target instead of a list of
bare names.

## A hub, not an ERD

The tab is deliberately **per-table**. A full-schema ASCII graph needs
layout (crossing minimization, edge routing) that no terminal-width box
model makes readable at 30 tables, and the question a database TUI is
actually asked — "what does this table hang off, and what hangs off
it?" — is answered by the hub view plus `enter` to walk. Users expecting
an ERD will not find one here.

## Two sources, one tab

| Half | Source | Cost |
| --- | --- | --- |
| Outgoing | `metaView.fks`, from the metadata fetch the introspection tabs share | already paid |
| Incoming | the namespace scan behind `G` (`ensureNamespaceFKs`) | one `TableForeignKeys` round trip per table |

There is no engine-independent way to ask "who references me", so the
incoming half is the same scan the reverse-direction jump uses
([design/foreign-key-navigation](foreign-key-navigation.md)) and shares
its cache: `refsCache`, keyed by connection + namespace. Opening the tab
after pressing `G`, or vice versa, costs nothing the second time.

The scan is started from `ensureMeta` rather than from each call site.
Every path that opens a relation — picking one in `[3]`, a `g`/`G` jump,
`ctrl+o` back, a Relations walk — already calls it, so putting the
condition there is what keeps the two fetches in step without five
copies of the same batching. `ensureMeta` therefore no longer returns
early on already-loaded metadata; it returns whichever of the two
commands is still needed.

While the scan runs the incoming half says `scanning the foreign keys of
…` instead of `none`. Nothing blocks: the scan is a `tea.Cmd` like every
other database call, and the outgoing half renders as soon as the
metadata lands.

`R` on the tab drops the namespace cache entry as well as the metadata,
because "read it again from the server" has to mean both halves.

## `enter` walks, and does not filter

`g`/`G` are about **one row**: they build a parameterized `WHERE` from
the cell values under the cursor. The Relations tab is about the
**schema**, so `enter` opens the related table unfiltered, with the
Relations tab still selected — which makes repeated `enter` a walk over
the foreign-key graph.

The walk pushes onto the same `browseStack` the row-level jumps use, so
`esc` (and `ctrl+o`) unwind a schema walk and a row jump the same way,
in the order they happened. `j`/`k` move one cursor over both halves:
`metaView.row[mainTabRelations]` indexes the flat outgoing-then-incoming
list, which is why the renderer tracks the screen line the cursor landed
on and windows around it rather than scrolling by offset the way
`Indexes` and `DDL` do.

## Degrading on a narrow terminal

Below `relationsWideWidth` (56 columns of main view) the hub box and its
connectors are dropped: the heading, a bare relation name and a `- `
bulleted list remain. A box whose sides do not line up reads worse than
no box, and the labels are the information — the art is not.
