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
	// ReadOnly asks the engine itself for a read-only session, on top of
	// the guard the conn applies. See ReadOnlyParams for what each engine
	// takes and which of them cannot honour it.
	ReadOnly bool
}

// FileBased reports whether an engine is addressed by a file path rather than
// a host/port pair. Callers use it to pick which form fields to show.
func FileBased(engine Engine) bool {
	return engine == EngineSQLite || engine == EngineDuckDB
}

// DatabaseNamespaces reports whether ListDatabases enumerates the server's
// databases, so that a profile pinning one can scope the listing to it.
// PostgreSQL returns false — its namespaces are the schemas of the connected
// database, where the pin already took effect at dial time — and so do the
// file-based engines.
func DatabaseNamespaces(engine Engine) bool {
	return engine == EngineMySQL || engine == EngineMariaDB
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

// ReadOnlyParams are the DSN parameters that put a session in read-only mode
// on the engine's own side. They are defence in depth: the connection-level
// guard in conn is what actually guarantees the mode, because not every engine
// or version honours these.
//
//   - MySQL/MariaDB: the go-sql-driver sends every unknown DSN parameter as a
//     `SET <name>=<value>` on each new pooled connection, which is the only
//     way `SET SESSION transaction_read_only` survives connection pooling.
//     MariaDB knows the variable from 10.2.2 on; an older server fails the
//     handshake rather than connecting read-write.
//   - PostgreSQL: `default_transaction_read_only` travels as a startup
//     parameter, so it applies to every transaction of the session.
//   - SQLite: `mode=ro` needs the `file:` URI form, which fileDSN switches to
//     as soon as any option is present. The file must already exist.
//   - DuckDB: `access_mode=read_only` needs an existing database file; an
//     in-memory database cannot be opened read-only at all, so ConnParams
//     leaves the flag off there (see engineReadOnlyParams).
func ReadOnlyParams(engine Engine) map[string]string {
	switch engine {
	case EngineMySQL, EngineMariaDB:
		return map[string]string{"transaction_read_only": "1"}
	case EnginePostgres:
		return map[string]string{"default_transaction_read_only": "on"}
	case EngineSQLite:
		return map[string]string{"mode": "ro"}
	case EngineDuckDB:
		return map[string]string{"access_mode": "read_only"}
	}
	return nil
}

// engineReadOnlyParams is ReadOnlyParams for one concrete set of connection
// parameters: it drops the flag where this particular connection could not
// honour it. Only DuckDB has such a case — an in-memory database opened
// read-only would have nothing to read and the driver refuses it.
func engineReadOnlyParams(engine Engine, p ConnParams) map[string]string {
	if engine == EngineDuckDB && strings.TrimSpace(p.File) == "" {
		return nil
	}
	return ReadOnlyParams(engine)
}

// dsnOptions is the option map a DSN is built from: the profile's own options
// plus, for a read-only connection, the engine's read-only parameters. The
// profile's map is never mutated — it belongs to the caller.
func dsnOptions(engine Engine, p ConnParams) map[string]string {
	ro := map[string]string{}
	if p.ReadOnly {
		ro = engineReadOnlyParams(engine, p)
	}
	if len(ro) == 0 {
		return p.Options
	}
	out := make(map[string]string, len(p.Options)+len(ro))
	for k, v := range p.Options {
		out[k] = v
	}
	// The read-only parameters win: a profile option must not be able to
	// quietly re-open the connection for writing.
	for k, v := range ro {
		out[k] = v
	}
	return out
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
		return fileDSN(p, dsnOptions(engine, p), true)
	case EngineDuckDB:
		return fileDSN(p, dsnOptions(engine, p), false)
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
	if opts := dsnOptions(engine, p); len(opts) > 0 {
		cfg.Params = map[string]string{}
		for _, k := range sortedOptionKeys(opts) {
			cfg.Params[k] = opts[k]
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
	if opts := dsnOptions(EnginePostgres, p); len(opts) > 0 {
		q := url.Values{}
		for _, k := range sortedOptionKeys(opts) {
			q.Set(k, opts[k])
		}
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

// fileDSN renders SQLite and DuckDB paths. SQLite needs the `file:` URI form
// once options are present; DuckDB takes a bare path with a query string.
func fileDSN(p ConnParams, opts map[string]string, uriForm bool) (string, error) {
	path := strings.TrimSpace(p.File)
	if len(opts) == 0 {
		return path, nil
	}
	q := url.Values{}
	for _, k := range sortedOptionKeys(opts) {
		q.Set(k, opts[k])
	}
	if uriForm {
		return "file:" + path + "?" + q.Encode(), nil
	}
	return path + "?" + q.Encode(), nil
}
