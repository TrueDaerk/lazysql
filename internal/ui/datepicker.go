package ui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"lazysql/internal/db"
)

// The date picker: a calendar grid plus hour/minute/second spinners, shown
// wherever a value goes into a column db.ClassifyType calls temporal. It
// only decides how a value string is produced — the edit modal stages it
// through the changeset and the insert form drops it into its own field,
// so nothing here executes or even builds SQL.
//
// Two things it deliberately is not:
//
//   - It is not the only way in. `e` inside the picker hands the column
//     back to raw text entry, which is where `now()`, CURRENT_TIMESTAMP and
//     NULL live: a calendar cannot express them and pretending otherwise
//     would make date columns harder to edit than they are today.
//   - It is not a second key table. Every key it answers to is a keyMap
//     binding, so the options bar, `?` and a `[keys]` override all see the
//     same set (see design/keybindings-single-source).

// pickerSection is which half of a date+time picker has the keyboard. A
// date-only or time-only column pins it to the half it has.
type pickerSection int

const (
	pickDate pickerSection = iota
	pickTime
)

// timeField indexes the hour/minute/second spinner under the cursor.
const (
	fieldHour = iota
	fieldMinute
	fieldSecond
	fieldCount
)

// datePickerModal is the calendar popup. back is the modal to restore when
// it closes (the insert form that opened it; nil when it was opened
// directly), onPick receives the ISO-formatted value and onRaw hands the
// column back to a plain text field.
type datePickerModal struct {
	title    string
	typeLine string
	current  string // what the cell holds today, for the header
	kind     db.TypeKind
	// keys is the shell's keyMap, carried in so the footer renders from
	// the same bindings update dispatches on.
	keys keyMap

	t       time.Time
	section pickerSection
	field   int

	back   modal
	onPick func(m *Model, value string) tea.Cmd
	onRaw  func(m *Model) tea.Cmd
}

func newDatePickerModal(title string, col db.Column, kind db.TypeKind, initial time.Time, k keyMap) *datePickerModal {
	p := &datePickerModal{
		title:    title,
		typeLine: typeTag(col),
		kind:     kind,
		keys:     k,
		t:        initial.Truncate(time.Second),
	}
	if !kind.HasDate() {
		p.section = pickTime
	}
	return p
}

// pickerStart decides where the picker opens: the value already in the
// cell when it parses, today otherwise. A time-only column keeps today's
// date underneath so the grid it never shows still holds a sane value.
func pickerStart(value any, kind db.TypeKind) time.Time {
	now := time.Now()
	switch v := value.(type) {
	case time.Time:
		return v
	case string:
		if t, ok := db.ParseDateTimeIn(v, now.Location()); ok {
			// A time-only literal parses onto the zero date; borrow today's
			// so the calendar half is not stuck in year 0.
			if t.Year() <= 1 && kind.HasDate() {
				return time.Date(now.Year(), now.Month(), now.Day(),
					t.Hour(), t.Minute(), t.Second(), 0, now.Location())
			}
			return t
		}
	}
	return now
}

// value renders the picked instant the way every supported engine accepts
// it as a literal.
func (p *datePickerModal) value() string { return p.t.Format(p.kind.Layout()) }

// close restores whatever modal opened the picker. Returning true with a
// replacement installed is the shell's modal-swap rule: the router only
// clears m.modal when the handler left it pointing at the same modal.
func (p *datePickerModal) close(m *Model) bool {
	if p.back != nil {
		m.modal = p.back
	}
	return true
}

func (p *datePickerModal) update(msg tea.KeyPressMsg, m *Model) (bool, tea.Cmd) {
	k := p.keys
	switch {
	case key.Matches(msg, k.Back):
		return p.close(m), nil

	case msg.String() == "enter", key.Matches(msg, k.AcceptChanges):
		p.close(m)
		if p.onPick == nil {
			return true, nil
		}
		return true, p.onPick(m, p.value())

	case key.Matches(msg, k.PickRaw):
		if p.onRaw == nil {
			return p.close(m), nil
		}
		// onRaw installs its own modal, so the swap rule applies again.
		return true, p.onRaw(m)

	case key.Matches(msg, k.PickToday):
		p.today()
		return false, nil

	case key.Matches(msg, k.PickSection):
		p.toggleSection()
		return false, nil

	case key.Matches(msg, k.PickPrev):
		p.step(-1)
		return false, nil

	case key.Matches(msg, k.PickNext):
		p.step(1)
		return false, nil

	case key.Matches(msg, k.PickUp):
		p.bigStep(-1)
		return false, nil

	case key.Matches(msg, k.PickDown):
		p.bigStep(1)
		return false, nil

	case key.Matches(msg, k.PickMonthPrev):
		p.addMonths(-1)
		return false, nil

	case key.Matches(msg, k.PickMonthNext):
		p.addMonths(1)
		return false, nil
	}
	return false, nil
}

