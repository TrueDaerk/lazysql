package db

import (
	"fmt"
	"strings"
)

// Staged mutations. lazysql never executes destructive SQL as a side
// effect of editing: a cell edit only records a CellChange here, a row
// delete a RowDelete and a row insert a RowInsert. The SQL runs when the
// user explicitly commits the whole changeset — in one transaction, so a
// failure applies nothing.

// Change is one staged mutation. The three implementations live in this
// file; the unexported methods keep it that way, so no UI package can
// smuggle its own SQL into a commit.
type Change interface {
	// key identifies what the change targets: two changes with the same
	// key are the same pending mutation, and the second replaces the
	// first instead of stacking on it.
	key() string
	// target is the database and table the change applies to.
	target() (database, table string)
	// Statement renders the change as one parameterized statement.
	Statement(d Dialect) Statement
}

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

// key of a cell change: same cell = same key. Values are keyed with
// their type so int64(1) and "1" stay distinct.
func (c CellChange) key() string {
	var b strings.Builder
	fmt.Fprintf(&b, "cell\x00%s\x00%s\x00%s", c.Database, c.Table, c.Column)
	writeKeyVals(&b, c.PKVals)
	return b.String()
}

func (c CellChange) target() (string, string) { return c.Database, c.Table }

func (c CellChange) Statement(d Dialect) Statement { return UpdateSQL(d, c) }

// RowDelete is one staged DELETE of a whole row, identified by the same
// full primary key a cell edit uses. A table without a declared primary
// key cannot be deleted from, for the same reason it cannot be edited.
type RowDelete struct {
	Database string
	Table    string
	PKCols   []string
	PKVals   []any
}

func (r RowDelete) key() string {
	var b strings.Builder
	fmt.Fprintf(&b, "del\x00%s\x00%s", r.Database, r.Table)
	writeKeyVals(&b, r.PKVals)
	return b.String()
}

func (r RowDelete) target() (string, string) { return r.Database, r.Table }

func (r RowDelete) Statement(d Dialect) Statement { return DeleteSQL(d, r) }

// RowInsert is one staged INSERT. Columns and Values are positionally
// paired and hold only the columns the user filled in — everything the
// form left on "DEFAULT" is simply absent, so the engine applies its own
// default or auto-increment. A nil value is an explicit SQL NULL.
//
// ID makes two identical inserts two rows: unlike an UPDATE or a DELETE,
// an INSERT has no natural identity to key on, so the changeset assigns
// one at staging time and `u` unstages by it.
type RowInsert struct {
	ID       int
	Database string
	Table    string
	Columns  []string
	Values   []any
}

func (r RowInsert) key() string {
	return fmt.Sprintf("ins\x00%s\x00%s\x00%d", r.Database, r.Table, r.ID)
}

func (r RowInsert) target() (string, string) { return r.Database, r.Table }

func (r RowInsert) Statement(d Dialect) Statement { return InsertSQL(d, r) }

// writeKeyVals appends type-tagged key values to a change key.
func writeKeyVals(b *strings.Builder, vals []any) {
	for _, v := range vals {
		fmt.Fprintf(b, "\x00%T:%v", v, v)
	}
}

// Changeset accumulates staged changes in the order they were first
// staged, which is also the order they execute in on commit.
type Changeset struct {
	ops    []Change
	index  map[string]int
	nextID int
}

// NewChangeset returns an empty changeset.
func NewChangeset() *Changeset {
	return &Changeset{index: map[string]int{}}
}

// stage appends a change, or replaces the one with the same key.
func (cs *Changeset) stage(c Change) {
	k := c.key()
	if i, ok := cs.index[k]; ok {
		cs.ops[i] = c
		return
	}
	cs.index[k] = len(cs.ops)
	cs.ops = append(cs.ops, c)
}

// removeAt drops one change and repairs the index.
func (cs *Changeset) removeAt(i int) {
	delete(cs.index, cs.ops[i].key())
	cs.ops = append(cs.ops[:i], cs.ops[i+1:]...)
	for j := i; j < len(cs.ops); j++ {
		cs.index[cs.ops[j].key()] = j
	}
}

