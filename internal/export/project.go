package export

import (
	"context"

	"lazysql/internal/db"
)

// Projection narrows a streamed result to a subset of its columns, in a
// given order: the columns the grid shows, with the hidden ones left out
// and the pinned ones first. Each entry names a column and the position
// the grid saw it at; the position wins while the name still matches it
// (a query result may carry two columns of one name, and only the
// position tells them apart), the name otherwise (a relation altered
// between the grid's page and the export's). A nil Projection keeps
// every column as it is.
type Projection []ProjectedColumn

// ProjectedColumn is one column a Projection keeps.
type ProjectedColumn struct {
	Name  string
	Index int
}

// resolve maps the projection onto one result's columns: the source index
// of every kept column, dropping any the result no longer has.
func (p Projection) resolve(cols []db.Column) []int {
	idx := make([]int, 0, len(p))
	for _, c := range p {
		if c.Index >= 0 && c.Index < len(cols) && cols[c.Index].Name == c.Name {
			idx = append(idx, c.Index)
			continue
		}
		for i, col := range cols {
			if col.Name == c.Name {
				idx = append(idx, i)
				break
			}
		}
	}
	return idx
}

func pick[T any](src []T, idx []int) []T {
	out := make([]T, len(idx))
	for i, j := range idx {
		if j < len(src) {
			out[i] = src[j]
		}
	}
	return out
}

// Pager wraps a Pager so every page it returns carries only the
// projection's columns.
func (p Projection) Pager(page Pager) Pager {
	if p == nil {
		return page
	}
	return func(ctx context.Context, limit, offset int) (*db.ResultSet, error) {
		rs, err := page(ctx, limit, offset)
		if err != nil || rs == nil {
			return rs, err
		}
		idx := p.resolve(rs.Columns)
		out := &db.ResultSet{Columns: pick(rs.Columns, idx), Rows: make([][]any, len(rs.Rows))}
		for i, row := range rs.Rows {
			out.Rows[i] = pick(row, idx)
		}
		return out, nil
	}
}

// QueryRunner wraps a QueryRunner so every row it yields carries only
// the projection's columns.
func (p Projection) QueryRunner(run QueryRunner) QueryRunner {
	if p == nil {
		return run
	}
	return func(ctx context.Context, onRow func(cols []db.Column, row []any) error) error {
		var idx []int
		var cols []db.Column
		return run(ctx, func(c []db.Column, row []any) error {
			if cols == nil {
				idx = p.resolve(c)
				cols = pick(c, idx)
			}
			return onRow(cols, pick(row, idx))
		})
	}
}
