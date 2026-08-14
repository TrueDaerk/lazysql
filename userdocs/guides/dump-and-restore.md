# Dump and restore

`B` on panel `[1] Connections` or `[2] Objects` opens a two-entry menu: dump
the database to a file, or restore such a file back into it.

lazysql drives **the engine's own tool** rather than serializing the database
itself, so what comes out is actually restorable by anything else that speaks
that format.

| Engine | Dump | Restore |
|---|---|---|
| PostgreSQL | `pg_dump` | `psql` |
| MySQL | `mysqldump` | `mysql` |
| MariaDB | `mariadb-dump` (or `mysqldump`) | `mariadb` (or `mysql`) |
| SQLite | `VACUUM INTO` | copy the file back |
| DuckDB | `EXPORT DATABASE` (a directory) | `IMPORT DATABASE` |

A missing binary is reported **by name** before any prompt appears, so a
typo in `PATH` does not turn into a half-run job.

## The form

Both directions open a form that shows the **exact command about to run**, with
an editable arguments line and a path field. Host, port, user and database are
prefilled from the connection.

Nothing is hidden: what the form shows is what is executed.

## Where the password goes

From the OS keyring, like everywhere else — and **never into the tool's
argv**, where `ps` would show it to every user on the machine, nor into its
environment.

It travels in a temporary `0600` file that is deleted the moment the tool
exits:

- a `.pgpass` named by `PGPASSFILE` for PostgreSQL,
- a MySQL option file named by `--defaults-extra-file` for MySQL/MariaDB.

It appears in neither the previewed command nor the command log.

## While it runs

The tool's stderr is streamed into the [command
log](../concepts/command-log.md) as it goes, so a dump that is going wrong
says so while it is going wrong.

`X` cancels a running job and kills the tool's whole process group. A
cancelled or failed **dump** has its partial file removed.

When the connection runs through an [SSH tunnel](ssh-tunnels.md), lazysql
opens a loopback-only local forward for the duration of the run and points the
tool at that — external tools cannot use lazysql's in-process transport.

## Restoring

!!! danger "A restore overwrites data and cannot be undone"
    It does not run until the **database name has been typed** into a
    confirmation field, and it is refused outright on a
    [read-only connection](configuration.md#read-only-connections).

That is the one place in lazysql where a confirmation asks you to type
something rather than press a key. Everything else is reversible or staged;
this is not.
