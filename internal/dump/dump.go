// Package dump builds and runs the external backup tools of each engine —
// pg_dump/psql, mysqldump/mysql, mariadb-dump/mariadb — and describes the
// two engines that need no external tool at all (SQLite's `VACUUM INTO`,
// DuckDB's `EXPORT DATABASE`/`IMPORT DATABASE`), which the caller runs
// through its own Driver.
//
// Credential hygiene is the point of this package. A password never
// reaches argv, where `ps` would show it to every user on the machine, and
// never reaches the rendered command either. It travels in a temporary
// credential file created with 0600 permissions — a `.pgpass` named by
// PGPASSFILE, or a MySQL option file named by `--defaults-extra-file` —
// which Command.Cleanup removes as soon as the child process is done.
package dump

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"lazysql/internal/db"
)

// Action is which direction a job runs in.
type Action int

const (
	// Dump writes the database to a file (or, for DuckDB, a directory).
	Dump Action = iota
	// Restore reads such a file back into the database, overwriting what
	// is there. The UI gates it behind a typed confirmation.
	Restore
)

func (a Action) String() string {
	if a == Restore {
		return "restore"
	}
	return "dump"
}

// Kind is how a job is carried out: by running an external tool, by
// executing SQL through lazysql's own Driver, or by copying a file.
type Kind int

const (
	// KindProcess runs an external binary.
	KindProcess Kind = iota
	// KindSQL executes statements through the connected Driver.
	KindSQL
	// KindCopy copies a file over the database file (SQLite restore).
	KindCopy
)

// Request describes one dump or restore. Host/Port are already the
// endpoint the tool should connect to: when the connection runs through an
// SSH tunnel the caller substitutes the local forwarded address, so
// nothing below this point knows a jump host exists.
type Request struct {
	Action Action
	Engine db.Engine

	Host     string
	Port     int
	User     string
	Database string
	// Schema is the browsed namespace when it is narrower than Database.
	// Only PostgreSQL has both, and only a dump can be limited to one.
	Schema string
	// File is the database file of a file-based engine (SQLite, DuckDB).
	File string

	// Password is used to write the temporary credential file. It is
	// never placed in argv, in the environment, or in any rendered text.
	Password string

	// Path is the dump's destination or the restore's source: a file for
	// every engine but DuckDB, whose EXPORT/IMPORT DATABASE take a
	// directory.
	Path string

	// ExtraArgs are the user's own edits to the arguments line. They are
	// appended after the arguments lazysql derives from the connection.
	ExtraArgs []string

	// lookPath is exec.LookPath, replaceable in tests.
	lookPath func(string) (string, error)

	// credPath is the credential file the assembled command refers to:
	// the real temporary file under Build, CredPlaceholder under Preview.
	// Empty means the engine needs none, or no password was supplied.
	credPath string
}

// CredPlaceholder stands in for the temporary credential file in a
// previewed command. A preview creates no file, so there is no path to
// show — and showing one would only invite the user to look inside it.
const CredPlaceholder = "<credential-file>"

// WithLookPath replaces exec.LookPath for this request. Tests use it to
// pretend a tool is or is not installed.
func (r Request) WithLookPath(fn func(string) (string, error)) Request {
	r.lookPath = fn
	return r
}

func (r Request) look() func(string) (string, error) {
	if r.lookPath != nil {
		return r.lookPath
	}
	return exec.LookPath
}

// Command is one fully resolved job.
type Command struct {
	Kind Kind

	// Bin is the resolved absolute path of the tool and Name the binary
	// it was looked up under; Args is its argv without argv[0].
	Bin  string
	Name string
	Args []string
	// Env are the extra environment entries the child needs, appended to
	// the parent's environment. No entry ever holds a password: the
	// PostgreSQL entry names the credential file, it does not contain
	// the secret.
	Env []string

	// StdinPath is the file fed to the tool's stdin (mysql restore) and
	// StdoutPath the file its stdout is captured into. Empty means the
	// tool writes the file itself, via its own `--file`-style option.
	StdinPath  string
	StdoutPath string

	// SQL are the statements a KindSQL job runs through the Driver.
	SQL []string
	// CopyFrom/CopyTo are a KindCopy job's source and destination.
	CopyFrom, CopyTo string

	// credFile is the temporary credential file, removed by Cleanup.
	credFile string
}

