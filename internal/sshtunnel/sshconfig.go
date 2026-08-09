package sshtunnel

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kevinburke/ssh_config"
)

// Resolve fills in whatever the profile left blank from ~/.ssh/config, then
// from the built-in defaults. The profile always wins: a host alias is a
// convenience, not an override.
//
// The four keywords that matter for a tunnel are HostName, User, Port and
// IdentityFile. Everything else in ~/.ssh/config (ProxyJump, ControlMaster,
// …) is out of scope — lazysql dials the bastion directly.
func Resolve(c Config) (Config, error) {
	alias := strings.TrimSpace(c.Host)
	if alias == "" {
		return c, errors.New("sshtunnel: SSH host is required")
	}
	cfg, err := c.loadSSHConfig()
	if err != nil {
		return c, err
	}

	get := func(key string) string {
		if cfg == nil {
			return ""
		}
		v, err := cfg.Get(alias, key)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(v)
	}

	if hostname := get("HostName"); hostname != "" {
		c.Host = hostname
	}
	if c.Port == 0 {
		if p := get("Port"); p != "" {
			n, err := strconv.Atoi(p)
			if err != nil || n < 1 || n > 65535 {
				return c, fmt.Errorf("sshtunnel: ssh config Port %q for %s is not a valid port", p, alias)
			}
			c.Port = n
		}
	}
	if c.Port == 0 {
		c.Port = DefaultPort
	}
	if c.User == "" {
		c.User = get("User")
	}
	if c.User == "" {
		if u, err := user.Current(); err == nil {
			c.User = u.Username
		}
	}
	if c.User == "" {
		return c, fmt.Errorf("sshtunnel: no SSH user for %s and none in ssh config", alias)
	}
	if c.Auth == AuthKey && c.KeyFile == "" {
		c.KeyFile = get("IdentityFile")
	}
	c.KeyFile = expandHome(c.KeyFile)
	return c, nil
}

// loadSSHConfig parses ~/.ssh/config, or the override. A missing file means no
// aliases are defined, which is not an error.
func (c Config) loadSSHConfig() (*ssh_config.Config, error) {
	path := c.SSHConfigFile
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			// No home directory just means no aliases to resolve.
			return nil, nil
		}
		path = filepath.Join(home, ".ssh", "config")
	}
	path = expandHome(path)
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sshtunnel: read %s: %w", path, err)
	}
	defer f.Close()
	cfg, err := ssh_config.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("sshtunnel: parse %s: %w", path, err)
	}
	return cfg, nil
}
