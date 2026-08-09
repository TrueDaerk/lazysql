package sshtunnel

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"lazysql/internal/sshtunnel/sshtest"
)

const testPassword = "hunter2"

// passwordServer is the common case: a jump host that takes a password.
func passwordServer(t *testing.T) *testSSHServer {
	return sshtest.NewPassword(t, "jump", testPassword)
}

// trusting builds a Config that already knows the server's host key, so a test
// can exercise everything else without tripping over host key checking.
func trusting(t *testing.T, s *testSSHServer) Config {
	dir := t.TempDir()
	return Config{
		Enabled:        true,
		Host:           s.Host(),
		Port:           s.Port(),
		User:           "jump",
		Auth:           AuthPassword,
		Secret:         testPassword,
		KnownHostsFile: sshtest.WriteKnownHosts(t, dir, s.KnownHostsLine()),
		SSHConfigFile:  filepath.Join(dir, "no-such-config"),
	}
}

func openTunnel(t *testing.T, cfg Config) *Tunnel {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tun, err := Open(ctx, cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return tun
}

// echoThrough opens a forwarded connection to the echo server and round-trips
// one message through it.
func echoThrough(t *testing.T, tun *Tunnel, target string) net.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := tun.DialContext(ctx, "tcp", target)
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	want := "select 1"
	if _, err := c.Write([]byte(want)); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, len(want))
	c.SetReadDeadline(time.Now().Add(10 * time.Second))
	if _, err := readFull(c, buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf) != want {
		t.Fatalf("echo = %q, want %q", buf, want)
	}
	return c
}

func readFull(c net.Conn, buf []byte) (int, error) {
	read := 0
	for read < len(buf) {
		n, err := c.Read(buf[read:])
		read += n
		if err != nil {
			return read, err
		}
	}
	return read, nil
}

// ---------- auth ----------

func TestPasswordAuthForwardsTraffic(t *testing.T) {
	srv := passwordServer(t)
	echo := sshtest.NewEcho(t)

	tun := openTunnel(t, trusting(t, srv))
	defer tun.Close()

	c := echoThrough(t, tun, echo.Addr())
	c.Close()

	if srv.Forwards() != 1 {
		t.Fatalf("server saw %d forwards, want 1", srv.Forwards())
	}
}

func TestKeyFileAuth(t *testing.T) {
	dir := t.TempDir()
	signer, keyPath := sshtest.NewKey(t, dir, "id_ed25519", "")
	srv := sshtest.NewPublicKey(t, signer.PublicKey())
	echo := sshtest.NewEcho(t)

	cfg := trusting(t, srv)
	cfg.Auth, cfg.KeyFile, cfg.Secret = AuthKey, keyPath, ""

	tun := openTunnel(t, cfg)
	defer tun.Close()
	echoThrough(t, tun, echo.Addr()).Close()
}

func TestEncryptedKeyNeedsPassphrase(t *testing.T) {
	dir := t.TempDir()
	signer, keyPath := sshtest.NewKey(t, dir, "id_locked", "s3cret")
	srv := sshtest.NewPublicKey(t, signer.PublicKey())

	cfg := trusting(t, srv)
	cfg.Auth, cfg.KeyFile, cfg.Secret = AuthKey, keyPath, ""

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := Open(ctx, cfg); !errors.Is(err, ErrPassphraseRequired) {
		t.Fatalf("Open err = %v, want ErrPassphraseRequired", err)
	}

	// With the passphrase the same key works.
	cfg.Secret = "s3cret"
	tun := openTunnel(t, cfg)
	tun.Close()
}

func TestAgentAuth(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix socket agent")
	}
	dir := t.TempDir()
	signer, _ := sshtest.NewKey(t, dir, "id_agent", "")

	keyring := agent.NewKeyring()
	if err := keyring.Add(agent.AddedKey{PrivateKey: signerKey(t, dir, "id_agent")}); err != nil {
		t.Fatalf("agent add: %v", err)
	}
	// A socket path under t.TempDir() can exceed the ~104 byte sun_path
	// limit on macOS, so the agent listens in the shortest place available.
	sockDir, err := os.MkdirTemp("", "ag")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	defer os.RemoveAll(sockDir)
	sock := filepath.Join(sockDir, "s")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("agent listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				agent.ServeAgent(keyring, c)
			}()
		}
	}()

	srv := sshtest.NewPublicKey(t, signer.PublicKey())
	echo := sshtest.NewEcho(t)

	cfg := trusting(t, srv)
	cfg.Auth, cfg.Secret, cfg.AgentSocket = AuthAgent, "", sock

	tun := openTunnel(t, cfg)
	defer tun.Close()
	echoThrough(t, tun, echo.Addr()).Close()
}