// Cleanup removes the temporary credential file. It runs as soon as the
// child process has exited — the secret is on disk for the lifetime of one
// process and no longer. Calling it more than once is safe.
func (c *Command) Cleanup() error {
	if c.credFile == "" {
		return nil
	}
	path := c.credFile
	c.credFile = ""
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// String renders the command the way the modal previews it and the way the
// command log records it. It is safe to display: the credential file is
// named, never its contents, and no password appears in argv or env.
func (c Command) String() string {
	switch c.Kind {
	case KindSQL:
		return strings.Join(c.SQL, " ")
	case KindCopy:
		return fmt.Sprintf("cp %s %s", shellQuote(c.CopyFrom), shellQuote(c.CopyTo))
	}
	parts := make([]string, 0, len(c.Env)+len(c.Args)+3)
	parts = append(parts, c.Env...)
	parts = append(parts, c.Name)
	for _, a := range c.Args {
		parts = append(parts, shellQuote(a))
	}
	if c.StdinPath != "" {
		parts = append(parts, "<", shellQuote(c.StdinPath))
	}
	if c.StdoutPath != "" {
		parts = append(parts, ">", shellQuote(c.StdoutPath))
	}
	return strings.Join(parts, " ")
}

// shellQuote makes an argument readable as one word in the preview. The
// string is never handed to a shell — Build produces an argv that
// os/exec runs directly — so this only has to be unambiguous, not safe.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if strings.IndexFunc(s, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\'' || r == '"' || r == '$' || r == '`' ||
			r == '\\' || r == ';' || r == '&' || r == '|' || r == '<' || r == '>'
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ErrMissingTool reports that none of an engine's candidate binaries is on
// PATH. Its message names every one that was tried, so the user knows what
// to install.
type ErrMissingTool struct {
	Candidates []string
	Engine     db.Engine
	Action     Action
}

func (e *ErrMissingTool) Error() string {
	if len(e.Candidates) == 1 {
		return fmt.Sprintf("%s needs %q, which is not on PATH — install it or add it to PATH",
			e.Action, e.Candidates[0])
	}
	return fmt.Sprintf("%s needs one of %s, none of which is on PATH — install one or add it to PATH",
		e.Action, strings.Join(quoteAll(e.Candidates), " or "))
}

func quoteAll(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = strconv.Quote(n)
	}
	return out
}

// Tools lists the binaries an engine's action can be carried out by, most
// preferred first. A file-based engine needs none and returns nil.
func Tools(engine db.Engine, action Action) []string {
	switch engine {
	case db.EnginePostgres:
		if action == Restore {
			return []string{"psql"}
		}
		return []string{"pg_dump"}
	case db.EngineMySQL:
		if action == Restore {
			return []string{"mysql", "mariadb"}
		}
		return []string{"mysqldump", "mariadb-dump"}
	case db.EngineMariaDB:
		// MariaDB ships the mariadb-* names and keeps the mysql-* ones
		// as compatibility symlinks; newer builds drop the symlinks, so
		// both spellings are tried with the native one first.
		if action == Restore {
			return []string{"mariadb", "mysql"}
		}
		return []string{"mariadb-dump", "mysqldump"}
	}
	return nil
}

// DefaultExtension is the suffix a dump of this engine is normally given.
// DuckDB reports "" because EXPORT DATABASE writes a directory.
func DefaultExtension(engine db.Engine) string {
	if engine == db.EngineDuckDB {
		return ""
	}
	return ".sql"
}

