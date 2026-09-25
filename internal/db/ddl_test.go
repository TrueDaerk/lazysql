package db

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// schemaSQLs renders a change and returns the statement texts.
func schemaSQLs(t *testing.T, e Engine, c SchemaChange) []string {
	t.Helper()
	stmts, err := SchemaSQL(dialect(t, e), c)
	if err != nil {
		t.Fatalf("%s: SchemaSQL(%s): %v", e, c.Describe(), err)
	}
	out := make([]string, 0, len(stmts))
	for _, s := range stmts {
		if len(s.Args) != 0 {
			t.Errorf("%s: DDL carries bound args %v", e, s.Args)
		}
		out = append(out, s.SQL)
	}
	return out
}

// Every operation renders with the dialect's quoting and its own
// spelling where the engines differ.
func TestSchemaSQLPerDialect(t *testing.T) {
	cases := []struct {
		name   string
		engine Engine
		change SchemaChange
		want   []string
	}{
		{"create table pg", EnginePostgres, CreateTable{Database: "app", Table: "t", Columns: []ColumnDef{
			{Name: "id", Type: "integer", NotNull: true, PrimaryKey: true},
			{Name: "note", Type: "varchar(40)", Default: ColumnDefault{Kind: DefaultText, Value: "it's"}},
		}}, []string{`CREATE TABLE "app"."t" ("id" integer NOT NULL, "note" varchar(40) DEFAULT 'it''s', PRIMARY KEY ("id"))`}},
		{"create table mysql backslash", EngineMySQL, CreateTable{Table: "t", Columns: []ColumnDef{
			{Name: "p", Type: "text", Default: ColumnDefault{Kind: DefaultText, Value: `C:\dir`}},
		}}, []string{"CREATE TABLE `t` (`p` text DEFAULT 'C:\\\\dir')"}},
		{"add column", EngineSQLite, AddColumn{Database: "main", Table: "t", Column: ColumnDef{
			Name: "n", Type: "integer", NotNull: true, Default: ColumnDefault{Kind: DefaultNumber, Value: "0"},
		}}, []string{`ALTER TABLE "main"."t" ADD COLUMN "n" integer NOT NULL DEFAULT 0`}},
		{"drop column", EngineMySQL, DropColumn{Database: "app", Table: "t", Column: "n"},
			[]string{"ALTER TABLE `app`.`t` DROP COLUMN `n`"}},
		{"rename table pg", EnginePostgres, RenameRelation{Database: "s", Name: "a", NewName: "b"},
			[]string{`ALTER TABLE "s"."a" RENAME TO "b"`}},
		{"rename view pg", EnginePostgres, RenameRelation{Database: "s", Name: "a", NewName: "b", Kind: RelationView},
			[]string{`ALTER VIEW "s"."a" RENAME TO "b"`}},
		{"rename table mysql", EngineMariaDB, RenameRelation{Database: "app", Name: "a", NewName: "b"},
			[]string{"RENAME TABLE `app`.`a` TO `app`.`b`"}},
		{"drop view", EngineDuckDB, DropRelation{Name: "v", Kind: RelationView},
			[]string{`DROP VIEW "v"`}},
		{"truncate pg", EnginePostgres, TruncateTable{Database: "s", Table: "t"},
			[]string{`TRUNCATE TABLE "s"."t"`}},
		{"truncate duckdb", EngineDuckDB, TruncateTable{Table: "t"},
			[]string{`TRUNCATE "t"`}},
		{"create index sqlite", EngineSQLite, CreateIndex{Database: "main", Table: "t", Name: "ix", Columns: []string{"a", "b"}, Unique: true},
			[]string{`CREATE UNIQUE INDEX "main"."ix" ON "t" ("a", "b")`}},
		{"create index pg", EnginePostgres, CreateIndex{Database: "s", Table: "t", Name: "ix", Columns: []string{"a"}},
			[]string{`CREATE INDEX "ix" ON "s"."t" ("a")`}},
		{"drop index pg", EnginePostgres, DropIndex{Database: "s", Table: "t", Name: "ix"},
			[]string{`DROP INDEX "s"."ix"`}},
		{"drop index mysql", EngineMySQL, DropIndex{Database: "app", Table: "t", Name: "ix"},
			[]string{"DROP INDEX `ix` ON `app`.`t`"}},
		{"alter pg folds actions, renames last", EnginePostgres, AlterColumn{Table: "t",
			Old:     Column{Name: "a", DataType: "integer", Nullable: true},
			NewName: "b", Type: "bigint", NotNull: true, Default: ColumnDefault{Kind: DefaultExpr, Value: "42"},
		}, []string{
			`ALTER TABLE "t" ALTER COLUMN "a" TYPE bigint, ALTER COLUMN "a" SET NOT NULL, ALTER COLUMN "a" SET DEFAULT 42`,
			`ALTER TABLE "t" RENAME COLUMN "a" TO "b"`,
		}},
		{"alter duckdb one action each", EngineDuckDB, AlterColumn{Table: "t",
			Old:  Column{Name: "a", DataType: "INTEGER", Nullable: false, Default: strPtr("1")},
			Type: "INTEGER", NotNull: false, Default: ColumnDefault{Kind: DefaultNone},
		}, []string{
			`ALTER TABLE "t" ALTER COLUMN "a" DROP NOT NULL`,
			`ALTER TABLE "t" ALTER COLUMN "a" DROP DEFAULT`,
		}},
		{"alter mysql default only keeps the column", EngineMySQL, AlterColumn{Table: "t",
			Old:  Column{Name: "a", DataType: "int", Nullable: true},
			Type: "int", NotNull: false, Default: ColumnDefault{Kind: DefaultNumber, Value: "7"},
		}, []string{"ALTER TABLE `t` ALTER COLUMN `a` SET DEFAULT 7"}},
		{"alter mysql retype restates the rest", EngineMySQL, AlterColumn{Table: "t",
			Old: Column{Name: "a", DataType: "int", Nullable: false, Default: strPtr("5"),
				Extra: "auto_increment"},
			NewName: "b", Type: "bigint", NotNull: true, Default: ColumnDefault{Kind: DefaultKeep},
		}, []string{"ALTER TABLE `t` CHANGE COLUMN `a` `b` bigint NOT NULL DEFAULT '5' AUTO_INCREMENT"}},
		{"alter mysql keeps an expression default", EngineMySQL, AlterColumn{Table: "t",
			Old: Column{Name: "ts", DataType: "timestamp", Nullable: true, Default: strPtr("CURRENT_TIMESTAMP"),
				Extra: "DEFAULT_GENERATED on update CURRENT_TIMESTAMP"},
			Type: "datetime", NotNull: false, Default: ColumnDefault{Kind: DefaultKeep},
		}, []string{"ALTER TABLE `t` CHANGE COLUMN `ts` `ts` datetime NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP"}},
		{"alter mariadb restates the reported default", EngineMariaDB, AlterColumn{Table: "t",
			Old:  Column{Name: "a", DataType: "varchar(10)", Nullable: true, Default: strPtr("'x'")},
			Type: "varchar(20)", NotNull: false, Default: ColumnDefault{Kind: DefaultKeep},
		}, []string{"ALTER TABLE `t` CHANGE COLUMN `a` `a` varchar(20) NULL DEFAULT 'x'"}},
		{"mysql expression default is parenthesized", EngineMySQL, AddColumn{Table: "t", Column: ColumnDef{
			Name: "u", Type: "varchar(36)", Default: ColumnDefault{Kind: DefaultExpr, Value: "uuid()"},
		}}, []string{"ALTER TABLE `t` ADD COLUMN `u` varchar(36) DEFAULT (uuid())"}},
		{"sqlite rename column", EngineSQLite, AlterColumn{Database: "main", Table: "t",
			Old: Column{Name: "a", DataType: "TEXT", Nullable: true}, NewName: "b", Type: "TEXT",
			Default: ColumnDefault{Kind: DefaultKeep},
		}, []string{`ALTER TABLE "main"."t" RENAME COLUMN "a" TO "b"`}},
		{"quoting survives hostile names", EngineSQLite, DropRelation{Name: `x"; DROP TABLE y; --`},
			[]string{`DROP TABLE "x""; DROP TABLE y; --"`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := schemaSQLs(t, tc.engine, tc.change); !slices.Equal(got, tc.want) {
				t.Errorf("got\n  %q\nwant\n  %q", got, tc.want)
			}
		})
	}
}

