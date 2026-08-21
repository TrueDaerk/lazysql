package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/config"
	"lazysql/internal/db"
	"lazysql/internal/dump"
	"lazysql/internal/sshtunnel"
)

// `B` on panel [1] or [2] opens the backup menu: dump the database to a
// file with the engine's own tool, or restore such a file back into it.
//
// Three things shape the flow. The command is shown before it runs, with
// an editable arguments line, because a dump is the one place where the
// user's own flags (`--data-only`, `--single-transaction`) matter and
// lazysql cannot guess them. The password never appears in that preview,
// in the command log, or in the child's argv — it travels in a 0600
// credential file that internal/dump removes as soon as the tool exits.
// And a restore is gated behind typing the database name, because unlike
// every other action in lazysql it overwrites data without staging.

// backupState is the one dump or restore a Model may have in flight. Like
// the export, only one runs at a time: two tools writing progress into the
// same log would interleave into nonsense, and nothing needs a queue.
type backupState struct {
	running bool
	// id distinguishes runs, so a cancelled job's late reply cannot
	// clobber the one started after it.
	id      int
	action  dump.Action
	subject string
	cancel  context.CancelFunc
	// ch carries stderr lines and the final outcome out of the worker.
	ch chan tea.Msg
}

// ---------- messages ----------

// backupLineMsg is one line the tool wrote to stderr — pg_dump and
// mysqldump both report progress there.
type backupLineMsg struct {
	id   int
	line string
}

// backupDoneMsg is the outcome. command is the redacted command that ran,
// for the log line.
type backupDoneMsg struct {
	id       int
	action   dump.Action
	path     string
	command  string
	exitCode int
	err      error
}

// ---------- entry points ----------

// backupConnection is the profile a dump or restore acts on. From panel
// [1] that is whatever the cursor is on — a dump does not need the profile
// to be connected, so picking one there and pressing `B` has to mean it.
// From anywhere else it is the live connection, since that is what the
// browsed database belongs to.
func (m Model) backupConnection() (config.Connection, bool) {
	if m.focus == panelConnections {
		if c, ok := m.selectedConnection(); ok {
			return c, true
		}
	}
	if m.active != "" {
		if c, ok := m.findConn(m.active); ok {
			return c, true
		}
	}
	return m.selectedConnection()
}

// openBackupMenu is `B`: the two-entry menu the issue's flow starts from.
func (m *Model) openBackupMenu() tea.Cmd {
	c, ok := m.backupConnection()
	if !ok {
		return logCmd("-- backup skipped: no connection selected")
	}
	if m.backup.running {
		return logCmd("-- backup skipped: a %s is already running (X cancels it)", m.backup.action)
	}
	// The entries dispatch through runAction, like the `a` actions menu,
	// so a key press and a menu pick reach the same code.
	entry := func(key, label string, id actionID) menuEntry {
		return menuEntry{key: key, label: label, action: func(mm *Model) tea.Cmd {
			next, cmd := mm.runAction(id)
			*mm = next
			return cmd
		}}
	}
	m.modal = &menuModal{
		title: "Backup — " + c.Name,
		entries: []menuEntry{
			entry("d", "Dump database to a file", actDumpDatabase),
			entry("r", "Restore dump into the database (overwrites data)", actRestoreDump),
			{key: "esc", label: "cancel"},
		},
	}
	return nil
}

// startBackup runs every check that does not need the user, then opens the
// modal. A profile that asks for its password on connect is prompted for
// it first: the tool authenticates on its own and cannot borrow the live
// session's credentials.
func (m *Model) startBackup(action dump.Action) tea.Cmd {
	c, ok := m.backupConnection()
	if !ok {
		return logCmd("-- %s skipped: no connection selected", action)
	}
	if m.backup.running {
		return logCmd("-- %s skipped: a %s is already running (X cancels it)", action, m.backup.action)
	}
	if db.FileBased(c.Engine) && (m.driver == nil || m.active != c.Name) {
		// SQLite and DuckDB run their dump through the open session's own
		// engine, so it has to be *this* profile's session: another
		// connection's driver would back up the wrong database.
		return logCmd("-- %s skipped: connect to %s first", action, c.Name)
	}
	if action == dump.Restore && (c.ReadOnly || m.readOnly()) {
		return logCmd("-- restore skipped: %s is read-only", c.Name)
	}
	if c.NeedsPassword() && c.AskPassword {
		m.modal = newPasswordPrompt(c, func(pw string) tea.Cmd {
			return func() tea.Msg { return backupPasswordMsg{action: action, password: pw} }
		})
		return nil
	}
	pw, err := resolvePassword(c, "", false)
	if err != nil {
		return logCmd("-- %s FAILED: read password from keyring: %v", action, err)
	}
	return m.openBackupModal(action, pw)
}

