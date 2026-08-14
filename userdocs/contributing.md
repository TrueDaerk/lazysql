# About this project &amp; contributing

## Why this exists

Browsing a database is a thing I do dozens of times a day, and neither of the
usual options fits it well.

A GUI client is a whole second application to keep open and a whole second
window to find, and it wants to be the place you *live*, when all I wanted was
to look at a table. The CLI clients are the opposite problem: `psql` and
`mysql` start instantly and are excellent at running a statement, but browsing
a schema through them is `SHOW TABLES`, then `DESCRIBE`, then a `SELECT` typed
out in full — nothing on screen to point at, and no memory of where you were.

lazygit had already solved the shape of this for Git: the objects on the left,
the selected thing on the right, one key per action, and a staging area
between "I changed something" and "it happened". A database wants exactly that
— especially the staging area, because an `UPDATE` you did not mean is a much
worse afternoon than a commit you did not mean.

So: a database client shaped like lazygit. Numbered panels, single-key
context-sensitive bindings, modal popups for anything that needs typing, and
**nothing that changes data runs until you say so**.

## What lazysql is

lazysql is a personal project. It is built by one person, to that person's
taste, with heavy AI assistance — "vibe-coded" is a fair description. The
defaults follow a specific set of lazygit and vim habits, on a German
keyboard, in a specific terminal, because that is the setup it was built for.

None of that is a warning label. It is just useful to know before you decide
whether it fits how *you* work.

- **Use it if you like it.** It is public on purpose, and the licence is MIT.
- **There is no support promise.** No SLA, no roadmap commitment, no guarantee
  a feature survives a refactor. Issues get worked on when they are
  interesting or in the way.
- **Improvements are very welcome.** Bug fixes, tests, documentation, dialect
  support and features that sit cleanly behind a config key all get read, and
  merged when they fit.

The one rule worth knowing in advance: a change that *adds* something has an
easier time than one that reshapes an existing default to a different taste.
If you are unsure which yours is, open an issue and ask before writing the
patch.

## How to contribute

The repository's conventions live in
[`CLAUDE.md`](https://github.com/TrueDaerk/lazysql/blob/main/CLAUDE.md), which
is written for AI agents but is the accurate description of how the project is
built. The short version:

1. **Open an issue**, or comment on an existing one, before writing code.
2. **Branch as `issue/<number>-<slug>`.**
3. **Ship tests.** `go vet ./...`, `go build ./...` and `go test ./...` all
   have to pass. Driver code needs tests against SQLite or DuckDB — both run
   in-process, so no server is required.
4. **Keep the one source of truth for keys.** Every binding lives in the
   `key.Binding` table in `internal/ui/keys.go`; the options bar, the actions
   menu and `?` all render from it. A key added anywhere else is a bug.
5. **Update the documentation your change invalidates** — `wiki/` for
   architecture, `userdocs/` for this site.
6. **Open a PR** whose body says `Closes #<number>`.

Everything is written in English — code, comments, commits, issues, PRs.

### Two conventions that surprise people

**Nothing executes destructive SQL as a side effect.** Cell edits, row deletes
and inserts all stage into the changeset and run on an explicit commit. A new
mutation belongs there too, not in a `tea.Cmd` that runs it directly.

**The UI never imports a SQL driver.** All database access goes through the
driver interface in `internal/db`; dialect differences — introspection
queries, quoting, `LIMIT` syntax — live behind it.

## The architecture documentation

This site documents *using* lazysql. How it is built is a separate,
contributor-facing knowledge bundle in the repository:

[**`wiki/`** :material-open-in-new:](https://github.com/TrueDaerk/lazysql/blob/main/wiki/index.md){ .md-button }

It is an [OKF 0.2](https://github.com/GoogleCloudPlatform/knowledge-catalog/blob/main/okf/SPEC.md)
bundle: one concept document per subsystem or decision, with YAML frontmatter,
plus an index and a chronological update log. Design decisions, dialect
quirks, driver behaviour and keybinding rationale all live there — including
the reasoning behind most of what this site merely describes.

It is deliberately **not** built into this site: it answers "why is it like
this", which is a different question from "how do I use it".

## Documentation changes

This site is [MkDocs Material](https://squidfunk.github.io/mkdocs-material/),
built from `userdocs/` at the repository root.

```sh
pip install -r userdocs/requirements.txt
make docs-serve     # live preview on http://127.0.0.1:8000
make docs-build     # what CI runs: mkdocs build --strict
```

The build is `--strict`, so a broken internal link fails it. CI builds the
site on every pull request that touches `userdocs/` or `mkdocs.yml`, and
publishes it on a push to `main`.

Keybinding documentation is checked against the code: a Go test asserts that
every action name in `internal/ui/keys.go` appears in
[Keybindings](reference/keybindings.md), so a new binding cannot land
undocumented.

## Filing a good bug report

Include:

- your **engine and version** (`SELECT version()` is usually enough);
- your **terminal emulator and version** — a fair share of "lazysql ignores my
  key" reports turn out to be the terminal claiming it before lazysql ever
  sees it, and `lazysql --debug-keys` answers that definitively;
- the relevant lines from the **command log** (`@` expands it), which is where
  the statement that misbehaved is written down.

[Issues on GitHub](https://github.com/TrueDaerk/lazysql/issues)