// Build resolves the request into a runnable command, creating the
// temporary credential file when the engine needs one. The caller must
// call Cleanup on the result, whether or not the run succeeded.
func Build(r Request) (Command, error) {
	if err := r.validate(); err != nil {
		return Command{}, err
	}
	if !r.needsCredFile() {
		return assemble(r)
	}
	path, err := r.writeCredFile()
	if err != nil {
		return Command{}, err
	}
	r.credPath = path
	cmd, err := assemble(r)
	if err != nil {
		os.Remove(path)
		return Command{}, err
	}
	cmd.credFile = path
	return cmd, nil
}

// Preview assembles the same command without creating anything: the
// credential file is named by CredPlaceholder and no secret is involved.
// It is what the modal renders and what the "tool is missing" check runs
// through, so a job that cannot start says so before a prompt is answered.
func Preview(r Request) (Command, error) {
	if err := r.validate(); err != nil {
		return Command{}, err
	}
	if r.needsCredFile() {
		r.credPath = CredPlaceholder
	}
	// A preview must never carry the secret into the assembled command,
	// even though no builder puts it there.
	r.Password = ""
	return assemble(r)
}

// needsCredFile reports whether this engine authenticates the external
// tool through a file lazysql has to write.
func (r Request) needsCredFile() bool {
	return !db.FileBased(r.Engine) && r.Password != ""
}

func (r Request) writeCredFile() (string, error) {
	switch r.Engine {
	case db.EnginePostgres:
		return writePgpass(r.Host, r.portOrDefault(), r.Database, r.User, r.Password)
	case db.EngineMySQL, db.EngineMariaDB:
		return writeMyCnf(r.Password)
	}
	return "", fmt.Errorf("dump: %s needs no credential file", r.Engine)
}

func assemble(r Request) (Command, error) {
	switch r.Engine {
	case db.EnginePostgres:
		return buildPostgres(r)
	case db.EngineMySQL, db.EngineMariaDB:
		return buildMySQL(r)
	case db.EngineSQLite:
		return buildSQLite(r)
	case db.EngineDuckDB:
		return buildDuckDB(r)
	}
	return Command{}, fmt.Errorf("dump: %s is not supported for %s", r.Engine, r.Action)
}

func (r Request) validate() error {
	if strings.TrimSpace(r.Path) == "" {
		if r.Action == Restore {
			return errors.New("no input path given")
		}
		return errors.New("no output path given")
	}
	if db.FileBased(r.Engine) {
		if strings.TrimSpace(r.File) == "" {
			return fmt.Errorf("dump: %s connection has no database file", r.Engine)
		}
		return nil
	}
	if strings.TrimSpace(r.Host) == "" {
		return errors.New("connection has no host")
	}
	if strings.TrimSpace(r.Database) == "" {
		return errors.New("connection names no database — set one on the profile first")
	}
	return nil
}

// resolve looks the engine's preferred binary up on PATH.
func (r Request) resolve() (bin, name string, err error) {
	candidates := Tools(r.Engine, r.Action)
	look := r.look()
	for _, c := range candidates {
		if path, err := look(c); err == nil {
			return path, c, nil
		}
	}
	return "", "", &ErrMissingTool{Candidates: candidates, Engine: r.Engine, Action: r.Action}
}

func (r Request) portOrDefault() int {
	if r.Port > 0 {
		return r.Port
	}
	return db.DefaultPort(r.Engine)
}

// ---------- PostgreSQL ----------

