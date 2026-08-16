package secrets

import (
	"errors"
	"os"
	"testing"

	"github.com/zalando/go-keyring"
)

// The mock keyring keeps the suite off the developer's real login keychain.
func TestMain(m *testing.M) {
	keyring.MockInit()
	os.Exit(m.Run())
}

func TestSetGetDelete(t *testing.T) {
	const name = "roundtrip"
	if err := Set(name, "hunter2"); err != nil {
		t.Fatal(err)
	}
	got, err := Get(name)
	if err != nil {
		t.Fatal(err)
	}
	if got != "hunter2" {
		t.Fatalf("Get = %q, want %q", got, "hunter2")
	}
	if err := Delete(name); err != nil {
		t.Fatal(err)
	}
	if _, err := Get(name); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after Delete = %v, want ErrNotFound", err)
	}
}

// Deleting a connection that never stored a password must succeed, so the
// delete flow does not fail on file-based profiles.
func TestDeleteMissingIsNotAnError(t *testing.T) {
	if err := Delete("never-existed"); err != nil {
		t.Fatalf("Delete of a missing entry = %v, want nil", err)
	}
}

func TestRenameMovesTheSecret(t *testing.T) {
	if err := Set("old", "pw"); err != nil {
		t.Fatal(err)
	}
	if err := Rename("old", "new"); err != nil {
		t.Fatal(err)
	}
	if got, err := Get("new"); err != nil || got != "pw" {
		t.Fatalf("Get(new) = %q, %v", got, err)
	}
	if _, err := Get("old"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old entry survived the rename: %v", err)
	}
	// Renaming a connection with no stored password is a no-op.
	if err := Rename("nothing-here", "elsewhere"); err != nil {
		t.Fatalf("Rename without a stored secret = %v, want nil", err)
	}
}

// A connection owns two secrets: the database password and the SSH one. They
// live in separate keyring slots and must move and disappear together.
func TestSSHSecretIsSeparateFromThePassword(t *testing.T) {
	const name = "with-tunnel"
	if err := Set(name, "db-pw"); err != nil {
		t.Fatal(err)
	}
	if err := Set(SSHKey(name), "ssh-pw"); err != nil {
		t.Fatal(err)
	}
	if got, _ := Get(name); got != "db-pw" {
		t.Fatalf("password = %q, want db-pw", got)
	}
	if got, _ := Get(SSHKey(name)); got != "ssh-pw" {
		t.Fatalf("ssh secret = %q, want ssh-pw", got)
	}
	if err := Delete(name); err != nil {
		t.Fatal(err)
	}
	if got, _ := Get(SSHKey(name)); got != "ssh-pw" {
		t.Fatal("deleting the password took the SSH secret with it")
	}
	if err := Forget(name); err != nil {
		t.Fatal(err)
	}
	if _, err := Get(SSHKey(name)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after Forget = %v, want ErrNotFound", err)
	}
}

func TestCopyDuplicatesBothSecrets(t *testing.T) {
	if err := Set("orig", "db-pw"); err != nil {
		t.Fatal(err)
	}
	if err := Set(SSHKey("orig"), "ssh-pw"); err != nil {
		t.Fatal(err)
	}
	if err := Copy("orig", "dup"); err != nil {
		t.Fatal(err)
	}
	if got, _ := Get("dup"); got != "db-pw" {
		t.Fatalf("password after copy = %q", got)
	}
	if got, _ := Get(SSHKey("dup")); got != "ssh-pw" {
		t.Fatalf("ssh secret after copy = %q", got)
	}
	// The source entries must survive the copy.
	if got, _ := Get("orig"); got != "db-pw" {
		t.Fatalf("source password after copy = %q, want unchanged", got)
	}
	if got, _ := Get(SSHKey("orig")); got != "ssh-pw" {
		t.Fatalf("source ssh secret after copy = %q, want unchanged", got)
	}
	if err := Forget("orig"); err != nil {
		t.Fatal(err)
	}
	if err := Forget("dup"); err != nil {
		t.Fatal(err)
	}
}

// Copying a connection that never stored a password must succeed, leaving
// the destination without one too.
func TestCopyMissingIsNotAnError(t *testing.T) {
	if err := Copy("never-existed", "also-never"); err != nil {
		t.Fatalf("Copy of a missing entry = %v, want nil", err)
	}
	if _, err := Get("also-never"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(also-never) = %v, want ErrNotFound", err)
	}
}

func TestRenameMovesBothSecrets(t *testing.T) {
	if err := Set("old", "db-pw"); err != nil {
		t.Fatal(err)
	}
	if err := Set(SSHKey("old"), "ssh-pw"); err != nil {
		t.Fatal(err)
	}
	if err := Rename("old", "new"); err != nil {
		t.Fatal(err)
	}
	if got, _ := Get("new"); got != "db-pw" {
		t.Fatalf("password after rename = %q", got)
	}
	if got, _ := Get(SSHKey("new")); got != "ssh-pw" {
		t.Fatalf("ssh secret after rename = %q", got)
	}
	if _, err := Get(SSHKey("old")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the old SSH slot survived the rename: %v", err)
	}
	if err := Forget("new"); err != nil {
		t.Fatal(err)
	}
}
