package db

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lazysql/internal/sshtunnel"
	"lazysql/internal/sshtunnel/sshtest"
)

// The end-to-end path: a real SSH server, a real forwarded channel, and the
// engine's own client library speaking its wire protocol over it. The servers
// on the far side answer the very first client packet with a protocol-level
// error, so a test that sees that error has proved the whole chain — DSN,
// dialer registration, SSH channel, bytes in both directions — works. Only
// the part after "the server said no" is missing, and that is the server's
// business, not lazysql's.

const (
	mysqlServerMessage    = "lazysql tunnel reached the mysql server"
	postgresServerMessage = "lazysql tunnel reached the postgres server"
)

// fakeMySQL answers a fresh connection with a MySQL ERR packet instead of the
// usual greeting, which the client library surfaces as a server error.
func fakeMySQL(c net.Conn) {
	payload := []byte{0xFF}
	payload = binary.LittleEndian.AppendUint16(payload, 1045) // ER_ACCESS_DENIED_ERROR
	payload = append(payload, '#')
	payload = append(payload, []byte("28000")...)
	payload = append(payload, []byte(mysqlServerMessage)...)

	header := []byte{byte(len(payload)), byte(len(payload) >> 8), byte(len(payload) >> 16), 0}
	c.Write(append(header, payload...))
	// Give the client a moment to read before the conn is closed under it.
	io.Copy(io.Discard, c)
}

// fakePostgres reads the startup packet and replies with an ErrorResponse.
func fakePostgres(c net.Conn) {
	var length [4]byte
	if _, err := io.ReadFull(c, length[:]); err != nil {
		return
	}
	n := int(binary.BigEndian.Uint32(length[:])) - 4
	if n < 0 || n > 1<<20 {
		return
	}
	if _, err := io.ReadFull(c, make([]byte, n)); err != nil {
		return
	}

	var fields []byte
	for _, f := range []struct {
		code byte
		text string
	}{
		{'S', "FATAL"},
		{'V', "FATAL"},
		{'C', "28000"},
		{'M', postgresServerMessage},
	} {
		fields = append(fields, f.code)
		fields = append(fields, []byte(f.text)...)
		fields = append(fields, 0)
	}
	fields = append(fields, 0)

	msg := []byte{'E'}
	msg = binary.BigEndian.AppendUint32(msg, uint32(len(fields)+4))
	msg = append(msg, fields...)
	c.Write(msg)
	io.Copy(io.Discard, c)
}

// tunnelTo starts an SSH jump host and returns a live tunnel to it.
func tunnelTo(t *testing.T) *sshtunnel.Tunnel {
	t.Helper()
	srv := sshtest.NewPassword(t, "jump", "hunter2")
	dir := t.TempDir()
	cfg := sshtunnel.Config{
		Enabled:        true,
		Host:           srv.Host(),
		Port:           srv.Port(),
		User:           "jump",
		Auth:           sshtunnel.AuthPassword,
		Secret:         "hunter2",
		KnownHostsFile: sshtest.WriteKnownHosts(t, dir, srv.KnownHostsLine()),
		SSHConfigFile:  filepath.Join(dir, "absent"),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tun, err := sshtunnel.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("open tunnel: %v", err)
	}
	t.Cleanup(func() { tun.Close() })
	return tun
}

// connectThroughTunnel runs the whole lazysql connect path against a target
// reachable only from the jump host's side.
func connectThroughTunnel(t *testing.T, engine Engine, serve func(net.Conn), opts map[string]string) error {
	t.Helper()
	target := sshtest.NewTCPServer(t, serve)
	tun := tunnelTo(t)

	dsn, err := BuildDSN(engine, ConnParams{
		Host: target.Host(), Port: target.Port(),
		User: "app", Database: "shop", Options: opts,
	})
	if err != nil {
		t.Fatalf("BuildDSN: %v", err)
	}
	drv, err := OpenWith(engine, tun.DialContext)
	if err != nil {
		t.Fatalf("OpenWith: %v", err)
	}
	defer drv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return drv.Connect(ctx, dsn)
}

func TestMySQLSpeaksItsProtocolThroughTheTunnel(t *testing.T) {
	err := connectThroughTunnel(t, EngineMySQL, fakeMySQL, nil)
	if err == nil {
		t.Fatal("Connect succeeded against a server that refuses every login")
	}
	if !strings.Contains(err.Error(), mysqlServerMessage) {
		t.Fatalf("err = %v, want the server's own message through the tunnel", err)
	}
}

func TestPostgresSpeaksItsProtocolThroughTheTunnel(t *testing.T) {
	err := connectThroughTunnel(t, EnginePostgres, fakePostgres,
		map[string]string{"sslmode": "disable"})
	if err == nil {
		t.Fatal("Connect succeeded against a server that refuses every login")
	}
	if !strings.Contains(err.Error(), postgresServerMessage) {
		t.Fatalf("err = %v, want the server's own message through the tunnel", err)
	}
}

// Closing the tunnel under a live driver makes further work fail rather than
// silently falling back to a direct connection.
func TestClosedTunnelStopsTheDriver(t *testing.T) {
	target := sshtest.NewTCPServer(t, fakePostgres)
	tun := tunnelTo(t)

	dsn, err := BuildDSN(EnginePostgres, ConnParams{
		Host: target.Host(), Port: target.Port(), User: "app",
		Options: map[string]string{"sslmode": "disable"},
	})
	if err != nil {
		t.Fatalf("BuildDSN: %v", err)
	}
	drv, err := OpenWith(EnginePostgres, tun.DialContext)
	if err != nil {
		t.Fatalf("OpenWith: %v", err)
	}
	defer drv.Close()

	tun.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = drv.Connect(ctx, dsn)
	if err == nil {
		t.Fatal("the driver connected through a closed tunnel")
	}
	if !strings.Contains(err.Error(), "closed") {
		t.Fatalf("err = %v, want it to name the closed tunnel", err)
	}
}