// backupPasswordMsg carries an "ask on connect" profile's password from
// the prompt back into the flow. It never reaches a log line.
type backupPasswordMsg struct {
	action   dump.Action
	password string
}

// baseRequest is the request with everything but the path and the user's
// own arguments filled in. The endpoint is still the profile's: the tunnel
// substitution happens in the worker, which is where the forward exists.
func (m Model) baseRequest(action dump.Action, c config.Connection, password string) dump.Request {
	database, schema := m.backupTarget(c)
	return dump.Request{
		Action:   action,
		Engine:   c.Engine,
		Host:     c.Host,
		Port:     c.Port,
		User:     c.User,
		Database: database,
		Schema:   schema,
		File:     c.File,
		Password: password,
	}
}

// backupTarget maps the browsed namespace onto the tool's arguments. Only
// PostgreSQL has both a database and a schema — panel [2] lists schemas
// there — so only there can a dump be narrowed to the browsed namespace.
// Everywhere else the browsed namespace *is* the database.
func (m Model) backupTarget(c config.Connection) (database, schema string) {
	browsed := m.database
	if browsed == pseudoDatabase {
		browsed = ""
	}
	switch c.Engine {
	case db.EnginePostgres:
		database = c.Database
		if database == "" {
			database = browsed
		}
		if browsed != "" && browsed != database {
			schema = browsed
		}
		return database, schema
	case db.EngineMySQL, db.EngineMariaDB:
		if browsed != "" {
			return browsed, ""
		}
		return c.Database, ""
	}
	// File engines have one namespace; the name is only used for the
	// default file name and the restore confirmation.
	if browsed != "" {
		return browsed, ""
	}
	return backupFileLabel(c), ""
}