// removeKey drops the change with a key and reports whether there was one.
func (cs *Changeset) removeKey(k string) bool {
	i, ok := cs.index[k]
	if !ok {
		return false
	}
	cs.removeAt(i)
	return true
}

// Stage records a confirmed cell edit, replacing an earlier edit of the
// same cell. A replacement keeps the original OldValue so unstaging or
// displaying the change still refers to what the database holds.
func (cs *Changeset) Stage(c CellChange) {
	if i, ok := cs.index[c.key()]; ok {
		if prev, ok := cs.ops[i].(CellChange); ok {
			c.OldValue = prev.OldValue
		}
	}
	cs.stage(c)
}

// StageDelete records a row delete and reports whether it was new. It
// also drops any staged cell edits of the same row: updating a row the
// same commit deletes is dead work, and hiding it behind a strikethrough
// row would be worse.
func (cs *Changeset) StageDelete(r RowDelete) bool {
	if _, ok := cs.index[r.key()]; ok {
		return false
	}
	for i := len(cs.ops) - 1; i >= 0; i-- {
		c, ok := cs.ops[i].(CellChange)
		if ok && c.Database == r.Database && c.Table == r.Table && keyValsEqual(c.PKVals, r.PKVals) {
			cs.removeAt(i)
		}
	}
	cs.stage(r)
	return true
}

// StageInsert records a row insert, assigning it the identity the UI
// unstages it by, and returns the stored change.
func (cs *Changeset) StageInsert(r RowInsert) RowInsert {
	cs.nextID++
	r.ID = cs.nextID
	cs.stage(r)
	return r
}

// Unstage drops the staged change of one cell and reports whether there
// was one.
func (cs *Changeset) Unstage(database, table string, pkVals []any, column string) bool {
	return cs.removeKey(CellChange{Database: database, Table: table, PKVals: pkVals, Column: column}.key())
}

// UnstageDelete drops the staged delete of one row and reports whether
// there was one.
func (cs *Changeset) UnstageDelete(database, table string, pkVals []any) bool {
	return cs.removeKey(RowDelete{Database: database, Table: table, PKVals: pkVals}.key())
}

// UnstageInsert drops one staged insert by its identity.
func (cs *Changeset) UnstageInsert(database, table string, id int) bool {
	return cs.removeKey(RowInsert{Database: database, Table: table, ID: id}.key())
}

// UnstageRow drops every staged change of one row — the delete and any
// cell edits — and reports how many went.
func (cs *Changeset) UnstageRow(database, table string, pkVals []any) int {
	n := 0
	for i := len(cs.ops) - 1; i >= 0; i-- {
		var (
			db2, t2 = cs.ops[i].target()
			vals    []any
		)
		switch c := cs.ops[i].(type) {
		case CellChange:
			vals = c.PKVals
		case RowDelete:
			vals = c.PKVals
		default:
			continue
		}
		if db2 == database && t2 == table && keyValsEqual(vals, pkVals) {
			cs.removeAt(i)
			n++
		}
	}
	return n
}

// Lookup returns the staged change of one cell, if any.
func (cs *Changeset) Lookup(database, table string, pkVals []any, column string) (CellChange, bool) {
	k := CellChange{Database: database, Table: table, PKVals: pkVals, Column: column}.key()
	if i, ok := cs.index[k]; ok {
		if c, ok := cs.ops[i].(CellChange); ok {
			return c, true
		}
	}
	return CellChange{}, false
}

// DeleteStaged reports whether a row is staged for deletion.
func (cs *Changeset) DeleteStaged(database, table string, pkVals []any) bool {
	_, ok := cs.index[RowDelete{Database: database, Table: table, PKVals: pkVals}.key()]
	return ok
}