func TestAgentAuthWithoutSocket(t *testing.T) {
	srv := passwordServer(t)
	cfg := trusting(t, srv)
	cfg.Auth, cfg.Secret = AuthAgent, ""
	cfg.AgentSocket = ""
	t.Setenv("SSH_AUTH_SOCK", "")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := Open(ctx, cfg); !errors.Is(err, ErrNoAgent) {
		t.Fatalf("Open err = %v, want ErrNoAgent", err)
	}
}

// signerKey re-reads a generated key file as a crypto private key, which is
// what agent.AddedKey wants.
func signerKey(t *testing.T, dir, name string) any {
	t.Helper()
	pem, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read key: %v", err)
	}
	key, err := ssh.ParseRawPrivateKey(pem)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}
	return key
}

// ---------- host keys ----------

func TestUnknownHostKeyIsRefusedThenAcceptable(t *testing.T) {
	srv := passwordServer(t)
	echo := sshtest.NewEcho(t)

	dir := t.TempDir()
	cfg := trusting(t, srv)
	// An empty known_hosts: every host is unknown.
	cfg.KnownHostsFile = filepath.Join(dir, "known_hosts")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := Open(ctx, cfg)
	var unknown *UnknownHostKeyError
	if !errors.As(err, &unknown) {
		t.Fatalf("Open err = %v, want *UnknownHostKeyError", err)
	}
	if !strings.HasPrefix(unknown.Fingerprint, "SHA256:") {
		t.Fatalf("fingerprint = %q, want a SHA256 fingerprint", unknown.Fingerprint)
	}
	if unknown.KeyType != "ssh-ed25519" {
		t.Fatalf("key type = %q", unknown.KeyType)
	}

	// The UI's confirm path: accept, then dial again.
	if err := AcceptHostKey(unknown); err != nil {
		t.Fatalf("AcceptHostKey: %v", err)
	}
	tun := openTunnel(t, cfg)
	defer tun.Close()
	echoThrough(t, tun, echo.Addr()).Close()

	// The key really landed in the file the error named.
	data, err := os.ReadFile(cfg.KnownHostsFile)
	if err != nil {
		t.Fatalf("read known_hosts: %v", err)
	}
	if !strings.Contains(string(data), "ssh-ed25519") {
		t.Fatalf("known_hosts = %q, want the accepted key", data)
	}
}

func TestChangedHostKeyIsRefused(t *testing.T) {
	srv := passwordServer(t)
	other := passwordServer(t)

	dir := t.TempDir()
	cfg := trusting(t, srv)
	// Record a different server's key for this address.
	cfg.KnownHostsFile = sshtest.WriteKnownHosts(t, dir, other.KnownHostsLineFor(srv.Addr()))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := Open(ctx, cfg)
	var mismatch *HostKeyMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("Open err = %v, want *HostKeyMismatchError", err)
	}
	if len(mismatch.KnownFiles) == 0 {
		t.Fatal("mismatch does not say where the recorded key is")
	}
	// There is no accept path for a changed key.
	if err := AcceptHostKey(nil); err == nil {
		t.Fatal("AcceptHostKey(nil) succeeded")
	}
}

func TestMissingKnownHostsMakesEveryHostUnknown(t *testing.T) {
	srv := passwordServer(t)
	cfg := trusting(t, srv)
	cfg.KnownHostsFile = filepath.Join(t.TempDir(), "nested", "known_hosts")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := Open(ctx, cfg)
	var unknown *UnknownHostKeyError
	if !errors.As(err, &unknown) {
		t.Fatalf("Open err = %v, want *UnknownHostKeyError", err)
	}
	// Accepting creates the directory as well as the file.
	if err := AcceptHostKey(unknown); err != nil {
		t.Fatalf("AcceptHostKey: %v", err)
	}
	if _, err := os.Stat(cfg.KnownHostsFile); err != nil {
		t.Fatalf("known_hosts not created: %v", err)
	}
}

