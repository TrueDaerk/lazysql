package sqlhl

import (
	"strings"
	"testing"
)

func TestFunctionsIsSortedAndUppercase(t *testing.T) {
	for _, d := range []Dialect{Generic, MySQL, Postgres, SQLite, DuckDB} {
		list := Functions(d)
		if len(list) == 0 {
			t.Fatalf("Functions(%q) is empty", d)
		}
		for i, w := range list {
			if w != strings.ToUpper(w) {
				t.Errorf("Functions(%q) yields %q, want upper case", d, w)
			}
			if i > 0 && list[i-1] >= w {
				t.Errorf("Functions(%q) is not sorted at %q", d, w)
			}
		}
	}
}

// The function catalog and the keyword set are built from separate lists
// (coreFunctions/dialectFunctions here, coreKeywords/dialectKeywords in
// keywords.go): adding a function must never widen what the highlighter
// colours as a keyword. A handful of names legitimately mean both things
// in SQL (CAST is also keyword syntax, LIST is also DuckDB's list type),
// so the two sets are allowed to intersect — this only guards against the
// whole catalog leaking in, which would turn every column named COUNT or
// LENGTH into a false positive for IsKeyword.
func TestFunctionsDoNotFloodTheKeywordSet(t *testing.T) {
	for _, d := range []Dialect{Generic, MySQL, Postgres, SQLite, DuckDB} {
		overlap := 0
		for _, w := range Functions(d) {
			if IsKeyword(d, w) {
				overlap++
			}
		}
		if n := len(Functions(d)); overlap > n/2 {
			t.Errorf("Functions(%q): %d of %d functions are also keywords, want the two lists to stay mostly separate", d, overlap, n)
		}
	}
}

func TestFunctionsCoreIsSharedByEveryDialect(t *testing.T) {
	for _, d := range []Dialect{Generic, MySQL, Postgres, SQLite, DuckDB} {
		list := Functions(d)
		for _, want := range []string{"COALESCE", "LENGTH", "NULLIF", "COUNT"} {
			found := false
			for _, w := range list {
				if w == want {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("Functions(%q) is missing the core function %q", d, want)
			}
		}
	}
}

func TestFunctionsDifferPerDialect(t *testing.T) {
	cases := []struct {
		dialect    Dialect
		has, hasnt string
	}{
		{MySQL, "GROUP_CONCAT", "STRING_AGG"},
		{Postgres, "STRING_AGG", "LIST"},
		{SQLite, "GROUP_CONCAT", "STRING_AGG"},
		{DuckDB, "LIST", "AUTO_INCREMENT"},
	}
	for _, c := range cases {
		list := Functions(c.dialect)
		has, hasnt := false, false
		for _, w := range list {
			if w == c.has {
				has = true
			}
			if w == c.hasnt {
				hasnt = true
			}
		}
		if !has {
			t.Errorf("Functions(%q) is missing %q", c.dialect, c.has)
		}
		if hasnt {
			t.Errorf("Functions(%q) offers %q, which belongs to another dialect", c.dialect, c.hasnt)
		}
	}
}
