package ui

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/csvimport"
	"lazysql/internal/db"
)

// `I` on a table in [2] Objects imports a CSV file into it — the way back
// in for what `E` exports. The flow has three steps, each a modal:
//
//  1. a path form with the shared filesystem completion;
//  2. a settings form showing what csvimport guessed — delimiter, header,
//     the CSV → table column mapping, the NULL marker — every one of them
//     editable, over a live preview of the first rows converted to the
//     target column types; enter there is the confirmation;
//  3. the import itself, a worker in the shape of the export's: progress
//     lines in the command log, `X` to cancel.
//
// The rows go through db.Driver.ImportRows — one transaction, one prepared
// INSERT, values bound as parameters — so a failing row, a field that does
// not fit its column and a cancel all roll the whole import back, and a
// read-only session refuses it in the driver.

// importProgressEvery is how many rows pass between progress lines.
const importProgressEvery = 5000

// importPreviewRows is how many rows the settings form previews.
const importPreviewRows = 5

// importState is the one import a Model may have in flight.
type importState struct {
	running bool
	id      int
	table   string
	cancel  context.CancelFunc
	ch      chan tea.Msg
}

// ---------- messages ----------

// importSetupMsg carries what the settings form needs: the target's
// columns and the head of the file. err is set when either could not be
// read.
type importSetupMsg struct {
	database, table, path string
	cols                  []db.Column
	sample                []byte
	truncated             bool
	size                  int64
	err                   error
}

type importProgressMsg struct {
	id         int
	rows       int64
	done, size int64
}

type importDoneMsg struct {
	id    int
	table string
	path  string
	rows  int64
	err   error
}

// ---------- step 1: the path ----------

// startImport is `I`: check the target, then ask for the file.
func (m *Model) startImport() tea.Cmd {
	if m.driver == nil {
		return logCmd("-- import skipped: not connected")
	}
	if m.readOnly() {
		return readOnlyBlocked("import")
	}
	if m.exports.csv.running {
		return logCmd("-- import skipped: %s is still importing (X cancels it)", m.exports.csv.table)
	}
	if cmd := m.refuseWithTx("import CSV"); cmd != nil {
		return cmd
	}
	n := m.selectedNode()
	if n == nil || n.kind != nodeObject || !n.cat.relational() {
		return logCmd("-- import skipped: select a table in [2] Objects")
	}
	if kind, _ := n.cat.relationKind(); kind != db.RelationTable {
		return logCmd("-- import skipped: %s is a view — import needs a table", n.name)
	}
	database, table := n.database, n.name
	form := newFormModal("Import CSV into "+table, []*formField{
		newTextField("path", "CSV file", "", "~/"+defaultExportPath(table)).
			withSuggest().withValidate(requiredField("a file")),
	}, func(mm *Model, f *formModal) (bool, tea.Cmd) {
		path, err := expandPath(f.value("path"))
		if err != nil {
			f.err = err.Error()
			return false, nil
		}
		info, err := os.Stat(path)
		if err != nil {
			f.err = err.Error()
			return false, nil
		}
		if info.IsDir() {
			f.err = path + " is a directory"
			return false, nil
		}
		return true, loadImportSetupCmd(mm.driver, database, table, path, info.Size())
	})
	form.footer = "tab complete · enter next · esc cancel"
	m.modal = form
	return nil
}

// loadImportSetupCmd reads the target's columns and the head of the file
// off the update loop.
func loadImportSetupCmd(drv db.Driver, database, table, path string, size int64) tea.Cmd {
	return func() tea.Msg {
		msg := importSetupMsg{database: database, table: table, path: path, size: size}
		cols, err := drv.TableColumns(context.Background(), database, table)
		if err != nil {
			msg.err = err
			return msg
		}
		if len(cols) == 0 {
			msg.err = fmt.Errorf("%s has no columns", table)
			return msg
		}
		msg.cols = cols
		f, err := os.Open(path)
		if err != nil {
			msg.err = err
			return msg
		}
		defer f.Close()
		buf := make([]byte, csvimport.SampleSize)
		n, err := io.ReadFull(f, buf)
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
			msg.err = err
			return msg
		}
		msg.sample = buf[:n]
		msg.truncated = int64(n) < size
		return msg
	}
}

