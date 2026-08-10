package sshtunnel

import (
	"net"
	"strings"
	"testing"
	"time"

	"lazysql/internal/sshtunnel/sshtest"
)

// A local forward is what an external tool (pg_dump, mysql) connects to
// when the profile runs through a jump host: it cannot be handed the Go
// dialer everything inside lazysql uses.

func TestForwardCarriesTrafficToTheRemote(t *testing.T) {
	srv := passwordServer(t)
	echo := sshtest.NewEcho(t)

	tun := openTunnel(t, trusting(t, srv))
	defer tun.Close()

	fwd, err := tun.Listen(echo.Addr())
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer fwd.Close()

	host, _, err := net.SplitHostPort(fwd.Addr())
	if err != nil {
		t.Fatal(err)
	}
	// Loopback only: the forwarded port speaks for the jump host's
	// network and must not be reachable from elsewhere.
	if host != "127.0.0.1" {
		t.Fatalf("forward listens on %q, want 127.0.0.1", host)
	}
	if fwd.Port() == 0 {
		t.Fatal("forward reports no port")
	}

	c, err := net.DialTimeout("tcp", fwd.Addr(), 10*time.Second)
	if err != nil {
		t.Fatalf("dial forward: %v", err)
	}
	defer c.Close()

	want := "select 1"
	if _, err := c.Write([]byte(want)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len(want))
	c.SetReadDeadline(time.Now().Add(10 * time.Second))
	if _, err := readFull(c, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != want {
		t.Fatalf("echo = %q, want %q", buf, want)
	}
	if srv.Forwards() != 1 {
		t.Fatalf("server saw %d forwards, want 1", srv.Forwards())
	}
}

// The forward's lifetime is the job's: closing it stops the listener and
// every connection it accepted.
func TestForwardCloseStopsAccepting(t *testing.T) {
	srv := passwordServer(t)
	echo := sshtest.NewEcho(t)
	_ = srv

	tun := openTunnel(t, trusting(t, srv))
	defer tun.Close()

	fwd, err := tun.Listen(echo.Addr())
	if err != nil {
		t.Fatal(err)
	}
	addr := fwd.Addr()

	live, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()

	if err := fwd.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// A second Close is a no-op, not an error.
	if err := fwd.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}

	if c, err := net.DialTimeout("tcp", addr, 2*time.Second); err == nil {
		c.Close()
		t.Fatal("the closed forward still accepts connections")
	}
}

// A closed tunnel cannot open channels, so the forward reports why rather
// than leaving the tool with an unexplained connection reset.
func TestForwardOnClosedTunnelRecordsTheError(t *testing.T) {
	srv := passwordServer(t)
	echo := sshtest.NewEcho(t)

	tun := openTunnel(t, trusting(t, srv))
	fwd, err := tun.Listen(echo.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer fwd.Close()
	tun.Close()

	c, err := net.DialTimeout("tcp", fwd.Addr(), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	// The splice fails on the tunnel side and closes the local end.
	c.SetReadDeadline(time.Now().Add(10 * time.Second))
	buf := make([]byte, 1)
	if _, err := c.Read(buf); err == nil {
		t.Fatal("want the local end to be closed when the tunnel is gone")
	}
	c.Close()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if fwd.Err() != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the forward recorded no error for a closed tunnel")
}

func TestListenRejectsAMalformedRemote(t *testing.T) {
	srv := passwordServer(t)
	tun := openTunnel(t, trusting(t, srv))
	defer tun.Close()

	if _, err := tun.Listen("not-an-endpoint"); err == nil {
		t.Fatal("want an error for a remote without a port")
	} else if !strings.Contains(err.Error(), "forward target") {
		t.Fatalf("err = %v", err)
	}
}
