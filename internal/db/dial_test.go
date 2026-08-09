package db

import (
	"context"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

// The MySQL driver logs every failed handshake to stderr. These tests fail
// handshakes on purpose, so the driver's logger is silenced for the package.
func TestMain(m *testing.M) {
	mysql.SetLogger(log.New(io.Discard, "", 0))
	os.Exit(m.Run())
}

// These tests prove the transport wiring, not a full session: a real MySQL or
// PostgreSQL handshake needs a real server. What matters here is that the
// driver stops using its own TCP dialer and calls ours with the address from
// the DSN — everything past that point is the server's job.

// recordingDialer answers like a socket that accepts and hangs up, and
// remembers what it was asked to dial.
type recordingDialer struct {
	mu    sync.Mutex
	addrs []string
	nets  []string
	ln    net.Listener
}

func newRecordingDialer(t *testing.T) *recordingDialer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	d := &recordingDialer{ln: ln}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			// Hanging up immediately makes every handshake fail fast and
			// deterministically, whatever protocol is spoken.
			c.Close()
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return d
}

func (d *recordingDialer) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	d.mu.Lock()
	d.addrs = append(d.addrs, addr)
	d.nets = append(d.nets, network)
	d.mu.Unlock()
	var nd net.Dialer
	return nd.DialContext(ctx, "tcp", d.ln.Addr().String())
}

func (d *recordingDialer) dialed() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.addrs...)
}

func tunnelledConnect(t *testing.T, engine Engine, p ConnParams) (*recordingDialer, error) {
	t.Helper()
	dsn, err := BuildDSN(engine, p)
	if err != nil {
		t.Fatalf("BuildDSN: %v", err)
	}
	d := newRecordingDialer(t)
	drv, err := OpenWith(engine, d.dial)
	if err != nil {
		t.Fatalf("OpenWith: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	connErr := drv.Connect(ctx, dsn)
	drv.Close()
	return d, connErr
}

func TestMySQLDialsThroughTheTunnel(t *testing.T) {
	d, err := tunnelledConnect(t, EngineMySQL, ConnParams{
		Host: "db.internal", Port: 3307, User: "u", Database: "shop",
	})
	if err == nil {
		t.Fatal("Connect succeeded against a socket that hangs up")
	}
	got := d.dialed()
	if len(got) == 0 {
		t.Fatal("the driver never called the tunnel dialer")
	}
	if got[0] != "db.internal:3307" {
		t.Fatalf("dialed %q, want db.internal:3307", got[0])
	}
}

func TestPostgresDialsThroughTheTunnel(t *testing.T) {
	d, err := tunnelledConnect(t, EnginePostgres, ConnParams{
		Host: "db.internal", Port: 5433, User: "u", Database: "shop",
		Options: map[string]string{"sslmode": "disable"},
	})
	if err == nil {
		t.Fatal("Connect succeeded against a socket that hangs up")
	}
	got := d.dialed()
	if len(got) == 0 {
		t.Fatal("the driver never called the tunnel dialer")
	}
	if !strings.HasPrefix(got[0], "db.internal:") {
		t.Fatalf("dialed %q, want the DSN host unresolved", got[0])
	}
	if !strings.HasSuffix(got[0], ":5433") {
		t.Fatalf("dialed %q, want port 5433", got[0])
	}
}

// A second tunnelled connection must work after the first was closed: each
// one registers under its own name and drops it again on Close.
func TestTunnelledConnectionsDoNotCollide(t *testing.T) {
	for i := 0; i < 3; i++ {
		if _, err := tunnelledConnect(t, EngineMySQL, ConnParams{Host: "h", Port: 3306}); err == nil {
			t.Fatal("Connect succeeded against a socket that hangs up")
		}
		if _, err := tunnelledConnect(t, EnginePostgres, ConnParams{Host: "h", Port: 5432}); err == nil {
			t.Fatal("Connect succeeded against a socket that hangs up")
		}
	}
}

func TestFileEnginesRefuseATunnel(t *testing.T) {
	for _, engine := range []Engine{EngineSQLite, EngineDuckDB} {
		if Tunnelled(engine) {
			t.Errorf("Tunnelled(%s) = true, want false", engine)
		}
		drv, err := OpenWith(engine, func(context.Context, string, string) (net.Conn, error) {
			return nil, nil
		})
		if err != nil {
			t.Fatalf("OpenWith(%s): %v", engine, err)
		}
		err = drv.Connect(context.Background(), "")
		if err == nil {
			t.Errorf("%s connected through a tunnel", engine)
		} else if !strings.Contains(err.Error(), "SSH tunnel") {
			t.Errorf("%s: err = %v, want it to name the tunnel", engine, err)
		}
		drv.Close()
	}
}

func TestUnixSocketHostRefusesATunnel(t *testing.T) {
	dsn, err := BuildDSN(EngineMySQL, ConnParams{Host: "/var/run/mysqld/mysqld.sock", User: "u"})
	if err != nil {
		t.Fatalf("BuildDSN: %v", err)
	}
	drv, err := OpenWith(EngineMySQL, func(context.Context, string, string) (net.Conn, error) {
		return nil, nil
	})
	if err != nil {
		t.Fatalf("OpenWith: %v", err)
	}
	defer drv.Close()
	if err := drv.Connect(context.Background(), dsn); err == nil {
		t.Fatal("a unix socket host was tunnelled")
	}
}

// Without a dialer nothing changes: the direct path still opens a handle.
func TestDirectConnectionNeedsNoRegistration(t *testing.T) {
	drv, err := Open(EngineSQLite)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer drv.Close()
	if err := drv.Connect(context.Background(), ":memory:"); err != nil {
		t.Fatalf("Connect: %v", err)
	}
}
