// Package config reads and writes lazysql's on-disk configuration: the list
// of named connections at ${XDG_CONFIG_HOME:-~/.config}/lazysql/config.toml.
//
// The file never contains secrets. Passwords live in the OS keyring (see
// package secrets) keyed by connection name, or are prompted for on connect
// when AskPassword is set.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"lazysql/internal/db"
)

const (
	// AppDir is the config subdirectory under the user config home.
	AppDir = "lazysql"
	// FileName is the config file inside AppDir.
	FileName = "config.toml"
)

// Connection is one named connection profile. Field order matters for the
// TOML encoder: Options is a nested table and must come after every scalar.
type Connection struct {
	Name   string    `toml:"name"`
	Engine db.Engine `toml:"engine"`

	Host     string `toml:"host,omitempty"`
	Port     int    `toml:"port,omitempty"`
	User     string `toml:"user,omitempty"`
	Database string `toml:"database,omitempty"`
	File     string `toml:"file,omitempty"`

	// AskPassword prompts for the password on connect instead of reading it
	// from the keyring.
	AskPassword bool `toml:"ask_password,omitempty"`

	Options map[string]string `toml:"options,omitempty"`
}

// Config is the whole config file.
type Config struct {
	Connections []Connection `toml:"connections"`
}

// Params converts a profile into the driver-agnostic connection parameters.
// The password is supplied separately; it is never stored here.
func (c Connection) Params(password string) db.ConnParams {
	return db.ConnParams{
		Host:     c.Host,
		Port:     c.Port,
		User:     c.User,
		Password: password,
		Database: c.Database,
		File:     c.File,
		Options:  c.Options,
	}
}

// NeedsPassword reports whether the engine authenticates with a password at
// all. File-based engines never do.
func (c Connection) NeedsPassword() bool { return !db.FileBased(c.Engine) }

// Validate checks required fields before a profile is written to disk.
func (c Connection) Validate() error {
	if strings.TrimSpace(c.Name) == "" {
		return errors.New("name is required")
	}
	if _, err := db.DialectFor(c.Engine); err != nil {
		return fmt.Errorf("unknown engine %q", c.Engine)
	}
	if db.FileBased(c.Engine) {
		if strings.TrimSpace(c.File) == "" && c.Engine == db.EngineSQLite {
			return errors.New("file path is required for SQLite")
		}
		return nil
	}
	if strings.TrimSpace(c.Host) == "" {
		return errors.New("host is required")
	}
	if c.Port < 0 || c.Port > 65535 {
		return fmt.Errorf("port %d out of range 1-65535", c.Port)
	}
	return nil
}

// ---------- encoding shim ----------

// The TOML encoder's `omitempty` treats only strings, containers and bools as
// empty — a numeric zero is still written. A file-based profile would then get
// a meaningless `port = 0`, so encoding goes through a shadow struct whose
// Port is a pointer. Decoding uses Connection directly: a missing `port`
// leaves the int at zero, which Load turns into the engine default.
type connectionFile struct {
	Name   string    `toml:"name"`
	Engine db.Engine `toml:"engine"`

	Host     string `toml:"host,omitempty"`
	Port     *int   `toml:"port,omitempty"`
	User     string `toml:"user,omitempty"`
	Database string `toml:"database,omitempty"`
	File     string `toml:"file,omitempty"`

	AskPassword bool `toml:"ask_password,omitempty"`

	Options map[string]string `toml:"options,omitempty"`
}

type configFile struct {
	Connections []connectionFile `toml:"connections"`
}

func (c *Config) forEncoding() configFile {
	out := configFile{Connections: make([]connectionFile, len(c.Connections))}
	for i, conn := range c.Connections {
		e := connectionFile{
			Name:        conn.Name,
			Engine:      conn.Engine,
			Host:        conn.Host,
			User:        conn.User,
			Database:    conn.Database,
			File:        conn.File,
			AskPassword: conn.AskPassword,
			Options:     conn.Options,
		}
		if conn.Port != 0 {
			port := conn.Port
			e.Port = &port
		}
		out.Connections[i] = e
	}
	return out
}

