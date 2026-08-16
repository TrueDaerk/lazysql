package ui

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/db"
	"lazysql/internal/sqlhl"
)

// The placeholder prompt: the modal that stands between `ctrl+r` on a
// statement carrying `?`/`:name` holes and the run itself. Detection and
// binding live in internal/db (placeholders.go); this file is only the
// asking — one form field per placeholder, plus the NULL toggle that
// makes an empty field unambiguous, and the session-scoped memory that
// pre-fills the next run of the same statement.

// paramFieldPrefix names the value field of the i-th placeholder, and
// paramNullPrefix its NULL toggle. Names, not labels: two placeholders can
// never share an index, while `? (1)` and `? (2)` only differ by their
// position anyway.
const (
	paramFieldPrefix = "p"
	paramNullPrefix  = "null"
)

// newParamsForm builds the prompt for phs. prev, when it has one entry per
// placeholder, pre-fills the fields with what the same statement was last
// run with. onSubmit receives the values in the order ExtractPlaceholders
// returned the placeholders — the order db.BindPlaceholders expects.
//
// It is the shared formModal rather than a bespoke popup: the fields, the
// cursor, the pinned geometry and the enter/esc contract are all already
// there, and the NULL toggle is exactly its fieldBool.
func newParamsForm(s styles, sql string, d sqlhl.Dialect, phs []db.Placeholder,
	prev []db.ParamValue, onSubmit func(*Model, []db.ParamValue) tea.Cmd) *formModal {

	fields := make([]*formField, 0, 2*len(phs))
	for i, ph := range phs {
		var val db.ParamValue
		if i < len(prev) {
			val = prev[i]
		}
		fields = append(fields,
			newTextField(paramName(paramFieldPrefix, i), ph.Label, val.Text, ""),
			newBoolField(paramName(paramNullPrefix, i), "  ↳ NULL", val.Null).
				withHelp("bind SQL NULL, ignoring the text above"))
	}

	f := newFormModal("Query parameters", fields,
		func(m *Model, f *formModal) (bool, tea.Cmd) {
			if onSubmit == nil {
				return true, nil
			}
			return true, onSubmit(m, paramValues(f, len(phs)))
		})
	f.footer = "tab/↑↓ field · space NULL · enter run · esc cancel"
	// The statement above the fields: which query is being parameterized
	// is the one thing the labels cannot say, and a prompt opened from the
	// history pane or a snippet may be about SQL that is nowhere on screen.
	f.withBody(func(*formModal) []string {
		lines := strings.Split(sql, "\n")
		const maxLines = 5
		var out []string
		for _, l := range lines[:min(len(lines), maxLines)] {
			out = append(out, highlightSQL(s, d, l))
		}
		if len(lines) > maxLines {
			out = append(out, s.muted.Render(fmt.Sprintf("… %d more lines", len(lines)-maxLines)))
		}
		return out
	})
	return f
}

// paramValues reads the entered values back out of the form, untrimmed:
// a parameter's leading or trailing spaces are part of the value, unlike
// a hostname's or a port's.
func paramValues(f *formModal, n int) []db.ParamValue {
	out := make([]db.ParamValue, n)
	for i := range out {
		if fl := f.field(paramName(paramFieldPrefix, i)); fl != nil {
			out[i].Text = fl.input.Value()
		}
		if fl := f.field(paramName(paramNullPrefix, i)); fl != nil {
			out[i].Null = fl.on
		}
	}
	return out
}

func paramName(prefix string, i int) string {
	return prefix + strconv.Itoa(i)
}

// ---------- session memory ----------

// paramMemoryCap bounds how many statements keep their last values. The
// store is a convenience, not a history: past a few dozen statements the
// oldest entry is one nobody is going to re-run in this session.
const paramMemoryCap = 50

// paramMemory is the last-used values per statement, keyed by the
// statement text exactly as it was typed — which is what makes a snippet
// and a hand-typed copy of it share one memory, and an edited statement
// (a different set of placeholders) start clean.
type paramMemory struct {
	byStmt map[string][]db.ParamValue
	order  []string // insertion order, oldest first, for eviction
}

func newParamMemory() *paramMemory {
	return &paramMemory{byStmt: map[string][]db.ParamValue{}}
}

// recall returns the values sql last ran with, or nil. It is nil-safe so a
// hand-built Model in a test needs no store.
func (p *paramMemory) recall(sql string) []db.ParamValue {
	if p == nil {
		return nil
	}
	return p.byStmt[sql]
}

func (p *paramMemory) remember(sql string, values []db.ParamValue) {
	if p == nil {
		return
	}
	if p.byStmt == nil {
		p.byStmt = map[string][]db.ParamValue{}
	}
	if _, ok := p.byStmt[sql]; !ok {
		p.order = append(p.order, sql)
	}
	p.byStmt[sql] = append([]db.ParamValue(nil), values...)
	for len(p.order) > paramMemoryCap {
		delete(p.byStmt, p.order[0])
		p.order = p.order[1:]
	}
}