// The engines that cannot do an operation answer ErrUnsupported — with a
// reason — instead of producing SQL that would fail on commit.
func TestSchemaUnsupportedPerEngine(t *testing.T) {
	cases := []struct {
		engine Engine
		change SchemaChange
	}{
		{EngineSQLite, TruncateTable{Table: "t"}},
		{EngineSQLite, RenameRelation{Name: "v", NewName: "w", Kind: RelationView}},
		{EngineSQLite, AlterColumn{Table: "t", Old: Column{Name: "a", DataType: "TEXT", Nullable: true},
			Type: "INTEGER", Default: ColumnDefault{Kind: DefaultKeep}}},
		// A rename that also changes the type needs both, and SQLite only
		// has one of them.
		{EngineSQLite, AlterColumn{Table: "t", Old: Column{Name: "a", DataType: "TEXT", Nullable: true},
			NewName: "b", Type: "TEXT", NotNull: true, Default: ColumnDefault{Kind: DefaultKeep}}},
	}
	for _, tc := range cases {
		_, err := SchemaSQL(dialect(t, tc.engine), tc.change)
		if !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s %s: err = %v, want ErrUnsupported", tc.engine, tc.change.Describe(), err)
			continue
		}
		if strings.HasPrefix(err.Error(), "db:") || err.Error() == "" {
			t.Errorf("%s: the error should carry the engine's reason, got %q", tc.engine, err)
		}
	}
	for _, op := range []SchemaOp{OpAlterColumn, OpTruncateTable, OpRenameView} {
		if err := dialect(t, EngineSQLite).schemaSupport(op); !errors.Is(err, ErrUnsupported) {
			t.Errorf("sqlite %s: %v, want ErrUnsupported", op, err)
		}
	}
	for _, e := range []Engine{EnginePostgres, EngineMySQL, EngineMariaDB, EngineDuckDB} {
		for op := OpCreateTable; op <= OpDropIndex; op++ {
			if err := dialect(t, e).schemaSupport(op); err != nil {
				t.Errorf("%s %s: %v", e, op, err)
			}
		}
	}
}