// step is h/l: one day in the calendar, one spinner to the side in the
// clock. It is the same key in both halves because it means the same
// thing — move sideways within whatever the cursor is on.
func (p *datePickerModal) step(delta int) {
	if p.section == pickDate {
		p.t = p.t.AddDate(0, 0, delta)
		return
	}
	p.field = wrapInt(p.field+delta, p.timeFields())
}

// bigStep is j/k: one week in the calendar, one unit on the spinner under
// the cursor. delta is the screen direction — down is later in the
// calendar but a *smaller* number on a spinner, which is the way every
// stepper control reads, so the clock inverts it.
//
// The spinner wraps inside its own field rather than carrying into the
// next one: nudging 59 minutes forward should not silently move the day.
func (p *datePickerModal) bigStep(delta int) {
	if p.section == pickDate {
		p.t = p.t.AddDate(0, 0, 7*delta)
		return
	}
	delta = -delta
	h, mi, s := p.t.Hour(), p.t.Minute(), p.t.Second()
	switch p.field {
	case fieldHour:
		h = wrapInt(h+delta, 24)
	case fieldMinute:
		mi = wrapInt(mi+delta, 60)
	case fieldSecond:
		s = wrapInt(s+delta, 60)
	}
	p.t = time.Date(p.t.Year(), p.t.Month(), p.t.Day(), h, mi, s, 0, p.t.Location())
}

// addMonths moves whole months with the day clamped to the shorter month,
// so 31 January back one month lands on 31 December, not 2 or 3 March the
// way time.AddDate's own normalization would put it.
func (p *datePickerModal) addMonths(delta int) {
	y, mo, d := p.t.Date()
	h, mi, s := p.t.Hour(), p.t.Minute(), p.t.Second()
	first := time.Date(y, mo, 1, 0, 0, 0, 0, p.t.Location()).AddDate(0, delta, 0)
	if n := daysInMonth(first.Year(), first.Month()); d > n {
		d = n
	}
	p.t = time.Date(first.Year(), first.Month(), d, h, mi, s, 0, p.t.Location())
}

// today jumps to now, keeping only the halves the column actually has.
func (p *datePickerModal) today() {
	now := time.Now().Truncate(time.Second)
	if !p.kind.HasTime() {
		now = time.Date(now.Year(), now.Month(), now.Day(),
			p.t.Hour(), p.t.Minute(), p.t.Second(), 0, now.Location())
	}
	p.t = now
}

func (p *datePickerModal) toggleSection() {
	if p.kind != db.KindDateTime {
		return
	}
	if p.section == pickDate {
		p.section = pickTime
		return
	}
	p.section = pickDate
}

// timeFields is how many spinners the clock shows. Seconds are part of
// every ISO form lazysql emits, so all three are always there; the helper
// exists so the cursor wrap has one place to read the count from.
func (p *datePickerModal) timeFields() int { return fieldCount }

func wrapInt(v, n int) int { return ((v % n) + n) % n }

