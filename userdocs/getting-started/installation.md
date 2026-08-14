# Installation

lazysql is a single Go binary with no runtime dependencies. Everything it
stores lives under your XDG directories; nothing is installed system-wide.

## A prebuilt binary

Releases carry binaries for **macOS and Linux**, on **amd64 and arm64**:

[Releases on GitHub :material-open-in-new:](https://github.com/TrueDaerk/lazysql/releases){ .md-button }

Unpack the archive for your platform, drop `lazysql` somewhere on your `PATH`,
and run it.

## From source

You need [Go](https://go.dev/dl/) — see `go.mod` in the repository for the
version the current tree is built with.

```sh
git clone https://github.com/TrueDaerk/lazysql.git
cd lazysql
make install                        # installs to ~/.local/bin/lazysql
make install BINDIR=/usr/local/bin  # or pick another directory
```

`make` on its own produces `./lazysql` without installing it. A plain
`go build .` works too, but skips the version stamp — a Makefile build records
the version and whether the tree was dirty, which `lazysql --version` prints
back:

```console
$ lazysql --version
lazysql version 0.1.21
```

!!! note "`go install` from the module path does not work"
    The repository's Go module is named `lazysql`, not
    `github.com/TrueDaerk/lazysql`, so `go install …@latest` cannot resolve
    it. Clone and build, or take a release binary.

## Build requirements

- **CGO is required for DuckDB.** `github.com/marcboeker/go-duckdb` bundles the
  DuckDB C++ engine, so a build needs a working C/C++ toolchain (`clang` or
  `gcc`) and `CGO_ENABLED=1` — the default whenever a toolchain is present.
- **SQLite needs nothing.** It uses the pure-Go `modernc.org/sqlite`.
- **MySQL/MariaDB and PostgreSQL need nothing.** Both drivers are pure Go.

If a build fails on the DuckDB package, a C toolchain is what is missing.

## Where lazysql keeps things

| Path | What |
|---|---|
| `${XDG_CONFIG_HOME:-~/.config}/lazysql/config.toml` | Connections, `[keys]`, `[theme]`, `restore_session` |
| `${XDG_CONFIG_HOME:-~/.config}/lazysql/state.toml` | Disposable UI state (the screen mode) |
| `${XDG_STATE_HOME:-~/.local/state}/lazysql/history` | Query history |
| `${XDG_STATE_HOME:-~/.local/state}/lazysql/snippets` | Named snippets |
| `${XDG_STATE_HOME:-~/.local/state}/lazysql/filters` | Per-table filter history |
| `${XDG_STATE_HOME:-~/.local/state}/lazysql/session.json` | Last browsing position, for `restore_session` |
| OS keyring | Connection passwords and SSH secrets |

None of those files ever contains a password. Deleting any of them is safe;
the config file is the only one you would miss.

## Uninstall

```sh
make uninstall                      # removes ~/.local/bin/lazysql
rm -rf ~/.config/lazysql ~/.local/state/lazysql
```

Keyring entries are removed with the connection that owns them, from inside
the app (`d` on panel `[1]`).
