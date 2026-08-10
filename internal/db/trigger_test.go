package db

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// SQLite stores triggers in sqlite_master next to its tables, so both the
// listing and the definition come back verbatim.
func TestSQLiteTriggers(t *testing.T) {
	ctx := context.Background()
	drv := openTest(t, EngineSQLite, ":memory:")
	seed(t, drv)

	if _, err := drv.Exec(ctx, `CREATE TABLE audit (id INTEGER PRIMARY KEY, note TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := drv.Exec(ctx, `CREATE TRIGGER users_ai AFTER INSERT ON users
		BEGIN INSERT INTO audit (note) VALUES (new.name); END`); err != nil {
		t.Fatal(err)
	}
	// A second trigger proves the listing is ordered by name rather than
	// by creation order.
	if _, err := drv.Exec(ctx, `CREATE TRIGGER audit_bd BEFORE DELETE ON audit
		BEGIN SELECT 1; END`); err != nil {
		t.Fatal(err)
	}

	trs, err := drv.ListTriggers(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := TriggerNames(trs); len(got) != 2 || got[0] != "audit_bd" || got[1] != "users_ai" {
		t.Fatalf("ListTriggers = %v, want [audit_bd users_ai]", got)
	}
	if trs[1].Table != "users" {
		t.Errorf("users_ai fires on %q, want users", trs[1].Table)
	}

	ddl, err := drv.TriggerDDL(ctx, "", "users_ai")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ddl, "CREATE TRIGGER") || !strings.Contains(ddl, "users_ai") {
		t.Fatalf("TriggerDDL = %q", ddl)
	}
	if _, err := drv.TriggerDDL(ctx, "", "no_such_trigger"); err == nil {
		t.Fatal("TriggerDDL of a missing trigger returned no error")
	}
}

// A namespace without triggers lists none — which is not the same answer
// as an engine that has no triggers at all.
func TestSQLiteTriggersEmpty(t *testing.T) {
	ctx := context.Background()
	drv := openTest(t, EngineSQLite, ":memory:")
	seed(t, drv)

	trs, err := drv.ListTriggers(ctx, "")
	if err != nil {
		t.Fatalf("ListTriggers on a trigger-free schema: %v", err)
	}
	if len(trs) != 0 {
		t.Fatalf("ListTriggers = %v, want none", trs)
	}
}

// DuckDB has no trigger concept, and says so with the sentinel rather
// than with an empty list the UI would render as "none defined".
func TestDuckDBTriggersUnsupported(t *testing.T) {
	ctx := context.Background()
	drv := openTest(t, EngineDuckDB, "")
	seed(t, drv)

	if _, err := drv.ListTriggers(ctx, ""); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("ListTriggers = %v, want ErrUnsupported", err)
	}
	if _, err := drv.TriggerDDL(ctx, "", "anything"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("TriggerDDL = %v, want ErrUnsupported", err)
	}
}

// The trigger listing runs through the same Logger every other statement
// does, so the command log shows it like any other introspection query.
func TestTriggerListingIsLogged(t *testing.T) {
	ctx := context.Background()
	drv := openTest(t, EngineSQLite, ":memory:")
	seed(t, drv)

	before := len(drv.Logger().Entries())
	if _, err := drv.ListTriggers(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if got := len(drv.Logger().Entries()); got != before+1 {
		t.Fatalf("logger entries = %d, want %d", got, before+1)
	}
}
