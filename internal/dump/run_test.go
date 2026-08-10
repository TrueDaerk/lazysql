package dump

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The external-tool tests drive /bin/sh rather than a real pg_dump: the
// lifecycle Run is responsible for — stderr streaming, exit codes, killing
// a whole process group — is the same whatever the binary is, and a shell
// is the one "tool" every CI machine has.
func shellCommand(t *testing.T, script string) Command {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the shell fixtures are POSIX")
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh on PATH")
	}
	return Command{Kind: KindProcess, Bin: sh, Name: "sh", Args: []string{"-c", script}}
}

func TestRunStreamsStderrAndSucceeds(t *testing.T) {
	cmd := shellCommand(t, `echo one 1>&2; echo two 1>&2; echo ignored`)
	var lines []string
	res := Run(context.Background(), cmd, func(l string) { lines = append(lines, l) })

	if res.Err != nil {
		t.Fatalf("err = %v", res.Err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d, want 0", res.ExitCode)
	}
	if strings.Join(lines, ",") != "one,two" {
		t.Fatalf("streamed %v, want [one two]", lines)
	}
	if strings.Join(res.Stderr, ",") != "one,two" {
		t.Fatalf("tail = %v", res.Stderr)
	}
}

// A non-zero exit is reported with the tail of stderr, so the log line
// says what the tool complained about rather than only that it failed.
func TestRunReportsExitCodeWithStderrTail(t *testing.T) {
	cmd := shellCommand(t, `echo "connection refused" 1>&2; exit 3`)
	res := Run(context.Background(), cmd, nil)

	if res.ExitCode != 3 {
		t.Fatalf("exit = %d, want 3", res.ExitCode)
	}
	if res.Err == nil {
		t.Fatal("want an error for a non-zero exit")
	}
	if !strings.Contains(res.Err.Error(), "status 3") ||
		!strings.Contains(res.Err.Error(), "connection refused") {
		t.Fatalf("err = %q, want the status and the stderr tail", res.Err)
	}
}

// Only the last stderrTail lines are kept: a tool that logs thousands of
// lines must not grow the message without bound.
func TestRunKeepsOnlyTheStderrTail(t *testing.T) {
	cmd := shellCommand(t, `i=1; while [ $i -le 40 ]; do echo line$i 1>&2; i=$((i+1)); done`)
	res := Run(context.Background(), cmd, nil)
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if len(res.Stderr) != stderrTail {
		t.Fatalf("tail has %d lines, want %d", len(res.Stderr), stderrTail)
	}
	if res.Stderr[len(res.Stderr)-1] != "line40" {
		t.Fatalf("tail ends at %q, want line40", res.Stderr[len(res.Stderr)-1])
	}
}

// Cancelling kills the child *and* everything it forked. The fixture
// leaves a grandchild running and touching a file; if the group kill
// worked, the file never appears.
func TestCancelKillsTheProcessGroup(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "grandchild-survived")
	started := filepath.Join(dir, "started")

	cmd := shellCommand(t, `(sleep 3; touch `+marker+`) & touch `+started+`; wait`)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		// Wait until the fixture is really up, so the kill cannot land
		// before there is a group to kill.
		for i := 0; i < 200; i++ {
			if _, err := os.Stat(started); err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
	}()

	start := time.Now()
	res := Run(ctx, cmd, nil)
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("run took %s — the kill did not stop it promptly", took)
	}
	if !errors.Is(res.Err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", res.Err)
	}

	// Well past the grandchild's own sleep: if it had survived the group
	// kill it would have created the marker by now.
	time.Sleep(3500 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("grandchild survived the cancel — the process group was not killed")
	}
}

func TestRunWritesStdoutToFileAndReadsStdin(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.sql")
	out := filepath.Join(dir, "out.txt")
	if err := os.WriteFile(in, []byte("SELECT 1;\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := shellCommand(t, `cat`)
	cmd.StdinPath = in
	cmd.StdoutPath = out
	if res := Run(context.Background(), cmd, nil); res.Err != nil {
		t.Fatal(res.Err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "SELECT 1;\n" {
		t.Fatalf("out = %q", data)
	}
}

// A tool that cannot even start reports the failure rather than panicking.
func TestRunReportsAMissingBinary(t *testing.T) {
	cmd := Command{Kind: KindProcess, Bin: "/nonexistent/lazysql-tool", Name: "lazysql-tool"}
	res := Run(context.Background(), cmd, nil)
	if res.Err == nil {
		t.Fatal("want an error for a binary that does not exist")
	}
	if res.ExitCode != -1 {
		t.Fatalf("exit = %d, want -1", res.ExitCode)
	}
}

// Run only runs external commands; a SQL or copy job reaching it is a
// programming error, not a crash.
func TestRunRejectsNonProcessKinds(t *testing.T) {
	res := Run(context.Background(), Command{Kind: KindSQL, SQL: []string{"VACUUM INTO 'x'"}}, nil)
	if res.Err == nil {
		t.Fatal("want an error for a non-process command")
	}
}

// A missing stdin file fails before the tool is started, so a restore
// cannot silently run against an empty input.
func TestRunFailsOnAMissingStdinFile(t *testing.T) {
	cmd := shellCommand(t, `cat`)
	cmd.StdinPath = filepath.Join(t.TempDir(), "nope.sql")
	res := Run(context.Background(), cmd, nil)
	if !errors.Is(res.Err, os.ErrNotExist) {
		t.Fatalf("err = %v, want a not-exist error", res.Err)
	}
}
