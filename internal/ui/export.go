package ui

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/db"
	"lazysql/internal/export"
)

// `E` exports the open relation — or, from the query editor, the open
// query result — to a file. Unlike a copy it is not bounded by what fits
// in memory: the writer streams straight into the file, so the export
// costs one row of memory whatever the result's size. The format comes
// from the extension, progress goes to the command log, and `X` cancels
// a run that is taking too long.
//
// A table export re-issues Driver.QueryPage by OFFSET, one page at a
// time — exportSource's tableSource. A query result cannot be re-paged
// that way without risking a change in what the statement means, so it
// re-runs once through Driver.QueryStream instead — querySource — and
// SQL/INSERT is only offered when db.SingleTableSelect can name the one
// table the columns belong to.

// exportProgressEvery is how many rows pass between progress lines. It
// is large enough that a fast export logs a handful of lines and small
// enough that a slow one keeps saying it is alive.
const exportProgressEvery = 5000

// exportState is the one export a Model may have in flight. Only one
// runs at a time: a second `E` would race two writers over the log, and
// nothing about the flow needs a queue.
type exportState struct {
	running bool
	// id distinguishes the run a message belongs to, so the reply of a
	// cancelled export cannot resurrect the progress line of its
	// successor.
	id int
	// table names the relation being exported, for the cancel and
	// "already running" log lines. The path travels with the worker's
	// own messages instead, so a stale reply can never name the wrong
	// file.
	table  string
	cancel context.CancelFunc
	// ch carries progress and the final outcome out of the worker
	// goroutine. It is unbuffered: a UI that stopped reading should
	// block the worker rather than let it run ahead.
	ch chan tea.Msg
}

// ---------- messages ----------

type exportProgressMsg struct {
	id   int
	rows int64
}

type exportDoneMsg struct {
	id    int
	path  string
	rows  int64
	bytes int64
	err   error
}

// ---------- the flow ----------

// startExport is `E`: prompt for a path, then stream. The prompt is
// pre-filled with a plausible name so the common case is one keystroke
// plus enter.
func (m *Model) startExport() tea.Cmd {
	if !m.data.browsing() && !(m.data.isQuery() && m.data.hasResult()) {
		return logCmd("-- export skipped: nothing to export")
	}
	if m.driver == nil {
		return logCmd("-- export skipped: not connected")
	}
	// The DDL tab exports the CREATE statement instead of streaming the
	// table's data — a different enough shape (one small write, no
	// pager, no progress) that it does not share exportState with the
	// data formats.
	if m.tab == mainTabDDL {
		return m.startDDLExport()
	}
	if m.export.running {
		return logCmd("-- export skipped: %s is still exporting (X cancels it)", m.export.table)
	}
	subject := m.dataSubject()
	m.modal = newPromptModal(
		"Export "+subject+" — file path",
		"~/"+defaultExportPath(subject),
		defaultExportPath(subject),
		func(mm *Model, value string) tea.Cmd { return mm.runExport(value) },
	)
	return nil
}

// sanitizePathComponent replaces path separators in a relation name with
// `-`, so a prompt's default value is always one path component.
func sanitizePathComponent(name string) string {
	return strings.Map(func(r rune) rune {
		if r == os.PathSeparator || r == '/' {
			return '-'
		}
		return r
	}, name)
}

// defaultExportPath is the prompt's initial value: the relation's name
// as a CSV in the working directory.
func defaultExportPath(table string) string {
	return sanitizePathComponent(table) + ".csv"
}

// defaultDDLExportPath is startDDLExport's prompt's initial value.
func defaultDDLExportPath(table string) string {
	return sanitizePathComponent(table) + "-ddl.sql"
}

// runExport validates the path and format, then dispatches to whichever
// scope the Data tab is showing. Everything that can fail synchronously
// does so here, so the worker only ever reports problems that needed the
// database.
func (m *Model) runExport(path string) tea.Cmd {
	if path == "" {
		return logCmd("-- export cancelled: no path given")
	}
	full, err := expandPath(path)
	if err != nil {
		return logCmd("-- export FAILED: %v", err)
	}
	format, err := export.FormatForPath(full)
	if err != nil {
		return logCmd("-- export %s FAILED: %v", full, err)
	}
	if m.driver == nil {
		return logCmd("-- export skipped: nothing open")
	}
	switch {
	case m.data.browsing():
		return m.runTableExport(full, format)
	case m.data.isQuery() && m.data.hasResult():
		return m.runQueryExport(full, format)
	default:
		return logCmd("-- export skipped: nothing open")
	}
}

