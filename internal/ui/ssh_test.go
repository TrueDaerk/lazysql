package ui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/config"
	"lazysql/internal/db"
	"lazysql/internal/sshtunnel"
)

// fieldNames lists the visible fields of a form, in order.
func fieldNames(f *formModal) []string {
	out := make([]string, 0, len(f.fields))
	for _, fl := range f.visibleFields() {
		out = append(out, fl.name)
	}
	return out
}

func hasField(f *formModal, name string) bool {
	for _, n := range fieldNames(f) {
		if n == name {
			return true
		}
	}
	return false
}

// setSelect moves a select field to a named value.
func setSelect(t *testing.T, f *formModal, name, value string) {
	t.Helper()
	fl := f.field(name)
	if fl == nil {
		t.Fatalf("no field %q", name)
	}
	for i, v := range fl.values {
		if v == value {
			fl.choice = i
			return
		}
	}
	t.Fatalf("field %q has no value %q", name, value)
}

func TestSSHSectionHiddenForFileEngines(t *testing.T) {
	f := newConnectionForm("New", config.Connection{Engine: db.EngineMySQL}, "")
	if !hasField(f, "ssh") {
		t.Fatalf("MySQL form has no SSH toggle: %v", fieldNames(f))
	}
	// A file engine's form is built without the section entirely.
	for _, engine := range []db.Engine{db.EngineSQLite, db.EngineDuckDB} {
		f := newConnectionForm("New", config.Connection{Engine: engine}, "")
		if hasField(f, "ssh") {
			t.Errorf("%s form shows the SSH toggle: %v", engine, fieldNames(f))
		}
	}
	f = newConnectionForm("New", config.Connection{Engine: db.EnginePostgres}, "")
	if !hasField(f, "ssh") {
		t.Errorf("PostgreSQL form has no SSH toggle: %v", fieldNames(f))
	}
}

// The section below the toggle only appears once the tunnel is switched on,
// and the auth choice decides which of the last two fields is shown.
func TestSSHFieldVisibilityFollowsTheToggleAndAuth(t *testing.T) {
	f := newConnectionForm("New", config.Connection{Engine: db.EngineMySQL}, "")
	if hasField(f, "ssh_host") {
		t.Fatal("SSH host is visible while the tunnel is off")
	}
	f.field("ssh").on = true
	for _, name := range []string{"ssh_host", "ssh_port", "ssh_user", "ssh_auth"} {
		if !hasField(f, name) {
			t.Errorf("%s hidden with the tunnel on: %v", name, fieldNames(f))
		}
	}
	// agent is the default: neither a key file nor a secret is asked for.
	if hasField(f, "ssh_key") || hasField(f, "ssh_secret") {
		t.Errorf("agent auth asks for a key or a secret: %v", fieldNames(f))
	}
	setSelect(t, f, "ssh_auth", string(sshtunnel.AuthKey))
	if !hasField(f, "ssh_key") || !hasField(f, "ssh_secret") {
		t.Errorf("key auth hides the key file or the passphrase: %v", fieldNames(f))
	}
	setSelect(t, f, "ssh_auth", string(sshtunnel.AuthPassword))
	if hasField(f, "ssh_key") {
		t.Errorf("password auth asks for a key file: %v", fieldNames(f))
	}
	if !hasField(f, "ssh_secret") {
		t.Errorf("password auth hides the password: %v", fieldNames(f))
	}
	if f.field("ssh_secret").kind != fieldPassword {
		t.Error("the SSH secret field is not masked")
	}
}

func TestFormBuildsTheSSHSection(t *testing.T) {
	f := newConnectionForm("New", config.Connection{Engine: db.EnginePostgres}, "")
	f.field("name").input.SetValue("prod")
	f.field("host").input.SetValue("10.0.0.5")
	f.field("ssh").on = true
	f.field("ssh_host").input.SetValue("bastion")
	f.field("ssh_port").input.SetValue("2222")
	f.field("ssh_user").input.SetValue("deploy")
	setSelect(t, f, "ssh_auth", string(sshtunnel.AuthKey))
	f.field("ssh_key").input.SetValue("~/.ssh/id_deploy")
	f.field("ssh_secret").input.SetValue("passphrase")

	c, sec, err := f.toConnection()
	if err != nil {
		t.Fatalf("toConnection: %v", err)
	}
	if c.SSH == nil {
		t.Fatal("no SSH section built")
	}
	if !c.SSH.Enabled || c.SSH.Host != "bastion" || c.SSH.Port != 2222 ||
		c.SSH.User != "deploy" || c.SSH.Auth != "key" || c.SSH.KeyFile != "~/.ssh/id_deploy" {
		t.Fatalf("SSH = %+v", *c.SSH)
	}
	if !sec.setSSH || sec.ssh != "passphrase" {
		t.Fatalf("SSH secret = %q (set %v), want the typed passphrase", sec.ssh, sec.setSSH)
	}
	// The secret is never part of the profile that reaches config.toml.
	if strings.Contains(c.SSH.KeyFile, "passphrase") {
		t.Fatal("the passphrase leaked into the profile")
	}
}

func TestFormRejectsABadSSHPort(t *testing.T) {
	f := newConnectionForm("New", config.Connection{Engine: db.EnginePostgres}, "")
	f.field("name").input.SetValue("prod")
	f.field("host").input.SetValue("h")
	f.field("ssh").on = true
	f.field("ssh_host").input.SetValue("bastion")
	f.field("ssh_port").input.SetValue("no")
	if _, _, err := f.toConnection(); err == nil {
		t.Fatal("toConnection accepted a non-numeric SSH port")
	}
}

