# Concepts

Four ideas explain most of how lazysql behaves. Read them once and the rest of
the app stops needing explanations.

- **[Panels and layout](panels-and-layout.md)** — three numbered side panels, a
  main view with tabs, the command log and the options bar.
- **[Focus and navigation](focus-and-navigation.md)** — exactly one panel is
  focused, the main view follows its selection, and modals swallow every key
  while they are open.
- **[Staged mutations](staged-mutations.md)** — edits, inserts and deletes
  collect in a pending changeset and run only when you commit them, in one
  transaction.
- **[The command log](command-log.md)** — every statement lazysql executes is
  written down where you can see it.

If you have used [lazygit](https://github.com/jesseduffield/lazygit), all four
will feel familiar: the panel model, the staging area, and the bottom bar that
tells you what the current context answers to are the same ideas applied to a
database.
