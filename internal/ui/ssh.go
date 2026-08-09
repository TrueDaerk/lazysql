package ui

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/config"
	"lazysql/internal/sshtunnel"
)

// The SSH section of the connection form. It is appended to the same
// formModal the rest of the profile lives in — one popup, not a wizard — and
// every field below the `ssh` toggle is hidden while the toggle is off. The
// whole section is hidden for SQLite and DuckDB: there is no socket to
// forward to a local file.

// sshEnabled is the visibility predicate for the fields under the toggle.
func sshEnabled(f *formModal) bool {
	return isServerEngine(f) && f.rawValue("ssh") == "true"
}

func sshAuthIs(method sshtunnel.AuthMethod) func(*formModal) bool {
	return func(f *formModal) bool {
		return sshEnabled(f) && sshtunnel.AuthMethod(f.rawValue("ssh_auth")) == method
	}
}

// sshNeedsSecret is true for the two auth methods that can carry a secret:
// a password, or the passphrase of an encrypted key.
func sshNeedsSecret(f *formModal) bool {
	if !sshEnabled(f) {
		return false
	}
	switch sshtunnel.AuthMethod(f.rawValue("ssh_auth")) {
	case sshtunnel.AuthPassword, sshtunnel.AuthKey:
		return true
	}
	return false
}

// sshFields builds the SSH section for a profile.
func sshFields(c config.Connection, oldName string) []*formField {
	s := config.SSH{Auth: string(sshtunnel.AuthAgent)}
	if c.SSH != nil {
		s = *c.SSH
	}
	if s.Auth == "" {
		s.Auth = string(sshtunnel.AuthAgent)
	}
	port := ""
	if s.Port > 0 {
		port = strconv.Itoa(s.Port)
	}

	var authLabels, authValues []string
	for _, a := range sshtunnel.AuthMethods() {
		authLabels = append(authLabels, sshtunnel.AuthLabel(a))
		authValues = append(authValues, string(a))
	}

	return []*formField{
		newBoolField("ssh", "SSH tunnel", s.Enabled).
			withHelp("dial the database through a jump host").
			withVisible(isServerEngine),
		newTextField("ssh_host", "SSH host", s.Host, "bastion.example.com").
			withHelp("a ~/.ssh/config alias works too").
			withVisible(sshEnabled),
		newTextField("ssh_port", "SSH port", port, "22").withVisible(sshEnabled),
		newTextField("ssh_user", "SSH user", s.User, "from ssh config").
			withVisible(sshEnabled),
		newSelectField("ssh_auth", "SSH auth", authLabels, authValues, s.Auth).
			withVisible(sshEnabled),
		newTextField("ssh_key", "Key file", s.KeyFile, "~/.ssh/id_ed25519").
			withHelp("empty = IdentityFile from ssh config").
			withVisible(sshAuthIs(sshtunnel.AuthKey)),
		newPasswordField("ssh_secret", "SSH secret", passwordPlaceholder(c, oldName)).
			withHelp("SSH password, or the key passphrase — kept in the OS keyring").
			withVisible(sshNeedsSecret),
	}
}

// toSSH reads the SSH section back out of the form. It returns nil when the
// section was never filled in, so a plain connection keeps a plain config
// entry instead of an empty `[connections.ssh]` table.
func (f *formModal) toSSH() (*config.SSH, error) {
	enabled := f.rawValue("ssh") == "true"
	s := config.SSH{
		Enabled: enabled,
		Host:    strings.TrimSpace(f.rawValue("ssh_host")),
		User:    strings.TrimSpace(f.rawValue("ssh_user")),
		Auth:    f.rawValue("ssh_auth"),
		KeyFile: strings.TrimSpace(f.rawValue("ssh_key")),
	}
	if p := strings.TrimSpace(f.rawValue("ssh_port")); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("SSH port %q is not a number", p)
		}
		if n < 1 || n > 65535 {
			return nil, fmt.Errorf("SSH port %d out of range 1-65535", n)
		}
		s.Port = n
	}
	if !enabled && s.Host == "" && s.User == "" && s.KeyFile == "" && s.Port == 0 {
		return nil, nil
	}
	return &s, nil
}

