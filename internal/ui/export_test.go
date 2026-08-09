package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/db"
	"lazysql/internal/export"
)

// typePath fills the open export prompt with a path and submits it.
// The value is set directly rather than typed: a textinput answers
// every keystroke with a cursor-blink command, which the synchronous
// test driver would block on.
func typePath(t *testing.T, m Model, path string) Model {
	t.Helper()
	p, ok := m.modal.(*promptModal)
	if !ok {
		t.Fatalf("modal = %T, want the export prompt", m.modal)
	}
	p.input.SetValue(path)
	return send(t, m, special(tea.KeyEnter, 0))
}

// `E` prompts for a path and writes every row of the relation, not just
// the visible page.
func TestExportCSVWritesEveryRow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "orders.csv")

	m := send(t, copyBrowsing(t), press('E'))
	m = typePath(t, m, path)

	if m.export.running {
		t.Error("the export is still marked as running after it finished")
	}
	if !logContains(m, "export wrote 3 rows") {
		t.Fatalf("command log = %v", m.commandLog)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("file has %d lines, want header + 3 rows:\n%s", len(lines), data)
	}
	if lines[0] != "id,person_id,status" {
		t.Errorf("header = %q", lines[0])
	}
}

// The format comes from the extension.
func TestExportFormatFromExtension(t *testing.T) {
	dir := t.TempDir()

	m := send(t, copyBrowsing(t), press('E'))
	m = typePath(t, m, filepath.Join(dir, "orders.json"))
	data, err := os.ReadFile(filepath.Join(dir, "orders.json"))
	if err != nil {
		t.Fatal(err)
	}
	var decoded []map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("exported JSON does not parse: %v\n%s", err, data)
	}
	if len(decoded) != 3 || decoded[1]["status"] != nil {
		t.Errorf("exported JSON = %s", data)
	}

	m = send(t, m, press('E'))
	m = typePath(t, m, filepath.Join(dir, "orders.sql"))
	data, err = os.ReadFile(filepath.Join(dir, "orders.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `INSERT INTO "orders" ("id", "person_id", "status") VALUES (2, 1, NULL);`) {
		t.Errorf("exported SQL = %s", data)
	}
}

// An extension nothing can be inferred from is refused before any file
// is created.
func TestExportUnknownExtensionIsRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "orders.txt")

	m := send(t, copyBrowsing(t), press('E'))
	m = typePath(t, m, path)
	if !logContains(m, "unsupported extension") {
		t.Fatalf("command log = %v", m.commandLog)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("a file was created anyway: %v", err)
	}
}

// The export honors the grid's WHERE filter.
func TestExportRespectsFilter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "filtered.csv")

	m := send(t, copyBrowsing(t), press('f'))
	m = send(t, m, press('i'), press('d'), press(' '), press('>'), press(' '), press('1'),
		special(tea.KeyEnter, 0))
	m = send(t, m, press('E'))
	m = typePath(t, m, path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("filtered export has %d lines, want header + 2 rows:\n%s", len(lines), data)
	}
}