// An alter that changes nothing is not a statement.
func TestAlterColumnNoChange(t *testing.T) {
	c := AlterColumn{Table: "t", Old: Column{Name: "a", DataType: "int", Nullable: true},
		NewName: "a", Type: "INT", Default: ColumnDefault{Kind: DefaultKeep}}
	if _, err := SchemaSQL(dialect(t, EnginePostgres), c); !errors.Is(err, ErrNoSchemaChange) {
		t.Fatalf("err = %v, want ErrNoSchemaChange", err)
	}
	if _, err := SchemaSQL(dialect(t, EnginePostgres), RenameRelation{Name: "a", NewName: "a"}); !errors.Is(err, ErrNoSchemaChange) {
		t.Fatalf("rename to itself: err = %v, want ErrNoSchemaChange", err)
	}
}

// A type is a type expression and nothing else.
func TestValidateTypeName(t *testing.T) {
	good := []string{"integer", "varchar(40)", "numeric(10, 2)", "timestamp with time zone",
		"double precision", "int unsigned", "integer[]", "STRUCT(a INT, b VARCHAR)", "public.my_type",
		"timestamp(3) without time zone"}
	for _, s := range good {
		if err := ValidateTypeName(s); err != nil {
			t.Errorf("ValidateTypeName(%q) = %v, want ok", s, err)
		}
	}
	bad := []string{"", "int, evil int", "int); DROP TABLE t; --", "text DEFAULT 'x'", "int -- c",
		"int /* c */", "varchar(40", "int)", "1int", "enum('a')"}
	for _, s := range bad {
		if err := ValidateTypeName(s); err == nil {
			t.Errorf("ValidateTypeName(%q) = ok, want an error", s)
		}
	}
}

