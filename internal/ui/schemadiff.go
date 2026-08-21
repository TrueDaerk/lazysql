package ui

import (
	"context"
	"fmt"
	"os"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"lazysql/internal/config"
	"lazysql/internal/db"
)

// Schema diff: `D` on a connection compares its schema with another
// connection's. Both sides are dialed fresh in a worker goroutine —
// the active session is never borrowed, so a diff can run while the
// user keeps browsing — introspected through the driver interface into
// db.Schema snapshots, and compared with db.DiffSchemas. The report
// takes over the main view while panel [1] stays focused.

// diffTimeout bounds one whole diff run: two dials plus two full
// introspections. Large schemas are many round trips, so it is far
// looser than dialTimeout.
const diffTimeout = 3 * time.Minute

// diffSideSpec is one side of the diff: the profile to dial and the
// namespace to introspect ("" = the connection's default).
type diffSideSpec struct {
	req      dialRequest
	database string
}

// label names the side in the report header and the command log.
func (s diffSideSpec) label() string {
	if s.database != "" {
		return s.req.conn.Name + "." + s.database
	}
	return s.req.conn.Name
}

// diffView is the schema diff on screen: the run in flight or the
// report it produced. Model.diff is nil when no diff is open.
type diffView struct {
	// id distinguishes runs, so a stale reply cannot fill a report the
	// user has already dismissed.
	id      int
	running bool
	// note is the latest progress line, shown while running.
	note           string
	aLabel, bLabel string

	report *db.SchemaDiff
	lines  []db.DiffLine
	err    string
	offset int

	// ch carries progress and the final outcome out of the worker. It
	// stays read until the done message lands, so the worker never
	// blocks on an abandoned channel.
	ch chan tea.Msg
}

// ---------- messages ----------

type schemaDiffProgressMsg struct {
	id   int
	note string
}

type schemaDiffDoneMsg struct {
	id     int
	report *db.SchemaDiff
	err    error
}

// ---------- the flow ----------

// openSchemaDiff is `D` on panel [1]: a form picking the other side and
// the namespace of each.
func (m *Model) openSchemaDiff() tea.Cmd {
	a, ok := m.selectedConnection()
	if !ok {
		return logCmd("-- schema diff skipped: no connection selected")
	}
	if m.diff != nil && m.diff.running {
		return logCmd("-- schema diff skipped: one is already running")
	}
	// Only saved profiles can be the other side: the picker lists the
	// config, and an ephemeral connection is not in it. With nothing
	// saved there is no B to compare against at all.
	names := m.cfg.Names()
	if len(names) == 0 {
		return logCmd("-- schema diff skipped: no saved connection to compare with")
	}
	// The same connection is a valid other side — two databases of one
	// server diff fine — so the list is not filtered.
	other := names[0]
	for _, n := range names {
		if n != a.Name {
			other = n
			break
		}
	}
	fields := []*formField{
		newSelectField("other", "Compare with", names, names, other),
		newTextField("db_a", "Database A", a.Database, "connection default").
			withHelp("namespace on " + a.Name),
		newTextField("db_b", "Database B", "", "connection default"),
	}
	m.modal = newFormModal("Schema diff — A: "+a.Name, fields,
		func(mm *Model, f *formModal) (bool, tea.Cmd) {
			b, ok := mm.cfg.Find(f.rawValue("other"))
			if !ok {
				f.err = "connection " + f.rawValue("other") + " no longer exists"
				return false, nil
			}
			// A's dial goes through dialRequestFor so an ephemeral side
			// brings its session setup (a Parquet view) along.
			specA := diffSideSpec{req: mm.dialRequestFor(a, false), database: f.rawValue("db_a")}
			specB := diffSideSpec{req: dialRequest{conn: b}, database: f.rawValue("db_b")}
			return true, mm.promptDiffSecrets(specA, specB)
		})
	return nil
}

// promptDiffSecrets collects the "ask on connect" passwords the two
// sides need — first A's, then B's — and then starts the run. Keyring
// passwords resolve inside dial as they do for a normal connect.
func (m *Model) promptDiffSecrets(a, b diffSideSpec) tea.Cmd {
	ask := func(c config.Connection, has bool) bool {
		return c.NeedsPassword() && c.AskPassword && !has
	}
	switch {
	case ask(a.req.conn, a.req.hasPassword):
		m.modal = newPasswordPrompt(a.req.conn, func(pw string) tea.Cmd {
			return func() tea.Msg { return diffSecretMsg{a: withPassword(a, pw), b: b} }
		})
		return nil
	case ask(b.req.conn, b.req.hasPassword):
		m.modal = newPasswordPrompt(b.req.conn, func(pw string) tea.Cmd {
			return func() tea.Msg { return diffSecretMsg{a: a, b: withPassword(b, pw)} }
		})
		return nil
	}
	return m.startSchemaDiff(a, b)
}

