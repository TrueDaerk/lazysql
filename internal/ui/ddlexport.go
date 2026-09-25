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
	// copied is set instead of path when the destination was the
	// clipboard: the copy already happened on the worker goroutine (a
	// clipboard write shells out and must not block Update), and what is
	// left is the line copyOut rendered plus, for an OSC 52 copy, the
	// text the program still has to push to its own tty.
	copied *copiedMsg
}

// ---------- the flow ----------

// ddlExportTarget is the namespace `E` exports and the relation listing
// it exports from: whatever the [2] tree's cursor points into while that
// panel has the focus — a category can be expanded without the namespace
// ever being browsed — and the browsed namespace otherwise.
func (m Model) ddlExportTarget() (string, []db.Relation) {
	if m.focus == panelObjects {
		if n := m.selectedNode(); n != nil {
			return n.database, m.tree.relations[n.database]
		}
	}
	return m.database, m.relations
}

// startDatabaseDDLExport is `E` on the Objects panel: pick a destination,
// then export every relation's DDL of the selected database there. The
// destination is a menu modal rather than a second top-level key — see
// wiki/design/ddl-destinations.md.
func (m *Model) startDatabaseDDLExport() tea.Cmd {
	if cmd := m.databaseDDLPrecheck("export database DDL"); cmd != nil {
		return cmd
	}
	database, _ := m.ddlExportTarget()
	m.modal = &menuModal{
		title: "Export " + displayDatabase(database) + " DDL",
		entries: []menuEntry{
			runActionEntry("f", "File — write a .sql file", actExportDatabaseDDLFile),
			runActionEntry("c", "Clipboard — copy the combined DDL", actExportDatabaseDDLClipboard),
			{key: "esc", label: "cancel"},
		},
	}
	return nil
}

// databaseDDLPrecheck is everything both destinations refuse for the same
// reason, so the menu never opens on an export that cannot run and each
// destination still refuses on its own — they are reachable from the `a`
// actions menu without passing through the destination menu at all.
func (m Model) databaseDDLPrecheck(what string) tea.Cmd {
	if m.driver == nil {
		return logCmd("-- %s skipped: not connected", what)
	}
	database, rels := m.ddlExportTarget()
	if len(rels) == 0 {
		return logCmd("-- %s skipped: %s has no relations", what, displayDatabase(database))
	}
	if m.exports.ddl.running {
		return logCmd("-- %s skipped: an export of %s is already running",
			what, displayDatabase(database))
	}
	return nil
}

// promptDatabaseDDLExportPath is the file destination: the `.sql` path
// prompt `E` used to open directly.
func (m *Model) promptDatabaseDDLExportPath() tea.Cmd {
	if cmd := m.databaseDDLPrecheck("export database DDL"); cmd != nil {
		return cmd
	}
	database, _ := m.ddlExportTarget()
	name := defaultDDLExportPath(displayDatabase(database))
	m.modal = newPromptModal(
		"Export "+displayDatabase(database)+" DDL — file path",
		"~/"+name,
		name,
		func(mm *Model, value string) tea.Cmd { return mm.runDatabaseDDLExport(value) },
	)
	return nil
}

// copyDatabaseDDL is the clipboard destination: the same scan producing
// the same text, handed to copyOut instead of os.WriteFile. Output too
// large for the clipboard takes the spill file exactly like every other
// table-scope copy.
func (m *Model) copyDatabaseDDL() tea.Cmd {
	if cmd := m.databaseDDLPrecheck("copy database DDL"); cmd != nil {
		return cmd
	}
	database, rels := m.ddlExportTarget()
	drv := m.driver
	tables := db.RelationNames(rels)

	ctx, cancel := context.WithTimeout(context.Background(), databaseDDLTimeout)
	m.exports.ddl = dbDDLExportState{running: true, id: m.exports.ddl.id + 1, cancel: cancel}
	id := m.exports.ddl.id

	return tea.Batch(
		logCmd("-- copy DDL of %s (%d relations) to the clipboard…",
			displayDatabase(database), len(tables)),
		func() tea.Msg { return runDatabaseDDLCopy(ctx, drv, database, tables, id) },
	)
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
	if cmd := m.databaseDDLPrecheck("export database DDL"); cmd != nil {
		return cmd
	}
	database, rels := m.ddlExportTarget()

	drv := m.driver
	tables := db.RelationNames(rels)

	ctx, cancel := context.WithTimeout(context.Background(), databaseDDLTimeout)
	m.exports.ddl = dbDDLExportState{running: true, id: m.exports.ddl.id + 1, cancel: cancel}
	id := m.exports.ddl.id

	return tea.Batch(
		logCmd("-- export DDL of %s (%d relations) to %s…",
			displayDatabase(database), len(tables), full),
		func() tea.Msg { return runDatabaseDDLScan(ctx, drv, database, tables, full, id) },
	)
}