// A profile that never touched the SSH section keeps a nil one, so config.toml
// gains no empty table.
func TestFormLeavesTheSSHSectionNilWhenUnused(t *testing.T) {
	f := newConnectionForm("New", config.Connection{Engine: db.EnginePostgres}, "")
	f.field("name").input.SetValue("plain")
	f.field("host").input.SetValue("h")
	c, _, err := f.toConnection()
	if err != nil {
		t.Fatalf("toConnection: %v", err)
	}
	if c.SSH != nil {
		t.Fatalf("SSH = %+v, want nil", *c.SSH)
	}
}

// ---------- dial-time prompts ----------

func tunnelledProfile(auth sshtunnel.AuthMethod) config.Connection {
	return config.Connection{
		Name: "prod", Engine: db.EnginePostgres, Host: "10.0.0.5", Port: 5432,
		SSH: &config.SSH{Enabled: true, Host: "bastion", Port: 22, User: "deploy", Auth: string(auth)},
	}
}

func TestUnknownHostKeyOpensAConfirmModal(t *testing.T) {
	req := dialRequest{conn: tunnelledProfile(sshtunnel.AuthAgent)}
	err := &sshtunnel.UnknownHostKeyError{
		Address: "[bastion]:22", KeyType: "ssh-ed25519",
		Fingerprint: "SHA256:abc", File: "/tmp/known_hosts",
	}
	mod := sshFailureModal(req, err)
	c, ok := mod.(*confirmModal)
	if !ok {
		t.Fatalf("modal = %T, want a confirm", mod)
	}
	if !c.danger {
		t.Error("the unknown host key modal is not marked dangerous")
	}
	for _, want := range []string{"SHA256:abc", "ssh-ed25519", "/tmp/known_hosts"} {
		if !strings.Contains(c.body, want) {
			t.Errorf("modal body does not show %q:\n%s", want, c.body)
		}
	}
	if c.onConfirm == nil {
		t.Error("the unknown host key modal has no accept action")
	}
}

// A changed key is refused outright: the modal has no way to say yes.
func TestChangedHostKeyModalHasNoAcceptAction(t *testing.T) {
	req := dialRequest{conn: tunnelledProfile(sshtunnel.AuthAgent)}
	err := &sshtunnel.HostKeyMismatchError{
		Address: "[bastion]:22", KeyType: "ssh-ed25519", Fingerprint: "SHA256:xyz",
		File: "/tmp/known_hosts", KnownFiles: []string{"/tmp/known_hosts"}, KnownLines: []int{3},
	}
	mod := sshFailureModal(req, err)
	c, ok := mod.(*confirmModal)
	if !ok {
		t.Fatalf("modal = %T, want a confirm", mod)
	}
	if c.onConfirm != nil {
		t.Fatal("a changed host key can be accepted from the UI")
	}
}

func TestMissingPassphraseOpensASecretPrompt(t *testing.T) {
	req := dialRequest{conn: tunnelledProfile(sshtunnel.AuthKey)}
	mod := sshFailureModal(req, sshtunnel.ErrPassphraseRequired)
	f, ok := mod.(*formModal)
	if !ok {
		t.Fatalf("modal = %T, want the secret prompt", mod)
	}
	if f.field("secret").kind != fieldPassword {
		t.Fatal("the passphrase prompt is not masked")
	}
	if !strings.Contains(f.title, "bastion") {
		t.Errorf("title = %q, want the jump host named", f.title)
	}
	// Once a secret was tried, a second failure is not a prompt loop.
	req.hasSSHSecret = true
	if mod := sshFailureModal(req, sshtunnel.ErrPassphraseRequired); mod != nil {
		t.Fatalf("modal = %T after a secret was supplied, want none", mod)
	}
}

// An ordinary database failure is not an SSH prompt.
func TestNonSSHFailureHasNoSSHModal(t *testing.T) {
	req := dialRequest{conn: tunnelledProfile(sshtunnel.AuthAgent)}
	if mod := sshFailureModal(req, errors.New("connection refused")); mod != nil {
		t.Fatalf("modal = %T, want none", mod)
	}
}

func TestTunnelSuffixNamesTheJumpHost(t *testing.T) {
	got := tunnelSuffix(tunnelledProfile(sshtunnel.AuthKey))
	for _, want := range []string{"deploy@bastion:22", "key file"} {
		if !strings.Contains(got, want) {
			t.Errorf("suffix = %q, want it to contain %q", got, want)
		}
	}
	if tunnelSuffix(config.Connection{Engine: db.EngineSQLite}) != "" {
		t.Error("a file engine produced a tunnel suffix")
	}
}

// ---------- lifecycle ----------

// Quitting tears the connection down synchronously: tea.Quit can stop the
// program before any batched command runs.
func TestQuitClosesTheConnection(t *testing.T) {
	m := sized(120, 40)
	m = send(t, m, special(tea.KeyEnter, 0)) // connect to the SQLite fixture
	if m.driver == nil {
		t.Fatal("the fixture connection did not open")
	}
	next, _ := m.Update(press('q'))
	m = next.(Model)
	if m.driver != nil || m.tunnel != nil || m.active != "" {
		t.Fatalf("quit left driver=%v tunnel=%v active=%q", m.driver, m.tunnel, m.active)
	}
}

func TestCloseSessionIsSafeWithoutAConnection(t *testing.T) {
	m := sized(120, 40)
	m.closeSession()
	if m.driver != nil || m.tunnel != nil {
		t.Fatal("closeSession left state behind")
	}
}