func daysInMonth(y int, mo time.Month) int {
	return time.Date(y, mo+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// ---------- view ----------

// weekdayHeads are the calendar's column heads, Monday first — the ISO
// week, matching the ISO values the picker produces.
var weekdayHeads = [7]string{"Mo", "Tu", "We", "Th", "Fr", "Sa", "Su"}

// calendarRows renders the month grid: one string per week, days
// right-aligned in two columns. The day under the cursor is highlighted,
// today is tinted, and the whole grid dims while the clock has the
// keyboard so it is obvious which half j/k moves.
func (p *datePickerModal) calendarRows(s styles) []string {
	active := p.section == pickDate
	first := time.Date(p.t.Year(), p.t.Month(), 1, 0, 0, 0, 0, p.t.Location())
	lead := (int(first.Weekday()) + 6) % 7 // Monday = 0
	n := daysInMonth(p.t.Year(), p.t.Month())
	now := time.Now()
	sameMonthAsToday := p.t.Year() == now.Year() && p.t.Month() == now.Month()

	head := s.muted.Render(strings.Join(weekdayHeads[:], " "))
	rows := []string{head}

	cells := make([]string, 0, 7)
	flush := func() {
		for len(cells) < 7 {
			cells = append(cells, "  ")
		}
		rows = append(rows, strings.Join(cells, " "))
		cells = cells[:0]
	}
	for i := 0; i < lead; i++ {
		cells = append(cells, "  ")
	}
	for d := 1; d <= n; d++ {
		txt := fmt.Sprintf("%2d", d)
		switch {
		case d == p.t.Day() && active:
			txt = s.cellCursor.Render(txt)
		case d == p.t.Day():
			txt = s.selected.Render(txt)
		case sameMonthAsToday && d == now.Day():
			txt = s.pending.Render(txt)
		case !active:
			txt = s.muted.Render(txt)
		}
		cells = append(cells, txt)
		if len(cells) == 7 {
			flush()
		}
	}
	if len(cells) > 0 {
		flush()
	}
	return rows
}

// clockRow renders hh:mm:ss with the spinner under the cursor picked out.
func (p *datePickerModal) clockRow(s styles) string {
	active := p.section == pickTime
	parts := [fieldCount]string{
		fmt.Sprintf("%02d", p.t.Hour()),
		fmt.Sprintf("%02d", p.t.Minute()),
		fmt.Sprintf("%02d", p.t.Second()),
	}
	out := make([]string, 0, fieldCount)
	for i, txt := range parts {
		switch {
		case i == p.field && active:
			txt = s.cellCursor.Render(txt)
		case !active:
			txt = s.muted.Render(txt)
		}
		out = append(out, txt)
	}
	sep := ":"
	if !active {
		sep = s.muted.Render(":")
	}
	return strings.Join(out, sep)
}

func (p *datePickerModal) view(s styles, maxW, maxH int) string {
	width := min(maxW-8, 70)
	if width < 20 {
		width = 20
	}

	lines := []string{
		s.modalTitle.Render(truncate(p.title, width)),
		s.muted.Render(truncate(p.typeLine, width)),
		"",
	}
	if p.current != "" {
		lines = append(lines, s.muted.Render("current: "+truncate(flatten(p.current), 50)), "")
	}
	if p.kind.HasDate() {
		month := p.t.Format("January 2006")
		if p.section == pickDate {
			month = s.titleFocused.Render(month)
		} else {
			month = s.muted.Render(month)
		}
		lines = append(lines,
			s.keyHint.Render("‹ ")+month+s.keyHint.Render(" ›"))
		lines = append(lines, p.calendarRows(s)...)
	}
	if p.kind.HasTime() {
		if p.kind.HasDate() {
			lines = append(lines, "")
		}
		lines = append(lines, p.clockRow(s))
	}
	lines = append(lines,
		"",
		s.pending.Render(p.value()),
		"",
		s.muted.Render(p.footer()))
	return s.modal.Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
}

// footer spells the picker's keys from the bindings update dispatches on,
// so a `[keys]` override reaches the hint too. Bindings that mean nothing
// for the column's kind are left out rather than shown as no-ops.
func (p *datePickerModal) footer() string {
	k := p.keys
	pair := func(b key.Binding, verb string) string { return b.Help().Key + " " + verb }

	var parts []string
	if p.section == pickDate {
		parts = append(parts,
			pair(k.PickPrev, "day"),
			pair(k.PickDown, "week"),
			pair(k.PickMonthNext, "month"))
	} else {
		parts = append(parts,
			pair(k.PickPrev, "field"),
			pair(k.PickUp, "adjust"))
	}
	if p.kind == db.KindDateTime {
		parts = append(parts, pair(k.PickSection, "date/time"))
	}
	parts = append(parts,
		pair(k.PickToday, "now"),
		pair(k.PickRaw, "raw text"),
		"enter confirm",
		pair(k.Back, "cancel"))
	return strings.Join(parts, " · ")
}
