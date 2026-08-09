package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lazysql/internal/db"
	"lazysql/internal/sshtunnel"
)

func TestSSHSectionRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := &Config{Connections: []Connection{
		{Name: "prod", Engine: db.EnginePostgres, Host: "10.0.0.5", Port: 5432, User: "app",
			SSH: &SSH{Enabled: true, Host: "bastion", Port: 2222, User: "deploy",
				Auth: string(sshtunnel.AuthKey), KeyFile: "~/.ssh/id_deploy"}},
		{Name: "direct", Engine: db.EngineMySQL, Host: "localhost", Port: 3306},
	}}
	if err := cfg.SaveTo(path); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "id_deploy") == false {
		t.Fatalf("config does not carry the key file:\n%s", data)
	}

	back, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	got := back.Connections[0].SSH
	if got == nil {
		t.Fatal("SSH section lost on reload")
	}
	if !got.Enabled || got.Host != "bastion" || got.Port != 2222 ||
		got.User != "deploy" || got.Auth != "key" || got.KeyFile != "~/.ssh/id_deploy" {
		t.Fatalf("SSH = %+v, want the saved section", *got)
	}
	if back.Connections[1].SSH != nil {
		t.Fatalf("a direct connection grew an SSH section: %+v", *back.Connections[1].SSH)
	}
}

// A tunnel on port 22 must not write `port = 0`, the same trap the database
// port has.
func TestSSHDefaultPortIsNotWritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := &Config{Connections: []Connection{
		{Name: "prod", Engine: db.EnginePostgres, Host: "h", Port: 5432,
			SSH: &SSH{Enabled: true, Host: "bastion", Auth: "agent"}},
	}}
	if err := cfg.SaveTo(path); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "port = 0") {
		t.Fatalf("config wrote a zero port:\n%s", data)
	}
}

func TestUsesSSHIgnoresFileEngines(t *testing.T) {
	tunnel := &SSH{Enabled: true, Host: "bastion", Auth: "agent"}
	if (Connection{Engine: db.EngineSQLite, SSH: tunnel}).UsesSSH() {
		t.Error("SQLite reported a tunnel")
	}
	if (Connection{Engine: db.EngineDuckDB, SSH: tunnel}).UsesSSH() {
		t.Error("DuckDB reported a tunnel")
	}
	if !(Connection{Engine: db.EngineMySQL, SSH: tunnel}).UsesSSH() {
		t.Error("MySQL did not report a tunnel")
	}
	if (Connection{Engine: db.EngineMySQL, SSH: &SSH{Host: "b"}}).UsesSSH() {
		t.Error("a disabled section reported a tunnel")
	}
}

func TestNeedsSSHSecret(t *testing.T) {
	for _, tc := range []struct {
		auth string
		want bool
	}{
		{"agent", false},
		{"password", true},
		{"key", true},
	} {
		c := Connection{Engine: db.EnginePostgres, SSH: &SSH{Enabled: true, Host: "b", Auth: tc.auth}}
		if got := c.NeedsSSHSecret(); got != tc.want {
			t.Errorf("NeedsSSHSecret(%s) = %v, want %v", tc.auth, got, tc.want)
		}
	}
}

func TestValidateRejectsBrokenSSHSection(t *testing.T) {
	base := Connection{Name: "n", Engine: db.EnginePostgres, Host: "h", Port: 5432}
	for name, ssh := range map[string]*SSH{
		"no host":      {Enabled: true, Auth: "agent"},
		"bad auth":     {Enabled: true, Host: "b", Auth: "magic"},
		"port too big": {Enabled: true, Host: "b", Auth: "agent", Port: 70000},
	} {
		c := base
		c.SSH = ssh
		if err := c.Validate(); err == nil {
			t.Errorf("%s: Validate accepted %+v", name, *ssh)
		}
	}
	// A disabled section is never validated: it is dormant state.
	c := base
	c.SSH = &SSH{Auth: "magic"}
	if err := c.Validate(); err != nil {
		t.Errorf("a disabled SSH section failed validation: %v", err)
	}
}

// Clone must deep-copy the section; the save goroutine works on the clone.
func TestCloneCopiesSSHSection(t *testing.T) {
	cfg := &Config{Connections: []Connection{
		{Name: "prod", Engine: db.EnginePostgres, Host: "h", Port: 5432,
			SSH: &SSH{Enabled: true, Host: "bastion", Auth: "agent"}},
	}}
	clone := cfg.Clone()
	clone.Connections[0].SSH.Host = "other"
	if cfg.Connections[0].SSH.Host != "bastion" {
		t.Fatal("Clone shared the SSH section with the original")
	}
}

func TestTunnelConfigCarriesTheSecret(t *testing.T) {
	c := Connection{Name: "prod", Engine: db.EnginePostgres, Host: "h", Port: 5432,
		SSH: &SSH{Enabled: true, Host: "bastion", Port: 2222, User: "deploy", Auth: "password"}}
	got := c.TunnelConfig("hunter2")
	if !got.Enabled || got.Host != "bastion" || got.Port != 2222 || got.User != "deploy" {
		t.Fatalf("TunnelConfig = %+v", got)
	}
	if got.Auth != sshtunnel.AuthPassword {
		t.Fatalf("Auth = %q, want password", got.Auth)
	}
	if got.Secret != "hunter2" {
		t.Fatalf("Secret = %q, want the supplied one", got.Secret)
	}
	// And a profile with no section produces an inert config.
	if (Connection{}).TunnelConfig("x").Enabled {
		t.Fatal("a profile without an SSH section produced an enabled tunnel")
	}
}