func TestAcceptHostKeyDoesNotGlueOntoAnUnterminatedFile(t *testing.T) {
	srv := passwordServer(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(path, []byte("# no trailing newline"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg := trusting(t, srv)
	cfg.KnownHostsFile = path

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := Open(ctx, cfg)
	var unknown *UnknownHostKeyError
	if !errors.As(err, &unknown) {
		t.Fatalf("Open err = %v, want *UnknownHostKeyError", err)
	}
	if err := AcceptHostKey(unknown); err != nil {
		t.Fatalf("AcceptHostKey: %v", err)
	}
	data, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(data), "# no trailing newline\n") {
		t.Fatalf("known_hosts = %q, want the comment kept on its own line", data)
	}
	// And the file is usable again.
	tun := openTunnel(t, cfg)
	tun.Close()
}

// ---------- teardown ----------

// TestCloseLeavesNoLeaks is the acceptance criterion: after Close, no
// forwarded socket is open on either side and no goroutine of ours is left
// running.
func TestCloseLeavesNoLeaks(t *testing.T) {
	srv := passwordServer(t)
	echo := sshtest.NewEcho(t)

	// Let anything left over from earlier tests settle first.
	before := stableGoroutines(t)

	for i := 0; i < 5; i++ {
		tun := openTunnel(t, trusting(t, srv))
		conns := make([]net.Conn, 0, 3)
		for j := 0; j < 3; j++ {
			conns = append(conns, echoThrough(t, tun, echo.Addr()))
		}
		if got := tun.openConns(); got != 3 {
			t.Fatalf("openConns = %d, want 3", got)
		}
		// Closing one conn deregisters it; Close handles the rest.
		if err := conns[0].Close(); err != nil {
			t.Fatalf("close conn: %v", err)
		}
		if got := tun.openConns(); got != 2 {
			t.Fatalf("openConns after one close = %d, want 2", got)
		}
		if err := tun.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if got := tun.openConns(); got != 0 {
			t.Fatalf("openConns after Close = %d, want 0", got)
		}
		// The channels Close tore down are really dead.
		for _, c := range conns[1:] {
			if _, err := c.Write([]byte("x")); err == nil {
				t.Fatal("write on a torn-down forwarded conn succeeded")
			}
		}
		// Close is idempotent, and a closed tunnel refuses new dials.
		if err := tun.Close(); err != nil {
			t.Fatalf("second Close: %v", err)
		}
		if _, err := tun.DialContext(context.Background(), "tcp", echo.Addr()); !errors.Is(err, ErrTunnelClosed) {
			t.Fatalf("DialContext after Close = %v, want ErrTunnelClosed", err)
		}
	}

	after := stableGoroutines(t)
	// Each round leaks nothing, so the count must come back to where it
	// started; a couple of runtime-owned goroutines can still drift.
	if after > before+2 {
		t.Fatalf("goroutines: %d before, %d after — tunnel teardown leaks", before, after)
	}
}

// stableGoroutines waits for the goroutine count to stop moving, so a
// still-exiting goroutine from the previous step is not read as a leak.
func stableGoroutines(t *testing.T) int {
	t.Helper()
	last := runtime.NumGoroutine()
	stable := 0
	for i := 0; i < 200; i++ {
		time.Sleep(10 * time.Millisecond)
		n := runtime.NumGoroutine()
		if n == last {
			stable++
			if stable == 3 {
				return n
			}
			continue
		}
		last, stable = n, 0
	}
	return last
}

func TestDialContextRejectsNonTCP(t *testing.T) {
	srv := passwordServer(t)
	tun := openTunnel(t, trusting(t, srv))
	defer tun.Close()

	if _, err := tun.DialContext(context.Background(), "unix", "/tmp/sock"); err == nil {
		t.Fatal("forwarding a unix socket succeeded")
	}
}

func TestOpenFailureClosesTheSocket(t *testing.T) {
	srv := passwordServer(t)
	cfg := trusting(t, srv)
	cfg.Secret = "wrong"

	before := stableGoroutines(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for i := 0; i < 5; i++ {
		if _, err := Open(ctx, cfg); err == nil {
			t.Fatal("Open with a wrong password succeeded")
		}
	}
	if after := stableGoroutines(t); after > before+2 {
		t.Fatalf("goroutines: %d before, %d after — a failed Open leaks", before, after)
	}
}

func TestClosedTunnelReportsAddr(t *testing.T) {
	srv := passwordServer(t)
	cfg := trusting(t, srv)
	tun := openTunnel(t, cfg)
	defer tun.Close()
	if tun.Addr() != net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)) {
		t.Fatalf("Addr = %q, want the jump host address", tun.Addr())
	}
	if !isClosedErr(tun.Close()) {
		t.Fatalf("Close reported an unexpected error")
	}
}
