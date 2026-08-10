package dump

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// stderrTail is how many of the tool's last stderr lines a failure
// message carries. Enough to see what went wrong, short enough for one
// command log line per entry.
const stderrTail = 10

// maxLineLen caps one streamed stderr line. pg_dump can emit a very long
// statement in an error; the log truncates anyway, and an unbounded
// bufio.Scanner would fail outright on a line over 64 KiB.
const maxLineLen = 4096

// RunResult is what a finished external tool reports back.
type RunResult struct {
	// ExitCode is the process's exit status, or -1 when it never ran or
	// was killed by a signal.
	ExitCode int
	// Stderr is the tail of what the tool wrote to stderr, newest last.
	Stderr []string
	Err    error
}

// Run executes an external command, handing every stderr line to onLine as
// it arrives, and returns once the process is done. Cancelling ctx kills
// the whole process group — pg_dump and mysql both fork helpers, and
// killing only the parent would leave those writing into the output file.
//
// Run does not remove the credential file; the caller owns that through
// Command.Cleanup, so it happens exactly once whatever path the run took.
func Run(ctx context.Context, c Command, onLine func(string)) RunResult {
	if c.Kind != KindProcess {
		return RunResult{Err: fmt.Errorf("dump: %v is not an external command", c.Kind)}
	}

	cmd := exec.Command(c.Bin, c.Args...)
	cmd.Env = append(os.Environ(), c.Env...)
	// A child that inherits the TUI's stdin would fight it for the
	// terminal; a tool that decides to prompt gets EOF instead.
	cmd.Stdin = nil
	setProcAttr(cmd)

	var closers []io.Closer
	defer func() {
		for _, c := range closers {
			c.Close()
		}
	}()

	if c.StdinPath != "" {
		f, err := os.Open(c.StdinPath)
		if err != nil {
			return RunResult{ExitCode: -1, Err: err}
		}
		closers = append(closers, f)
		cmd.Stdin = f
	}
	if c.StdoutPath != "" {
		f, err := os.Create(c.StdoutPath)
		if err != nil {
			return RunResult{ExitCode: -1, Err: err}
		}
		closers = append(closers, f)
		cmd.Stdout = f
	} else {
		// Nothing reads the tool's stdout when it writes the file itself,
		// but psql chats on it; discarding beats a full pipe buffer
		// deadlocking the process.
		cmd.Stdout = io.Discard
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return RunResult{ExitCode: -1, Err: err}
	}
	if err := cmd.Start(); err != nil {
		return RunResult{ExitCode: -1, Err: err}
	}

	// The kill watcher stops when the process is reaped, so a finished run
	// leaves no goroutine behind.
	done := make(chan struct{})
	var killed bool
	var mu sync.Mutex
	go func() {
		select {
		case <-ctx.Done():
			mu.Lock()
			killed = true
			mu.Unlock()
			killGroup(cmd)
		case <-done:
		}
	}()

	tail := newRing(stderrTail)
	scan := bufio.NewScanner(stderr)
	scan.Buffer(make([]byte, 0, 4096), maxLineLen)
	for scan.Scan() {
		line := strings.TrimRight(scan.Text(), "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		tail.add(line)
		if onLine != nil {
			onLine(line)
		}
	}

	waitErr := cmd.Wait()
	close(done)

	mu.Lock()
	wasKilled := killed
	mu.Unlock()

	res := RunResult{ExitCode: exitCode(waitErr), Stderr: tail.all()}
	switch {
	case wasKilled:
		res.Err = context.Canceled
	case waitErr != nil:
		res.Err = fmt.Errorf("%s exited with %s%s", c.Name, exitDescription(waitErr), tailSuffix(res.Stderr))
	}
	return res
}

// exitCode extracts the process's status from Wait's error. -1 means it
// did not exit normally — killed by a signal, most often our own.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

func exitDescription(err error) string {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if code := ee.ExitCode(); code >= 0 {
			return fmt.Sprintf("status %d", code)
		}
		return ee.String()
	}
	return err.Error()
}

// tailSuffix appends the tail of stderr to a failure message, so the log
// line says why rather than only that.
func tailSuffix(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return ": " + strings.Join(lines, " | ")
}

// ring keeps the last n strings added to it.
type ring struct {
	buf []string
	n   int
}

func newRing(n int) *ring { return &ring{n: n} }

func (r *ring) add(s string) {
	r.buf = append(r.buf, s)
	if len(r.buf) > r.n {
		r.buf = r.buf[len(r.buf)-r.n:]
	}
}

func (r *ring) all() []string {
	out := make([]string, len(r.buf))
	copy(out, r.buf)
	return out
}