// diffSecretMsg re-enters promptDiffSecrets after a password prompt, so
// a run needing two prompts chains them.
type diffSecretMsg struct{ a, b diffSideSpec }

func withPassword(s diffSideSpec, pw string) diffSideSpec {
	s.req.password, s.req.hasPassword = pw, true
	return s
}

// startSchemaDiff launches the worker and puts the running view on
// screen.
func (m *Model) startSchemaDiff(a, b diffSideSpec) tea.Cmd {
	id := 1
	if m.diff != nil {
		id = m.diff.id + 1
	}
	// The activity report shares this main view; a starting diff puts it
	// away, and with it the auto-refresh it may have running.
	m.closeActivity()
	m.diff = &diffView{
		id: id, running: true,
		note:   "connecting…",
		aLabel: a.label(), bLabel: b.label(),
		ch: make(chan tea.Msg),
	}
	return tea.Batch(
		logCmd("-- schema diff %s vs %s…", a.label(), b.label()),
		startSchemaDiffCmd(id, a, b, m.diff.ch),
	)
}

// startSchemaDiffCmd runs the worker goroutine and blocks until its
// first message; waitDiffCmd pulls the rest, one per Update round trip.
func startSchemaDiffCmd(id int, a, b diffSideSpec, ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		go schemaDiffWorker(id, a, b, ch)
		return <-ch
	}
}

func waitDiffCmd(ch chan tea.Msg) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg { return <-ch }
}

// schemaDiffWorker dials both sides, introspects them and sends the
// report. Every dial is fresh and closed here — the UI's own session is
// never touched, so errors on either side cost only the diff.
func schemaDiffWorker(id int, a, b diffSideSpec, ch chan tea.Msg) {
	ctx, cancel := context.WithTimeout(context.Background(), diffTimeout)
	defer cancel()

	introspect := func(side string, s diffSideSpec) (*db.Schema, error) {
		ch <- schemaDiffProgressMsg{id: id, note: "connecting to " + s.req.conn.Name + "…"}
		drv, tunnel, _, err := dial(s.req)
		if err != nil {
			return nil, fmt.Errorf("side %s (%s): %w", side, s.req.conn.Name, err)
		}
		defer func() {
			drv.Close()
			closeTunnel(tunnel)
		}()
		ch <- schemaDiffProgressMsg{id: id, note: "reading schema of " + s.label() + "…"}
		schema, err := db.IntrospectSchema(ctx, drv, databaseArg(s.database), s.label(),
			func(done, total int) {
				if done%20 == 0 {
					ch <- schemaDiffProgressMsg{id: id,
						note: fmt.Sprintf("reading schema of %s… %d/%d tables", s.label(), done, total)}
				}
			})
		if err != nil {
			return nil, fmt.Errorf("side %s (%s): %w", side, s.label(), err)
		}
		return schema, nil
	}

	sa, err := introspect("A", a)
	if err != nil {
		ch <- schemaDiffDoneMsg{id: id, err: err}
		return
	}
	sb, err := introspect("B", b)
	if err != nil {
		ch <- schemaDiffDoneMsg{id: id, err: err}
		return
	}
	ch <- schemaDiffDoneMsg{id: id, report: db.DiffSchemas(sa, sb)}
}

// ---------- model wiring ----------

// applyDiffProgress records a progress note and keeps reading.
func (m *Model) applyDiffProgress(msg schemaDiffProgressMsg) tea.Cmd {
	if m.diff == nil || msg.id != m.diff.id || !m.diff.running {
		return nil
	}
	m.diff.note = msg.note
	return waitDiffCmd(m.diff.ch)
}

// finishDiff lands the report (or the failure) in the view.
func (m *Model) finishDiff(msg schemaDiffDoneMsg) tea.Cmd {
	if m.diff == nil || msg.id != m.diff.id {
		return nil
	}
	m.diff.running = false
	m.diff.ch = nil
	if msg.err != nil {
		m.diff.err = msg.err.Error()
		return logCmd("-- schema diff FAILED: %v", msg.err)
	}
	m.diff.report = msg.report
	m.diff.lines = msg.report.Render()
	note := fmt.Sprintf("%d tables only in A, %d only in B, %d changed",
		len(msg.report.TablesOnlyA), len(msg.report.TablesOnlyB), len(msg.report.TableDiffs))
	if msg.report.Empty() {
		note = "no differences"
	}
	return logCmd("-- schema diff %s vs %s: %s", m.diff.aLabel, m.diff.bLabel, note)
}

