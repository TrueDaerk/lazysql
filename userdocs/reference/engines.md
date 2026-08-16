# Engines

lazysql speaks five engines through one driver interface, so the UI is the
same everywhere. What differs is what the engine can be *asked*, and this page
collects the places that shows.

| Engine | Kind | Default port | Driver |
|---|---|---|---|
| PostgreSQL | server | 5432 | `jackc/pgx` (pure Go) |
| MySQL | server | 3306 | `go-sql-driver/mysql` (pure Go) |
| MariaDB | server | 3306 | `go-sql-driver/mysql` (pure Go) |
| SQLite | file | — | `modernc.org/sqlite` (pure Go) |
| DuckDB | file or in-memory | — | `marcboeker/go-duckdb` (**needs CGO**) |

MariaDB shares MySQL's dialect; where the two differ, this page says so.

## Namespaces

PostgreSQL, MySQL and MariaDB have many databases or schemas, so the object
tree starts at that level. SQLite — and DuckDB with nothing attached — has
exactly one, so the tree skips the level entirely and starts at the
categories.

## Connection strings

Assembled in one place; the UI never concatenates one.

| Engine | Shape |
|---|---|
| MySQL / MariaDB | `user:pass@tcp(host:port)/db?parseTime=true` |
| PostgreSQL | `postgres://user:pass@host:port/db?sslmode=…` |
| SQLite | a bare path, or `file:<path>?<opts>` once options are set |
| DuckDB | a bare path plus a query string; an empty path is in-memory |

A **unix socket** is expressed as the host: a `host` starting with `/`
switches MySQL from TCP to a unix socket at that path. There is no separate
socket field.

Anything you put in the connection form's **Options** field is appended as DSN
parameters, sorted, so the string in the command log is stable between
identical connects.

## Triggers

The `Triggers` category of the object tree:

| Engine | Where they come from |
|---|---|
| SQLite | `sqlite_master` — the original `CREATE TRIGGER` text, verbatim |
| PostgreSQL | `pg_trigger`, not `information_schema` (which has one row per event and hides triggers on tables you do not own) |
| MySQL / MariaDB | Synthesized `CREATE TRIGGER` from `information_schema.triggers` |
| DuckDB | **Not supported** — DuckDB has no triggers, so the category stays empty |

MySQL/MariaDB trigger introspection needs the `TRIGGER` privilege on the
schema; without it the category comes back empty rather than erroring.

## DDL

| Engine | Source |
|---|---|
| MySQL / MariaDB | `SHOW CREATE TABLE` |
| SQLite | The stored statement in `sqlite_master` |
| PostgreSQL | Synthesized from `information_schema` and `pg_*` |
| DuckDB | Synthesized from the `duckdb_*` functions |

A synthesized statement includes foreign-key constraints and secondary
`CREATE INDEX` statements, so it says as much as a stored one.

SQLite's stored DDL is the statement you originally wrote — including
`AUTOINCREMENT`, which it does not re-derive, and without a name for an
unnamed foreign key.

## `EXPLAIN`

`ctrl+e` in the query editor. Each engine's own form is used, and `ANALYZE` is
**never** added, so nothing is executed.

| Engine | Statement | Rendering |
|---|---|---|
| PostgreSQL | `EXPLAIN (FORMAT JSON)` | Indented tree, with cost and row estimates |
| MySQL / MariaDB | `EXPLAIN FORMAT=JSON` | Indented tree; falls back to the tabular `EXPLAIN` where the JSON format is unavailable |
| SQLite | `EXPLAIN QUERY PLAN` | The id/parent tree |
| DuckDB | `EXPLAIN` | The engine's own ASCII diagram |

## Server activity

`A` on panel `[1]` — see [Server activity](../guides/server-activity.md).