// runDatabaseDDLScan is the file destination's worker: build the combined
// text, then write it as one file.
func runDatabaseDDLScan(
	ctx context.Context, drv db.Driver, database string, tables []string, path string, id int,
) tea.Msg {
	text, order, acyclic, failed, err := buildDatabaseDDL(ctx, drv, database, tables)
	if err != nil {
		return databaseDDLExportedMsg{id: id, database: database, path: path, err: err}
	}
	werr := os.WriteFile(path, []byte(text), 0o644)
	return databaseDDLExportedMsg{
		id: id, database: database, path: path, tables: len(order),
		acyclic: acyclic, failed: failed, err: werr,
	}
}

// runDatabaseDDLCopy is the clipboard destination's worker. It produces
// byte-for-byte the text runDatabaseDDLScan writes — same ordering, same
// header comments, same separators — and hands it to copyOut, which falls
// back to OSC 52 and then to a spill file like any other copy. The copy
// runs here rather than in Update for the reason every other copy does:
// the clipboard write shells out, and Update may not wait for that.
func runDatabaseDDLCopy(
	ctx context.Context, drv db.Driver, database string, tables []string, id int,
) tea.Msg {
	text, order, acyclic, failed, err := buildDatabaseDDL(ctx, drv, database, tables)
	if err != nil {
		return databaseDDLExportedMsg{id: id, database: database, err: err}
	}
	name := displayDatabase(database)
	out := copyOut(
		fmt.Sprintf("DDL of %s (%d relations)", name, len(order)),
		name+"-ddl.sql",
		text,
	)
	return databaseDDLExportedMsg{
		id: id, database: database, tables: len(order),
		acyclic: acyclic, failed: failed, copied: &out,
	}
}

// buildDatabaseDDL is the scan both destinations share: one
// TableForeignKeys and one TableDDL round trip per relation, ordered by
// dependency, assembled into one document. A relation whose DDL (or
// foreign-key read) fails does not abort the run — it is noted inline and
// in the returned tally, and the rest still lands in the text.
//
// ctx is cancelled from resetBrowse when the connection it reads through
// is about to close; each loop checks it before its next round trip, so a
// cancelled scan stops promptly instead of running every remaining
// relation through a closed driver. A cancellation returns no text at
// all, which is what keeps a half-scanned database off the disk and off
// the clipboard alike.
func buildDatabaseDDL(
	ctx context.Context, drv db.Driver, database string, tables []string,
) (text string, order []string, acyclic bool, failed []string, err error) {
	deps := make(map[string][]string, len(tables))
	for _, t := range tables {
		if ctx.Err() != nil {
			return "", nil, false, nil, ctx.Err()
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

	order, acyclic = db.DDLOrder(tables, deps)

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
			return "", nil, false, nil, ctx.Err()
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

	return b.String(), order, acyclic, failed, nil
}

// ---------- model wiring ----------

// finishDatabaseDDLExport clears the in-flight state and renders the
// outcome: a write failure (or a cancellation) is fatal to the whole
// run, but a per-table read failure is only a footnote next to the
// count that did succeed.
func (m *Model) finishDatabaseDDLExport(msg databaseDDLExportedMsg) tea.Cmd {
	m.exports.ddl = dbDDLExportState{}
	if errors.Is(msg.err, context.Canceled) {
		return logCmd("-- export DDL of %s cancelled", displayDatabase(msg.database))
	}
	if msg.err != nil {
		return logCmd("-- export DDL of %s FAILED: %v", displayDatabase(msg.database), msg.err)
	}
	// A clipboard export has already been rendered by copyOut; only the
	// scan's own footnotes still have to be appended. Handing the
	// copiedMsg back to the update loop is also what gets an OSC 52 copy
	// written out — only the program may write to its tty.
	if msg.copied != nil {
		out := *msg.copied
		out.line += ddlExportFootnotes(msg)
		return func() tea.Msg { return out }
	}
	line := fmt.Sprintf("-- export DDL of %s wrote %d relation(s) to %s",
		displayDatabase(msg.database), msg.tables, msg.path)
	line += ddlExportFootnotes(msg)
	return logCmd("%s", line)
}

// ddlExportFootnotes is what both destinations append to their outcome
// line: the ordering fallback and the relations that had no DDL. Written
// once, so a file export and a clipboard copy cannot describe the same
// scan differently.
func ddlExportFootnotes(msg databaseDDLExportedMsg) string {
	out := ""
	if !msg.acyclic {
		out += " (alphabetical order — foreign keys form a cycle)"
	}
	if len(msg.failed) > 0 {
		out += fmt.Sprintf("  -- %d relation(s) had no DDL: %s",
			len(msg.failed), strings.Join(msg.failed, "; "))
	}
	return out
}