// ---------- step 2: settings and preview ----------

// openImportSettings shows csvimport's guesses as editable fields over a
// preview of what would be inserted.
func (m *Model) openImportSettings(msg importSetupMsg) tea.Cmd {
	if msg.err != nil {
		return logCmd("-- import into %s FAILED: %v", msg.table, msg.err)
	}
	if len(msg.sample) == 0 {
		return logCmd("-- import into %s skipped: %s is empty", msg.table, msg.path)
	}
	guess := csvimport.Sniff(msg.sample, msg.truncated, msg.cols)
	head, err := csvimport.NewReader(strings.NewReader(string(msg.sample)), guess)
	if err != nil {
		return logCmd("-- import into %s FAILED: %s: %v", msg.table, msg.path, err)
	}
	mapping := csvimport.DefaultMapping(head.Header, head.Width, guess.Header, msg.cols)

	fields := []*formField{
		newTextField("delim", "Delimiter", csvimport.FormatDelimiter(guess.Delimiter), `, ; \t |`).
			withHelp("detected; \\t is a tab").
			withValidate(func(_ *formModal, v string) string {
				if _, err := csvimport.ParseDelimiter(v); err != nil {
					return err.Error()
				}
				return ""
			}),
		newBoolField("header", "First line is a header", guess.Header),
		newTextField("mapping", "Columns", csvimport.FormatMapping(mapping, msg.cols), "table column per CSV column").
			withHelp("one per CSV column, - skips"),
		newTextField("null", "NULL marker", "", "empty field").
			withHelp("field text read as NULL"),
	}
	st := &importSettings{setup: msg}
	form := newFormModal("Import "+displayPath(msg.path)+" into "+msg.table, fields,
		func(mm *Model, f *formModal) (bool, tea.Cmd) {
			plan, err := st.plan(f)
			if err != nil {
				f.err = err.Error()
				return false, nil
			}
			return true, mm.runImport(msg, plan)
		})
	form.footer = "tab/↑↓ field · space toggle · enter import · esc cancel"
	form.withBody(st.body)
	m.modal = form
	return nil
}

// displayPath shortens a path under the home directory to `~/…` for a
// title.
func displayPath(path string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(path, home+string(os.PathSeparator)) {
		return "~" + path[len(home):]
	}
	return path
}

// importSettings turns the settings form's fields into a csvimport.Plan,
// and renders the preview. The preview re-parses the sample, so it is
// cached on the field values it was built from: the form draws far more
// often than it is edited.
type importSettings struct {
	setup    importSetupMsg
	cacheKey string
	cache    []string
}

// importBodyRows is the fixed height of the preview block. The form pins
// its geometry at the first draw, so the block is always this tall.
const importBodyRows = 5 + importPreviewRows

func (st *importSettings) settings(f *formModal) (csvimport.Settings, error) {
	d, err := csvimport.ParseDelimiter(f.rawValue("delim"))
	if err != nil {
		return csvimport.Settings{}, err
	}
	return csvimport.Settings{
		Delimiter: d,
		Header:    f.field("header").on,
		Null:      f.rawValue("null"),
	}, nil
}

// plan validates the form into a Plan. The mapping is checked against the
// width of the file's first record under the chosen delimiter.
func (st *importSettings) plan(f *formModal) (csvimport.Plan, error) {
	s, err := st.settings(f)
	if err != nil {
		return csvimport.Plan{}, err
	}
	rd, err := csvimport.NewReader(strings.NewReader(string(st.setup.sample)), s)
	if err != nil {
		return csvimport.Plan{}, err
	}
	mapping, err := csvimport.ParseMapping(f.rawValue("mapping"), rd.Width, st.setup.cols)
	if err != nil {
		return csvimport.Plan{}, err
	}
	return csvimport.Plan{Settings: s, Mapping: mapping, Table: st.setup.cols}, nil
}

