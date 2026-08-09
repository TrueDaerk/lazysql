// Package sshtunnel opens an SSH connection to a bastion host and hands out
// net.Conns that are tunnelled through it. It is the transport half of
// lazysql's SSH support: the db package plugs a Tunnel's DialContext into the
// concrete SQL driver, so nothing above this package knows a jump host is
// involved.
//
// Nothing here writes a secret anywhere. Passwords and key passphrases arrive
// in Config.Secret from the OS keyring or a prompt and are used once.
package sshtunnel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// AuthMethod is how lazysql authenticates to the jump host.
type AuthMethod string

const (
	// AuthAgent asks the running ssh-agent at $SSH_AUTH_SOCK for signers.
	AuthAgent AuthMethod = "agent"
	// AuthKey reads a private key file, decrypting it with Secret when the
	// key is passphrase-protected.
	AuthKey AuthMethod = "key"
	// AuthPassword sends Secret as the SSH password.
	AuthPassword AuthMethod = "password"
)

// AuthMethods lists the supported methods in the order the form offers them.
func AuthMethods() []AuthMethod { return []AuthMethod{AuthAgent, AuthKey, AuthPassword} }

// AuthLabel is the human-readable name of a method.
func AuthLabel(a AuthMethod) string {
	switch a {
	case AuthKey:
		return "key file"
	case AuthPassword:
		return "password"
	default:
		return "agent"
	}
}

// DefaultPort is the port used when neither the profile nor ~/.ssh/config
// names one.
const DefaultPort = 22

// dialTimeout bounds the TCP dial and the SSH handshake when the caller's
// context has no deadline of its own.
const dialTimeout = 15 * time.Second

// Config describes one jump host. Host may be a ~/.ssh/config alias: Resolve
// expands it into HostName/User/Port/IdentityFile before anything is dialled.
//
// The three *File fields exist so tests can point at a temporary HOME; empty
// means the usual location under the user's home directory.
type Config struct {
	Enabled bool
	Host    string
	Port    int
	User    string
	Auth    AuthMethod
	KeyFile string

	// Secret is the SSH password for AuthPassword and the private key
	// passphrase for AuthKey. It is never persisted by this package.
	Secret string

	// AgentSocket overrides $SSH_AUTH_SOCK.
	AgentSocket string
	// SSHConfigFile overrides ~/.ssh/config.
	SSHConfigFile string
	// KnownHostsFile overrides ~/.ssh/known_hosts.
	KnownHostsFile string
}

// Addr is the jump host's dial address after resolution.
func (c Config) Addr() string {
	port := c.Port
	if port == 0 {
		port = DefaultPort
	}
	return net.JoinHostPort(c.Host, strconv.Itoa(port))
}

// Tunnel is a live SSH connection plus every channel opened through it. Close
// tears down both, in that order, so no forwarded socket outlives the tunnel.
type Tunnel struct {
	client *ssh.Client
	addr   string

	mu      sync.Mutex
	conns   map[*trackedConn]struct{}
	extra   []io.Closer // agent socket, if any
	closed  bool
	closeCh chan struct{}
}

// ErrTunnelClosed is returned by DialContext after Close.
var ErrTunnelClosed = errors.New("sshtunnel: tunnel is closed")

