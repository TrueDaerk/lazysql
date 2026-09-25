package export

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"lazysql/internal/db"
)

// A projected pager hands the writer only the kept columns, in the
// projection's order.
func TestProjectionPagerKeepsAndReordersColumns(t *testing.T) {
	src := &db.ResultSet{
		Columns: cols("id", "note", "status"),
		Rows:    [][]any{{int64(1), "long", "open"}, {int64(2), nil, "done"}},
	}
	p := Projection{{Name: "status", Index: 2}, {Name: "id", Index: 0}}
	page := p.Pager(func(context.Context, int, int) (*db.ResultSet, error) { return src, nil })

	var b strings.Builder
	w, err := NewWriter(&b, FormatCSV, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Stream(context.Background(), w, page, StreamOptions{PageSize: 10}); err != nil {
		t.Fatal(err)
	}
	if want := "status,id\nopen,1\ndone,2\n"; b.String() != want {
		t.Fatalf("csv =\n%q\nwant\n%q", b.String(), want)
	}
}

// Two columns of one name are told apart by position; a column that
// moved is still found by name.
func TestProjectionResolvesByPositionThenName(t *testing.T) {
	p := Projection{{Name: "id", Index: 2}, {Name: "name", Index: 1}}
	if got := p.resolve(cols("id", "name", "id")); !reflect.DeepEqual(got, []int{2, 1}) {
		t.Fatalf("duplicate names resolved to %v, want [2 1]", got)
	}
	if got := p.resolve(cols("name", "id")); !reflect.DeepEqual(got, []int{1, 0}) {
		t.Fatalf("moved columns resolved to %v, want [1 0]", got)
	}
}

// A projected query runner narrows every row it yields.
func TestProjectionQueryRunner(t *testing.T) {
	p := Projection{{Name: "b", Index: 1}}
	run := p.QueryRunner(func(ctx context.Context, onRow func([]db.Column, []any) error) error {
		for _, r := range [][]any{{1, 2}, {3, 4}} {
			if err := onRow(cols("a", "b"), r); err != nil {
				return err
			}
		}
		return nil
	})
	var got [][]any
	err := run(context.Background(), func(c []db.Column, row []any) error {
		if len(c) != 1 || c[0].Name != "b" {
			t.Fatalf("columns = %v", c)
		}
		got = append(got, row)
		return nil
	})
	if err != nil || !reflect.DeepEqual(got, [][]any{{2}, {4}}) {
		t.Fatalf("rows = %v, err = %v", got, err)
	}
}
