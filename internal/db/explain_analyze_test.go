package db

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestExplainAnalyzeDuckDB(t *testing.T) {
	drv := openTest(t, EngineDuckDB, "")
	seed(t, drv)
	if err := drv.ExplainAnalyzeSupport(); err != nil {
		t.Fatalf("ExplainAnalyzeSupport: %v", err)
	}
	plan, err := drv.ExplainAnalyze(context.Background(), "SELECT * FROM users WHERE name = 'alice';")
	if err != nil {
		t.Fatalf("ExplainAnalyze: %v", err)
	}
	if !plan.Analyzed {
		t.Error("an analyzed plan is not marked Analyzed")
	}
	if plan.SQL != "EXPLAIN ANALYZE SELECT * FROM users WHERE name = 'alice'" {
		t.Errorf("SQL = %q", plan.SQL)
	}
	if len(plan.Lines()) == 0 {
		t.Fatal("no plan lines")
	}
	var logged bool
	for _, e := range drv.Logger().Entries() {
		if strings.HasPrefix(e.SQL, "EXPLAIN ANALYZE ") {
			logged = true
		}
	}
	if !logged {
		t.Error("the EXPLAIN ANALYZE statement did not reach the command log")
	}
}

// The plain Explain must never come back marked analyzed: the view tells
// the two apart by this flag.
func TestExplainIsNotAnalyzed(t *testing.T) {
	drv := openTest(t, EngineDuckDB, "")
	seed(t, drv)
	plan, err := drv.Explain(context.Background(), "SELECT * FROM users")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Analyzed {
		t.Error("Explain returned a plan marked Analyzed")
	}
}

// A write is refused before anything reaches the engine, by the same
// classification the read-only guard uses — including one already spelled
// EXPLAIN ANALYZE by hand.
func TestExplainAnalyzeRefusesWrites(t *testing.T) {
	drv := openTest(t, EngineDuckDB, "")
	seed(t, drv)
	ctx := context.Background()
	rowsBefore, err := drv.CountRows(ctx, "", "users", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		"DELETE FROM users",
		"UPDATE users SET name = 'x'",
		"EXPLAIN ANALYZE DELETE FROM users",
		"WITH d AS (DELETE FROM users RETURNING id) SELECT * FROM d",
		"SELECT 1; DELETE FROM users",
	} {
		before := len(drv.Logger().Entries())
		_, err := drv.ExplainAnalyze(ctx, stmt)
		if !errors.Is(err, ErrAnalyzeWrite) {
			t.Errorf("%q: err = %v, want ErrAnalyzeWrite", stmt, err)
		}
		if n := len(drv.Logger().Entries()); n != before {
			t.Errorf("%q: a refused statement reached the engine", stmt)
		}
	}
	n, err := drv.CountRows(ctx, "", "users", nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != rowsBefore {
		t.Fatalf("rows after refused analyzes = %d, want %d", n, rowsBefore)
	}
}

func TestExplainAnalyzeUnsupportedOnSQLite(t *testing.T) {
	drv := openTest(t, EngineSQLite, "")
	seed(t, drv)
	if err := drv.ExplainAnalyzeSupport(); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("ExplainAnalyzeSupport = %v, want ErrUnsupported", err)
	}
	_, err := drv.ExplainAnalyze(context.Background(), "SELECT * FROM users")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("ExplainAnalyze = %v, want ErrUnsupported", err)
	}
	if !strings.Contains(err.Error(), "SQLite") {
		t.Errorf("the refusal does not say why: %v", err)
	}
}

// A read-only session may still analyze a read — it changes nothing —
// and still refuses a write.
func TestExplainAnalyzeOnReadOnlySession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ro.duckdb")
	rw := openTest(t, EngineDuckDB, path)
	seed(t, rw)
	if err := rw.Close(); err != nil {
		t.Fatal(err)
	}
	dsn, err := BuildDSN(EngineDuckDB, ConnParams{File: path, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	drv, err := OpenOpts(EngineDuckDB, Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := drv.Connect(context.Background(), dsn); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { drv.Close() })

	plan, err := drv.ExplainAnalyze(context.Background(), "SELECT * FROM users")
	if err != nil {
		t.Fatalf("analyzing a read on a read-only session: %v", err)
	}
	if !plan.Analyzed {
		t.Error("plan not marked Analyzed")
	}
	if _, err := drv.ExplainAnalyze(context.Background(), "DELETE FROM users"); !errors.Is(err, ErrAnalyzeWrite) {
		t.Fatalf("analyzing a write on a read-only session: err = %v, want ErrAnalyzeWrite", err)
	}
}

const pgAnalyzedFixture = `[
  {
    "Plan": {
      "Node Type": "Seq Scan",
      "Relation Name": "users",
      "Alias": "users",
      "Startup Cost": 0.00,
      "Total Cost": 22.70,
      "Plan Rows": 1270,
      "Plan Width": 36,
      "Actual Startup Time": 0.011,
      "Actual Total Time": 0.013,
      "Actual Rows": 3,
      "Actual Loops": 1,
      "Plans": [
        {
          "Node Type": "Index Scan",
          "Relation Name": "orders",
          "Startup Cost": 0.00,
          "Total Cost": 1.00,
          "Plan Rows": 1,
          "Plan Width": 8,
          "Actual Loops": 0
        }
      ]
    },
    "Planning Time": 0.052,
    "Execution Time": 0.031
  }
]`

func TestParsePostgresAnalyzedPlan(t *testing.T) {
	nodes, footer, err := parsePostgresPlan(pgAnalyzedFixture)
	if err != nil {
		t.Fatal(err)
	}
	want := "(cost=0.00..22.70 rows=1270 width=36) (actual time=0.011..0.013 rows=3 loops=1)"
	if nodes[0].Detail != want {
		t.Errorf("detail = %q, want %q", nodes[0].Detail, want)
	}
	if d := nodes[0].Children[0].Detail; !strings.HasSuffix(d, "(never executed)") {
		t.Errorf("unexecuted node detail = %q", d)
	}
	if len(footer) != 2 || footer[0] != "Planning Time: 0.052 ms" || footer[1] != "Execution Time: 0.031 ms" {
		t.Errorf("footer = %v", footer)
	}
	p := &Plan{Format: PlanTree, Nodes: nodes, Footer: footer}
	lines := p.Lines()
	if lines[len(lines)-1] != "Execution Time: 0.031 ms" {
		t.Errorf("footer not rendered last: %v", lines)
	}
}

// An estimated plan's JSON has no Actual keys, and its rendering must not
// grow any.
func TestParsePostgresEstimatedPlanHasNoActuals(t *testing.T) {
	nodes, footer, err := parsePostgresPlan(pgPlanFixture)
	if err != nil {
		t.Fatal(err)
	}
	if len(footer) != 0 {
		t.Errorf("footer = %v, want none", footer)
	}
	if strings.Contains(strings.Join((&Plan{Nodes: nodes}).Lines(), "\n"), "actual") {
		t.Error("an estimated plan rendered actual figures")
	}
}
