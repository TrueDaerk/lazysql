---
type: Design Decision
title: CI on native runners, release binaries built per-target instead of goreleaser release
description: Why ci.yml runs go vet/build/test natively on ubuntu-latest and macos-latest, why release.yml builds each of the four darwin/linux × amd64/arm64 targets on its own native runner (including a hosted arm64 Linux runner) with goreleaser build --single-target rather than CGO cross-compiling, why DuckDB needs no build-tag gating as a result, and why the GitHub release itself is assembled by softprops/action-gh-release instead of goreleaser release.
tags: [ci, release, goreleaser, duckdb, cgo, version]
generated:
  by: claude-code/sonnet-5
  at: 2026-08-09T13:57:35Z
sources:
  - resource: https://goreleaser.com/cookbooks/cgo-and-crosscompiling/
  - resource: https://goreleaser.com/customization/release/
---

# CI and release pipeline

## Decision

DuckDB (`github.com/marcboeker/go-duckdb/v2`) requires CGO; the other four
drivers do not (`modernc.org/sqlite` and the MySQL/Postgres drivers are pure
Go). CGO does not cross-compile cleanly between operating systems — building
a Linux binary's C++ runtime from a macOS host (or vice versa) needs a
matching sysroot and cross-linker that GitHub-hosted runners don't ship with.
Rather than gate DuckDB behind a build tag for the targets that would need
cross-compiling, **every release target is built on a runner whose native
OS/arch matches the target**, so CGO always compiles natively and DuckDB
ships in all four binaries:

| Target | Runner | Note |
|---|---|---|
| darwin/arm64 | `macos-latest` | native |
| darwin/amd64 | `macos-latest` | cross-*arch*, same OS — Apple's `clang` is a universal toolchain, so `GOARCH=amd64 CGO_ENABLED=1` just works |
| linux/amd64 | `ubuntu-latest` | native |
| linux/arm64 | `ubuntu-24.04-arm` | native — GitHub's hosted arm64 Linux runner, not a cross-compile |

This means the issue's hinted fallback ("gate DuckDB behind a build tag") was
never needed. If GitHub-hosted arm64 Linux runners ever stop being available
on this repo's plan, the fallback is: cross-compile linux/arm64 from
`ubuntu-latest` with `CC=aarch64-linux-gnu-gcc CXX=aarch64-linux-gnu-g++`, and
if that breaks on DuckDB's C++ sources, gate `internal/db/dialect_duckdb.go`
behind a `!noduckdb` build tag and pass `-tags noduckdb` for that leg only —
the dialect self-registers via `init()` and nothing else in `internal/db`
depends on it being present, so the rest of the package still compiles.

## Why release.yml doesn't call `goreleaser release`

`goreleaser release` always builds every `goos`/`goarch` combination declared
in `builds:` in one process — there is no flag to make it build only the
current target and later append pre-built artifacts to the same GitHub
release (the `--skip` flag's valid values don't include `build`, confirmed
against goreleaser v2.17.1 locally). Splitting a CGO build across several
native runners and merging their outputs into one release is a GoReleaser
**Pro** feature (`goreleaser continue` / split-merge); it isn't in the OSS
build used here.

So the pipeline splits the work instead:

- Each matrix leg in `release.yml` runs `goreleaser build --single-target
  --clean` (which *does* restrict itself to the current `GOOS`/`GOARCH` env
  vars — verified locally) using `.goreleaser.yaml` for the build config and
  `-ldflags` version injection, then archives the single binary with plain
  `tar`/`shasum` and uploads it as a build artifact.
- A final `publish` job downloads every leg's archive, concatenates the
  checksums, and hands everything to `softprops/action-gh-release` with
  `generate_release_notes: true` for the changelog (GitHub's own PR/commit-based
  notes, not goreleaser's changelog stage).

`.goreleaser.yaml` therefore only has `builds:` — no `archives:`/`checksum:`/
`changelog:`/`release:` blocks, since nothing in the pipeline invokes
`goreleaser release`.

## Version injection

`internal/version.Version` defaults to `"dev"` and is overwritten via
`-ldflags "-X lazysql/internal/version.Version={{ .Tag }}"` in
`.goreleaser.yaml`'s `builds.ldflags`, so a release binary's `--version`
prints the exact pushed tag (e.g. `v0.1.0`), and the options bar
([[keybindings-single-source]] area — see `internal/ui/view.go`
`renderOptionsBar`) shows it on the right alongside the screen mode and app
name.