// A default expression cannot end the statement or the column definition
// it is put in, whatever the dialect's lexer makes of it.
func TestValidateExpression(t *testing.T) {
	good := []string{"CURRENT_TIMESTAMP", "now()", "nextval('seq')", "'a;b'", "'--x'",
		"lower('A')", "coalesce(1, 2)", "(1 + 2) * 3"}
	for _, s := range good {
		if err := ValidateExpression(EnginePostgres, s); err != nil {
			t.Errorf("ValidateExpression(%q) = %v, want ok", s, err)
		}
	}
	bad := []string{"", "1; DROP TABLE t", "1 -- rest", "1 /* x */", "'open", "1)", "(1",
		"1, evil int", "$$open"}
	for _, s := range bad {
		if err := ValidateExpression(EnginePostgres, s); err == nil {
			t.Errorf("ValidateExpression(%q) = ok, want an error", s)
		}
	}
	// MySQL reads a backslash as an escape and `#` as a comment.
	for _, s := range []string{`'a\'`, "1 # rest"} {
		if err := ValidateExpression(EngineMySQL, s); err == nil {
			t.Errorf("mysql ValidateExpression(%q) = ok, want an error", s)
		}
	}
	// A bad default never renders.
	_, err := SchemaSQL(dialect(t, EngineSQLite), AddColumn{Table: "t", Column: ColumnDef{
		Name: "c", Type: "int", Default: ColumnDefault{Kind: DefaultNumber, Value: "1; DROP TABLE t"}}})
	if err == nil {
		t.Error("a non-numeric numeric default rendered")
	}
}

// Schema changes join the one changeset: they render at their staging
// position, alongside row changes, and staging one twice is one change.
func TestChangesetStagesSchemaChanges(t *testing.T) {
	cs := NewChangeset()
	cs.Stage(CellChange{Table: "t", PKCols: []string{"id"}, PKVals: []any{int64(1)}, Column: "a", NewValue: "x"})
	cs.StageSchema(AddColumn{Table: "t", Column: ColumnDef{Name: "b", Type: "int"}})
	cs.StageSchema(DropRelation{Name: "u"})
	cs.StageSchema(DropRelation{Name: "u"})
	if cs.Len() != 3 {
		t.Fatalf("Len = %d, want 3 (the second drop replaces the first)", cs.Len())
	}
	got := mustStatements(t, cs, dialect(t, EngineSQLite))
	want := []string{`UPDATE "t" SET "a" = ? WHERE "id" = ?`, `ALTER TABLE "t" ADD COLUMN "b" int`, `DROP TABLE "u"`}
	if len(got) != len(want) {
		t.Fatalf("got %d statements, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].SQL != want[i] {
			t.Errorf("statement %d = %q, want %q", i, got[i].SQL, want[i])
		}
	}
	if n := len(cs.SchemaChangesFor("", "t")); n != 1 {
		t.Errorf("SchemaChangesFor(t) = %d, want 1", n)
	}
	if !cs.UnstageSchema(DropRelation{Name: "u"}) || len(cs.SchemaChanges()) != 1 {
		t.Error("UnstageSchema did not drop the staged drop")
	}
	// An operation the engine cannot do fails the whole render rather than
	// being skipped.
	cs.StageSchema(TruncateTable{Table: "t"})
	if _, err := cs.Statements(dialect(t, EngineSQLite)); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Statements with a SQLite TRUNCATE: err = %v, want ErrUnsupported", err)
	}
}

