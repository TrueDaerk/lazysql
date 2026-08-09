package db

import "sort"

// DDLOrder orders a namespace's relations for a whole-database DDL export
// so a table referenced by another table's foreign keys is written before
// the table that references it. deps maps each relation to the relations
// its foreign keys point at; an entry naming a relation outside `tables`
// or the relation itself is ignored — a relation can always create itself
// regardless of order, and an export only ever covers what it was asked
// to export.
//
// The dependency graph is not guaranteed acyclic: two tables can reference
// each other. When it has a cycle, order is instead every relation sorted
// alphabetically and acyclic is false, so the caller can note the fallback
// in the file it writes.
func DDLOrder(tables []string, deps map[string][]string) (order []string, acyclic bool) {
	set := make(map[string]bool, len(tables))
	for _, t := range tables {
		set[t] = true
	}

	remaining := make(map[string]map[string]bool, len(tables))
	for _, t := range tables {
		rem := make(map[string]bool)
		for _, d := range deps[t] {
			if d != t && set[d] {
				rem[d] = true
			}
		}
		remaining[t] = rem
	}

	emitted := make(map[string]bool, len(tables))
	order = make([]string, 0, len(tables))
	for len(order) < len(tables) {
		var ready []string
		for _, t := range tables {
			if !emitted[t] && len(remaining[t]) == 0 {
				ready = append(ready, t)
			}
		}
		if len(ready) == 0 {
			// A cycle: no relation is left whose dependencies are all
			// already emitted. Alphabetical is the only order left that
			// does not depend on the graph.
			return alphabeticalOrder(tables), false
		}
		sort.Strings(ready)
		for _, t := range ready {
			emitted[t] = true
			order = append(order, t)
		}
		for _, t := range tables {
			if emitted[t] {
				continue
			}
			for d := range remaining[t] {
				if emitted[d] {
					delete(remaining[t], d)
				}
			}
		}
	}
	return order, true
}

func alphabeticalOrder(tables []string) []string {
	out := append([]string(nil), tables...)
	sort.Strings(out)
	return out
}