// ---------- dial-time prompts ----------

// tunnelSuffix is what the command log appends to a redacted DSN so the log
// says which jump host carried the connection.
func tunnelSuffix(c config.Connection) string {
	if !c.UsesSSH() {
		return ""
	}
	target := c.SSH.Host
	if c.SSH.User != "" {
		target = c.SSH.User + "@" + target
	}
	if c.SSH.Port > 0 {
		target = fmt.Sprintf("%s:%d", target, c.SSH.Port)
	}
	return fmt.Sprintf("[via ssh %s, %s auth]", target, sshtunnel.AuthLabel(sshtunnel.AuthMethod(c.SSH.Auth)))
}

// sshFailureModal turns a failed dial into the right follow-up popup, or nil
// when the failure is not one the user can answer.
//
// Three SSH failures are recoverable in place:
//   - an unknown host key: show the fingerprint and, only on confirmation,
//     append it to known_hosts and dial again;
//   - a passphrase-protected key with no passphrase;
//   - password auth with nothing in the keyring.
//
// A changed host key is deliberately not in that list: it is what a
// man-in-the-middle looks like, so lazysql refuses and says where to look.
func sshFailureModal(req dialRequest, err error) modal {
	var unknown *sshtunnel.UnknownHostKeyError
	if errors.As(err, &unknown) {
		return &confirmModal{
			title: "Unknown SSH host key",
			body: fmt.Sprintf(
				"%s presented a %s key with fingerprint\n\n  %s\n\n"+
					"Nothing vouches for it. Accept and append it to %s?",
				unknown.Address, unknown.KeyType, unknown.Fingerprint, unknown.File),
			danger: true,
			onConfirm: func(m *Model) tea.Cmd {
				if err := sshtunnel.AcceptHostKey(unknown); err != nil {
					return logCmd("-- ssh %s FAILED: %v", req.conn.Name, err)
				}
				return tea.Batch(
					logCmd("-- ssh host key for %s added to %s (%s)",
						unknown.Address, unknown.File, unknown.Fingerprint),
					redialCmd(req),
				)
			},
		}
	}

	var mismatch *sshtunnel.HostKeyMismatchError
	if errors.As(err, &mismatch) {
		// Refused, not offered: there is no confirm action on this modal.
		return &confirmModal{
			title:  "SSH host key changed",
			body:   mismatch.Error() + "\n\nlazysql will not connect. Remove the stale entry from known_hosts if this change is expected.",
			danger: true,
		}
	}

	if needsSSHSecretPrompt(req, err) {
		title := fmt.Sprintf("SSH password for %s", req.conn.SSH.Host)
		if errors.Is(err, sshtunnel.ErrPassphraseRequired) {
			title = fmt.Sprintf("Key passphrase for %s", req.conn.SSH.Host)
		}
		return newSecretPrompt(title, func(secret string) tea.Cmd {
			next := req
			next.sshSecret, next.hasSSHSecret = secret, true
			return redialCmd(next)
		})
	}
	return nil
}

// needsSSHSecretPrompt reports whether the failure was "no secret to try".
func needsSSHSecretPrompt(req dialRequest, err error) bool {
	if req.hasSSHSecret || !req.conn.NeedsSSHSecret() {
		return false
	}
	if errors.Is(err, sshtunnel.ErrPassphraseRequired) {
		return true
	}
	// The password method reports its own missing-secret case; matching on
	// the message keeps that string in one place, the sshtunnel package.
	return sshtunnel.AuthMethod(req.conn.SSH.Auth) == sshtunnel.AuthPassword &&
		strings.Contains(err.Error(), "no password given")
}

// redialCmd repeats a dial attempt, test or connect, exactly as it was.
func redialCmd(req dialRequest) tea.Cmd {
	if req.test {
		return tea.Batch(logCmd("-- test %s …", req.conn.Name), testConnCmd(req))
	}
	return tea.Batch(logCmd("-- connecting %s …", req.conn.Name), connectCmd(req))
}
