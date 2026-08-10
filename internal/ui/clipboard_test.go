package ui

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

// fakeOSC52 declares whether the terminal can carry an OSC 52 write for
// the duration of a test. The suite's default is "it cannot" (see
// TestMain), so a test that wants the fallback asks for it.
func fakeOSC52(t *testing.T, available bool) {
	t.Helper()
	prev := osc52Available
	osc52Available = func() bool { return available }
	t.Cleanup(func() { osc52Available = prev })
}

// noClipboard makes the native clipboard fail the way a headless SSH
// session does.
func noClipboard(t *testing.T) {
	t.Helper()
	prev := clipboardWrite
	clipboardWrite = func(string) error { return errors.New("no pbcopy") }
	t.Cleanup(func() { clipboardWrite = prev })
}

// With no pbcopy/xclip but a terminal that can carry it, a copy goes
// out as an OSC 52 write rather than to a temp file the user then has
// to go find.
func TestCopyFallsBackToOSC52(t *testing.T) {
	noClipboard(t)
	fakeOSC52(t, true)
	spilled := fakeSpill(t)

	msg := copyOut("cell users.name", "users-name.txt", "hello")
	if msg.osc52 != "hello" {
		t.Fatalf("osc52 payload = %q, want the copied text", msg.osc52)
	}
	if !strings.Contains(msg.line, "OSC 52") {
		t.Fatalf("log line = %q, want it to name OSC 52", msg.line)
	}
	if *spilled != "" {
		t.Fatalf("spilled %q to a file although OSC 52 was available", *spilled)
	}
}

// A copy too large for an escape sequence takes the temp file instead:
// an over-long OSC 52 write is dropped by the terminal without a word,
// and a silent copy is worse than a file path in the log.
func TestOversizedCopySkipsOSC52(t *testing.T) {
	noClipboard(t)
	fakeOSC52(t, true)
	spilled := fakeSpill(t)

	big := strings.Repeat("x", osc52Limit+1)
	msg := copyOut("users as CSV", "users.csv", big)
	if msg.osc52 != "" {
		t.Fatalf("osc52 payload = %d bytes, want the copy spilled instead", len(msg.osc52))
	}
	if *spilled != big {
		t.Fatalf("spill file holds %d bytes, want %d", len(*spilled), len(big))
	}
	if !strings.Contains(msg.line, "no clipboard") {
		t.Fatalf("log line = %q", msg.line)
	}
}

// With neither a native clipboard nor a terminal to hand the sequence
// to, the temp-file spill is still what happens.
func TestCopyWithoutOSC52StillSpills(t *testing.T) {
	noClipboard(t)
	fakeOSC52(t, false)
	spilled := fakeSpill(t)

	msg := copyOut("cell users.name", "users-name.txt", "hello")
	if msg.osc52 != "" {
		t.Fatalf("osc52 payload = %q, want none", msg.osc52)
	}
	if *spilled != "hello" {
		t.Fatalf("spill file = %q", *spilled)
	}
}

// The escape sequence has to leave through the program's own tty, so
// the copy message carries it back to the root, which turns it into a
// tea.SetClipboard command.
func TestOSC52CopyIsWrittenThroughTheUpdateLoop(t *testing.T) {
	m := sized(120, 40)
	_, cmd := m.Update(copiedMsg{line: "-- copy cell to clipboard via OSC 52", osc52: "hello"})

	var wrote bool
	for _, msg := range drain(cmd) {
		// setClipboardMsg is unexported: Bubble Tea only means it to be
		// produced by tea.SetClipboard and consumed by the runtime, which
		// is exactly what this asserts happened.
		if strings.Contains(fmt.Sprintf("%T", msg), "setClipboardMsg") && fmt.Sprint(msg) == "hello" {
			wrote = true
		}
	}
	if !wrote {
		t.Fatalf("commands = %v, want a tea.SetClipboard(\"hello\")", drain(cmd))
	}
}

// A copy that reached the native clipboard never also writes an escape
// sequence: two clipboard writes for one `y` is one too many.
func TestNativeCopyDoesNotAlsoWriteOSC52(t *testing.T) {
	fakeClipboard(t)
	fakeOSC52(t, true)

	msg := copyOut("cell users.name", "users-name.txt", "hello")
	if msg.osc52 != "" {
		t.Fatalf("osc52 payload = %q, want none after a native copy", msg.osc52)
	}
	if !strings.Contains(msg.line, "to clipboard (") {
		t.Fatalf("log line = %q", msg.line)
	}
}

// The environment escape hatch exists for the terminal that prints the
// sequence instead of acting on it.
func TestOSC52CanBeDisabledByEnvironment(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("LAZYSQL_NO_OSC52", "1")
	if detectOSC52() {
		t.Fatal("LAZYSQL_NO_OSC52 did not turn the fallback off")
	}
	t.Setenv("LAZYSQL_NO_OSC52", "")
	t.Setenv("TERM", "dumb")
	if detectOSC52() {
		t.Fatal("a dumb terminal was offered the OSC 52 fallback")
	}
}

// Under `go test` stdout is a pipe, so the detection says no on its own
// — the guard that keeps a test run from writing to a real terminal
// even if the environment variables above were not set.
func TestOSC52NeedsATerminal(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("LAZYSQL_NO_OSC52", "")
	if fi, err := os.Stdout.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		t.Skip("stdout is a terminal: the test binary was run directly")
	}
	if detectOSC52() {
		t.Fatal("OSC 52 offered although stdout is not a terminal")
	}
}