// InsertsFor returns the staged inserts of one table in staging order.
// The grid renders them as phantom rows after the last real one.
func (cs *Changeset) InsertsFor(database, table string) []RowInsert {
	var out []RowInsert
	for _, op := range cs.ops {
		if r, ok := op.(RowInsert); ok && r.Database == database && r.Table == table {
			out = append(out, r)
		}
	}
	return out
}

// PKColsFor returns the primary key columns recorded for a table, taken
// from any staged change of it that identifies a row. It lets the grid
// find the key columns of rendered rows without re-introspecting the
// table.
func (cs *Changeset) PKColsFor(database, table string) []string {
	for _, op := range cs.ops {
		d, t := op.target()
		if d != database || t != table {
			continue
		}
		switch c := op.(type) {
		case CellChange:
			return c.PKCols
		case RowDelete:
			return c.PKCols
		}
	}
	return nil
}

// Len is how many changes are staged.
func (cs *Changeset) Len() int { return len(cs.ops) }

// All returns the staged changes in commit order. Callers must not
// mutate the slice.
func (cs *Changeset) All() []Change { return cs.ops }

// Clear discards every staged change. The insert counter is not reset:
// nothing depends on the numbering, and monotonic ids keep a stale
// reference from ever matching a fresh insert.
func (cs *Changeset) Clear() {
	cs.ops = nil
	cs.index = map[string]int{}
}

// keyValsEqual compares two primary key tuples. The normalized cell
// types (nil, string, int64, float64, bool, time.Time) are comparable.
func keyValsEqual(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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
	args = writeKeyPredicates(&b, d, c.PKCols, c.PKVals, args)
	return Statement{SQL: b.String(), Args: args}
}

// DeleteSQL renders one staged row delete as a parameterized DELETE.
// The full primary key is in the WHERE clause, so the statement can
// never hit more than the row the user pointed at.
func DeleteSQL(d Dialect, r RowDelete) Statement {
	var b strings.Builder
	b.WriteString("DELETE FROM ")
	b.WriteString(qualifiedTable(d, r.Database, r.Table))
	args := writeKeyPredicates(&b, d, r.PKCols, r.PKVals, make([]any, 0, len(r.PKVals)))
	return Statement{SQL: b.String(), Args: args}
}

// InsertSQL renders one staged row insert as a parameterized INSERT.
// Only the columns the form filled in are named; the rest are left to
// the engine's DEFAULT / auto-increment, which is why an insert with no
// columns at all is still a valid statement.
func InsertSQL(d Dialect, r RowInsert) Statement {
	var b strings.Builder
	b.WriteString("INSERT INTO ")
	b.WriteString(qualifiedTable(d, r.Database, r.Table))
	if len(r.Columns) == 0 {
		b.WriteString(defaultValuesClause(d))
		return Statement{SQL: b.String()}
	}
	args := make([]any, 0, len(r.Values))
	b.WriteString(" (")
	for i, c := range r.Columns {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(d.QuoteIdent(c))
	}
	b.WriteString(") VALUES (")
	for i := range r.Columns {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(d.Placeholder(i + 1))
		if i < len(r.Values) {
			args = append(args, r.Values[i])
		} else {
			args = append(args, nil)
		}
	}
	b.WriteString(")")
	return Statement{SQL: b.String(), Args: args}
}

// writeKeyPredicates appends " WHERE pk = ? AND …" and returns the
// argument list extended with the key values.
func writeKeyPredicates(b *strings.Builder, d Dialect, cols []string, vals, args []any) []any {
	b.WriteString(" WHERE ")
	for i, pk := range cols {
		if i > 0 {
			b.WriteString(" AND ")
		}
		b.WriteString(d.QuoteIdent(pk))
		b.WriteString(" = ")
		b.WriteString(d.Placeholder(len(args) + 1))
		args = append(args, vals[i])
	}
	return args
}

// Statements renders the whole changeset in commit order.
func (cs *Changeset) Statements(d Dialect) []Statement {
	out := make([]Statement, 0, len(cs.ops))
	for _, c := range cs.ops {
		out = append(out, c.Statement(d))
	}
	return out
}
