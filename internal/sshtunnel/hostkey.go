package sshtunnel

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// UnknownHostKeyError reports that the jump host is not in known_hosts. It is
// deliberately a hard failure: lazysql never accepts a key silently. The UI
// shows the fingerprint in a modal and, only on explicit confirmation, calls
// AcceptHostKey and dials again.
type UnknownHostKeyError struct {
	// Address is the "host:port" form that gets written to known_hosts.
	Address string
	// KeyType is the key algorithm, e.g. "ssh-ed25519".
	KeyType string
	// Fingerprint is the SHA256 fingerprint, the form ssh(1) prints.
	Fingerprint string
	// File is the known_hosts file the key would be appended to.
	File string

	key ssh.PublicKey
}

func (e *UnknownHostKeyError) Error() string {
	return fmt.Sprintf("sshtunnel: host %s is not in %s (%s key fingerprint %s)",
		e.Address, e.File, e.KeyType, e.Fingerprint)
}

// HostKeyMismatchError reports that the jump host presented a different key
// than the one recorded in known_hosts. This is what a man-in-the-middle looks
// like, so there is no accept path for it: the user has to fix known_hosts.
type HostKeyMismatchError struct {
	Address     string
	KeyType     string
	Fingerprint string
	File        string
	// KnownFiles/KnownLines locate the recorded entries that disagree.
	KnownFiles []string
	KnownLines []int
}

func (e *HostKeyMismatchError) Error() string {
	where := ""
	for i, f := range e.KnownFiles {
		if i < len(e.KnownLines) {
			where += fmt.Sprintf(" %s:%d", f, e.KnownLines[i])
		}
	}
	return fmt.Sprintf("sshtunnel: HOST KEY CHANGED for %s — %s key %s does not match%s",
		e.Address, e.KeyType, e.Fingerprint, where)
}

// hostKeyChecker wraps the known_hosts callback so the reason a handshake
// failed survives whatever the ssh package wraps around it.
type hostKeyChecker struct {
	inner ssh.HostKeyCallback
	file  string

	mu  sync.Mutex
	err error
}

// hostKeyChecker builds the callback for this config. A missing known_hosts
// file is not an error — every host is then simply unknown, and the first
// connection prompts.
func (c Config) hostKeyChecker() (*hostKeyChecker, error) {
	path, err := c.knownHostsPath()
	if err != nil {
		return nil, err
	}
	hk := &hostKeyChecker{file: path}
	if _, statErr := os.Stat(path); statErr == nil {
		inner, err := knownhosts.New(path)
		if err != nil {
			return nil, fmt.Errorf("sshtunnel: read %s: %w", path, err)
		}
		hk.inner = inner
	}
	return hk, nil
}

func (hk *hostKeyChecker) callback(hostname string, remote net.Addr, key ssh.PublicKey) error {
	err := hk.check(hostname, remote, key)
	if err != nil {
		hk.mu.Lock()
		hk.err = err
		hk.mu.Unlock()
	}
	return err
}

func (hk *hostKeyChecker) check(hostname string, remote net.Addr, key ssh.PublicKey) error {
	addr := knownhosts.Normalize(hostname)
	if hk.inner == nil {
		return hk.unknown(addr, key)
	}
	err := hk.inner(hostname, remote, key)
	if err == nil {
		return nil
	}
	var keyErr *knownhosts.KeyError
	if errors.As(err, &keyErr) {
		if len(keyErr.Want) == 0 {
			return hk.unknown(addr, key)
		}
		mismatch := &HostKeyMismatchError{
			Address:     addr,
			KeyType:     key.Type(),
			Fingerprint: ssh.FingerprintSHA256(key),
			File:        hk.file,
		}
		for _, w := range keyErr.Want {
			mismatch.KnownFiles = append(mismatch.KnownFiles, w.Filename)
			mismatch.KnownLines = append(mismatch.KnownLines, w.Line)
		}
		return mismatch
	}
	return err
}

func (hk *hostKeyChecker) unknown(addr string, key ssh.PublicKey) error {
	return &UnknownHostKeyError{
		Address:     addr,
		KeyType:     key.Type(),
		Fingerprint: ssh.FingerprintSHA256(key),
		File:        hk.file,
		key:         key,
	}
}

// captured returns the host key error the callback recorded, if any.
func (hk *hostKeyChecker) captured() error {
	hk.mu.Lock()
	defer hk.mu.Unlock()
	var unknown *UnknownHostKeyError
	var mismatch *HostKeyMismatchError
	if errors.As(hk.err, &unknown) {
		return unknown
	}
	if errors.As(hk.err, &mismatch) {
		return mismatch
	}
	return nil
}

// AcceptHostKey appends the rejected key to known_hosts. It is only ever
// called after the user confirmed the fingerprint in a modal — there is no
// code path that reaches it on its own.
func AcceptHostKey(e *UnknownHostKeyError) error {
	if e == nil || e.key == nil {
		return errors.New("sshtunnel: no host key to accept")
	}
	if err := os.MkdirAll(filepath.Dir(e.File), 0o700); err != nil {
		return fmt.Errorf("sshtunnel: create %s: %w", filepath.Dir(e.File), err)
	}
	f, err := os.OpenFile(e.File, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("sshtunnel: open %s: %w", e.File, err)
	}
	defer f.Close()
	line := knownhosts.Line([]string{e.Address}, e.key)
	// A file that does not end in a newline would otherwise glue the new
	// entry onto the last one.
	if needsNewline(e.File) {
		line = "\n" + line
	}
	if _, err := f.WriteString(line + "\n"); err != nil {
		return fmt.Errorf("sshtunnel: append to %s: %w", e.File, err)
	}
	return nil
}

func needsNewline(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 1)
	if _, err := f.ReadAt(buf, info.Size()-1); err != nil {
		return false
	}
	return buf[0] != '\n'
}

// knownHostsPath is the file host keys are checked against.
func (c Config) knownHostsPath() (string, error) {
	if c.KnownHostsFile != "" {
		return expandHome(c.KnownHostsFile), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("sshtunnel: locate home directory: %w", err)
	}
	return filepath.Join(home, ".ssh", "known_hosts"), nil
}

// expandHome resolves a leading `~/` against the user's home directory, which
// is how both ~/.ssh/config and lazysql's own form spell key paths.
func expandHome(path string) string {
	if path == "" || !strings.HasPrefix(path, "~") {
		return path
	}
	if path != "~" && !strings.HasPrefix(path, "~/") {
		// ~user/… is not resolvable without the user database; leave it be.
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
}
