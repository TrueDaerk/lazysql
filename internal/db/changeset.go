package db

import (
	"fmt"
	"strings"
)

// Staged mutations. lazysql never executes destructive SQL as a side
// effect of editing: a cell edit only records a CellChange here, and the
// SQL runs when the user explicitly commits the whole changeset — in one
// transaction, so a failure applies nothing.

// CellChange is one staged single-cell UPDATE. The row is identified by
// the table's primary key, never by heuristics: PKCols and PKVals are
// positionally paired and always cover the full (possibly composite)
// key. NewValue nil means SQL NULL.
type CellChange struct {
	Database string
	Table    string
	PKCols   []string
	PKVals   []any
	Column   string
	OldValue any
	NewValue any
}

// key identifies the cell a change targets: same key = same cell, so a
// second edit of a cell replaces the first instead of stacking on it.
// Values are keyed with their type so int64(1) and "1" stay distinct.
func (c CellChange) key() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\x00%s\x00%s", c.Database, c.Table, c.Column)
	for _, v := range c.PKVals {
		fmt.Fprintf(&b, "\x00%T:%v", v, v)
	}
	return b.String()
}

// Changeset accumulates staged changes in the order they were first
// staged, which is also the order they execute in on commit.
type Changeset struct {
	changes []CellChange
	index   map[string]int
}

// NewChangeset returns an empty changeset.
func NewChangeset() *Changeset {
	return &Changeset{index: map[string]int{}}
}

// Stage records a change, replacing an earlier change of the same cell.
// A replacement keeps the original OldValue so unstaging or displaying
// the change still refers to what the database holds.
func (cs *Changeset) Stage(c CellChange) {
	k := c.key()
	if i, ok := cs.index[k]; ok {
		c.OldValue = cs.changes[i].OldValue
		cs.changes[i] = c
		return
	}
	cs.index[k] = len(cs.changes)
	cs.changes = append(cs.changes, c)
}

// Unstage drops the staged change of one cell and reports whether there
// was one.
func (cs *Changeset) Unstage(database, table string, pkVals []any, column string) bool {
	k := CellChange{Database: database, Table: table, PKVals: pkVals, Column: column}.key()
	i, ok := cs.index[k]
	if !ok {
		return false
	}
	cs.changes = append(cs.changes[:i], cs.changes[i+1:]...)
	delete(cs.index, k)
	for j := i; j < len(cs.changes); j++ {
		cs.index[cs.changes[j].key()] = j
	}
	return true
}

// Lookup returns the staged change of one cell, if any.
func (cs *Changeset) Lookup(database, table string, pkVals []any, column string) (CellChange, bool) {
	k := CellChange{Database: database, Table: table, PKVals: pkVals, Column: column}.key()
	if i, ok := cs.index[k]; ok {
		return cs.changes[i], true
	}
	return CellChange{}, false
}

// PKColsFor returns the primary key columns recorded for a table, taken
// from any staged change of it. It lets the grid find the key columns of
// rendered rows without re-introspecting the table.
func (cs *Changeset) PKColsFor(database, table string) []string {
	for _, c := range cs.changes {
		if c.Database == database && c.Table == table {
			return c.PKCols
		}
	}
	return nil
}

// Len is how many changes are staged.
func (cs *Changeset) Len() int { return len(cs.changes) }

// All returns the staged changes in commit order. Callers must not
// mutate the slice.
func (cs *Changeset) All() []CellChange { return cs.changes }

// Clear discards every staged change.
func (cs *Changeset) Clear() {
	cs.changes = nil
	cs.index = map[string]int{}
}

// Statement is one parameterized SQL statement ready to execute.
type Statement struct {
	SQL  string
	Args []any
}

// UpdateSQL renders one staged change as a parameterized UPDATE: the new
// value and every key value travel as parameters, identifiers get the
// dialect's quoting. Nothing user-supplied reaches the statement text.
func UpdateSQL(d Dialect, c CellChange) Statement {
	var b strings.Builder
	args := make([]any, 0, 1+len(c.PKVals))
	b.WriteString("UPDATE ")
	b.WriteString(qualifiedTable(d, c.Database, c.Table))
	b.WriteString(" SET ")
	b.WriteString(d.QuoteIdent(c.Column))
	b.WriteString(" = ")
	b.WriteString(d.Placeholder(1))
	args = append(args, c.NewValue)
	b.WriteString(" WHERE ")
	for i, pk := range c.PKCols {
		if i > 0 {
			b.WriteString(" AND ")
		}
		b.WriteString(d.QuoteIdent(pk))
		b.WriteString(" = ")
		b.WriteString(d.Placeholder(len(args) + 1))
		args = append(args, c.PKVals[i])
	}
	return Statement{SQL: b.String(), Args: args}
}

// Statements renders the whole changeset in commit order.
func (cs *Changeset) Statements(d Dialect) []Statement {
	out := make([]Statement, 0, len(cs.changes))
	for _, c := range cs.changes {
		out = append(out, UpdateSQL(d, c))
	}
	return out
}