// A read-only session refuses every schema change at the driver, not
// only in the UI, and says so in the command log.
func TestReadOnlyRefusesSchemaChanges(t *testing.T) {
	drv := openReadOnlyTest(t, seedFile(t))
	changes := []SchemaChange{
		CreateTable{Table: "n", Columns: []ColumnDef{{Name: "id", Type: "int"}}},
		AddColumn{Table: "t", Column: ColumnDef{Name: "c", Type: "int"}},
		AlterColumn{Table: "t", Old: Column{Name: "name", DataType: "TEXT", Nullable: true}, NewName: "nm",
			Default: ColumnDefault{Kind: DefaultKeep}},
		DropColumn{Table: "t", Column: "name"},
		RenameRelation{Name: "t", NewName: "u"},
		DropRelation{Name: "t"},
		TruncateTable{Table: "t"},
		CreateIndex{Table: "t", Name: "ix", Columns: []string{"name"}},
		DropIndex{Table: "t", Name: "ix"},
	}
	for _, c := range changes {
		if _, err := drv.SchemaSQL(c); !errors.Is(err, ErrReadOnly) {
			t.Errorf("SchemaSQL(%s) = %v, want ErrReadOnly", c.Describe(), err)
		}
	}
	for op := OpCreateTable; op <= OpDropIndex; op++ {
		if err := drv.SchemaSupport(op); !errors.Is(err, ErrReadOnly) {
			t.Errorf("SchemaSupport(%s) = %v, want ErrReadOnly", op, err)
		}
	}
	rejected := 0
	for _, e := range drv.Logger().Entries() {
		if strings.HasPrefix(e.SQL, rejectedPrefix) {
			rejected++
		}
	}
	if rejected != len(changes) {
		t.Errorf("rejected log lines = %d, want %d", rejected, len(changes))
	}
	// And a changeset staged elsewhere cannot get out through the commit.
	stmts, err := SchemaSQL(drv.Dialect(), DropRelation{Name: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := drv.ExecTx(context.Background(), stmts); !errors.Is(err, ErrReadOnly) {
		t.Errorf("ExecTx(DROP TABLE) = %v, want ErrReadOnly", err)
	}
}

// commitSchema stages changes, renders them for the driver and runs the
// commit exactly as the UI does.
func commitSchema(t *testing.T, drv Driver, changes ...SchemaChange) {
	t.Helper()
	cs := NewChangeset()
	for _, c := range changes {
		if _, err := drv.SchemaSQL(c); err != nil {
			t.Fatalf("SchemaSQL(%s): %v", c.Describe(), err)
		}
		cs.StageSchema(c)
	}
	stmts, err := cs.Statements(drv.Dialect())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := drv.ExecTx(context.Background(), stmts); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func columnNames(t *testing.T, drv Driver, database, table string) []string {
	t.Helper()
	cols, err := drv.TableColumns(context.Background(), database, table)
	if err != nil {
		t.Fatalf("TableColumns(%s): %v", table, err)
	}
	var out []string
	for _, c := range cols {
		out = append(out, c.Name)
	}
	return out
}

func indexNames(t *testing.T, drv Driver, database, table string) []string {
	t.Helper()
	idx, err := drv.TableIndexes(context.Background(), database, table)
	if err != nil {
		t.Fatalf("TableIndexes(%s): %v", table, err)
	}
	var out []string
	for _, ix := range idx {
		if !ix.Primary {
			out = append(out, ix.Name)
		}
	}
	return out
}

func relationNames(t *testing.T, drv Driver, database string) []string {
	t.Helper()
	names, err := drv.ListTables(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	return names
}

// The whole lifecycle, executed in-process: every statement the dialect
// renders is one the engine accepts.
func ddlLifecycle(t *testing.T, engine Engine, database string) {
	drv := openTest(t, engine, "")
	ctx := context.Background()

	commitSchema(t, drv, CreateTable{Database: database, Table: "items", Columns: []ColumnDef{
		{Name: "id", Type: "INTEGER", NotNull: true, PrimaryKey: true},
		{Name: "name", Type: "VARCHAR(40)", NotNull: true, Default: ColumnDefault{Kind: DefaultText, Value: "it's"}},
	}})
	if got := columnNames(t, drv, database, "items"); !slices.Equal(got, []string{"id", "name"}) {
		t.Fatalf("columns after CREATE = %v", got)
	}
	if _, err := drv.Exec(ctx, `INSERT INTO `+qualifiedTable(drv.Dialect(), database, "items")+` (id) VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	rs, err := drv.Query(ctx, `SELECT name FROM `+qualifiedTable(drv.Dialect(), database, "items"))
	if err != nil {
		t.Fatal(err)
	}
	if got := rs.Rows[0][0]; got != "it's" {
		t.Errorf("escaped text default = %v, want it's", got)
	}

	commitSchema(t, drv,
		AddColumn{Database: database, Table: "items", Column: ColumnDef{
			Name: "qty", Type: "INTEGER", Default: ColumnDefault{Kind: DefaultNumber, Value: "3"}}},
		AlterColumn{Database: database, Table: "items",
			Old: Column{Name: "qty", DataType: "INTEGER", Nullable: true}, NewName: "amount", Type: "INTEGER",
			Default: ColumnDefault{Kind: DefaultKeep}},
		// DuckDB refuses to ALTER a table that has an index on it, so the
		// index comes after the alter and is gone again before the next one.
		CreateIndex{Database: database, Table: "items", Name: "items_name", Columns: []string{"name"}, Unique: true},
	)
	if got := columnNames(t, drv, database, "items"); !slices.Equal(got, []string{"id", "name", "amount"}) {
		t.Fatalf("columns after ADD + RENAME = %v", got)
	}
	if got := indexNames(t, drv, database, "items"); !slices.Contains(got, "items_name") {
		t.Fatalf("indexes after CREATE INDEX = %v", got)
	}

	commitSchema(t, drv, DropIndex{Database: database, Table: "items", Name: "items_name"})
	if got := indexNames(t, drv, database, "items"); slices.Contains(got, "items_name") {
		t.Fatalf("indexes after DROP INDEX = %v", got)
	}

	commitSchema(t, drv, DropColumn{Database: database, Table: "items", Column: "amount"})
	if got := columnNames(t, drv, database, "items"); !slices.Equal(got, []string{"id", "name"}) {
		t.Fatalf("columns after DROP COLUMN = %v", got)
	}

	commitSchema(t, drv, RenameRelation{Database: database, Name: "items", NewName: "goods"})
	if got := relationNames(t, drv, database); !slices.Equal(got, []string{"goods"}) {
		t.Fatalf("relations after RENAME = %v", got)
	}

	if err := drv.SchemaSupport(OpTruncateTable); err == nil {
		commitSchema(t, drv, TruncateTable{Database: database, Table: "goods"})
		n, err := drv.CountRows(ctx, database, "goods", nil)
		if err != nil || n != 0 {
			t.Fatalf("rows after TRUNCATE = %d, %v", n, err)
		}
	}

	commitSchema(t, drv, DropRelation{Database: database, Name: "goods"})
	if got := relationNames(t, drv, database); len(got) != 0 {
		t.Fatalf("relations after DROP = %v", got)
	}
}

func TestSQLiteDDLLifecycle(t *testing.T) { ddlLifecycle(t, EngineSQLite, "main") }
func TestDuckDBDDLLifecycle(t *testing.T) { ddlLifecycle(t, EngineDuckDB, "") }

// DuckDB can change a column's definition, which SQLite cannot: type,
// nullability and default, each in a statement of its own.
func TestDuckDBAlterColumnDefinition(t *testing.T) {
	drv := openTest(t, EngineDuckDB, "")
	commitSchema(t, drv, CreateTable{Table: "t", Columns: []ColumnDef{
		{Name: "id", Type: "INTEGER", NotNull: true, PrimaryKey: true},
		{Name: "n", Type: "INTEGER"},
	}})
	commitSchema(t, drv, AlterColumn{Table: "t", Old: Column{Name: "n", DataType: "INTEGER", Nullable: true},
		NewName: "m", Type: "BIGINT", NotNull: true, Default: ColumnDefault{Kind: DefaultNumber, Value: "9"}})
	cols, err := drv.TableColumns(context.Background(), "", "t")
	if err != nil {
		t.Fatal(err)
	}
	c := cols[1]
	if c.Name != "m" || c.DataType != "BIGINT" || c.Nullable || c.Default == nil || *c.Default != "9" {
		t.Fatalf("altered column = %+v (default %v)", c, c.Default)
	}
	// And a view can be renamed and dropped.
	if _, err := drv.Exec(context.Background(), `CREATE VIEW v AS SELECT * FROM t`); err != nil {
		t.Fatal(err)
	}
	commitSchema(t, drv, RenameRelation{Name: "v", NewName: "w", Kind: RelationView})
	commitSchema(t, drv, DropRelation{Name: "w", Kind: RelationView})
	if got := relationNames(t, drv, ""); !slices.Equal(got, []string{"t"}) {
		t.Fatalf("relations = %v", got)
	}
}

// A failing DDL statement rolls the whole commit back on the engines that
// run DDL inside the transaction.
func TestSQLiteDDLCommitIsAtomic(t *testing.T) {
	drv := openTest(t, EngineSQLite, "")
	cs := NewChangeset()
	cs.StageSchema(CreateTable{Table: "a", Columns: []ColumnDef{{Name: "id", Type: "INTEGER"}}})
	cs.StageSchema(DropRelation{Name: "does_not_exist"})
	stmts, err := cs.Statements(drv.Dialect())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := drv.ExecTx(context.Background(), stmts); err == nil {
		t.Fatal("commit of a DROP of a missing table succeeded")
	}
	if got := relationNames(t, drv, ""); len(got) != 0 {
		t.Fatalf("relations after a failed commit = %v, want the CREATE rolled back", got)
	}
	if !TransactionalDDL(EngineSQLite) || TransactionalDDL(EngineMySQL) {
		t.Error("TransactionalDDL disagrees with the engines")
	}
}

// A DuckDB namespace is a catalog: the lifecycle holds when every
// statement is catalog-qualified too.
func TestDuckDBDDLLifecycleQualified(t *testing.T) { ddlLifecycle(t, EngineDuckDB, "memory") }