// A table larger than one streaming page is written in full, and the
// progress lines report it along the way.
func TestExportStreamsManyPages(t *testing.T) {
	m := copyBrowsing(t)
	// 2500 rows is more than two full streaming pages and more than one
	// grid page, so the export can only be complete if it streamed.
	if _, err := m.driver.Exec(t.Context(),
		`INSERT INTO orders (person_id, status)
		 WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i < 2500)
		 SELECT 1, 'bulk' FROM n`); err != nil {
		t.Fatal(err)
	}
	m = send(t, m, press('R'))

	dir := t.TempDir()
	path := filepath.Join(dir, "big.csv")
	m = send(t, m, press('E'))
	m = typePath(t, m, path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if want := 2503 + 1; len(lines) != want { // 3 fixture rows + 2500 + header
		t.Fatalf("file has %d lines, want %d", len(lines), want)
	}
	if !logContains(m, fmt.Sprintf("export wrote %d rows", 2503)) {
		t.Fatalf("command log = %v", m.commandLog)
	}
}

// A whole-table clipboard copy is capped, and the cap is reported
// rather than silently applied.
func TestCopyTableTruncationIsReported(t *testing.T) {
	got := fakeClipboard(t)
	m := copyBrowsing(t)
	if _, err := m.driver.Exec(t.Context(),
		fmt.Sprintf(`INSERT INTO orders (person_id, status)
		 WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i < %d)
		 SELECT 1, 'bulk' FROM n`, copyRowLimit+100)); err != nil {
		t.Fatal(err)
	}
	m = send(t, m, press('R'), press('y'), press('C'))

	if !logContains(m, "TRUNCATED") || !logContains(m, "press E to export") {
		t.Fatalf("command log = %v", m.commandLog)
	}
	lines := strings.Split(strings.TrimRight(*got, "\n"), "\n")
	if len(lines) != copyRowLimit+1 {
		t.Errorf("copied %d lines, want the cap plus a header", len(lines))
	}
}

// `X` is only offered while an export is running, so the options bar
// does not advertise a key that would do nothing.
func TestCancelExportBindingIsConditional(t *testing.T) {
	m := copyBrowsing(t)
	if m.keys.CancelExport.Enabled() {
		t.Error("X is enabled with no export running")
	}
	if help := send(t, m, press('?')); strings.Contains(help.View().Content, "cancel export") {
		t.Error("? advertises X with no export running")
	}
	if cmd := m.cancelExport(); cmd != nil {
		t.Error("X did something with no export running")
	}
}

// A cancelled export stops mid-stream and removes the partial file — a
// half-written export is worse than none, because it looks complete.
func TestExportCancellationRemovesPartialFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cancelled.csv")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	job := exportJob{
		id:     1,
		ctx:    ctx,
		file:   f,
		path:   path,
		format: export.FormatCSV,
		pager: func(c context.Context, limit, offset int) (*db.ResultSet, error) {
			// The second page never arrives: the export is cancelled
			// while the first one is being written.
			if offset > 0 {
				<-c.Done()
				return nil, c.Err()
			}
			rs := &db.ResultSet{Columns: []db.Column{{Name: "id"}}}
			for i := range limit {
				rs.Rows = append(rs.Rows, []any{int64(i)})
			}
			return rs, nil
		},
		ch: make(chan tea.Msg),
	}
	go job.run()

	cancel()
	var done exportDoneMsg
	for {
		msg := <-job.ch
		if d, ok := msg.(exportDoneMsg); ok {
			done = d
			break
		}
	}
	if !errors.Is(done.err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", done.err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the partial file survived: %v", err)
	}

	// The model turns that message into a log line and stops offering X.
	m := copyBrowsing(t)
	m.export = exportState{running: true, id: 1, table: "orders", cancel: func() {}}
	m.keys.CancelExport.SetEnabled(true)
	m = send(t, m, done)
	if m.export.running || m.keys.CancelExport.Enabled() {
		t.Error("the export state survived its own completion")
	}
	if !logContains(m, "export cancelled") {
		t.Fatalf("command log = %v", m.commandLog)
	}
}

// `X` cancels the running export and is only bound while one runs.
func TestCancelExportKeyCancels(t *testing.T) {
	cancelled := false
	m := copyBrowsing(t)
	m.export = exportState{running: true, id: 1, table: "orders", cancel: func() { cancelled = true }}
	m.keys.CancelExport.SetEnabled(true)

	m = send(t, m, press('X'))
	if !cancelled {
		t.Fatal("X did not cancel the export")
	}
	if !logContains(m, "cancelling export of orders") {
		t.Fatalf("command log = %v", m.commandLog)
	}
	// `?` lists every binding of the focused panel, X included now.
	if help := send(t, m, press('?')); !strings.Contains(help.View().Content, "cancel export") {
		t.Error("? does not offer X while an export runs")
	}
}

// Two exports never run at once: the second is refused rather than
// racing the first over the log.
func TestOnlyOneExportRunsAtATime(t *testing.T) {
	m := copyBrowsing(t)
	m.export.running = true
	m.export.table = "orders"
	next, cmd := m.runAction(actExportTable)
	if next.modal != nil {
		t.Error("a second export opened a prompt")
	}
	var lines []string
	for _, msg := range drain(cmd) {
		if l, ok := msg.(commandLogMsg); ok {
			lines = append(lines, l.line)
		}
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "still exporting") {
		t.Fatalf("log = %v", lines)
	}
}
