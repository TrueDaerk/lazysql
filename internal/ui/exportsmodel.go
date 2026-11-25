package ui

// exportsModel groups the file transfers that run through one
// connection's driver: a streaming table export, a whole-database DDL
// export and a CSV import. Each is a one-at-a-time job with its own id,
// cancel handle and (for the streaming ones) progress channel. Every
// open connection owns one exportsModel (see sessions.go), so a job
// keeps running — and reports into its own session — while another
// connection is on screen. A dump or restore is not here: it targets a
// profile rather than a session and lives on the root Model. Like the
// other sub-models it is a plain struct, not a tea.Model. See
// wiki/design/tui-shell-architecture.md.
type exportsModel struct {
	// file is the file export in flight, if any. At most one runs at
	// a time; `X` cancels it.
	file exportState

	// ddl guards a whole-database DDL export the same way: only one
	// runs at a time. It has no cancel key of its own — one round trip
	// per relation finishes long before a data export would — but
	// cancelAll still stops it when the connection it reads through is
	// closing.
	ddl dbDDLExportState

	// csv is the CSV import in flight, if any. It writes rather than
	// reads, but it is the same shape of job — a worker, progress in the
	// log, `X` to cancel — and closing the connection must stop it too.
	csv importState
}

// cancelAll stops every job that is running: they all read through the
// driver that is about to close. The jobs' own done messages clear the
// running flags when their workers wind down.
func (x *exportsModel) cancelAll() {
	if x.file.running && x.file.cancel != nil {
		x.file.cancel()
	}
	if x.ddl.running && x.ddl.cancel != nil {
		x.ddl.cancel()
	}
	if x.csv.running && x.csv.cancel != nil {
		x.csv.cancel()
	}
}

// running reports whether any job is still in flight — or still winding
// down after a cancel.
func (x exportsModel) running() bool {
	return x.file.running || x.ddl.running || x.csv.running
}
