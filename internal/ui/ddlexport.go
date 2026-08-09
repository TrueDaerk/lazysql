package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/db"
)

// databaseDDLTimeout bounds the whole scan: one TableForeignKeys and one
// TableDDL round trip per relation. It is generous because a namespace of
// dozens of tables means dozens of round trips.
const databaseDDLTimeout = 2 * time.Minute

// dbDDLExportState is the whole-database DDL export in flight, if any —
// export.go's exportState with the row-streaming fields dropped. id
// distinguishes runs so a stale reply cannot clobber one started since;
// cancel is called from resetBrowse when the connection it reads through
// is about to close.
type dbDDLExportState struct {
	running bool
	id      int
	cancel  context.CancelFunc
}

// ---------- messages ----------

// databaseDDLExportedMsg is the outcome of a whole-database DDL export.
// id distinguishes the run a reply belongs to, the same way exportDoneMsg
// does for the streaming table export: a disconnect between the prompt
// and the reply must not let a stale result clobber a run started since.
type databaseDDLExportedMsg struct {
	id       int
	database string
	path     string
	tables   int
	acyclic  bool
	failed   []string
	err      error
}

// ---------- the flow ----------

// startDatabaseDDLExport is `E` on the Tables panel: prompt for a `.sql`
// path, then write every relation's DDL of the browsed database into it.
func (m *Model) startDatabaseDDLExport() tea.Cmd {
	if m.driver == nil {
		return logCmd("-- export database DDL skipped: not connected")
	}
	if len(m.relations) == 0 {
		return logCmd("-- export database DDL skipped: %s has no relations",
			displayDatabase(m.database))
	}
	if m.dbDDLExport.running {
		return logCmd("-- export database DDL skipped: an export of %s is already running",
			displayDatabase(m.database))
	}
	name := defaultDDLExportPath(displayDatabase(m.database))
	m.modal = newPromptModal(
		"Export "+displayDatabase(m.database)+" DDL — file path",
		"~/"+name,
		name,
		func(mm *Model, value string) tea.Cmd { return mm.runDatabaseDDLExport(value) },
	)
	return nil
}

// runDatabaseDDLExport validates the path and starts the scan. Everything
// that can fail synchronously does so here, so the worker only ever
// reports problems that needed the database.
func (m *Model) runDatabaseDDLExport(path string) tea.Cmd {
	if path == "" {
		return logCmd("-- export cancelled: no path given")
	}
	full, err := expandPath(path)
	if err != nil {
		return logCmd("-- export FAILED: %v", err)
	}
	if !strings.EqualFold(filepath.Ext(full), ".sql") {
		return logCmd("-- export %s FAILED: database DDL export needs a .sql path", full)
	}
	if m.driver == nil || len(m.relations) == 0 {
		return logCmd("-- export skipped: nothing to export")
	}
	if m.dbDDLExport.running {
		return logCmd("-- export database DDL skipped: an export of %s is already running",
			displayDatabase(m.database))
	}

	drv := m.driver
	database := m.database
	tables := db.RelationNames(m.relations)

	ctx, cancel := context.WithTimeout(context.Background(), databaseDDLTimeout)
	m.dbDDLExport = dbDDLExportState{running: true, id: m.dbDDLExport.id + 1, cancel: cancel}
	id := m.dbDDLExport.id

	return tea.Batch(
		logCmd("-- export DDL of %s (%d relations) to %s…",
			displayDatabase(database), len(tables), full),
		func() tea.Msg { return runDatabaseDDLScan(ctx, drv, database, tables, full, id) },
	)
}

// runDatabaseDDLScan is the worker: one TableForeignKeys and one TableDDL
// round trip per relation, ordered by dependency, written as one file. A
// relation whose DDL (or foreign-key read) fails does not abort the run —
// it is noted inline and in the final tally, and the rest still exports.
// ctx is cancelled from resetBrowse when the connection it reads through
// is about to close; each loop checks it before its next round trip, so
// a cancelled scan stops promptly instead of running every remaining
// relation through a closed driver.
func runDatabaseDDLScan(
	ctx context.Context, drv db.Driver, database string, tables []string, path string, id int,
) tea.Msg {
	cancelled := func() tea.Msg {
		return databaseDDLExportedMsg{id: id, database: database, path: path, err: ctx.Err()}
	}

	deps := make(map[string][]string, len(tables))
	var failed []string
	for _, t := range tables {
		if ctx.Err() != nil {
			return cancelled()
		}
		fks, err := drv.TableForeignKeys(ctx, database, t)
		if err != nil {
			failed = append(failed, fmt.Sprintf("%s: foreign keys: %v", t, err))
			continue
		}
		refs := make([]string, 0, len(fks))
		for _, fk := range fks {
			ns, ref := db.SplitQualified(fk.RefTable)
			if ns == "" || ns == database {
				refs = append(refs, ref)
			}
		}
		deps[t] = refs
	}

	order, acyclic := db.DDLOrder(tables, deps)

	var b strings.Builder
	fmt.Fprintf(&b, "-- lazysql DDL export of %s\n", displayDatabase(database))
	if acyclic {
		b.WriteString("-- ordering: foreign-key dependency order\n")
	} else {
		b.WriteString("-- ordering: alphabetical — the foreign keys form a cycle\n")
	}
	b.WriteString("\n")

	for _, t := range order {
		if ctx.Err() != nil {
			return cancelled()
		}
		fmt.Fprintf(&b, "-- table: %s\n", t)
		ddl, err := drv.TableDDL(ctx, database, t)
		if err != nil {
			failed = append(failed, fmt.Sprintf("%s: DDL: %v", t, err))
			fmt.Fprintf(&b, "-- DDL unavailable: %v\n\n", err)
			continue
		}
		stmt := strings.TrimRight(ddl, " \t\n")
		if stmt != "" && !strings.HasSuffix(stmt, ";") {
			stmt += ";"
		}
		b.WriteString(stmt)
		b.WriteString("\n\n")
	}

	werr := os.WriteFile(path, []byte(b.String()), 0o644)
	return databaseDDLExportedMsg{
		id: id, database: database, path: path, tables: len(order),
		acyclic: acyclic, failed: failed, err: werr,
	}
}

// ---------- model wiring ----------

// finishDatabaseDDLExport clears the in-flight state and renders the
// outcome: a write failure (or a cancellation) is fatal to the whole
// run, but a per-table read failure is only a footnote next to the
// count that did succeed.
func (m *Model) finishDatabaseDDLExport(msg databaseDDLExportedMsg) tea.Cmd {
	m.dbDDLExport = dbDDLExportState{}
	if errors.Is(msg.err, context.Canceled) {
		return logCmd("-- export DDL of %s cancelled", displayDatabase(msg.database))
	}
	if msg.err != nil {
		return logCmd("-- export DDL of %s FAILED: %v", displayDatabase(msg.database), msg.err)
	}
	line := fmt.Sprintf("-- export DDL of %s wrote %d relation(s) to %s",
		displayDatabase(msg.database), msg.tables, msg.path)
	if !msg.acyclic {
		line += " (alphabetical order — foreign keys form a cycle)"
	}
	if len(msg.failed) > 0 {
		line += fmt.Sprintf("  -- %d relation(s) had no DDL: %s",
			len(msg.failed), strings.Join(msg.failed, "; "))
	}
	return logCmd("%s", line)
}