| Engine | Sessions from | Lock waits from |
|---|---|---|
| PostgreSQL | `pg_stat_activity` (needs **10 or newer**: `backend_type`) | `pg_blocking_pids()`, in the same query |
| MySQL | `information_schema.processlist` | `performance_schema.data_lock_waits` — best-effort |
| MariaDB | `information_schema.processlist` | `information_schema.innodb_lock_waits` — best-effort, and **removed in MariaDB 10.6** |
| SQLite / DuckDB | **Not supported** — they run inside lazysql, so there is no session but yours | — |

"Best-effort" means the lock-wait query is allowed to fail: a server whose
InnoDB lock views are missing, disabled or not permitted still shows its
process list, just with an empty `Blocked by` column. The attempt is visible
in the command log like every other statement.

Killing a session sends `KILL CONNECTION <id>` on MySQL/MariaDB and
`SELECT pg_terminate_backend(<id>)` on PostgreSQL. On PostgreSQL a `false`
answer — the backend was already gone, or your role may not signal it — is
reported as a failure rather than a silent no-op.

## Query cancellation

`ctrl+c` cancels a run. What that means at the server differs:

| Engine | What happens |
|---|---|
| SQLite | A real engine-level interrupt — the scan aborts within one VM step |
| DuckDB | A real engine-level interrupt, through its pending-result API |
| PostgreSQL | A protocol-level `CancelRequest` on a second connection — exactly what `psql`'s own ++ctrl+c++ does |
| MySQL / MariaDB | **Client-side only**: the connection is dropped and the server-side statement is left to finish on its own |

So on MySQL, cancelling gets your UI back immediately but does not stop the
query on the server. The other three genuinely stop it.

## Read-only sessions

A [read-only connection](../guides/configuration.md#read-only-connections) is
enforced by lazysql's own driver session — that is the guarantee. On top of
it, the engine is asked for a read-only session where one exists:

| Engine | DSN parameter | Applies to |
|---|---|---|
| SQLite | `mode=ro` | The whole connection |
| DuckDB | `access_mode=read_only` | The whole connection |
| PostgreSQL | `default_transaction_read_only=on` | Every transaction of the session |
| MySQL / MariaDB | `transaction_read_only=1` | Every transaction of every pooled connection |

Caveats worth knowing:

- **SQLite `mode=ro` never creates the file.** A read-only profile pointed at
  a path that does not exist fails to connect instead of opening an empty
  database.
- **An in-memory DuckDB cannot be read-only** — there would be nothing in it
  to read — so the parameter is dropped for that combination. lazysql's own
  guard still refuses every write.
- **MySQL needs 5.7.20+, MariaDB 10.2.2+** for `transaction_read_only`. An
  older server refuses the handshake with `Unknown system variable` rather
  than connecting read-write — the safe direction, but worth recognizing.
- **`transaction_read_only` does not stop DDL on MySQL**, where `CREATE TABLE`
  is not transactional. lazysql's session guard classifies DDL as a write, so
  the mode holds anyway.

## Literals and escaping

Everything you type into a filter, an edit or an insert travels as a **bound
query parameter**, so escaping is the driver's problem, not yours. Where
lazysql does have to write a literal — an `INSERT` statement copied to the
clipboard, an SQL export — the dialect's own rules apply:

- **MySQL** honours backslash escapes inside string literals; PostgreSQL,
  SQLite and DuckDB do not.
- Booleans and timestamps have per-engine spellings.
- An `INSERT` that names no columns is `DEFAULT VALUES` in PostgreSQL, SQLite
  and DuckDB, and `() VALUES ()` in MySQL/MariaDB.

## One SQLite trap

SQLite reads a **double-quoted identifier that matches no column as a string
literal**. So a filter on a misspelled column quietly matches nothing instead
of failing:

```sql
WHERE "usr_id" = 1     -- no such column: matches no rows, raises no error
```

Safety is unaffected — the value is still bound — but a filter that returns
zero rows on SQLite is worth a second look at the column name.

## More detail

The contributor wiki carries a dialect note per topic, with the queries and
the sources behind each of these:
[`wiki/index.md`](https://github.com/TrueDaerk/lazysql/blob/main/wiki/index.md).