// runTableExport streams a browsed relation's rows, page by page, into
// full.
func (m *Model) runTableExport(full string, format export.Format) tea.Cmd {
	f, err := os.Create(full)
	if err != nil {
		return logCmd("-- export FAILED: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.export = exportState{
		running: true,
		id:      m.export.id + 1,
		table:   m.data.table,
		cancel:  cancel,
		ch:      make(chan tea.Msg),
	}
	m.keys.CancelExport.SetEnabled(true)

	job := exportJob{
		id:      m.export.id,
		ctx:     ctx,
		file:    f,
		path:    full,
		format:  format,
		options: m.exportOptions(""),
		source:  tableSource(pagerFor(m.driver, m.data)),
		ch:      m.export.ch,
	}
	return tea.Batch(
		logCmd("-- export %s to %s as %s (streaming %d rows per page)…",
			m.data.table, full, strings.ToUpper(string(format)), export.DefaultPageSize),
		startExportCmd(job),
	)
}

// runQueryExport re-runs the query result's own statement once, through
// Driver.QueryStream, and writes every row it yields to full. This is
// what makes the export cover the whole result rather than the
// maxQueryRows-capped, in-memory copy the grid pages from.
func (m *Model) runQueryExport(full string, format export.Format) tea.Cmd {
	table := ""
	if format == export.FormatSQL {
		t, ok := db.SingleTableSelect(m.driver.Engine(), m.data.query)
		if !ok {
			return logCmd(
				"-- export %s FAILED: query result has no single-table mapping for SQL — use .csv, .json or .md",
				full)
		}
		table = t
	}

	f, err := os.Create(full)
	if err != nil {
		return logCmd("-- export FAILED: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.export = exportState{
		running: true,
		id:      m.export.id + 1,
		table:   "query result",
		cancel:  cancel,
		ch:      make(chan tea.Msg),
	}
	m.keys.CancelExport.SetEnabled(true)

	job := exportJob{
		id:      m.export.id,
		ctx:     ctx,
		file:    f,
		path:    full,
		format:  format,
		options: export.Options{Database: m.data.database, Table: table, Dialect: m.driver.Dialect()},
		source:  querySource(queryRunnerFor(m.driver, m.data)),
		ch:      m.export.ch,
	}
	return tea.Batch(
		logCmd("-- export query result to %s as %s (streaming)…", full, strings.ToUpper(string(format))),
		startExportCmd(job),
	)
}

// expandPath resolves `~` and makes the path absolute, so the log line
// names the file the user can actually open.
func expandPath(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot resolve ~: %w", err)
		}
		path = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(path, "~"), "/"))
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return abs, nil
}

// ---------- the worker ----------

// exportJob is everything the worker goroutine needs. It is a value, so
// the goroutine shares nothing mutable with the Model.
type exportJob struct {
	id      int
	ctx     context.Context
	file    *os.File
	path    string
	format  export.Format
	options export.Options
	source  exportSource
	ch      chan tea.Msg
}

// exportSource is how exportJob reads the rows it writes: a table pager,
// re-issued once per streaming page, or a query runner, whose one-shot
// statement streams straight through. The worker calls either the same
// way, so it does not need to know which kind it has.
type exportSource func(ctx context.Context, w export.Writer, o export.StreamOptions) (rows int64, truncated bool, err error)

func tableSource(pager export.Pager) exportSource {
	return func(ctx context.Context, w export.Writer, o export.StreamOptions) (int64, bool, error) {
		return export.Stream(ctx, w, pager, o)
	}
}

func querySource(run export.QueryRunner) exportSource {
	return func(ctx context.Context, w export.Writer, o export.StreamOptions) (int64, bool, error) {
		return export.StreamQuery(ctx, w, run, o)
	}
}

// startExportCmd launches the worker and blocks until its first
// message. Subsequent messages are pulled by waitExportCmd, which the
// root re-issues after every progress line — the standard Bubbletea way
// to turn a long job into a stream of messages.
func startExportCmd(job exportJob) tea.Cmd {
	return func() tea.Msg {
		go job.run()
		return <-job.ch
	}
}

func waitExportCmd(ch chan tea.Msg) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg { return <-ch }
}

