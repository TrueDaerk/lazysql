# Terminal setup

lazysql works in any terminal that can draw a box — there is nothing to
configure before the first run. What *is* worth knowing is that a handful of
gestures depend on what your terminal reports, and every one of them has a
fallback that needs nothing special.

## Nothing is required

No Kitty keyboard protocol, no truecolor, no config file. The default theme
tracks your terminal's own ANSI colors, so lazysql looks like the rest of your
terminal out of the box. If you run a light background, `theme = "light"` is
the preset tuned for it — see [Configuration](../guides/configuration.md).

## What depends on the terminal

### `shift`+arrows

Extending a row selection with ++shift+up++ / ++shift+down++ (and a column
block with ++shift+left++ / ++shift+right++) needs the terminal to distinguish
a shifted arrow from a plain one. kitty, WezTerm, Ghostty, iTerm2, Alacritty
and xterm do; **macOS Terminal.app does not** — there the shifted keys are
indistinguishable from plain arrows and simply never match.

Every one of those gestures has an unshifted equivalent that works anywhere:

| Gesture | Shifted | Works everywhere |
|---|---|---|
| Start a row selection | `ctrl+v` | `V` |
| Extend it up / down | `shift+↑` / `shift+↓` | `K` / `J` |
| Narrow it to a column block | `shift+←` / `shift+→` | `C`, then `h` / `l` |

### `ctrl+enter`

`ctrl+enter` (and `cmd+enter`) is an *additive* alias for "accept" — in the
edit modal, the insert form, the filter line, a confirm modal and the commit
key. Terminals that implement the Kitty keyboard protocol's key
disambiguation report it; the rest send a plain carriage return, which is
indistinguishable from ++enter++. Plain ++enter++ therefore does everything
`ctrl+enter` does, in every terminal. Nothing is lost by its absence.

### AltGr keys on QWERTZ and AZERTY

`@`, `[` and `]` are AltGr chords on German QWERTZ and French AZERTY, and
terminals may deliver them as alt-chords or not at all. Each of them has a
layout-neutral spelling that stays bound:

| Action | Primary | Also bound |
|---|---|---|
| Expand the command log | `@` | `L` |
| Previous / next main tab | `<` / `>` | `[` / `]`, `,` / `.` |
| Previous / next month in the date picker | `[` / `]` | `H` / `L`, `,` / `.` |

Both spellings work at once, and either can be replaced through `[keys]`.

### Mouse

lazysql asks the terminal for mouse events, so clicks and the wheel work
everywhere they are reported. Two side effects come with that:

- the terminal stops mapping the wheel to arrow keys on its own;
- dragging to select text needs the terminal's override modifier — ++shift++
  in most terminals, ++option++ / ++alt++ in iTerm2 and Terminal.app.

`y` copies from inside the app and never needs the override.

### Clipboard

`y` uses the native clipboard (`pbcopy`, `xclip`, `xsel`) when one exists. Over
SSH or inside a container there is none, so the text goes out as an
**OSC 52** escape sequence and lands on the clipboard of the terminal you are
actually sitting in front of. Some terminals ship with that off (iTerm2 has a
"may access clipboard" setting; macOS Terminal.app does not support it at
all), and tmux needs `set-clipboard` at `external` or `on`.

If neither works — no terminal, `TERM=dumb`, or a copy over 128 KiB, which
terminals silently drop — the copy falls back to a temp file whose path the
command log names. The log always says which of the three happened. Set
`LAZYSQL_NO_OSC52=1` to skip the escape sequence entirely.

## Telling a missing key from a wrong binding

```sh
lazysql --debug-keys
```

prints exactly what your terminal reports for each key you press (`ctrl+q`
quits). If a key you expect produces no line at all, the terminal never sent
it and no amount of rebinding will help. If it produces a line naming a key
different from the one you pressed, `[keys]` can bind that spelling instead.

More detail — including which terminals emit which sequences — is in the
repository wiki, under
[`wiki/reference/terminal-key-reporting.md`](https://github.com/TrueDaerk/lazysql/blob/main/wiki/reference/terminal-key-reporting.md).
