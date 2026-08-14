# Troubleshooting

The failure modes worth knowing about, in rough order of how often they come
up.

## lazysql will not start

### `unknown key action "…"` or `unknown color "…"`

A typo in `[keys]` or `[theme]`. Both sections are validated before anything
is drawn, and the message lists **every** valid name — pick the one you meant.
This is deliberate: a half-applied override that silently drops the binding
you were changing would be worse.

### A build fails on the DuckDB package

CGO. `github.com/marcboeker/go-duckdb` bundles the DuckDB C++ engine, so a
build needs a C/C++ toolchain and `CGO_ENABLED=1`. See
[Installation](getting-started/installation.md#build-requirements).

### `go install github.com/TrueDaerk/lazysql@latest` cannot resolve

It never could — the repository's Go module is named `lazysql`, not the GitHub
path. Clone and `make install`, or take a
[release binary](https://github.com/TrueDaerk/lazysql/releases).

## A key does nothing

Work through it in this order:

1. **Is it bound in this context?** `?` lists exactly what the focused panel
   answers to right now. The grid's keys need the *main view* focused —
   ++tab++ into it.
2. **Did the terminal send it?** `lazysql --debug-keys` prints what your
   terminal reports for each key (`ctrl+q` quits). No line at all means the
   key never arrived, and rebinding it cannot help.
3. **Is there a layout-neutral alias?** `@`, `[` and `]` are AltGr chords on
   QWERTZ and AZERTY. `L`, `<` / `>` and `,` / `.` reach the same actions.

### `shift`+arrows do nothing

Your terminal cannot report them — macOS Terminal.app is the common case. Use
the unshifted equivalents, which work anywhere: `V` to start a selection,
`K` / `J` to extend it, `C` to anchor a column block (then plain `h` / `l`).

### `ctrl+enter` does nothing

Same shape of problem: the terminal has to implement the Kitty keyboard
protocol's key disambiguation to distinguish it from ++enter++. Nothing is
lost — plain ++enter++ does everything `ctrl+enter` does, everywhere.

### `q` does not quit

Something is claiming the key. An open modal swallows every key (++esc++
closes it), and the query editor's insert mode types `q` into the buffer
(++esc++ leaves it). With staged changes, `q` asks for confirmation rather
than quitting outright.

## Editing

### "table has no primary key" — the edit keys refuse

Cell edits and row deletes identify their row by its full primary key. Without
one there is no statement that provably touches the row you meant, so both
refuse. Inserts still work. See
[Staged mutations](concepts/staged-mutations.md#how-a-row-is-identified).

### `connection is read-only`

The profile has `read_only = true`. The write keys drop out of the options bar
and every blocked attempt is logged as `-- REJECTED (read-only)`. Clear the
flag in the connection form (`e` on panel `[1]`) if you meant to write.

### A commit failed and my changes are gone from the database

They were never applied: a commit runs inside one transaction, and a failure
rolls the whole thing back. The **changeset is kept**, so fix the cause — the
error is in the command log — and commit again.

## Browsing and queries

### A new table does not show up

Category contents are read once and cached. `R` reloads them.

### The filter says `where (verbatim)`

lazysql could not take your clause apart into comparisons, so it is passed
through as written instead of being parameterized. `IN`, `OR`, parentheses and
subselects all do that. It still works; it is a note, not an error.

### A filter on SQLite returns nothing

Check the column name. SQLite reads a double-quoted identifier that matches no
column as a *string literal*, so a misspelling matches zero rows instead of
raising an error. See [Engines](reference/engines.md#one-sqlite-trap).

### `ctrl+c` did not stop the query on MySQL

It cannot. The MySQL driver can only drop the client connection; the
server-side statement finishes on its own. SQLite, DuckDB and PostgreSQL are
genuinely interrupted. See
[Engines](reference/engines.md#query-cancellation).

### `EXPLAIN` refuses my statement

A statement with `?` or `:name` placeholders has no values to plan with. Run
it with ++ctrl+r++ (which prompts and binds them) instead.

### A read-only SQLite connection will not open

`mode=ro` never creates a database file, so the path has to exist already.

## Copy and export

### `y` copied, but the clipboard is empty

Read the command log — it names which of the three routes was taken. Over SSH
or in a container the copy goes out as an **OSC 52** escape sequence, which
iTerm2 has behind a "may access clipboard" setting, tmux behind
`set-clipboard`, and macOS Terminal.app does not support at all. When
everything fails, the text is written to a temp file whose path the log
prints. `LAZYSQL_NO_OSC52=1` skips straight to the file.

### A whole-table copy is truncated

A clipboard copy is capped at 5000 rows and two minutes, because the whole
thing has to be held at once. `E` streams to a file with no such cap.

### `unsupported extension` on export

The extension picks the format: `.csv`, `.json`, `.sql` or `.md` /
`.markdown`. Anything else is refused rather than guessed at.

## Dump and restore

### `pg_dump: command not found` — reported before the prompt

lazysql drives the engine's own tool and checks for it up front. Install the
client package for your engine, or put it on `PATH`.

### The dump file is gone after I cancelled

By design — a cancelled or failed dump removes its partial file, which is not
restorable anyway.

### The restore form will not submit

A restore overwrites data irreversibly, so it does not run until the database
name has been typed into the confirmation field. On a read-only connection it
is refused outright.

## Display

### Text selection with the mouse stopped working

lazysql asks the terminal for mouse events, so the terminal hands them to the
app instead of using them for selection. Hold your terminal's override
modifier while dragging — ++shift++ in most terminals, ++option++ / ++alt++ in
iTerm2 and Terminal.app. Or use `y`, which copies from inside the app.

### Colors look wrong on a light background

The `default` preset tracks your terminal's ANSI colors, which can go either
way. `theme = "light"` is tuned for a white background — see
[Configuration](guides/configuration.md#theme-colors).

## Still stuck

Open an issue with your **engine and version**, your **terminal emulator and
version**, and the relevant lines from the command log (`@` expands it).
[Issues on GitHub](https://github.com/TrueDaerk/lazysql/issues).
