package db

import (
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/go-sql-driver/mysql"
)

// ConnParams is the engine-agnostic description of one connection. UI and
// config code fill it in; only this package knows how to turn it into a DSN
// for a concrete database/sql driver.
type ConnParams struct {
	Host     string
	Port     int
	User     string
	Password string
	Database string
	File     string // SQLite / DuckDB path; empty means in-memory
	Options  map[string]string
}

// FileBased reports whether an engine is addressed by a file path rather than
// a host/port pair. Callers use it to pick which form fields to show.
func FileBased(engine Engine) bool {
	return engine == EngineSQLite || engine == EngineDuckDB
}

// DefaultPort is the port used when a connection leaves it unset. File-based
// engines return 0.
func DefaultPort(engine Engine) int {
	switch engine {
	case EngineMySQL, EngineMariaDB:
		return 3306
	case EnginePostgres:
		return 5432
	}
	return 0
}

// sortedOptionKeys keeps generated DSNs stable across runs, which matters for
// tests and for the DSN echoed into the command log.
func sortedOptionKeys(opts map[string]string) []string {
	keys := make([]string, 0, len(opts))
	for k := range opts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// BuildDSN renders the driver-specific connection string for an engine.
func BuildDSN(engine Engine, p ConnParams) (string, error) {
	if _, err := DialectFor(engine); err != nil {
		return "", err
	}
	switch engine {
	case EngineMySQL, EngineMariaDB:
		return mysqlDSN(engine, p)
	case EnginePostgres:
		return postgresDSN(p)
	case EngineSQLite:
		return fileDSN(p, true)
	case EngineDuckDB:
		return fileDSN(p, false)
	}
	return "", fmt.Errorf("db: no DSN builder for engine %q", engine)
}

// PasswordMask is what a redacted DSN shows instead of the password. It is
// deliberately URL-safe so percent-encoding cannot obscure it in the log.
const PasswordMask = "REDACTED"

// RedactDSN replaces the password in a DSN with PasswordMask so the command
// log can show what was dialled without leaking the secret.
func RedactDSN(engine Engine, p ConnParams) (string, error) {
	if p.Password != "" {
		p.Password = PasswordMask
	}
	return BuildDSN(engine, p)
}

func hostPort(p ConnParams, engine Engine) (string, int) {
	host := p.Host
	if host == "" {
		host = "localhost"
	}
	port := p.Port
	if port == 0 {
		port = DefaultPort(engine)
	}
	return host, port
}

func mysqlDSN(engine Engine, p ConnParams) (string, error) {
	host, port := hostPort(p, engine)
	cfg := mysql.NewConfig()
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort(host, strconv.Itoa(port))
	cfg.User = p.User
	cfg.Passwd = p.Password
	cfg.DBName = p.Database
	// Normalized cell values depend on real time.Time, not []byte.
	cfg.ParseTime = true
	if len(p.Options) > 0 {
		cfg.Params = map[string]string{}
		for _, k := range sortedOptionKeys(p.Options) {
			cfg.Params[k] = p.Options[k]
		}
	}
	// A unix socket is expressed by giving the host an absolute path.
	if strings.HasPrefix(p.Host, "/") {
		cfg.Net = "unix"
		cfg.Addr = p.Host
	}
	return cfg.FormatDSN(), nil
}

func postgresDSN(p ConnParams) (string, error) {
	host, port := hostPort(p, EnginePostgres)
	u := &url.URL{
		Scheme: "postgres",
		Host:   net.JoinHostPort(host, strconv.Itoa(port)),
		Path:   "/" + p.Database,
	}
	if p.User != "" {
		if p.Password != "" {
			u.User = url.UserPassword(p.User, p.Password)
		} else {
			u.User = url.User(p.User)
		}
	}
	if len(p.Options) > 0 {
		q := url.Values{}
		for _, k := range sortedOptionKeys(p.Options) {
			q.Set(k, p.Options[k])
		}
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

// fileDSN renders SQLite and DuckDB paths. SQLite needs the `file:` URI form
// once options are present; DuckDB takes a bare path with a query string.
func fileDSN(p ConnParams, uriForm bool) (string, error) {
	path := strings.TrimSpace(p.File)
	if len(p.Options) == 0 {
		return path, nil
	}
	q := url.Values{}
	for _, k := range sortedOptionKeys(p.Options) {
		q.Set(k, p.Options[k])
	}
	if uriForm {
		return "file:" + path + "?" + q.Encode(), nil
	}
	return path + "?" + q.Encode(), nil
}
