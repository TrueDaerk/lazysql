package ui

import (
	"strings"
	"testing"

	"lazysql/internal/db"
)

// Keyboard navigation runs through the same coalescer as the wheel
// (issue #78): the first press of a burst applies immediately, repeats
// that arrive before the flush tick only accumulate, and the flush
// applies the net delta. These tests drive Update raw — without send's
// synchronous command draining — so the burst never flushes between
// presses, which is exactly what a backlogged input queue looks like.

func TestKeyRepeatCoalescesInSidePanel(t *testing.T) {
	m := sized(120, 40)
	m, _ = raw(m, press('j'))
	if got := m.panels[panelConnections].cursor; got != 1 {
		t.Fatalf("first press moved the cursor to %d, want 1", got)
	}
	m, _ = raw(m, press('j'))
	m, _ = raw(m, press('j'))
	if got := m.panels[panelConnections].cursor; got != 1 {
		t.Fatalf("repeats inside the burst moved the cursor to %d, want it still on 1", got)
	}
	if m.wheel.pending != 2 {
		t.Fatalf("pending = %d, want the 2 accumulated repeats", m.wheel.pending)
	}
	m, _ = raw(m, wheelFlushMsg{gen: m.wheel.gen})
	if got := m.panels[panelConnections].cursor; got != 3 {
		t.Fatalf("flush left the cursor on %d, want 3", got)
	}
}

func TestKeyRepeatReversalAppliesNetDelta(t *testing.T) {
	m := sized(120, 40)
	m.panels[panelConnections].setItems([]string{"a", "b", "c", "d", "e", "f"})
	// One applied j, two queued j — then the user reverses with three k
	// while the backlog is still pending. The flush must apply the net
	// (2 - 3 = -1 from row 1), not the downs first and the ups after.
	for _, r := range "jjjkkk" {
		m, _ = raw(m, press(r))
	}
	if m.wheel.pending != -1 {
		t.Fatalf("pending = %d, want the net -1", m.wheel.pending)
	}
	m, _ = raw(m, wheelFlushMsg{gen: m.wheel.gen})
	if got := m.panels[panelConnections].cursor; got != 0 {
		t.Fatalf("flush left the cursor on %d, want 0", got)
	}
}

func TestSinglePressFlushDisarms(t *testing.T) {
	m := sized(120, 40)
	m, _ = raw(m, press('j'))
	m, _ = raw(m, wheelFlushMsg{gen: m.wheel.gen})
	if m.wheel.armed {
		t.Fatal("an empty flush left the coalescer armed")
	}
	// The next slow, deliberate press applies immediately again.
	m, _ = raw(m, press('j'))
	if got := m.panels[panelConnections].cursor; got != 2 {
		t.Fatalf("press after the burst moved the cursor to %d, want 2", got)
	}
}

func TestKeyRepeatCoalescesInDataGrid(t *testing.T) {
	m := sized(120, 40)
	rows := make([][]any, 20)
	for i := range rows {
		rows[i] = []any{i}
	}
	m.data = dataView{
		conn: "c", database: "d", table: "t",
		cols: []db.Column{{Name: "id", DataType: "int"}},
		rows: rows,
	}
	m.table = "t"
	m.setFocus(panelMain)
	for _, r := range "jjj" {
		m, _ = raw(m, press(r))
	}
	if m.data.row != 1 {
		t.Fatalf("burst moved the row cursor to %d before the flush, want 1", m.data.row)
	}
	m, _ = raw(m, wheelFlushMsg{gen: m.wheel.gen})
	if m.data.row != 3 {
		t.Fatalf("flush left the row cursor on %d, want 3", m.data.row)
	}
}

func TestKeyRepeatCoalescesInQueryEditor(t *testing.T) {
	m := editorAt(t, "one\ntwo\nthree\nfour\nfive")
	for _, r := range "jjj" {
		m, _ = raw(m, press(r))
	}
	if row, _ := editorCursor(m); row != 1 {
		t.Fatalf("burst moved the caret to row %d before the flush, want 1", row)
	}
	m, _ = raw(m, wheelFlushMsg{gen: m.wheel.gen})
	if row, _ := editorCursor(m); row != 3 {
		t.Fatalf("flush left the caret on row %d, want 3", row)
	}
}

// The editor's render memo must never outlive what it renders from: the
// same box re-rendered after a caret move or an edit has to reflect the
// new state, not the memoized block.
func TestEditorRenderMemoFollowsCaretAndEdits(t *testing.T) {
	m := editorAt(t, "SELECT 1\nSELECT 2\nSELECT 3")
	before := m.editorBlock(40, 6)
	if again := m.editorBlock(40, 6); again != before {
		t.Fatal("re-rendering unchanged state produced a different block")
	}
	m = send(t, m, press('j'))
	afterMove := m.editorBlock(40, 6)
	if afterMove == before {
		t.Fatal("caret move did not change the rendered block")
	}
	m = send(t, m, press('d'), press('d'))
	afterEdit := m.editorBlock(40, 6)
	if strings.Contains(afterEdit, "SELECT 2") {
		t.Fatalf("edit did not invalidate the render memo:\n%s", afterEdit)
	}
}
