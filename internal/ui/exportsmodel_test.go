package ui

import "testing"

// This test exercises the exports sub-model on its own: no root Model, no
// driver, no rendered frame.

func TestExportsCancelAllStopsOnlyRunningJobs(t *testing.T) {
	var x exportsModel
	var fileCancelled, importCancelled, ddlCancelled int

	// Running with a cancel handle: stopped.
	x.file = exportState{running: true, id: 3, table: "t", cancel: func() { fileCancelled++ }}
	x.csv = importState{running: true, id: 2, cancel: func() { importCancelled++ }}
	// Not running any more, handle still set (its done message has not
	// been reduced yet): left alone.
	x.ddl = dbDDLExportState{running: false, id: 1, cancel: func() { ddlCancelled++ }}

	x.cancelAll()
	if fileCancelled != 1 || importCancelled != 1 || ddlCancelled != 0 {
		t.Errorf("cancelAll cancelled file=%d import=%d ddl=%d, want 1/1/0", fileCancelled, importCancelled, ddlCancelled)
	}
	// cancelAll is not the one that clears the running flags — the
	// jobs' own done messages are — so the ids stay for those to match.
	if !x.file.running || x.file.id != 3 || !x.csv.running || x.csv.id != 2 {
		t.Errorf("cancelAll changed the job state: file=%+v import=%+v", x.file, x.csv)
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
