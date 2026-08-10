package ui

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/atotto/clipboard"
)

// A copy tries three mechanisms in order, and stops at the first one
// that can work:
//
//  1. the native clipboard (pbcopy / xclip / xsel via atotto/clipboard),
//     which is the only one that gives an answer about whether it
//     worked, and the only one that works when the terminal is not
//     lazysql's own — a copy from a detached tmux pane, say;
//  2. OSC 52, the escape sequence a terminal turns into a clipboard
//     write itself. It is what makes `y` work over SSH and inside
//     containers, where there is no pbcopy to shell out to. The
//     terminal never answers, so a copy that goes out this way is
//     reported as sent, not as confirmed;
//  3. the temp-file spill, for a session with neither — a bare tty, a
//     dumb terminal, output redirected somewhere that is not a screen.
//
// See wiki/design/clipboard-strategy.md.

// clipboardWrite is the one place lazysql talks to the system
// clipboard. It is a variable so tests can replace it: a test run must
// never overwrite the developer's real clipboard. The copy/export flows
// grow around this seam rather than call the library directly.
var clipboardWrite = writeClipboard

func writeClipboard(text string) error {
	// Headless sessions have no pbcopy/xclip to hand the text to.
	if clipboard.Unsupported {
		return errors.New("no clipboard available in this environment")
	}
	return clipboard.WriteAll(text)
}

// osc52Limit caps what may go out as an OSC 52 sequence. Terminals put
// their own ceiling on the length of an escape sequence (xterm's is a
// build-time constant, tmux's a buffer size), and a sequence past it is
// not truncated but dropped — silently, because nothing answers an OSC
// 52 write. A whole-table copy can easily reach megabytes, so anything
// this large takes the temp file instead, where the log names a path
// the user can actually open.
const osc52Limit = 128 * 1024

// osc52Available reports whether the OSC 52 fallback may be used. It is
// a variable for the same reason clipboardWrite is — a test decides for
// itself which mechanisms exist.
var osc52Available = detectOSC52

// detectOSC52 asks whether there is a terminal on the other end of
// stdout at all. There is no way to ask whether it understands OSC 52
// — the sequence has no reply — so the check is deliberately coarse:
// a terminal that ignores it swallows the sequence without printing
// anything, which is why the fallback is safe to attempt blind.
// LAZYSQL_NO_OSC52 turns it off for the terminal where it is not.
func detectOSC52() bool {
	if os.Getenv("LAZYSQL_NO_OSC52") != "" {
		return false
	}
	switch os.Getenv("TERM") {
	case "", "dumb":
		return false
	}
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// spillFile is where a failed clipboard write lands. It is a variable
// for the same reason clipboardWrite is: tests must not leave files in
// the developer's temp directory.
var spillFile = writeSpillFile

func writeSpillFile(name, text string) (string, error) {
	f, err := os.CreateTemp("", spillPattern(name))
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.WriteString(f, text); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// spillPattern turns a copy's subject into a recognizable temp file
// name, keeping the extension after the random part so the file opens
// in the right thing.
func spillPattern(name string) string {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	base = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, base)
	if base == "" {
		base = "copy"
	}
	return "lazysql-" + base + "-*" + ext
}

// copyOut is every copy in the app: it puts text on the system
// clipboard, hands it to the terminal as an OSC 52 write when there is
// no native clipboard to put it on — an SSH session, a container — and
// spills it to a temp file when there is neither. A copy therefore
// never simply fails; the worst case is that the user has to reach for
// the file.
//
// The escape sequence cannot be written from here: it has to leave
// through the same tty Bubble Tea is drawing on, so it travels back to
// the update loop as copiedMsg.osc52 and goes out as tea.SetClipboard.
// The returned line is ready for the command log either way.
func copyOut(subject, filename, text string) copiedMsg {
	err := clipboardWrite(text)
	if err == nil {
		return copiedMsg{line: fmt.Sprintf("-- copy %s to clipboard (%s)", subject, byteCount(len(text)))}
	}
	if len(text) <= osc52Limit && osc52Available() {
		return copiedMsg{
			line: fmt.Sprintf("-- copy %s to clipboard via OSC 52 (%s; no local clipboard: %v)",
				subject, byteCount(len(text)), err),
			osc52: text,
		}
	}
	path, spillErr := spillFile(filename, text)
	if spillErr != nil {
		return copiedMsg{line: fmt.Sprintf("-- copy %s FAILED: %v (and no temp file: %v)", subject, err, spillErr)}
	}
	return copiedMsg{line: fmt.Sprintf("-- copy %s: no clipboard (%v) — wrote %s to %s",
		subject, err, byteCount(len(text)), path)}
}

// byteCount renders a size the way the command log wants it: exact for
// anything small enough to read, rounded once it stops being useful.
func byteCount(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d bytes", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KiB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1024*1024))
	}
}
