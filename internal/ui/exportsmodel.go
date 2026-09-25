package ui

// exportsModel groups the three file transfers the shell can have in
// flight: a streaming table export, a dump or restore, and a
// whole-database DDL export. Each is a one-at-a-time job with its own
// id, cancel handle and (for the first two) progress channel; the root
// Model owns one exportsModel and reduces the jobs' messages. Like the
// other sub-models it is a plain struct, not a tea.Model. See
// wiki/design/tui-shell-architecture.md.
type exportsModel struct {
	// file is the file export in flight, if any. At most one runs at
	// a time; `X` cancels it.
	file exportState

	// backup is the dump or restore in flight, if any — an external tool
	// (pg_dump, mysqldump) or the file engines' own SQL. Like the export
	// only one runs at a time and `X` cancels it.
	backup backupState

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
// driver (or the tunnel) that is about to close. The jobs' own done
// messages clear the running flags when their workers wind down.
func (x *exportsModel) cancelAll() {
	if x.file.running && x.file.cancel != nil {
		x.file.cancel()
	}
	if x.ddl.running && x.ddl.cancel != nil {
		x.ddl.cancel()
	}
	if x.backup.running && x.backup.cancel != nil {
		x.backup.cancel()
	}
	if x.csv.running && x.csv.cancel != nil {
		x.csv.cancel()
	}
}
