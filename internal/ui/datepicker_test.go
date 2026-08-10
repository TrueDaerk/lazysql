package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/db"
)

// datesBrowsing opens a fixture table with one column per temporal kind
// plus a plain text column, so the picker's wiring and the unchanged
// free-text path can both be driven from the same grid.
func datesBrowsing(t *testing.T) Model {
	t.Helper()
	m := browsing(t)
	ctx := context.Background()
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS dates`,
		`CREATE TABLE dates (
			id    INTEGER PRIMARY KEY,
			d     DATE,
			ts    TIMESTAMP,
			tm    TIME,
			label TEXT)`,
		`INSERT INTO dates (id, d, ts, tm, label)
		 VALUES (1, '2026-08-10', '2026-08-10 14:32:07', '14:32:07', 'plain')`,
	} {
		if _, err := m.driver.Exec(ctx, stmt); err != nil {
			t.Fatalf("fixture %q: %v", stmt, err)
		}
	}
	m = send(t, m, press('2'), press('R'))
	if !m.panels[panelObjects].selectByName("dates") {
		t.Fatalf("fixture table not listed: %v", m.panels[panelObjects].items)
	}
	m = send(t, m, special(tea.KeyEnter, 0))
	if m.focus != panelMain {
		t.Fatalf("focus = %v, want the data grid", m.focus)
	}
	return m
}

// onColumn moves the cell cursor onto the named column.
func onColumn(t *testing.T, m Model, name string) Model {
	t.Helper()
	for i, c := range m.data.cols {
		if c.Name != name {
			continue
		}
		for m.data.col < i {
			m = send(t, m, press('l'))
		}
		for m.data.col > i {
			m = send(t, m, press('h'))
		}
		return m
	}
	t.Fatalf("column %q not in %v", name, m.data.cols)
	return m
}

// openPicker is `e` on the cursor cell, asserting the calendar opened.
func openPicker(t *testing.T, m Model) (Model, *datePickerModal) {
	t.Helper()
	m = send(t, m, press('e'))
	p, ok := m.modal.(*datePickerModal)
	if !ok {
		t.Fatalf("e opened %T, want the date picker", m.modal)
	}
	return m, p
}

// A DATE column opens the calendar, and confirming stages the
// ISO-formatted value through the changeset — nothing executes.
func TestDatePickerStagesISODate(t *testing.T) {
	m := datesBrowsing(t)
	m = onColumn(t, m, "d")
	m, p := openPicker(t, m)

	if p.kind != db.KindDate {
		t.Fatalf("kind = %v, want KindDate", p.kind)
	}
	if got := p.t.Format("2006-01-02"); got != "2026-08-10" {
		t.Fatalf("picker prefilled at %s, want the cell value", got)
	}
	// h/l is a day, j/k a week, ]/[ a month.
	m = send(t, m, press('l'), press('j'), press(']'))
	if got := p.value(); got != "2026-09-18" {
		t.Fatalf("value = %q, want 2026-09-18 after +1 day +1 week +1 month", got)
	}
	m = send(t, m, special(tea.KeyEnter, 0))

	if m.modal != nil {
		t.Fatalf("modal = %T after enter, want it closed", m.modal)
	}
	ch, ok := m.changes.Lookup("", "dates", []any{int64(1)}, "d")
	if !ok {
		t.Fatal("nothing staged")
	}
	// The SQLite driver hands DATE cells back as time.Time, so the picked
	// value is converted back to one — the changeset stays typed.
	got, isTime := ch.NewValue.(time.Time)
	if !isTime || got.Format("2006-01-02") != "2026-09-18" {
		t.Fatalf("staged %#v, want 2026-09-18", ch.NewValue)
	}
	// Staged only: the row on the server is untouched.
	rs, err := m.driver.Query(context.Background(), "SELECT d FROM dates WHERE id = 1")
	if err != nil {
		t.Fatal(err)
	}
	if db.FormatValue(rs.Rows[0][0], "") != "2026-08-10T00:00:00Z" {
		t.Fatalf("server value = %v, want it unchanged before commit", rs.Rows[0][0])
	}
}

// A TIMESTAMP column gets both halves: tab hands the keyboard to the
// clock, where h/l pick the field and j/k adjust it.
func TestDatePickerDateTimeHalves(t *testing.T) {
	m := datesBrowsing(t)
	m = onColumn(t, m, "ts")
	m, p := openPicker(t, m)

	if p.kind != db.KindDateTime {
		t.Fatalf("kind = %v, want KindDateTime", p.kind)
	}
	if p.section != pickDate {
		t.Fatal("a date+time picker should start on the calendar")
	}
	if got := p.value(); got != "2026-08-10 14:32:07" {
		t.Fatalf("prefill = %q", got)
	}
	m = send(t, m, special(tea.KeyTab, 0))
	if p.section != pickTime {
		t.Fatal("tab did not reach the clock")
	}
	// Hour +1, then minute -1, then second +1.
	m = send(t, m, press('k'))
	m = send(t, m, press('l'), press('j'))
	m = send(t, m, press('l'), press('k'))
	if got := p.value(); got != "2026-08-10 15:31:08" {
		t.Fatalf("value = %q, want 2026-08-10 15:31:08", got)
	}
	// The spinner wraps inside its own field rather than moving the day.
	for i := 0; i < 60; i++ {
		m = send(t, m, press('k'))
	}
	if got := p.value(); got != "2026-08-10 15:31:08" {
		t.Fatalf("value = %q after a full second wrap, want the day untouched", got)
	}
	m = send(t, m, special(tea.KeyEnter, 0))
	ch, _ := m.changes.Lookup("", "dates", []any{int64(1)}, "ts")
	got, isTime := ch.NewValue.(time.Time)
	if !isTime || got.Format("2006-01-02 15:04:05") != "2026-08-10 15:31:08" {
		t.Fatalf("staged %#v", ch.NewValue)
	}
}

// A TIME column has no calendar at all: the picker opens on the clock and
// tab cannot move off it.
func TestDatePickerTimeOnly(t *testing.T) {
	m := datesBrowsing(t)
	m = onColumn(t, m, "tm")
	m, p := openPicker(t, m)

	if p.kind != db.KindTime {
		t.Fatalf("kind = %v, want KindTime", p.kind)
	}
	if p.section != pickTime {
		t.Fatal("a time-only picker should start on the clock")
	}
	m = send(t, m, special(tea.KeyTab, 0))
	if p.section != pickTime {
		t.Fatal("tab moved a time-only picker onto a calendar it does not have")
	}
	if got := p.value(); got != "14:32:07" {
		t.Fatalf("prefill = %q", got)
	}
	out := m.View().Content
	if strings.Contains(out, "August 2026") {
		t.Error("a time-only picker rendered a calendar")
	}
	m = send(t, m, press('k'), special(tea.KeyEnter, 0))
	ch, _ := m.changes.Lookup("", "dates", []any{int64(1)}, "tm")
	if ch.NewValue != "15:32:07" {
		t.Fatalf("staged %v, want the ISO time", ch.NewValue)
	}
}

// `t` jumps to now, keeping only the halves the column has.
func TestDatePickerToday(t *testing.T) {
	m := datesBrowsing(t)
	m = onColumn(t, m, "d")
	m, p := openPicker(t, m)
	m = send(t, m, press('t'))
	if got, want := p.value(), time.Now().Format("2006-01-02"); got != want {
		t.Fatalf("t landed on %q, want %q", got, want)
	}
	_ = m
}

// esc closes the picker without staging anything.
func TestDatePickerEscapeStagesNothing(t *testing.T) {
	m := datesBrowsing(t)
	m = onColumn(t, m, "d")
	m, _ = openPicker(t, m)
	m = send(t, m, press('l'), special(tea.KeyEscape, 0))
	if m.modal != nil {
		t.Fatalf("modal = %T after esc, want it closed", m.modal)
	}
	if m.changes.Len() != 0 {
		t.Fatalf("changeset = %d after esc, want nothing staged", m.changes.Len())
	}
}

// `e` inside the picker falls back to raw text entry — the only way to
// spell NULL or a SQL expression — and ctrl+t goes back to the calendar.
func TestDatePickerRawTextFallback(t *testing.T) {
	m := datesBrowsing(t)
	m = onColumn(t, m, "ts")
	m, _ = openPicker(t, m)

	m = send(t, m, press('e'))
	e, ok := m.modal.(*editCellModal)
	if !ok {
		t.Fatalf("e inside the picker opened %T, want the text editor", m.modal)
	}
	// A SQL expression the calendar cannot express.
	e.input.SetValue("CURRENT_TIMESTAMP")
	e.null = false
	if !strings.Contains(m.View().Content, "ctrl+t picker") {
		t.Error("the text editor does not offer the way back to the picker")
	}
	m2 := send(t, m, ctrl('t'))
	if _, ok := m2.modal.(*datePickerModal); !ok {
		t.Fatalf("ctrl+t opened %T, want the picker back", m2.modal)
	}

	m = send(t, m, special(tea.KeyEnter, 0))
	ch, ok := m.changes.Lookup("", "dates", []any{int64(1)}, "ts")
	if !ok || ch.NewValue != "CURRENT_TIMESTAMP" {
		t.Fatalf("staged %+v, want the raw expression", ch)
	}

	// And the same fallback reaches NULL.
	m = send(t, m, press('e'))
	m = send(t, m, press('e'))
	m = send(t, m, ctrl('n'), special(tea.KeyEnter, 0))
	ch, _ = m.changes.Lookup("", "dates", []any{int64(1)}, "ts")
	if ch.NewValue != nil {
		t.Fatalf("staged %v, want NULL", ch.NewValue)
	}
}

// A non-temporal column is untouched: `e` still opens the plain text
// editor and ctrl+t there does nothing.
func TestNonTemporalColumnKeepsTextEditor(t *testing.T) {
	m := datesBrowsing(t)
	m = onColumn(t, m, "label")
	m = send(t, m, press('e'))
	if _, ok := m.modal.(*editCellModal); !ok {
		t.Fatalf("e opened %T, want the plain text editor", m.modal)
	}
	m = send(t, m, ctrl('t'))
	if _, ok := m.modal.(*editCellModal); !ok {
		t.Fatalf("ctrl+t on a text column opened %T", m.modal)
	}
	if !strings.Contains(m.View().Content, "ctrl+n NULL · esc cancel") {
		t.Error("the text editor footer changed for a non-temporal column")
	}
}

// The insert form opens the picker on top of itself with ctrl+t, writes
// the ISO value back into the field and restores the form untouched on
// esc.
func TestInsertFormDatePicker(t *testing.T) {
	m := datesBrowsing(t)
	m = send(t, m, press('n'))
	f, ok := m.modal.(*insertRowModal)
	if !ok {
		t.Fatalf("n opened %T, want the insert form", m.modal)
	}
	// Move to the `d` field.
	for f.current().col.Name != "d" {
		m = send(t, m, special(tea.KeyTab, 0))
	}
	m = send(t, m, ctrl('t'))
	p, ok := m.modal.(*datePickerModal)
	if !ok {
		t.Fatalf("ctrl+t opened %T, want the picker", m.modal)
	}
	if p.back != modal(f) {
		t.Fatal("the picker does not know which form to restore")
	}

	// esc restores the form with the field untouched.
	m = send(t, m, press('l'), special(tea.KeyEscape, 0))
	if m.modal != modal(f) {
		t.Fatalf("esc left %T, want the form back", m.modal)
	}
	if got := f.current().input.Value(); got != "" {
		t.Fatalf("field = %q after esc, want it untouched", got)
	}

	// Confirming writes the ISO value into the field as a typed value.
	m = send(t, m, ctrl('t'))
	p = m.modal.(*datePickerModal)
	want := p.t.AddDate(0, 0, 1).Format("2006-01-02")
	m = send(t, m, press('l'), special(tea.KeyEnter, 0))
	if m.modal != modal(f) {
		t.Fatalf("enter left %T, want the form back", m.modal)
	}
	if got := f.current().input.Value(); got != want {
		t.Fatalf("field = %q, want %q", got, want)
	}
	if f.current().mode != insertValue {
		t.Fatal("the picked value did not switch the field to a typed value")
	}

	// Staging the form still goes through the changeset, not the server.
	m = send(t, m, special(tea.KeyEnter, 0))
	ins := m.changes.InsertsFor("", "dates")
	if len(ins) != 1 {
		t.Fatalf("staged inserts = %d, want 1", len(ins))
	}
	v, bound := insertValueFor(ins[0], "d")
	if !bound || v != want {
		t.Fatalf("insert binds d = %v (bound %v), want %q", v, bound, want)
	}
}

// Every picker key is documented in `?` and offered by the options bar
// while the picker is open.
func TestDatePickerKeysAreDocumented(t *testing.T) {
	m := datesBrowsing(t)
	m = send(t, m, press('?'))
	help := m.View().Content
	for _, want := range []string{
		"prev day / time field", "next day / time field",
		"prev week", "next week", "prev month", "next month",
		"jump to now", "date / time half", "raw text", "date picker",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("`?` is missing %q", want)
		}
	}

	// While the picker is open the bar shows its keys instead of the
	// grid's. It truncates like any other bar, so the assertion is on the
	// leading entries plus the picker's own always-complete footer.
	m = send(t, m, special(tea.KeyEscape, 0))
	m = onColumn(t, m, "ts")
	m, p := openPicker(t, m)
	lines := strings.Split(m.View().Content, "\n")
	bar := lines[len(lines)-1]
	for _, want := range []string{"date picker", "prev day / time field"} {
		if !strings.Contains(bar, want) {
			t.Errorf("options bar is missing %q: %q", want, bar)
		}
	}
	if strings.Contains(bar, "edit cell") {
		t.Error("the options bar still shows the grid's keys while the picker is open")
	}
	for _, want := range []string{"h/← day", "j/↓ week", "]/L month", "t now", "e raw text",
		"enter confirm", "esc cancel", "tab date/time"} {
		if !strings.Contains(p.footer(), want) {
			t.Errorf("picker footer is missing %q: %q", want, p.footer())
		}
	}
}

// The classifier decides which editor a column gets, straight from the
// declared type the driver reported.
func TestPickerStartFallsBackToToday(t *testing.T) {
	today := time.Now().Format("2006-01-02")
	if got := pickerStart("not a date", db.KindDate).Format("2006-01-02"); got != today {
		t.Errorf("pickerStart on unparsable text = %s, want today", got)
	}
	if got := pickerStart(nil, db.KindDateTime).Format("2006-01-02"); got != today {
		t.Errorf("pickerStart on NULL = %s, want today", got)
	}
	// A time-only literal borrows today's date so the calendar half of a
	// date+time picker is not stuck in year 0.
	got := pickerStart("14:32:07", db.KindDateTime)
	if got.Format("2006-01-02 15:04:05") != today+" 14:32:07" {
		t.Errorf("pickerStart on a bare time = %v", got)
	}
	// A driver that hands back a real time.Time is used as-is.
	ts := time.Date(2020, 3, 4, 5, 6, 7, 0, time.UTC)
	if !pickerStart(ts, db.KindDateTime).Equal(ts) {
		t.Error("pickerStart discarded a time.Time value")
	}
}
