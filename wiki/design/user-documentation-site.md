---
type: Design Decision
title: User documentation site — MkDocs Material in userdocs/, separate from the wiki
description: Why issue #146 put the user-facing docs in their own MkDocs Material site under userdocs/ instead of growing the README or the OKF wiki, the strict-build PR check, why issue #148 pulled the gh-pages deploy out of CI in favor of local `make docs-deploy`, and the test that keeps the keybinding reference from drifting.
tags: [docs, tooling, ci, github-pages, mkdocs, keybindings]
generated:
  by: claude-code/opus-5
  at: 2026-08-14T00:00:00Z
sources:
  - resource: https://squidfunk.github.io/mkdocs-material/
  - resource: https://www.mkdocs.org/user-guide/deploying-your-docs/
---

# User documentation site — MkDocs Material in `userdocs/`

Issue #146. Before it, everything a *user* could read was `README.md`
(533 lines, and growing with every feature) and this wiki, which is
written for contributors. There was no browsable site and no page
boundary: the README had become a linear transcript of every feature in
implementation order, which is the worst possible shape for "how do I
filter rows".

The sibling project **ike** already had the setup worth copying, so the
decision here was mostly "adopt it, adapted", and the parts that are
genuinely lazysql's own are recorded below.

## Three audiences, three artifacts

| Artifact | Audience | Question it answers |
| --- | --- | --- |
| `README.md` | someone on the repo page | what is this, should I try it |
| `userdocs/` (this site) | someone using it | how do I do X |
| `wiki/` | someone changing it | why is it like this |

The wiki is **deliberately not built into the site**. It is an OKF
bundle whose concepts assume the code is open next to them; publishing
it would mean either rewriting every concept for an audience that does
not have that, or shipping pages that answer a question nobody browsing
a user manual asked. The About page links to it instead, which is the
whole of the integration.

The README stays, shortened only by a documentation link — it is what a
GitHub visitor sees, and it should keep working without the site.

## Tooling: MkDocs Material, `strict: true`

- **MkDocs Material `>=9.5,<10`**, pinned in `userdocs/requirements.txt`.
  9.5 is the floor for the grid cards, the palette toggle and
  `content.action.edit` the site uses. The `<10` cap is not cosmetic:
  the Material maintainers have announced that MkDocs 2.0 removes the
  plugin system and the theming API with no migration path, so an
  unbounded range would break the build on a patch release.
- **`docs_dir: userdocs`**, not `docs/`. `docs/` reads like "the
  documentation" and would invite the wiki, screenshots and design
  notes to migrate into it; `userdocs/` says what it holds.
- **`strict: true`** everywhere — locally, in `make docs-build`, and in
  CI. A broken cross-reference is a build failure rather than a 404
  discovered by a reader. That is affordable because every link in the
  site is internal.
- **`pymdownx.keys` is enabled but used sparingly.** Its key database
  renders `++j++` and `++J++` identically (both "J"), which is wrong for
  an application where `d` and `D` are different bindings. Bare keys are
  therefore written as inline code, and the keycap rendering is reserved
  for unambiguous chords (`ctrl+r`, `esc`, `tab`). Keeping the extension
  on costs nothing and leaves the option open.

## CI builds a PR check only; publishing is local (#148)

`.github/workflows/docs.yml` runs `mkdocs build --strict` on pull requests
that touch `userdocs/`, `mkdocs.yml` or the workflow itself, and stops
there. It has no push-to-`main` trigger, no `contents: write` permission and
no deploy step — the workflow cannot publish anything.

Publishing to `gh-pages` happens only via `make docs-deploy`
(`mkdocs gh-deploy --strict --force`), run by hand from whoever's machine has
the change ready to go live. The repository's Pages settings are the other,
one-time half of this: "Deploy from a branch" → `gh-pages` → `/root`, set
once in the GitHub UI, not by any workflow.

This was a deliberate correction (issue #148) of the initial #146
implementation, which had CI push to `gh-pages` on every merge to `main`.
Reasons for pulling that out again:

- **No standing write credential in CI.** A workflow with `contents: write`
  that runs on every push to `main` is a broader blast radius than the docs
  site needs — a bad merge or a compromised action could silently rewrite
  the published site with no human in the loop.
- **`gh-deploy` already works identically from a laptop.** `--force` exists
  precisely because `gh-pages` is disposable generated output with no
  history worth preserving; that same property makes "run it locally when
  you mean to publish" strictly simpler than "gate publishing on CI green
  plus a merge."
- **Publishing and merging are different decisions.** A docs PR landing on
  `main` does not have to mean the public site updates in the same instant;
  making the deploy an explicit local command keeps those two events
  separable.

The alternative — `upload-pages-artifact` + `deploy-pages`, which needs
Pages set to "GitHub Actions" — was not chosen either: `gh-deploy` keeps the
published site inspectable as an ordinary branch, and running it from a
laptop needs no Actions permissions at all.

## Content rule: verified against the code, not the README

The README had drifted — it documented `backspace` as the only opener of
the history pane (`H` had been added), omitted `x` (row detail) and the
`+`/`_` screen modes, and advertised
`go install github.com/TrueDaerk/lazysql@latest`, which cannot work
because the Go module is named `lazysql`. Every page of the site was
therefore written against `internal/`, and the keybinding reference in
particular against `keyMap.slots()` rather than against any prose.

To keep that true, `internal/ui/keys_docs_test.go` asserts that **every**
`slots()` action name appears in `userdocs/reference/keybindings.md`. It
is the cheapest possible guard — a new binding cannot land undocumented
— and it deliberately does not check the *keys*, only that the action
has a home: key strings change for good reasons, and a test that fails
on every rebind would be turned off.

The bindings that are not in `slots()` (the form, engine-picker and
path-completion keys, dispatched inside a modal that claims every key)
are documented with a dash in the "Action name" column, which is also
where the site explains why they cannot be overridden.

## Structure

Home → Getting started → Concepts → Guides → Reference → Troubleshooting
→ About & contributing, mirroring ike's nav. The lazysql-specific shape
is in **Concepts**: the panel model, focus, the *staged changeset* and
the command log — the four ideas that make the rest of the app stop
needing explanations, and the two (staging, the log) a user coming from
a GUI client will not expect.

The personal-project / heavy-AI-assistance notice appears on the landing
page as a `!!! note` and in full on the About page, per the issue.