func (st *importSettings) body(f *formModal) []string {
	key := f.rawValue("delim") + "\x00" + fmt.Sprint(f.field("header").on) + "\x00" +
		f.rawValue("mapping") + "\x00" + f.rawValue("null")
	if key != st.cacheKey || st.cache == nil {
		st.cacheKey, st.cache = key, st.render(f)
	}
	return st.cache
}

func (st *importSettings) render(f *formModal) []string {
	lines := []string{fmt.Sprintf("File  %s (%s)", st.setup.path, byteCount(int(st.setup.size)))}
	s, err := st.settings(f)
	if err == nil {
		lines = append(lines, st.describe(s))
	}
	var plan csvimport.Plan
	if err == nil {
		plan, err = st.plan(f)
	}
	if err != nil {
		lines = append(lines, "", "✗ "+err.Error())
		return padLines(lines, importBodyRows)
	}
	lines = append(lines, "Maps  "+mappingSummary(plan), "")
	rows, err := csvimport.Preview(st.setup.sample, st.setup.truncated, plan, importPreviewRows)
	lines = append(lines, previewLines(plan, rows)...)
	if err != nil {
		lines = append(lines, "✗ "+err.Error())
	}
	return padLines(lines, importBodyRows)
}

// describe is the "what was detected" line: the delimiter, the header and
// the file's width, so a wrong delimiter shows up as a width of 1.
func (st *importSettings) describe(s csvimport.Settings) string {
	rd, err := csvimport.NewReader(strings.NewReader(string(st.setup.sample)), s)
	width := 0
	if err == nil {
		width = rd.Width
	}
	header := "no header"
	if s.Header {
		header = "header line"
	}
	delim := csvimport.FormatDelimiter(s.Delimiter)
	return fmt.Sprintf("Read  delimiter %q · %s · %d columns", delim, header, width)
}

// mappingSummary spells the mapping as `csv#→column TYPE` pairs.
func mappingSummary(p csvimport.Plan) string {
	var parts []string
	for i, j := range p.Mapping {
		if j == csvimport.Skip {
			parts = append(parts, fmt.Sprintf("%d→skip", i+1))
			continue
		}
		c := p.Table[j]
		parts = append(parts, fmt.Sprintf("%d→%s %s", i+1, c.Name, c.DataType))
	}
	return strings.Join(parts, " · ")
}

// previewLines renders the preview as a small table with the target
// columns as its header. A row that would stop the import shows its
// reason instead of its values.
func previewLines(p csvimport.Plan, rows []csvimport.PreviewRow) []string {
	const cellW = 14
	cell := func(s string) string { return pad(truncate(s, cellW), cellW) }
	var head strings.Builder
	head.WriteString("line  ")
	for _, c := range p.Columns() {
		head.WriteString(cell(c) + " ")
	}
	out := []string{strings.TrimRight(head.String(), " ")}
	if len(rows) == 0 {
		return append(out, "(no data rows)")
	}
	for _, r := range rows {
		var b strings.Builder
		b.WriteString(pad(fmt.Sprint(r.Line), 6))
		if r.Err != nil {
			b.WriteString("✗ " + r.Err.Error())
		} else {
			for _, v := range r.Values {
				b.WriteString(cell(previewValue(v)) + " ")
			}
		}
		out = append(out, strings.TrimRight(b.String(), " "))
	}
	return out
}

// previewValue renders a converted value the way the grid shows it, with
// a string's control characters made visible so a quoted newline does not
// break the table.
func previewValue(v any) string {
	s := db.FormatValue(v, "NULL")
	return strings.NewReplacer("\n", "↵", "\r", "", "\t", "→").Replace(s)
}

func padLines(lines []string, n int) []string {
	for len(lines) < n {
		lines = append(lines, "")
	}
	return lines[:n]
}

// ---------- step 3: the import ----------

