package db

import (
	"context"
	"strings"
	"testing"
)

// ---------- SQLite / DuckDB, against a live in-process engine ----------

func TestExplainSQLite(t *testing.T) {
	drv := openTest(t, EngineSQLite, "")
	seed(t, drv)
	plan, err := drv.Explain(context.Background(), "SELECT * FROM users WHERE name = 'alice'")
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if plan.Format != PlanTree {
		t.Fatalf("Format = %v, want PlanTree", plan.Format)
	}
	if !strings.HasPrefix(plan.SQL, "EXPLAIN QUERY PLAN ") {
		t.Errorf("SQL = %q, want an EXPLAIN QUERY PLAN statement", plan.SQL)
	}
	if len(plan.Nodes) == 0 {
		t.Fatal("no plan nodes")
	}
	text := plan.RenderText()
	if !strings.Contains(strings.ToLower(text), "users") {
		t.Errorf("plan does not mention the table:\n%s", text)
	}
}

// TestExplainSQLiteDoesNotExecute is the ANALYZE guard: explaining a
// DELETE must leave every row where it was.
func TestExplainSQLiteDoesNotExecute(t *testing.T) {
	drv := openTest(t, EngineSQLite, "")
	seed(t, drv)
	ctx := context.Background()
	if _, err := drv.Explain(ctx, "DELETE FROM users"); err != nil {
		t.Fatalf("Explain DELETE: %v", err)
	}
	n, err := drv.CountRows(ctx, "", "users", nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("rows after explaining a DELETE = %d, want 3", n)
	}
}

func TestExplainDuckDB(t *testing.T) {
	drv := openTest(t, EngineDuckDB, "")
	seed(t, drv)
	plan, err := drv.Explain(context.Background(), "SELECT * FROM users WHERE name = 'alice';")
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if plan.Format != PlanText {
		t.Fatalf("Format = %v, want PlanText", plan.Format)
	}
	if plan.SQL != "EXPLAIN SELECT * FROM users WHERE name = 'alice'" {
		t.Errorf("SQL = %q — the trailing semicolon should be gone", plan.SQL)
	}
	if len(plan.Lines()) == 0 {
		t.Fatal("no plan lines")
	}
	if !strings.Contains(strings.ToLower(plan.RenderText()), "users") {
		t.Errorf("plan does not mention the table:\n%s", plan.RenderText())
	}
}

func TestExplainInLog(t *testing.T) {
	drv := openTest(t, EngineSQLite, "")
	seed(t, drv)
	if _, err := drv.Explain(context.Background(), "SELECT 1"); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range drv.Logger().Entries() {
		if strings.HasPrefix(e.SQL, "EXPLAIN QUERY PLAN ") {
			found = true
		}
	}
	if !found {
		t.Fatal("the EXPLAIN statement did not reach the command log")
	}
}

func TestExplainEmptyStatement(t *testing.T) {
	drv := openTest(t, EngineSQLite, "")
	if _, err := drv.Explain(context.Background(), "  ;  "); err == nil {
		t.Fatal("explaining an empty statement should fail")
	}
}

// ---------- PostgreSQL, against captured server output ----------

// pgPlanFixture is the answer of
// `EXPLAIN (FORMAT JSON) SELECT * FROM orders o JOIN users u ON u.id = o.user_id WHERE u.name = 'alice' ORDER BY o.id`
// captured from PostgreSQL 16.
const pgPlanFixture = `[
  {
    "Plan": {
      "Node Type": "Sort",
      "Parallel Aware": false,
      "Startup Cost": 24.15,
      "Total Cost": 24.16,
      "Plan Rows": 2,
      "Plan Width": 76,
      "Sort Key": ["o.id"],
      "Plans": [
        {
          "Node Type": "Hash Join",
          "Parent Relationship": "Outer",
          "Join Type": "Inner",
          "Startup Cost": 12.30,
          "Total Cost": 24.14,
          "Plan Rows": 2,
          "Plan Width": 76,
          "Hash Cond": "(o.user_id = u.id)",
          "Plans": [
            {
              "Node Type": "Seq Scan",
              "Parent Relationship": "Outer",
              "Relation Name": "orders",
              "Alias": "o",
              "Startup Cost": 0.00,
              "Total Cost": 10.70,
              "Plan Rows": 70,
              "Plan Width": 8
            },
            {
              "Node Type": "Index Scan",
              "Parent Relationship": "Inner",
              "Relation Name": "users",
              "Alias": "u",
              "Index Name": "idx_users_name",
              "Startup Cost": 0.00,
              "Total Cost": 12.28,
              "Plan Rows": 2,
              "Plan Width": 68,
              "Index Cond": "(name = 'alice'::text)",
              "Filter": "(email IS NOT NULL)"
            }
          ]
        }
      ]
    }
  }
]`

