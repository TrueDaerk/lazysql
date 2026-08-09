// Package sshtest runs a real SSH jump host in-process for tests.
//
// It exists because the interesting parts of lazysql's SSH support — host key
// enforcement, teardown, and a database driver actually speaking its protocol
// through a forwarded channel — cannot be checked against a stub. The server
// here does the real handshake and honours direct-tcpip channel opens; tests
// point it at whatever they want on the far side.
package sshtest

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// Server is an SSH jump host listening on 127.0.0.1.
type Server struct {
	ln      net.Listener
	hostKey ssh.Signer
	config  *ssh.ServerConfig

	wg       sync.WaitGroup
	mu       sync.Mutex
	sessions []*ssh.ServerConn
	forwards int
}

// New starts a jump host. authorize fills in the authentication callbacks;
// the host key is generated per server. The server is stopped on test cleanup.
func New(t *testing.T, authorize func(*ssh.ServerConfig)) *Server {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("sshtest: generate host key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("sshtest: host signer: %v", err)
	}
	cfg := &ssh.ServerConfig{}
	authorize(cfg)
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("sshtest: listen: %v", err)
	}
	s := &Server{ln: ln, hostKey: signer, config: cfg}
	s.wg.Add(1)
	go s.serve()
	t.Cleanup(s.Stop)
	return s
}

// NewPassword starts a jump host that accepts exactly one user/password pair.
func NewPassword(t *testing.T, user, password string) *Server {
	return New(t, func(cfg *ssh.ServerConfig) {
		cfg.PasswordCallback = func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if c.User() == user && string(pass) == password {
				return nil, nil
			}
			return nil, errors.New("sshtest: denied")
		}
	})
}

// NewPublicKey starts a jump host that accepts one public key.
func NewPublicKey(t *testing.T, authorized ssh.PublicKey) *Server {
	want := string(authorized.Marshal())
	return New(t, func(cfg *ssh.ServerConfig) {
		cfg.PublicKeyCallback = func(c ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if string(key.Marshal()) == want {
				return nil, nil
			}
			return nil, errors.New("sshtest: denied")
		}
	})
}

// Addr is the "host:port" the server listens on.
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Host is the listening address without the port.
func (s *Server) Host() string {
	h, _, _ := net.SplitHostPort(s.Addr())
	return h
}

// Port is the listening port.
func (s *Server) Port() int { return s.ln.Addr().(*net.TCPAddr).Port }

// PublicKey is the server's host key.
func (s *Server) PublicKey() ssh.PublicKey { return s.hostKey.PublicKey() }

// KnownHostsLine is the known_hosts entry for this server's own address.
func (s *Server) KnownHostsLine() string { return s.KnownHostsLineFor(s.Addr()) }

// KnownHostsLineFor files this server's key under another address, which is
// how a changed-host-key situation is staged.
func (s *Server) KnownHostsLineFor(addr string) string {
	return knownhosts.Line([]string{knownhosts.Normalize(addr)}, s.PublicKey())
}

// Forwards is how many direct-tcpip channels the server has accepted.
func (s *Server) Forwards() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.forwards
}

// Stop closes the listener and every live session, then waits for the
// server's goroutines. It is idempotent.
func (s *Server) Stop() {
	s.ln.Close()
	s.mu.Lock()
	sessions := s.sessions
	s.sessions = nil
	s.mu.Unlock()
	for _, sc := range sessions {
		sc.Close()
	}
	s.wg.Wait()
}

func (s *Server) serve() {
	defer s.wg.Done()
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.wg.Add(1)
		go s.handle(c)
	}
}

func (s *Server) handle(c net.Conn) {
	defer s.wg.Done()
	sc, chans, reqs, err := ssh.NewServerConn(c, s.config)
	if err != nil {
		c.Close()
		return
	}
	s.mu.Lock()
	s.sessions = append(s.sessions, sc)
	s.mu.Unlock()

	go ssh.DiscardRequests(reqs)
	for newChan := range chans {
		if newChan.ChannelType() != "direct-tcpip" {
			newChan.Reject(ssh.UnknownChannelType, "sshtest: only direct-tcpip")
			continue
		}
		var payload struct {
			Host       string
			Port       uint32
			OriginHost string
			OriginPort uint32
		}
		if err := ssh.Unmarshal(newChan.ExtraData(), &payload); err != nil {
			newChan.Reject(ssh.ConnectionFailed, "sshtest: bad payload")
			continue
		}
		remote, err := net.Dial("tcp", net.JoinHostPort(payload.Host, strconv.Itoa(int(payload.Port))))
		if err != nil {
			newChan.Reject(ssh.ConnectionFailed, err.Error())
			continue
		}
		ch, chReqs, err := newChan.Accept()
		if err != nil {
			remote.Close()
			continue
		}
		s.mu.Lock()
		s.forwards++
		s.mu.Unlock()
		go ssh.DiscardRequests(chReqs)
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			// The channel-to-target direction owns the teardown: once it
			// ends both sides are closed, which is what unblocks the
			// target-to-channel copy. Waiting on that copy instead would
			// hang on a peer that never closes its half.
			go func() {
				io.Copy(ch, remote)
				ch.CloseWrite()
			}()
			io.Copy(remote, ch)
			remote.Close()
			ch.Close()
		}()
	}
	sc.Close()
}

// ---------- helpers ----------

// TCPServer is a target behind the tunnel: every accepted connection is handed
// to serve, which owns closing it.
type TCPServer struct {
	ln net.Listener
	wg sync.WaitGroup
}

// NewTCPServer starts a target server on 127.0.0.1.
func NewTCPServer(t *testing.T, serve func(net.Conn)) *TCPServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("sshtest: listen: %v", err)
	}
	s := &TCPServer{ln: ln}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				defer c.Close()
				serve(c)
			}()
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		s.wg.Wait()
	})
	return s
}

// NewEcho starts a target that echoes whatever it reads, which is enough to
// prove a forwarded connection carries bytes both ways.
func NewEcho(t *testing.T) *TCPServer {
	return NewTCPServer(t, func(c net.Conn) { io.Copy(c, c) })
}

// Addr is the target's "host:port".
func (s *TCPServer) Addr() string { return s.ln.Addr().String() }

// Host and Port split Addr for callers that build a DSN.
func (s *TCPServer) Host() string {
	h, _, _ := net.SplitHostPort(s.Addr())
	return h
}

func (s *TCPServer) Port() int { return s.ln.Addr().(*net.TCPAddr).Port }

// NewKey writes a generated ed25519 private key to dir/name, optionally
// passphrase-protected, and returns its signer and path.
func NewKey(t *testing.T, dir, name, passphrase string) (ssh.Signer, string) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("sshtest: generate key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("sshtest: signer: %v", err)
	}
	var block *pem.Block
	if passphrase == "" {
		block, err = ssh.MarshalPrivateKey(priv, "")
	} else {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(passphrase))
	}
	if err != nil {
		t.Fatalf("sshtest: marshal key: %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("sshtest: write key: %v", err)
	}
	return signer, path
}

// WriteKnownHosts writes a known_hosts file in dir and returns its path.
func WriteKnownHosts(t *testing.T, dir string, lines ...string) string {
	t.Helper()
	path := filepath.Join(dir, "known_hosts")
	content := ""
	for _, l := range lines {
		content += l + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("sshtest: write known_hosts: %v", err)
	}
	return path
}