// runImport starts the worker.
func (m *Model) runImport(setup importSetupMsg, plan csvimport.Plan) tea.Cmd {
	if m.driver == nil {
		return logCmd("-- import skipped: not connected")
	}
	if m.exports.csv.running {
		return logCmd("-- import skipped: %s is still importing", m.exports.csv.table)
	}
	f, err := os.Open(setup.path)
	if err != nil {
		return logCmd("-- import into %s FAILED: %v", setup.table, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.exports.csv = importState{
		running: true,
		id:      m.exports.csv.id + 1,
		table:   setup.table,
		cancel:  cancel,
		ch:      make(chan tea.Msg),
	}
	m.keys.CancelImport.SetEnabled(true)
	job := importJob{
		id:     m.exports.csv.id,
		ctx:    ctx,
		driver: m.driver,
		file:   f,
		setup:  setup,
		plan:   plan,
		ch:     m.exports.csv.ch,
	}
	return tea.Batch(
		logCmd("-- import %s into %s (%s, one transaction)…", setup.path, setup.table, byteCount(int(setup.size))),
		startImportCmd(job),
	)
}

// importJob is everything the worker goroutine needs, by value.
type importJob struct {
	id     int
	ctx    context.Context
	driver db.Driver
	file   *os.File
	setup  importSetupMsg
	plan   csvimport.Plan
	ch     chan tea.Msg
}

func startImportCmd(job importJob) tea.Cmd {
	return func() tea.Msg {
		go job.run()
		return <-job.ch
	}
}

func waitImportCmd(ch chan tea.Msg) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg { return <-ch }
}

func (j importJob) run() {
	defer j.file.Close()
	read := &countingReader{r: j.file}
	rows, err := j.importRows(read)
	// The final message goes out even on cancellation, so the root can
	// clear the running flag.
	j.ch <- importDoneMsg{id: j.id, table: j.setup.table, path: j.setup.path, rows: rows, err: err}
}

func (j importJob) importRows(read *countingReader) (int64, error) {
	rd, err := csvimport.NewReader(bufio.NewReaderSize(read, 64*1024), j.plan.Settings)
	if err != nil {
		return 0, err
	}
	if rd.Width != len(j.plan.Mapping) {
		return 0, fmt.Errorf("the file has %d columns now, the mapping %d", rd.Width, len(j.plan.Mapping))
	}
	return j.driver.ImportRows(j.ctx, db.ImportRequest{
		Database:      j.setup.database,
		Table:         j.setup.table,
		Columns:       j.plan.Columns(),
		Next:          csvimport.Source(rd, j.plan),
		ProgressEvery: importProgressEvery,
		Progress: func(n int64) {
			// A cancelled import must not block on a UI that stopped
			// reading.
			select {
			case j.ch <- importProgressMsg{id: j.id, rows: n, done: read.n.Load(), size: j.setup.size}:
			case <-j.ctx.Done():
			}
		},
	})
}

// countingReader tracks how far into the file the import has read, for
// the progress percentage.
type countingReader struct {
	r io.Reader
	n atomic.Int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(int64(n))
	return n, err
}

// percentSuffix is " (42%)" for a progress line, or "" when the size is
// unknown.
func percentSuffix(done, size int64) string {
	if size <= 0 {
		return ""
	}
	return fmt.Sprintf(" (%d%%)", min(done*100/size, 100))
}

// ---------- model wiring ----------

// cancelImport is `X` while an import runs. The driver notices between
// rows and rolls the transaction back.
func (m *Model) cancelImport() tea.Cmd {
	if !m.exports.csv.running {
		return nil
	}
	m.exports.csv.cancel()
	return logCmd("-- cancelling import into %s…", m.exports.csv.table)
}

// finishImport clears the in-flight state and renders the outcome. A
// failure names the row and the reason; either way nothing was kept.
func (m *Model) finishImport(msg importDoneMsg) tea.Cmd {
	m.exports.csv.running = false
	m.exports.csv.cancel = nil
	m.exports.csv.ch = nil
	m.keys.CancelImport.SetEnabled(false)
	switch {
	case errors.Is(msg.err, context.Canceled):
		return logCmd("-- import into %s cancelled after %d rows — rolled back, nothing imported", msg.table, msg.rows)
	case msg.err != nil:
		return logCmd("-- import into %s FAILED: %v — rolled back, nothing imported", msg.table, msg.err)
	default:
		return logCmd("-- import into %s: %d rows from %s committed", msg.table, msg.rows, msg.path)
	}
}