// buildPostgres produces `pg_dump --file=OUT` or `psql --file=IN`, both
// with `--no-password` so a missing credential fails instead of blocking
// on a prompt no TUI can answer, and both pointed at a temporary `.pgpass`
// through PGPASSFILE.
func buildPostgres(r Request) (Command, error) {
	bin, name, err := r.resolve()
	if err != nil {
		return Command{}, err
	}
	port := r.portOrDefault()
	args := []string{
		"--host=" + r.Host,
		"--port=" + strconv.Itoa(port),
		"--dbname=" + r.Database,
		"--no-password",
	}
	if r.User != "" {
		args = append(args, "--username="+r.User)
	}
	if r.Action == Restore {
		// A restore that keeps going after a failed statement leaves a
		// half-applied database and still exits 0; ON_ERROR_STOP is what
		// makes the exit code mean something.
		args = append(args, "--set=ON_ERROR_STOP=1", "--file="+r.Path)
	} else {
		if r.Schema != "" {
			args = append(args, "--schema="+r.Schema)
		}
		args = append(args, "--file="+r.Path)
	}
	args = append(args, r.ExtraArgs...)

	cmd := Command{Kind: KindProcess, Bin: bin, Name: name, Args: args}
	if r.credPath != "" {
		// PGPASSFILE names the file; the password itself never enters the
		// environment, let alone argv.
		cmd.Env = []string{"PGPASSFILE=" + r.credPath}
	}
	return cmd, nil
}

// ---------- MySQL / MariaDB ----------

// buildMySQL produces `mysqldump --result-file=OUT DB` or `mysql DB` with
// the dump on stdin. `--defaults-extra-file` must be the very first
// argument, which is why the credential file is written before anything
// else is appended.
func buildMySQL(r Request) (Command, error) {
	bin, name, err := r.resolve()
	if err != nil {
		return Command{}, err
	}
	port := r.portOrDefault()

	var args []string
	if r.credPath != "" {
		// mysqldump insists this be the very first argument, before any
		// other option, or it exits with "unknown variable".
		args = append(args, "--defaults-extra-file="+r.credPath)
	}
	args = append(args,
		"--host="+r.Host,
		"--port="+strconv.Itoa(port),
	)
	if r.User != "" {
		args = append(args, "--user="+r.User)
	}

	cmd := Command{Kind: KindProcess, Bin: bin, Name: name}
	if r.Action == Restore {
		args = append(args, r.ExtraArgs...)
		args = append(args, r.Database)
		cmd.StdinPath = r.Path
	} else {
		args = append(args, "--result-file="+r.Path)
		args = append(args, r.ExtraArgs...)
		args = append(args, r.Database)
	}
	cmd.Args = args
	return cmd, nil
}

// ---------- SQLite ----------

// buildSQLite needs no external tool: a dump is `VACUUM INTO 'path'`,
// which writes a consistent copy of the database without touching the
// source, and a restore is that file copied back over the database file.
func buildSQLite(r Request) (Command, error) {
	if r.Action == Restore {
		return Command{Kind: KindCopy, CopyFrom: r.Path, CopyTo: r.File}, nil
	}
	return Command{
		Kind: KindSQL,
		SQL:  []string{"VACUUM INTO " + sqlQuote(r.Path)},
	}, nil
}

// ---------- DuckDB ----------

// buildDuckDB uses the engine's own statements. Both take a directory, not
// a file: EXPORT DATABASE writes a `schema.sql`, a `load.sql` and one CSV
// per table into it, and IMPORT DATABASE reads that layout back.
func buildDuckDB(r Request) (Command, error) {
	stmt := "EXPORT DATABASE " + sqlQuote(r.Path)
	if r.Action == Restore {
		stmt = "IMPORT DATABASE " + sqlQuote(r.Path)
	}
	return Command{Kind: KindSQL, SQL: []string{stmt}}, nil
}

// sqlQuote renders a path as a SQL string literal. Both engines that take
// one here read a backslash literally, so doubling the single quote is the
// whole escape.
func sqlQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// Endpoint is "host:port" for the engines that have one, for logging and
// for the SSH forward's remote address.
func Endpoint(host string, port int) string {
	return net.JoinHostPort(host, strconv.Itoa(port))
}