// backupFileLabel names a file-based connection for prompts and defaults:
// the database file's base name, or the profile's name for an in-memory
// DuckDB.
func backupFileLabel(c config.Connection) string {
	if c.File == "" {
		return c.Name
	}
	base := filepath.Base(c.File)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// defaultBackupPath is the modal's prefilled path. DuckDB gets a directory
// because EXPORT DATABASE writes one.
func defaultBackupPath(engine db.Engine, database string) string {
	name := sanitizePathComponent(database)
	if name == "" {
		name = "database"
	}
	if ext := dump.DefaultExtension(engine); ext != "" {
		return name + ext
	}
	return name + "-export"
}

// openBackupModal previews the command — which is also the upfront
// "is the tool installed" check — and then puts it on screen.
func (m *Model) openBackupModal(action dump.Action, password string) tea.Cmd {
	c, ok := m.backupConnection()
	if !ok {
		return logCmd("-- %s skipped: no connection selected", action)
	}
	base := m.baseRequest(action, c, password)
	defaultPath := defaultBackupPath(c.Engine, base.Database)

	// Previewing is the upfront tool check: it resolves the binary on
	// PATH, so a missing pg_dump is a named error here rather than a
	// failure after the user has filled the form in.
	probe := base
	probe.Path = defaultPath
	if _, err := dump.Preview(probe); err != nil {
		return logCmd("-- %s of %s FAILED: %v", action, c.Name, err)
	}

	pathLabel, verb := "Output file", "dump"
	if action == dump.Restore {
		pathLabel, verb = "Input file", "restore"
	}
	if c.Engine == db.EngineDuckDB {
		pathLabel = strings.Replace(pathLabel, "file", "directory", 1)
	}

	fields := []*formField{
		newTextField("path", pathLabel, defaultPath, "~/"+defaultPath),
		newTextField("args", "Arguments", "", "extra flags for the tool").
			withHelp("appended to the command above"),
	}
	if action == dump.Restore {
		fields = append(fields,
			newTextField("confirm", "Type "+base.Database, "", base.Database).
				withHelp("a restore overwrites data and cannot be undone"))
	}

	title := fmt.Sprintf("%s %s — %s", capitalize(verb), m.taggedConnName(c.Name), base.Database)
	form := newFormModal(title, fields, func(mm *Model, f *formModal) (bool, tea.Cmd) {
		if action == dump.Restore && f.value("confirm") != base.Database {
			f.err = "type " + base.Database + " to confirm the overwrite"
			return false, nil
		}
		path, err := expandPath(f.value("path"))
		if err != nil {
			f.err = err.Error()
			return false, nil
		}
		req := base
		req.Path = path
		req.ExtraArgs = strings.Fields(f.value("args"))
		return true, mm.runBackup(req)
	})
	form.footer = "tab/↑↓ field · enter run · esc cancel"
	form.withBody(func(f *formModal) []string {
		req := base
		req.Path = strings.TrimSpace(f.rawValue("path"))
		req.ExtraArgs = strings.Fields(f.rawValue("args"))
		return backupPreviewLines(req)
	})
	m.modal = form
	return nil
}

// backupPreviewLines renders the command the form would run. A request
// that cannot be assembled yet — an empty path while the user is still
// typing — shows the reason instead, so the box never goes blank.
func backupPreviewLines(req dump.Request) []string {
	cmd, err := dump.Preview(req)
	if err != nil {
		return []string{"$ " + err.Error()}
	}
	return []string{"$ " + cmd.String()}
}

// ---------- running ----------

// capitalize upper-cases the first byte of an ASCII word — enough for the
// two verbs this file titles a modal with.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// ---------- running ----------

// runBackup starts the worker. SQLite's restore is the one case that
// touches the session first: the file is about to be overwritten, and a
// connection left open over the old one would replay a stale journal onto
// the new database.
func (m *Model) runBackup(req dump.Request) tea.Cmd {
	c, ok := m.backupConnection()
	if !ok {
		return logCmd("-- %s skipped: no connection selected", req.Action)
	}
	if m.backup.running {
		return logCmd("-- %s skipped: a %s is already running", req.Action, m.backup.action)
	}

	job := backupJob{ctx: nil, req: req}
	var closed tea.Cmd
	if db.FileBased(c.Engine) {
		if m.driver == nil || m.active != c.Name {
			return logCmd("-- %s skipped: connect to %s first", req.Action, c.Name)
		}
		if c.Engine == db.EngineSQLite && req.Action == dump.Restore {
			name := m.active
			// Synchronous, not closeSessionCmd: the copy must not race a
			// teardown still queued as a command.
			m.closeSession()
			m.setConnStatus(name, statusIdle, "")
			m.resetBrowse()
			closed = logCmd("-- restore closed the connection to %s — reconnect when it finishes", name)
		} else {
			job.driver = m.driver
		}
	} else if m.tunnel != nil && m.active == c.Name {
		// The tool cannot be handed a Go dialer, so the live tunnel gets
		// a real loopback port for the duration of the run.
		job.tunnel = m.tunnel
		job.remote = dump.Endpoint(c.Host, portOrDefault(c))
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.backup = backupState{
		running: true,
		id:      m.backup.id + 1,
		action:  req.Action,
		subject: req.Database,
		cancel:  cancel,
		ch:      make(chan tea.Msg),
	}
	m.keys.CancelBackup.SetEnabled(true)

	job.id = m.backup.id
	job.ctx = ctx
	job.ch = m.backup.ch

	return tea.Batch(
		closed,
		logCmd("-- %s %s to %s…", req.Action, req.Database, req.Path),
		startBackupCmd(job),
	)
}

// portOrDefault is the profile's port, or the engine's default when it
// left the field empty.
func portOrDefault(c config.Connection) int {
	if c.Port > 0 {
		return c.Port
	}
	return db.DefaultPort(c.Engine)
}

// backupJob is everything the worker needs. It is a value: the goroutine
// shares nothing mutable with the Model. driver is only set for the
// engines whose dump is SQL, and tunnel only for a live tunnelled
// connection.
type backupJob struct {
	id     int
	ctx    context.Context
	req    dump.Request
	driver db.Driver
	tunnel *sshtunnel.Tunnel
	remote string
	ch     chan tea.Msg
}

func startBackupCmd(job backupJob) tea.Cmd {
	return func() tea.Msg {
		go job.run()
		return <-job.ch
	}
}

func waitBackupCmd(ch chan tea.Msg) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg { return <-ch }
}

func (j backupJob) run() {
	done := func(command string, exit int, err error) {
		j.ch <- backupDoneMsg{
			id: j.id, action: j.req.Action, path: j.req.Path,
			command: command, exitCode: exit, err: err,
		}
	}

	req := j.req
	if j.tunnel != nil {
		fwd, err := j.tunnel.Listen(j.remote)
		if err != nil {
			done("", -1, err)
			return
		}
		defer fwd.Close()
		// Everything below now talks to the loopback end of the tunnel;
		// the credential file is written for that endpoint too, which is
		// what a `.pgpass` host field has to match.
		req.Host, req.Port = "127.0.0.1", fwd.Port()
	}

	cmd, err := dump.Build(req)
	if err != nil {
		done("", -1, err)
		return
	}
	defer cmd.Cleanup()

	switch cmd.Kind {
	case dump.KindProcess:
		j.runProcess(cmd, done)
	case dump.KindSQL:
		j.runSQL(cmd, done)
	case dump.KindCopy:
		err := copyFile(cmd.CopyFrom, cmd.CopyTo)
		if err == nil {
			// A journal left over from the database that was just
			// replaced would be replayed onto the new one.
			removeSQLiteSidecars(cmd.CopyTo)
		}
		done(cmd.String(), 0, err)
	default:
		done(cmd.String(), -1, fmt.Errorf("unsupported job kind %v", cmd.Kind))
	}
}

func (j backupJob) runProcess(cmd dump.Command, done func(string, int, error)) {
	res := dump.Run(j.ctx, cmd, func(line string) {
		// A cancelled run must not block forever on a UI that stopped
		// reading; the final message below always gets through.
		select {
		case j.ch <- backupLineMsg{id: j.id, line: line}:
		case <-j.ctx.Done():
		}
	})
	done(cmd.String(), res.ExitCode, res.Err)
}

// runSQL is the file engines' path: the statement goes through the same
// Driver every other statement does, so it lands in the command log by
// itself rather than being echoed here.
func (j backupJob) runSQL(cmd dump.Command, done func(string, int, error)) {
	if j.driver == nil {
		done(cmd.String(), -1, errors.New("not connected"))
		return
	}
	for _, stmt := range cmd.SQL {
		if _, err := j.driver.Exec(j.ctx, stmt); err != nil {
			done(cmd.String(), -1, err)
			return
		}
	}
	done(cmd.String(), 0, nil)
}

// copyFile writes src over dst through a temporary file in dst's
// directory, so a failure part-way leaves the old database intact rather
// than a truncated one.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	tmp, err := os.CreateTemp(filepath.Dir(dst), ".lazysql-restore-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

// removeSQLiteSidecars drops the write-ahead log and shared-memory files
// of a database that has just been replaced. They belong to the old file
// only, and SQLite would otherwise treat them as the new one's journal.
func removeSQLiteSidecars(path string) {
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		os.Remove(path + suffix)
	}
}

