package db

import "testing"

func TestSingleTableSelect(t *testing.T) {
	cases := []struct {
		name      string
		engine    Engine
		sql       string
		wantTable string
		wantOK    bool
	}{
		{"star", EngineSQLite, "SELECT * FROM orders", "orders", true},
		{
			"bare columns with clauses", EngineSQLite,
			"SELECT id, status FROM orders WHERE id > 1 ORDER BY id LIMIT 10",
			"orders", true,
		},
		{"table alias", EngineSQLite, "SELECT o.id, o.status FROM orders o", "orders", true},
		{"AS alias", EngineSQLite, "SELECT id FROM orders AS o WHERE id > 1", "orders", true},
		{"trailing semicolon", EngineSQLite, "SELECT id FROM orders;", "orders", true},
		{"schema-qualified table", EnginePostgres, `SELECT id FROM public.orders`, "orders", true},
		{
			"mysql backtick identifiers", EngineMySQL,
			"SELECT `id`, `status` FROM `orders`",
			"orders", true,
		},
		{
			"quoted column named like a keyword", EngineSQLite,
			`SELECT "join" FROM orders`,
			"orders", true,
		},
		{"qualified star", EngineSQLite, "SELECT o.* FROM orders o", "orders", true},

		{"join", EngineSQLite, "SELECT a.id FROM a JOIN b ON a.id = b.id", "", false},
		{"left join", EngineSQLite, "SELECT a.id FROM a LEFT JOIN b ON a.id = b.id", "", false},
		{"comma join", EngineSQLite, "SELECT id FROM a, b", "", false},
		{"aggregate function", EngineSQLite, "SELECT COUNT(*) FROM orders", "", false},
		{"expression", EngineSQLite, "SELECT id + 1 FROM orders", "", false},
		{"column alias", EngineSQLite, "SELECT id AS oid FROM orders", "", false},
		{"bare column alias", EngineSQLite, "SELECT id oid FROM orders", "", false},
		{"subquery in FROM", EngineSQLite, "SELECT id FROM (SELECT * FROM orders) t", "", false},
		{"union", EngineSQLite, "SELECT id FROM orders UNION SELECT id FROM other", "", false},
		{"group by", EngineSQLite, "SELECT status FROM orders GROUP BY status", "", false},
		{"distinct", EngineSQLite, "SELECT DISTINCT status FROM orders", "", false},
		{"cte", EngineSQLite, "WITH x AS (SELECT 1) SELECT * FROM x", "", false},
		{"not a select", EngineSQLite, "UPDATE orders SET status = 'x'", "", false},
		{"empty", EngineSQLite, "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			table, ok := SingleTableSelect(c.engine, c.sql)
			if ok != c.wantOK || table != c.wantTable {
				t.Errorf("SingleTableSelect(%q) = (%q, %v), want (%q, %v)",
					c.sql, table, ok, c.wantTable, c.wantOK)
			}
		})
	}
}