// Open resolves the config, authenticates against the jump host and returns a
// live tunnel. An unknown host key produces an *UnknownHostKeyError, which the
// UI turns into a prompt; a changed host key produces a *HostKeyMismatchError,
// which is never acceptable automatically.
func Open(ctx context.Context, cfg Config) (*Tunnel, error) {
	resolved, err := Resolve(cfg)
	if err != nil {
		return nil, err
	}
	auths, extra, err := resolved.authMethods()
	if err != nil {
		return nil, err
	}
	// Everything below can fail; the agent socket must not leak when it does.
	closeExtra := func() {
		for _, c := range extra {
			c.Close()
		}
	}
	checker, err := resolved.hostKeyChecker()
	if err != nil {
		closeExtra()
		return nil, err
	}

	clientCfg := &ssh.ClientConfig{
		User:            resolved.User,
		Auth:            auths,
		HostKeyCallback: checker.callback,
		Timeout:         dialTimeout,
	}

	addr := resolved.Addr()
	var d net.Dialer
	raw, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		closeExtra()
		return nil, fmt.Errorf("sshtunnel: dial %s: %w", addr, err)
	}
	// The handshake has no context of its own, so it borrows the caller's
	// deadline through the socket.
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(dialTimeout)
	}
	raw.SetDeadline(deadline)

	sshConn, chans, reqs, err := ssh.NewClientConn(raw, addr, clientCfg)
	if err != nil {
		raw.Close()
		closeExtra()
		// A rejected host key surfaces as the typed error the UI can act on,
		// whatever wrapping the handshake put around it.
		if captured := checker.captured(); captured != nil {
			return nil, captured
		}
		return nil, fmt.Errorf("sshtunnel: connect %s: %w", addr, err)
	}
	raw.SetDeadline(time.Time{})

	t := &Tunnel{
		client:  ssh.NewClient(sshConn, chans, reqs),
		addr:    addr,
		conns:   map[*trackedConn]struct{}{},
		extra:   extra,
		closeCh: make(chan struct{}),
	}
	return t, nil
}

// Addr is the jump host this tunnel is connected to.
func (t *Tunnel) Addr() string { return t.addr }

// DialContext opens a forwarded connection to addr as seen from the jump
// host. Its signature is what both mysql.RegisterDialContext and pgx's
// DialFunc expect.
func (t *Tunnel) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil, ErrTunnelClosed
	}
	t.mu.Unlock()

	// The ssh package only forwards TCP; a driver asking for anything else
	// would otherwise fail deep inside the channel open.
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, fmt.Errorf("sshtunnel: cannot forward network %q", network)
	}
	c, err := t.client.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("sshtunnel: forward to %s via %s: %w", addr, t.addr, err)
	}
	tc := &trackedConn{Conn: c, tunnel: t}

	t.mu.Lock()
	if t.closed {
		// Close raced us; do not hand out a conn nobody will ever close.
		t.mu.Unlock()
		c.Close()
		return nil, ErrTunnelClosed
	}
	t.conns[tc] = struct{}{}
	t.mu.Unlock()
	return tc, nil
}

// Close shuts every forwarded connection, then the SSH connection itself, and
// waits for the client's reader goroutines to exit. It is safe to call more
// than once.
func (t *Tunnel) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		<-t.closeCh
		return nil
	}
	t.closed = true
	conns := make([]*trackedConn, 0, len(t.conns))
	for c := range t.conns {
		conns = append(conns, c)
	}
	t.conns = map[*trackedConn]struct{}{}
	extra := t.extra
	t.extra = nil
	t.mu.Unlock()

	for _, c := range conns {
		c.Conn.Close()
	}
	err := t.client.Close()
	// Wait returns once the transport loop has stopped, which is what makes
	// teardown observable instead of eventually-consistent.
	t.client.Wait()
	for _, c := range extra {
		c.Close()
	}
	close(t.closeCh)
	if err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, io.EOF) {
		return fmt.Errorf("sshtunnel: close %s: %w", t.addr, err)
	}
	return nil
}

// trackedConn removes itself from the tunnel's set when it is closed, so a
// long-lived tunnel does not accumulate dead entries and Close has an exact
// list of what is still open.
type trackedConn struct {
	net.Conn
	tunnel *Tunnel
	once   sync.Once
}

func (c *trackedConn) Close() error {
	c.once.Do(func() {
		c.tunnel.mu.Lock()
		delete(c.tunnel.conns, c)
		c.tunnel.mu.Unlock()
	})
	return c.Conn.Close()
}

// openConns reports how many forwarded connections are still open. Tests use
// it to prove teardown; nothing in the app depends on it.
func (t *Tunnel) openConns() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.conns)
}