// ---------- model wiring ----------

// cancelBackup is `X` while a job runs. The child process group is killed
// and a partial dump is removed; a restore is left as it is, because
// half a database is not something lazysql can undo by deleting a file.
func (m *Model) cancelBackup() tea.Cmd {
	if !m.backup.running {
		return nil
	}
	m.backup.cancel()
	return logCmd("-- cancelling %s of %s…", m.backup.action, m.backup.subject)
}

// finishBackup clears the in-flight state and renders the outcome.
func (m *Model) finishBackup(msg backupDoneMsg) tea.Cmd {
	subject := m.backup.subject
	m.backup.running = false
	m.backup.cancel = nil
	m.backup.ch = nil
	m.keys.CancelBackup.SetEnabled(false)

	var cmds []tea.Cmd
	if msg.command != "" {
		// The command is the audit trail of what ran; internal/dump
		// guarantees it carries no secret.
		cmds = append(cmds, logCmd("$ %s", msg.command))
	}
	switch {
	case errors.Is(msg.err, context.Canceled):
		if msg.action == dump.Dump {
			// A half-written dump is worse than none: it looks complete.
			os.Remove(msg.path)
			cmds = append(cmds, logCmd("-- dump of %s cancelled — %s removed", subject, msg.path))
		} else {
			cmds = append(cmds, logCmd(
				"-- restore of %s cancelled — the database may be partially restored", subject))
		}
	case msg.err != nil:
		if msg.action == dump.Dump {
			os.Remove(msg.path)
		}
		cmds = append(cmds, logCmd("-- %s of %s FAILED: %v", msg.action, subject, msg.err))
	case msg.action == dump.Restore:
		cmds = append(cmds, logCmd("-- restore of %s applied %s", subject, msg.path))
	default:
		cmds = append(cmds, logCmd("-- dump of %s wrote %s (%s)",
			subject, msg.path, byteCount(int(pathSize(msg.path)))))
	}
	return tea.Batch(cmds...)
}

// pathSize is the size of a finished dump, for the log line. A directory
// (DuckDB's EXPORT DATABASE) reports the sum of its entries.
func pathSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	if !info.IsDir() {
		return info.Size()
	}
	var total int64
	entries, err := os.ReadDir(path)
	if err != nil {
		return 0
	}
	for _, e := range entries {
		if fi, err := e.Info(); err == nil {
			total += fi.Size()
		}
	}
	return total
}
