package db

import "testing"

func TestFindUnguardedWrites(t *testing.T) {
	cases := []struct {
		name   string
		engine Engine
		sql    string
		want   []UnguardedWrite // nil means "not reported"
	}{
		{"insert never warns", EngineSQLite, "INSERT INTO t VALUES (1)", nil},
		{"create table never warns", EngineSQLite, "CREATE TABLE t (id int)", nil},
		{"select never warns", EngineSQLite, "SELECT * FROM t", nil},
		{
			"update with where is guarded", EngineSQLite,
			"UPDATE t SET x = 1 WHERE id = 1", nil,
		},
		{
			"delete with where is guarded", EngineSQLite,
			"DELETE FROM t WHERE id = 1", nil,
		},
		{
			"delete without where warns", EngineSQLite,
			"DELETE FROM orders",
			[]UnguardedWrite{{Statement: "DELETE FROM orders", Verb: "DELETE", Table: "orders"}},
		},
		{
			"update without where warns", EngineSQLite,
			"UPDATE orders SET x = 1",
			[]UnguardedWrite{{Statement: "UPDATE orders SET x = 1", Verb: "UPDATE", Table: "orders"}},
		},
		{
			"mysql update with limit is guarded", EngineMySQL,
			"UPDATE orders SET x = 1 LIMIT 10", nil,
		},
		{
			"comment mentioning where does not suppress the warning", EngineSQLite,
			"DELETE FROM orders -- where\n",
			[]UnguardedWrite{{Statement: "DELETE FROM orders -- where\n", Verb: "DELETE", Table: "orders"}},
		},
		{
			"string literal mentioning where does not suppress the warning", EngineSQLite,
			"UPDATE orders SET c = 'where'",
			[]UnguardedWrite{{Statement: "UPDATE orders SET c = 'where'", Verb: "UPDATE", Table: "orders"}},
		},
		{
			"where only inside a subquery still warns for the outer statement", EngineSQLite,
			"UPDATE orders SET x = (SELECT max(x) FROM other WHERE y = 1)",
			[]UnguardedWrite{{
				Statement: "UPDATE orders SET x = (SELECT max(x) FROM other WHERE y = 1)",
				Verb:      "UPDATE", Table: "orders",
			}},
		},
		{
			"where outside the subquery still guards", EngineSQLite,
			"DELETE FROM orders WHERE id IN (SELECT id FROM other)", nil,
		},
		{
			"quoted table name is unquoted for display", EngineMySQL,
			"DELETE FROM `orders`",
			[]UnguardedWrite{{Statement: "DELETE FROM `orders`", Verb: "DELETE", Table: "orders"}},
		},
		{
			"schema-qualified table name", EnginePostgres,
			`DELETE FROM public.orders`,
			[]UnguardedWrite{{Statement: `DELETE FROM public.orders`, Verb: "DELETE", Table: "public.orders"}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := FindUnguardedWrites(c.engine, []string{c.sql})
			if len(got) != len(c.want) {
				t.Fatalf("FindUnguardedWrites(%q) = %+v, want %+v", c.sql, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("FindUnguardedWrites(%q)[%d] = %+v, want %+v", c.sql, i, got[i], c.want[i])
				}
			}
		})
	}
}

func TestFindUnguardedWritesMultiStatement(t *testing.T) {
	stmts := []string{
		"SELECT * FROM t",
		"DELETE FROM orders",
		"UPDATE customers SET active = 0 WHERE id = 1",
		"UPDATE promos SET active = 0",
	}
	got := FindUnguardedWrites(EngineSQLite, stmts)
	if len(got) != 2 {
		t.Fatalf("FindUnguardedWrites() = %+v, want 2 entries", got)
	}
	if got[0].Table != "orders" || got[1].Table != "promos" {
		t.Fatalf("FindUnguardedWrites() tables = %q, %q, want orders, promos", got[0].Table, got[1].Table)
	}
}