// updateDiffKeys owns the keyboard while the report is on screen (panel
// [1] focused, diff open). It returns handled=false only for keys the
// report has no meaning for, which then act on the panel as usual.
func (m Model) updateDiffKeys(msg tea.KeyPressMsg) (Model, tea.Cmd, bool) {
	d := m.diff
	k := m.keys
	switch {
	case key.Matches(msg, k.Back):
		if d.running {
			return m, logCmd("-- schema diff still running…"), true
		}
		m.diff = nil
		return m, nil, true
	case key.Matches(msg, k.Down):
		d.offset++
		return m, nil, true
	case key.Matches(msg, k.Up):
		d.offset--
		return m, nil, true
	case key.Matches(msg, k.NextPage):
		d.offset += 10
		return m, nil, true
	case key.Matches(msg, k.PrevPage):
		d.offset -= 10
		return m, nil, true
	case key.Matches(msg, k.CopyMenu):
		if d.report == nil {
			return m, nil, true
		}
		return m, copyTextCmd("schema diff "+d.aLabel+" vs "+d.bLabel,
			"schema-diff.txt", d.report.RenderText()), true
	case key.Matches(msg, k.ExportTable):
		if d.report == nil {
			return m, nil, true
		}
		text := d.report.RenderText()
		m.modal = newPromptModal(
			"Export schema diff — file path",
			"~/schema-diff.txt",
			"schema-diff.txt",
			func(mm *Model, value string) tea.Cmd { return writeDiffReport(value, text) },
		)
		return m, nil, true
	}
	switch msg.String() {
	case "g", "home":
		d.offset = 0
		return m, nil, true
	case "G", "end":
		d.offset = len(d.lines)
		return m, nil, true
	}
	return m, nil, false
}

// writeDiffReport writes the report to a file. The diff is already in
// memory, so unlike a table export there is nothing to stream.
func writeDiffReport(path, text string) tea.Cmd {
	if path == "" {
		return logCmd("-- export schema diff cancelled: no path given")
	}
	full, err := expandPath(path)
	if err != nil {
		return logCmd("-- export schema diff FAILED: %v", err)
	}
	return func() tea.Msg {
		if err := os.WriteFile(full, []byte(text+"\n"), 0o644); err != nil {
			return commandLogMsg{line: fmt.Sprintf("-- export schema diff FAILED: %v", err)}
		}
		return commandLogMsg{line: fmt.Sprintf(
			"-- export schema diff wrote %s to %s", byteCount(len(text)+1), full)}
	}
}

// ---------- rendering ----------

// diffContent is the main view while a diff is open: the run's progress
// line, the failure, or the scrollable report.
func (m Model) diffContent(w, h int) string {
	d := m.diff
	var lines []string
	switch {
	case d.running:
		lines = append(lines, "", m.style.pending.Render(truncate(d.note, w)))
	case d.err != "":
		lines = append(lines, "", m.style.danger.Render(truncate("schema diff failed: "+d.err, w)))
		lines = append(lines, "", m.style.keyHint.Render("esc close"))
	default:
		body := maxInt(h-1, 0) // footer — the title is the border title
		rendered := make([]string, 0, len(d.lines))
		for _, l := range d.lines {
			rendered = append(rendered, m.diffLineStyle(l.Kind).Render(l.Text))
		}
		lines = append(lines, scrollLines(rendered, d.offset, w, body)...)
		hint := fmt.Sprintf("%d lines — j/k scroll · y copy · E export · esc close", len(d.lines))
		lines = append(lines, m.style.keyHint.Render(truncate(hint, w)))
	}
	return joinTruncated(lines, w, h)
}

// diffTitle is the main view's border title while a diff report is open.
func (m Model) diffTitle() string {
	d := m.diff
	return m.style.titleFocused.Render("Schema diff") +
		m.style.muted.Render(" — A: "+d.aLabel+" · B: "+d.bLabel)
}

func (m Model) diffLineStyle(kind db.DiffLineKind) lipgloss.Style {
	switch kind {
	case db.DiffAdded:
		return m.style.diffAdd
	case db.DiffRemoved:
		return m.style.diffDel
	case db.DiffChanged:
		return m.style.diffChange
	}
	return m.style.plain
}
