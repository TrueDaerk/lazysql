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

	"lazysql/internal/export"
)

// `E` exports the open relation to a file. Unlike a copy it is not
// bounded by what fits in memory: the writer streams pages straight
// into the file, so the export costs one page of rows whatever the
// table's size. The format comes from the extension, progress goes to
// the command log, and `X` cancels a run that is taking too long.

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
	if !m.data.open() {
		return logCmd("-- export skipped: no relation open")
	}
	if m.driver == nil {
		return logCmd("-- export skipped: not connected")
	}
	if m.export.running {
		return logCmd("-- export skipped: %s is still exporting (X cancels it)", m.export.table)
	}
	m.modal = newPromptModal(
		"Export "+m.data.table+" — file path",
		"~/"+m.data.table+".csv",
		defaultExportPath(m.data.table),
		func(mm *Model, value string) tea.Cmd { return mm.runExport(value) },
	)
	return nil
}

// defaultExportPath is the prompt's initial value: the relation's name
// as a CSV in the working directory.
func defaultExportPath(table string) string {
	name := strings.Map(func(r rune) rune {
		if r == os.PathSeparator || r == '/' {
			return '-'
		}
		return r
	}, table)
	return name + ".csv"
}

// runExport validates the path, opens the file and starts the worker.
// Everything that can fail synchronously does so here, so the worker
// only ever reports problems that needed the database.
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
	if !m.data.open() || m.driver == nil {
		return logCmd("-- export skipped: nothing open")
	}

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
		pager:   pagerFor(m.driver, m.data),
		ch:      m.export.ch,
	}
	return tea.Batch(
		logCmd("-- export %s to %s as %s (streaming %d rows per page)…",
			m.data.table, full, strings.ToUpper(string(format)), export.DefaultPageSize),
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
	pager   export.Pager
	ch      chan tea.Msg
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
		rows, _, err = export.Stream(j.ctx, w, j.pager, export.StreamOptions{
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
