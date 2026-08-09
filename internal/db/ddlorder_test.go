package db

import (
	"slices"
	"testing"
)

// A chain of foreign keys orders referenced tables first, regardless of
// the input order.
func TestDDLOrderChain(t *testing.T) {
	tables := []string{"orders", "line_items", "people"}
	deps := map[string][]string{
		"orders":     {"people"},
		"line_items": {"orders"},
		"people":     nil,
	}
	order, acyclic := DDLOrder(tables, deps)
	if !acyclic {
		t.Fatal("a chain has no cycle")
	}
	if !slices.Equal(order, []string{"people", "orders", "line_items"}) {
		t.Errorf("order = %v", order)
	}
}

// Independent tables with no foreign keys between them fall back to
// alphabetical order, since nothing else distinguishes them.
func TestDDLOrderNoDependencies(t *testing.T) {
	tables := []string{"zebras", "apples", "mangoes"}
	order, acyclic := DDLOrder(tables, nil)
	if !acyclic {
		t.Fatal("no foreign keys at all is trivially acyclic")
	}
	if !slices.Equal(order, []string{"apples", "mangoes", "zebras"}) {
		t.Errorf("order = %v", order)
	}
}

// Two tables referencing each other cannot be ordered; the whole export
// falls back to alphabetical and says so.
func TestDDLOrderCycleFallsBackToAlphabetical(t *testing.T) {
	tables := []string{"b_table", "a_table"}
	deps := map[string][]string{
		"a_table": {"b_table"},
		"b_table": {"a_table"},
	}
	order, acyclic := DDLOrder(tables, deps)
	if acyclic {
		t.Fatal("a mutual reference is a cycle")
	}
	if !slices.Equal(order, []string{"a_table", "b_table"}) {
		t.Errorf("order = %v", order)
	}
}

// A self-referencing foreign key (a tree table pointing at its own
// primary key) is not a cycle: the table can always create itself.
func TestDDLOrderSelfReferenceIsNotACycle(t *testing.T) {
	tables := []string{"categories"}
	deps := map[string][]string{"categories": {"categories"}}
	order, acyclic := DDLOrder(tables, deps)
	if !acyclic {
		t.Fatal("a self-reference should not trigger the cycle fallback")
	}
	if !slices.Equal(order, []string{"categories"}) {
		t.Errorf("order = %v", order)
	}
}

// A dependency naming a table outside the export set is ignored — an
// export only ever covers what it was asked to export.
func TestDDLOrderIgnoresDependenciesOutsideTheSet(t *testing.T) {
	tables := []string{"orders"}
	deps := map[string][]string{"orders": {"people"}}
	order, acyclic := DDLOrder(tables, deps)
	if !acyclic {
		t.Fatal("a dependency outside the set should not block ordering")
	}
	if !slices.Equal(order, []string{"orders"}) {
		t.Errorf("order = %v", order)
	}
}
