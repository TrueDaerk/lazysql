package ui

import "testing"

// This test exercises the exports sub-model on its own: no root Model, no
// driver, no rendered frame. Before the export state was extracted from
// Model (issue #229) the cancel-on-disconnect rule could only be reached
// through resetBrowse on a full shell.

func TestExportsCancelAllStopsOnlyRunningJobs(t *testing.T) {
	var x exportsModel
	var fileCancelled, backupCancelled, ddlCancelled int

	// Running with a cancel handle: stopped.
	x.file = exportState{running: true, id: 3, table: "t", cancel: func() { fileCancelled++ }}
	x.backup = backupState{running: true, id: 2, cancel: func() { backupCancelled++ }}
	// Not running any more, handle still set (its done message has not
	// been reduced yet): left alone.
	x.ddl = dbDDLExportState{running: false, id: 1, cancel: func() { ddlCancelled++ }}

	x.cancelAll()
	if fileCancelled != 1 || backupCancelled != 1 || ddlCancelled != 0 {
		t.Errorf("cancelAll cancelled file=%d backup=%d ddl=%d, want 1/1/0", fileCancelled, backupCancelled, ddlCancelled)
	}
	// cancelAll is not the one that clears the running flags — the
	// jobs' own done messages are — so the ids stay for those to match.
	if !x.file.running || x.file.id != 3 || !x.backup.running || x.backup.id != 2 {
		t.Errorf("cancelAll changed the job state: file=%+v backup=%+v", x.file, x.backup)
	}

	// A running job without a cancel handle (a fixture built by hand)
	// must not panic.
	x.ddl = dbDDLExportState{running: true, id: 4}
	x.cancelAll()
	if fileCancelled != 2 {
		t.Errorf("second cancelAll cancelled the file export %d times in total, want 2", fileCancelled)
	}

	// Nothing running at all is a no-op.
	var empty exportsModel
	empty.cancelAll()
}
