// Package secrets stores connection passwords in the OS keyring. Nothing in
// lazysql writes a password to a config file or to the command log.
package secrets

import (
	"errors"
	"fmt"

	"github.com/zalando/go-keyring"
)

// Service is the keyring service name every lazysql entry is filed under.
// The keyring "user" is the connection name.
const Service = "lazysql"

// ErrNotFound reports that no password is stored for a connection.
var ErrNotFound = keyring.ErrNotFound

// ErrUnsupported reports that this machine has no usable keyring backend.
var ErrUnsupported = errors.New("secrets: no OS keyring available")

// Get returns the stored password for a connection. It returns ErrNotFound
// when nothing is stored, which callers treat as "no password", not a failure.
func Get(name string) (string, error) {
	pw, err := keyring.Get(Service, name)
	if err != nil {
		return "", wrap(err, name)
	}
	return pw, nil
}

// Set stores (or replaces) the password for a connection.
func Set(name, password string) error {
	if err := keyring.Set(Service, name, password); err != nil {
		return wrap(err, name)
	}
	return nil
}

// Delete removes the stored password. A missing entry is not an error, so
// deleting a connection that never had a password succeeds.
func Delete(name string) error {
	err := keyring.Delete(Service, name)
	if err == nil || errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return wrap(err, name)
}

// Rename moves a stored password to a new connection name. It is a no-op when
// no password is stored under the old name.
func Rename(oldName, newName string) error {
	if oldName == newName {
		return nil
	}
	pw, err := Get(oldName)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := Set(newName, pw); err != nil {
		return err
	}
	return Delete(oldName)
}

func wrap(err error, name string) error {
	switch {
	case errors.Is(err, keyring.ErrNotFound):
		return fmt.Errorf("secrets: no password stored for %q: %w", name, ErrNotFound)
	case errors.Is(err, keyring.ErrUnsupportedPlatform):
		return ErrUnsupported
	default:
		return fmt.Errorf("secrets: keyring access for %q: %w", name, err)
	}
}