func TestParsePostgresPlan(t *testing.T) {
	nodes, err := ParsePostgresPlan(pgPlanFixture)
	if err != nil {
		t.Fatalf("ParsePostgresPlan: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("got %d roots, want 1", len(nodes))
	}
	root := nodes[0]
	if root.Label != "Sort" {
		t.Errorf("root label = %q", root.Label)
	}
	if root.Detail != "(cost=24.15..24.16 rows=2 width=76)" {
		t.Errorf("root detail = %q", root.Detail)
	}
	if len(root.Notes) != 1 || root.Notes[0] != "Sort Key: o.id" {
		t.Errorf("root notes = %v", root.Notes)
	}
	join := root.Children[0]
	if join.Label != "Hash Join (Inner)" {
		t.Errorf("join label = %q", join.Label)
	}
	seq := join.Children[0]
	if seq.Label != "Seq Scan on orders o" {
		t.Errorf("seq label = %q", seq.Label)
	}
	if seq.Detail != "(cost=0.00..10.70 rows=70 width=8)" {
		t.Errorf("seq detail = %q", seq.Detail)
	}
	idx := join.Children[1]
	if idx.Label != "Index Scan using idx_users_name on users u" {
		t.Errorf("index label = %q", idx.Label)
	}
	wantNotes := []string{"Index Cond: (name = 'alice'::text)", "Filter: (email IS NOT NULL)"}
	if len(idx.Notes) != len(wantNotes) {
		t.Fatalf("index notes = %v", idx.Notes)
	}
	for i, w := range wantNotes {
		if idx.Notes[i] != w {
			t.Errorf("index note %d = %q, want %q", i, idx.Notes[i], w)
		}
	}
}

func TestPostgresPlanLines(t *testing.T) {
	nodes, err := ParsePostgresPlan(pgPlanFixture)
	if err != nil {
		t.Fatal(err)
	}
	p := &Plan{Engine: EnginePostgres, Format: PlanTree, Nodes: nodes}
	lines := p.Lines()
	want := []string{
		"Sort  (cost=24.15..24.16 rows=2 width=76)",
		"  Sort Key: o.id",
		"  -> Hash Join (Inner)  (cost=12.30..24.14 rows=2 width=76)",
		"     Hash Cond: (o.user_id = u.id)",
		"    -> Seq Scan on orders o  (cost=0.00..10.70 rows=70 width=8)",
		"    -> Index Scan using idx_users_name on users u  (cost=0.00..12.28 rows=2 width=68)",
		"       Index Cond: (name = 'alice'::text)",
		"       Filter: (email IS NOT NULL)",
	}
	if len(lines) != len(want) {
		t.Fatalf("got %d lines, want %d:\n%s", len(lines), len(want), strings.Join(lines, "\n"))
	}
	for i, w := range want {
		if lines[i] != w {
			t.Errorf("line %d =\n%q\nwant\n%q", i, lines[i], w)
		}
	}
}

func TestParsePostgresPlanBroken(t *testing.T) {
	if _, err := ParsePostgresPlan("not json"); err == nil {
		t.Error("broken JSON should fail")
	}
	if _, err := ParsePostgresPlan("[]"); err == nil {
		t.Error("an empty plan array should fail")
	}
}

// ---------- MySQL, against captured server output ----------

// mysqlPlanFixture is the answer of
// `EXPLAIN FORMAT=JSON SELECT * FROM users WHERE name = 'alice'`
// captured from MySQL 8.0.
const mysqlPlanFixture = `{
  "query_block": {
    "select_id": 1,
    "cost_info": {"query_cost": "0.45"},
    "table": {
      "table_name": "users",
      "access_type": "ref",
      "possible_keys": ["idx_users_name"],
      "key": "idx_users_name",
      "rows_examined_per_scan": 1,
      "filtered": "100.00",
      "cost_info": {"read_cost": "0.25", "eval_cost": "0.10"},
      "used_columns": ["id", "name", "email"]
    }
  }
}`

func TestParseMySQLPlan(t *testing.T) {
	nodes, err := ParseMySQLPlan(mysqlPlanFixture)
	if err != nil {
		t.Fatalf("ParseMySQLPlan: %v", err)
	}
	p := &Plan{Engine: EngineMySQL, Format: PlanTree, Nodes: nodes}
	text := p.RenderText()
	for _, want := range []string{
		"query_block",
		"select_id: 1",
		"query_cost: 0.45",
		"table_name: users",
		"access_type: ref",
		"key: idx_users_name",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("plan is missing %q:\n%s", want, text)
		}
	}
	// Document order is the server's, not the map's: the table's name
	// must still come before its access type.
	if i, j := strings.Index(text, "table_name"), strings.Index(text, "access_type"); i > j {
		t.Errorf("keys reordered:\n%s", text)
	}
}

func TestParseMySQLPlanBroken(t *testing.T) {
	if _, err := ParseMySQLPlan("{"); err == nil {
		t.Error("broken JSON should fail")
	}
}

// ---------- shared rendering ----------

func TestBuildSQLitePlanTree(t *testing.T) {
	nodes := BuildSQLitePlan([]sqlitePlanRow{
		{ID: 2, Parent: 0, Detail: "SCAN orders"},
		{ID: 3, Parent: 2, Detail: "USE TEMP B-TREE FOR ORDER BY"},
		{ID: 4, Parent: 0, Detail: "SEARCH users USING INDEX idx_users_name"},
	})
	if len(nodes) != 2 {
		t.Fatalf("got %d roots, want 2", len(nodes))
	}
	if len(nodes[0].Children) != 1 {
		t.Fatalf("first root has %d children, want 1", len(nodes[0].Children))
	}
	want := []string{
		"SCAN orders",
		"  -> USE TEMP B-TREE FOR ORDER BY",
		"SEARCH users USING INDEX idx_users_name",
	}
	got := (&Plan{Format: PlanTree, Nodes: nodes}).Lines()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("lines =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestPlanGridLines(t *testing.T) {
	p := &Plan{
		Format: PlanGrid,
		Note:   "-- FORMAT=JSON unavailable: tabular EXPLAIN",
		Grid: &ResultSet{
			Columns: []Column{{Name: "id"}, {Name: "table"}, {Name: "type"}},
			Rows:    [][]any{{int64(1), "users", "ALL"}, {int64(1), "orders_long", nil}},
		},
	}
	lines := p.Lines()
	want := []string{
		"-- FORMAT=JSON unavailable: tabular EXPLAIN",
		"",
		"id  table        type",
		"--  -----------  ----",
		"1   users        ALL",
		"1   orders_long  NULL",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("lines =\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
}
