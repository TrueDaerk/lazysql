//go:build unix

package db

import (
	"context"
	"io"
	"log"
	"net"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// captureStderr points file descriptor 2 at a pipe, so a write by any
// logger holding the original *os.File is seen too, and returns what was
// written once the returned func is called.
func captureStderr(t *testing.T) func() string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved, err := unix.Dup(2)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Dup2(int(w.Fd()), 2); err != nil {
		t.Fatal(err)
	}
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	return func() string {
		unix.Dup2(saved, 2)
		unix.Close(saved)
		w.Close()
		return <-done
	}
}

func nextWithin(t *testing.T, d time.Duration) DriverMessage {
	t.Helper()
	select {
	case m := <-driverMessages:
		return m
	case <-time.After(d):
		t.Fatal("no driver message arrived")
		return DriverMessage{}
	}
}

// A connection the MySQL server drops makes the driver log an error. That
// line, and one from the standard log package, arrive as driver messages
// — the MySQL one tagged with its connection's Logger — and not a byte
// reaches the process's stderr.
func TestCapturedLogsReachTheQueueNotStderr(t *testing.T) {
	drainDriverMessages()
	stderr := captureStderr(t)
	restore := CaptureProcessLogs()

	log.Printf("dependency says %s", "hello")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	drv, err := Open(EngineMySQL)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := drv.Connect(ctx, "u:secret@tcp("+ln.Addr().String()+")/db"); err == nil {
		t.Fatal("connect to a server that hangs up succeeded")
	}

	var sawLog, sawMySQL bool
	for !(sawLog && sawMySQL) {
		m := nextWithin(t, 5*time.Second)
		switch m.Source {
		case "log":
			if m.Text != "dependency says hello" || m.Owner != nil {
				t.Errorf("log message = %+v", m)
			}
			sawLog = true
		case "mysql":
			if m.Owner != drv.Logger() {
				t.Errorf("mysql message %q not attributed to its connection", m.Text)
			}
			sawMySQL = true
		}
	}

	restore()
	if out := stderr(); out != "" {
		t.Fatalf("stderr got %q, want nothing", out)
	}
}