func (j exportJob) run() {
	buf := bufio.NewWriterSize(j.file, 64*1024)
	counting := &countingWriter{w: buf}

	send := func(msg tea.Msg) bool {
		// A cancelled export must not block forever on a UI that has
		// stopped reading the channel.
		select {
		case j.ch <- msg:
			return true
		case <-j.ctx.Done():
			return false
		}
	}

	var rows int64
	w, err := export.NewWriter(counting, j.format, j.options)
	if err == nil {
		rows, _, err = j.source(j.ctx, w, export.StreamOptions{
			PageSize:      export.DefaultPageSize,
			ProgressEvery: exportProgressEvery,
			Progress: func(n int64) {
				send(exportProgressMsg{id: j.id, rows: n})
			},
		})
	}
	if flushErr := buf.Flush(); err == nil {
		err = flushErr
	}
	if closeErr := j.file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		// A half-written export is worse than none: it looks complete.
		os.Remove(j.path)
	}

	// The final message goes out even on cancellation, so the root can
	// clear the running flag; a plain send would have been swallowed by
	// the ctx.Done() branch above.
	j.ch <- exportDoneMsg{id: j.id, path: j.path, rows: rows, bytes: counting.n, err: err}
}

// countingWriter tracks how much the export wrote, for the log line.
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// ---------- model wiring ----------

// cancelExport is `X`. The worker notices the cancelled context between
// pages and between rows and removes the partial file.
func (m *Model) cancelExport() tea.Cmd {
	if !m.export.running {
		return nil
	}
	m.export.cancel()
	return logCmd("-- cancelling export of %s…", m.export.table)
}

// finishExport clears the in-flight state and renders the outcome.
func (m *Model) finishExport(msg exportDoneMsg) tea.Cmd {
	m.export.running = false
	m.export.cancel = nil
	m.export.ch = nil
	m.keys.CancelExport.SetEnabled(false)
	switch {
	case errors.Is(msg.err, context.Canceled):
		return logCmd("-- export cancelled after %d rows — %s removed", msg.rows, msg.path)
	case msg.err != nil:
		return logCmd("-- export FAILED after %d rows: %v — %s removed", msg.rows, msg.err, msg.path)
	default:
		return logCmd("-- export wrote %d rows (%s) to %s",
			msg.rows, byteCount(int(msg.bytes)), msg.path)
	}
}

// ---------- DDL-only export ----------

// startDDLExport is `E` on the DDL tab: prompt for a `.sql` path and
// write the relation's CREATE statement to it verbatim. It fetches the
// metadata first when nothing has needed it yet, the same way `y` does
// on a cold DDL tab.
func (m *Model) startDDLExport() tea.Cmd {
	if !m.meta.loaded {
		// A dedicated action, not actExportTable: if the metadata fetch
		// is still in flight when the user leaves the DDL tab, the
		// deferred replay must still open the DDL prompt rather than
		// falling into startExport's now-not-DDL-tab branch and offering
		// the plain CSV/JSON/SQL export instead.
		return m.deferUntilMeta(actExportDDL)
	}
	if m.meta.ddl == "" {
		return logCmd("-- export DDL of %s skipped: %s", m.data.table, m.ddlProblem())
	}
	m.modal = newPromptModal(
		"Export "+m.data.table+" DDL — file path",
		"~/"+defaultDDLExportPath(m.data.table),
		defaultDDLExportPath(m.data.table),
		func(mm *Model, value string) tea.Cmd { return mm.runDDLExport(value) },
	)
	return nil
}

// runDDLExport writes the DDL already cached on the meta view to a file.
// A DDL statement is a handful of kilobytes at most, so this skips the
// streaming worker the data formats use and writes synchronously off the
// update loop instead.
func (m *Model) runDDLExport(path string) tea.Cmd {
	if path == "" {
		return logCmd("-- export cancelled: no path given")
	}
	full, err := expandPath(path)
	if err != nil {
		return logCmd("-- export FAILED: %v", err)
	}
	if !strings.EqualFold(filepath.Ext(full), ".sql") {
		return logCmd("-- export %s FAILED: DDL export needs a .sql path", full)
	}
	if !m.data.browsing() || m.meta.ddl == "" {
		return logCmd("-- export skipped: nothing to export")
	}

	table, ddl := m.data.table, m.meta.ddl
	return func() tea.Msg {
		if err := os.WriteFile(full, []byte(ddl), 0o644); err != nil {
			return commandLogMsg{line: fmt.Sprintf("-- export DDL of %s FAILED: %v", table, err)}
		}
		return commandLogMsg{line: fmt.Sprintf("-- export DDL of %s wrote %s", table, full)}
	}
}
