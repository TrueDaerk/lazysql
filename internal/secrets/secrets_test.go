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
