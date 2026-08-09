package sshtunnel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSSHConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write ssh config: %v", err)
	}
	return path
}

const aliasConfig = `
Host bastion
  HostName jump.example.com
  User deploy
  Port 2222
  IdentityFile ~/.ssh/id_deploy

Host *
  User fallback
`

func TestResolveExpandsHostAlias(t *testing.T) {
	cfg := Config{Host: "bastion", Auth: AuthKey, SSHConfigFile: writeSSHConfig(t, aliasConfig)}
	got, err := Resolve(cfg)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Host != "jump.example.com" {
		t.Errorf("Host = %q, want jump.example.com", got.Host)
	}
	if got.User != "deploy" {
		t.Errorf("User = %q, want deploy", got.User)
	}
	if got.Port != 2222 {
		t.Errorf("Port = %d, want 2222", got.Port)
	}
	if strings.HasPrefix(got.KeyFile, "~") {
		t.Errorf("KeyFile = %q, want the ~ expanded", got.KeyFile)
	}
	if !strings.HasSuffix(got.KeyFile, filepath.Join(".ssh", "id_deploy")) {
		t.Errorf("KeyFile = %q, want the alias IdentityFile", got.KeyFile)
	}
}

// The profile is the source of truth: an alias only fills in blanks.
func TestResolveProfileWins(t *testing.T) {
	cfg := Config{
		Host:          "bastion",
		Port:          2200,
		User:          "root",
		Auth:          AuthKey,
		KeyFile:       "/keys/explicit",
		SSHConfigFile: writeSSHConfig(t, aliasConfig),
	}
	got, err := Resolve(cfg)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// HostName is the one thing an alias always supplies: the alias is not
	// a resolvable name by itself.
	if got.Host != "jump.example.com" {
		t.Errorf("Host = %q, want jump.example.com", got.Host)
	}
	if got.Port != 2200 || got.User != "root" || got.KeyFile != "/keys/explicit" {
		t.Errorf("alias overrode the profile: %+v", got)
	}
}

func TestResolveFallsBackToDefaults(t *testing.T) {
	cfg := Config{Host: "db.internal", User: "u", SSHConfigFile: filepath.Join(t.TempDir(), "absent")}
	got, err := Resolve(cfg)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Host != "db.internal" {
		t.Errorf("Host = %q, want it untouched", got.Host)
	}
	if got.Port != DefaultPort {
		t.Errorf("Port = %d, want %d", got.Port, DefaultPort)
	}
}

// A `Host *` block still supplies a user when nothing more specific matches.
func TestResolveUsesWildcardBlock(t *testing.T) {
	cfg := Config{Host: "anything.example.com", SSHConfigFile: writeSSHConfig(t, aliasConfig)}
	got, err := Resolve(cfg)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.User != "fallback" {
		t.Errorf("User = %q, want fallback", got.User)
	}
}

func TestResolveRejectsEmptyHost(t *testing.T) {
	if _, err := Resolve(Config{}); err == nil {
		t.Fatal("Resolve accepted an empty host")
	}
}

func TestResolveRejectsBadConfigPort(t *testing.T) {
	cfg := Config{Host: "weird", SSHConfigFile: writeSSHConfig(t, "Host weird\n  Port nonsense\n")}
	if _, err := Resolve(cfg); err == nil {
		t.Fatal("Resolve accepted a non-numeric Port")
	}
}

func TestAddrUsesDefaultPort(t *testing.T) {
	if got := (Config{Host: "h"}).Addr(); got != "h:22" {
		t.Fatalf("Addr = %q, want h:22", got)
	}
}
