package sshtunnel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
)

// Everything lazysql itself runs through a tunnel uses Tunnel.DialContext:
// the SQL driver is handed a transport and never learns a jump host is
// involved (see wiki/design/ssh-tunnels.md). An external tool cannot be
// handed a Go dialer, so a dump or restore needs the one thing the rest of
// the app deliberately avoids — a real local socket. Forward is that
// exception, and it exists only for the lifetime of one child process.

// Forward is a local TCP listener whose every accepted connection is
// spliced onto a channel opened through the tunnel to remote. It is the
// `ssh -L` half of the tunnel, used only where a real address is needed.
type Forward struct {
	listener net.Listener
	remote   string

	// ctx is cancelled by Close, so a channel open still in flight does
	// not outlive the forward it belongs to.
	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	closed  bool
	conns   map[net.Conn]struct{}
	wg      sync.WaitGroup
	lastErr error
}

// Listen opens a local listener on 127.0.0.1 with a port picked by the OS
// and forwards it to remote ("host:port" as seen from the jump host).
// Close stops the listener and every connection it accepted.
func (t *Tunnel) Listen(remote string) (*Forward, error) {
	if _, _, err := net.SplitHostPort(remote); err != nil {
		return nil, fmt.Errorf("sshtunnel: forward target %q: %w", remote, err)
	}
	// Loopback only: the forwarded port speaks for the jump host's network
	// and must never be reachable from outside this machine.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("sshtunnel: listen for %s: %w", remote, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	f := &Forward{
		listener: ln, remote: remote, conns: map[net.Conn]struct{}{},
		ctx: ctx, cancel: cancel,
	}
	f.wg.Add(1)
	go f.accept(t)
	return f, nil
}

// Addr is the local "127.0.0.1:port" an external tool connects to.
func (f *Forward) Addr() string { return f.listener.Addr().String() }

// Port is Addr's port alone, for tools that take host and port separately.
func (f *Forward) Port() int {
	addr, ok := f.listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0
	}
	return addr.Port
}

// Err reports the last forwarding failure, if any. A tool that could not
// connect gets its own error; this is what says the tunnel, rather than
// the tool, is why.
func (f *Forward) Err() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastErr
}

func (f *Forward) accept(t *Tunnel) {
	defer f.wg.Done()
	for {
		local, err := f.listener.Accept()
		if err != nil {
			return
		}
		f.mu.Lock()
		if f.closed {
			f.mu.Unlock()
			local.Close()
			return
		}
		f.conns[local] = struct{}{}
		f.mu.Unlock()

		f.wg.Add(1)
		go func() {
			defer f.wg.Done()
			f.splice(t, local)
		}()
	}
}

// splice opens the tunnelled connection for one accepted client and copies
// in both directions until either side is done.
func (f *Forward) splice(t *Tunnel, local net.Conn) {
	defer func() {
		f.mu.Lock()
		delete(f.conns, local)
		f.mu.Unlock()
		local.Close()
	}()

	remote, err := t.DialContext(f.ctx, "tcp", f.remote)
	if err != nil {
		f.mu.Lock()
		f.lastErr = err
		f.mu.Unlock()
		return
	}
	defer remote.Close()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); copyHalf(remote, local) }()
	go func() { defer wg.Done(); copyHalf(local, remote) }()
	wg.Wait()
}

// copyHalf copies one direction and then half-closes the destination, so
// a client that stops writing (psql reading a dump from stdin) makes the
// server see EOF rather than hanging.
func copyHalf(dst, src net.Conn) {
	io.Copy(dst, src)
	type closeWriter interface{ CloseWrite() error }
	if cw, ok := dst.(closeWriter); ok {
		cw.CloseWrite()
		return
	}
	dst.Close()
}

// Close stops accepting, tears down every open connection and waits for
// the copy goroutines to finish. It is safe to call more than once.
func (f *Forward) Close() error {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		f.wg.Wait()
		return nil
	}
	f.closed = true
	f.cancel()
	conns := make([]net.Conn, 0, len(f.conns))
	for c := range f.conns {
		conns = append(conns, c)
	}
	f.mu.Unlock()

	err := f.listener.Close()
	for _, c := range conns {
		c.Close()
	}
	f.wg.Wait()
	if err != nil && !errors.Is(err, net.ErrClosed) {
		return fmt.Errorf("sshtunnel: close forward %s: %w", f.remote, err)
	}
	return nil
}
