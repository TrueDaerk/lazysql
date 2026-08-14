# Schema diff

`D` on a connection in panel `[1]` compares its schema against another
connection's — staging against production, two developers' local databases,
a database before and after a migration.

## Running one

`D` opens a popup that picks:

- the **other** connection, and
- the **namespace** on each side (leave a side empty to use that connection's
  default database).

Both sides are then dialed **fresh** — keyring passwords resolve exactly as on
connect, and an "ask on connect" profile prompts first — introspected through
the same driver interface the Structure tab uses, and closed again. Neither
dial disturbs the connection you are browsing.

## The report

The report takes over the main view:

| Color | Meaning |
|---|---|
| Red | The table exists only in **A** |
| Green | The table exists only in **B** |
| Yellow | The table exists in both, with differences |

Differing tables are broken down per column, index and foreign key as
`A: … / B: …` lines.

| Key | Action |
|---|---|
| `j` / `k` | Scroll |
| `ctrl+f` / `ctrl+b` | Scroll a screen |
| `y` | Copy the report |
| `E` | Export it to a file |
| ++esc++ | Dismiss it |

## Type synonyms

Within one engine family, type synonyms are **normalized** before comparison —
SQLite's `INT` and `INTEGER` are the same type and do not show up as a
difference.

A **cross-engine** diff (PostgreSQL against MySQL, say) compares types
verbatim, because there is no meaningful synonym table across families. The
report header says so, so a screen full of type differences is not a mystery.

!!! info "The report generates no migration SQL"
    It tells you what differs, and stops there. Writing the migration is a
    decision, not a diff.
