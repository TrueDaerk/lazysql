package sshtunnel

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// ErrPassphraseRequired reports that the selected key file is encrypted and no
// passphrase was supplied. The UI treats it like a missing password: prompt,
// then retry.
var ErrPassphraseRequired = errors.New("sshtunnel: private key is passphrase-protected")

// ErrNoAgent reports that agent auth was selected but no agent socket is
// reachable.
var ErrNoAgent = errors.New("sshtunnel: no ssh-agent socket (is SSH_AUTH_SOCK set?)")

// authMethods builds the ssh.AuthMethod list for the configured method. The
// returned closers must be closed when the tunnel is torn down: agent auth
// holds a unix socket open for as long as signing can happen.
func (c Config) authMethods() ([]ssh.AuthMethod, []io.Closer, error) {
	switch c.Auth {
	case AuthPassword:
		if c.Secret == "" {
			return nil, nil, errors.New("sshtunnel: password auth selected but no password given")
		}
		return []ssh.AuthMethod{ssh.Password(c.Secret)}, nil, nil

	case AuthKey:
		signer, err := c.keySigner()
		if err != nil {
			return nil, nil, err
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil, nil

	case AuthAgent, "":
		sock := c.AgentSocket
		if sock == "" {
			sock = os.Getenv("SSH_AUTH_SOCK")
		}
		if sock == "" {
			return nil, nil, ErrNoAgent
		}
		conn, err := net.Dial("unix", sock)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %v", ErrNoAgent, err)
		}
		client := agent.NewClient(conn)
		// Signers is deferred: the agent is queried at handshake time, so a
		// key added to the agent after lazysql started is still found.
		return []ssh.AuthMethod{ssh.PublicKeysCallback(client.Signers)}, []io.Closer{conn}, nil
	}
	return nil, nil, fmt.Errorf("sshtunnel: unknown auth method %q", c.Auth)
}

// keySigner loads the private key file, decrypting it when a passphrase was
// supplied. An encrypted key with no passphrase yields ErrPassphraseRequired
// rather than a generic parse failure, so the UI can tell the two apart.
func (c Config) keySigner() (ssh.Signer, error) {
	path := expandHome(c.KeyFile)
	if path == "" {
		return nil, errors.New("sshtunnel: key auth selected but no key file given")
	}
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("sshtunnel: read key %s: %w", path, err)
	}
	if c.Secret != "" {
		signer, err := ssh.ParsePrivateKeyWithPassphrase(pem, []byte(c.Secret))
		if err != nil {
			return nil, fmt.Errorf("sshtunnel: decrypt key %s: %w", path, err)
		}
		return signer, nil
	}
	signer, err := ssh.ParsePrivateKey(pem)
	if err != nil {
		var missing *ssh.PassphraseMissingError
		if errors.As(err, &missing) {
			return nil, fmt.Errorf("%w: %s", ErrPassphraseRequired, path)
		}
		return nil, fmt.Errorf("sshtunnel: parse key %s: %w", path, err)
	}
	return signer, nil
}