// ---------- file location ----------

// Dir returns the lazysql config directory, honouring XDG_CONFIG_HOME.
func Dir() (string, error) {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, AppDir), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config: locate home directory: %w", err)
	}
	return filepath.Join(home, ".config", AppDir), nil
}

// Path returns the full path of the config file.
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, FileName), nil
}

// ---------- load / save ----------

// Load reads the config file. A missing file is not an error: it yields an
// empty config, which is what a first run should see.
func Load() (*Config, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	return LoadFrom(path)
}

// LoadFrom reads a config file from an explicit path.
func LoadFrom(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	for i := range cfg.Connections {
		if cfg.Connections[i].Port == 0 {
			cfg.Connections[i].Port = db.DefaultPort(cfg.Connections[i].Engine)
		}
	}
	return &cfg, nil
}

// Save writes the config file, creating the directory if needed.
func (c *Config) Save() error {
	path, err := Path()
	if err != nil {
		return err
	}
	return c.SaveTo(path)
}

// SaveTo writes the config to an explicit path. The write is atomic
// (temp file + rename) so a crash cannot truncate an existing config, and
// both directory and file are owner-only.
func (c *Config) SaveTo(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("config: create %s: %w", filepath.Dir(path), err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.toml")
	if err != nil {
		return fmt.Errorf("config: create temp file: %w", err)
	}
	defer os.Remove(tmp.Name())

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("config: chmod temp file: %w", err)
	}
	enc := toml.NewEncoder(tmp)
	if err := enc.Encode(c.forEncoding()); err != nil {
		tmp.Close()
		return fmt.Errorf("config: encode: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("config: close temp file: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	return nil
}

// ---------- list operations ----------

// Index returns the position of the named connection, or -1.
func (c *Config) Index(name string) int {
	for i, conn := range c.Connections {
		if conn.Name == name {
			return i
		}
	}
	return -1
}

// Find returns the named connection.
func (c *Config) Find(name string) (Connection, bool) {
	if i := c.Index(name); i >= 0 {
		return c.Connections[i], true
	}
	return Connection{}, false
}

// Upsert inserts conn, or replaces the profile previously stored under
// oldName. Pass an empty oldName when adding. It reports an error when the
// new name collides with a different existing profile.
func (c *Config) Upsert(oldName string, conn Connection) error {
	if err := conn.Validate(); err != nil {
		return err
	}
	if i := c.Index(conn.Name); i >= 0 && conn.Name != oldName {
		return fmt.Errorf("a connection named %q already exists", conn.Name)
	}
	if oldName != "" {
		if i := c.Index(oldName); i >= 0 {
			c.Connections[i] = conn
			return nil
		}
	}
	c.Connections = append(c.Connections, conn)
	return nil
}

// Remove deletes the named connection, reporting whether it existed.
func (c *Config) Remove(name string) bool {
	i := c.Index(name)
	if i < 0 {
		return false
	}
	c.Connections = append(c.Connections[:i], c.Connections[i+1:]...)
	return true
}

// Clone returns a deep copy. Saving happens in a tea.Cmd goroutine; it works
// on a clone so it can never race the model's copy.
func (c *Config) Clone() *Config {
	out := &Config{Connections: make([]Connection, len(c.Connections))}
	copy(out.Connections, c.Connections)
	for i, conn := range c.Connections {
		if conn.Options == nil {
			continue
		}
		opts := make(map[string]string, len(conn.Options))
		for k, v := range conn.Options {
			opts[k] = v
		}
		out.Connections[i].Options = opts
	}
	return out
}

// Names lists connection names in file order.
func (c *Config) Names() []string {
	out := make([]string, len(c.Connections))
	for i, conn := range c.Connections {
		out[i] = conn.Name
	}
	return out
}
