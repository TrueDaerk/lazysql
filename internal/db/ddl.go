package db

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"lazysql/internal/sqlhl"
)

// Staged schema changes. DDL rides the same changeset as cell edits and
// row operations: staging records a value describing the operation, the
// dialect renders it to SQL, and nothing reaches the server before the
// user confirms the commit. The types here carry names, types and default
// values — never SQL text — so the one place a statement is spelled is the
// dialect's schemaSQL, and the one place it is executed is ExecTx.
//
// See wiki/design/staged-ddl.md.

// SchemaOp is one kind of DDL operation. The UI asks Driver.SchemaSupport
// about a kind before it offers it, and a change asks its dialect about
// every kind it needs before it renders, so an engine that cannot do
// something answers ErrUnsupported instead of producing broken SQL.
type SchemaOp int

const (
	OpCreateTable SchemaOp = iota
	OpAddColumn
	// OpRenameColumn and OpAlterColumn are separate because engines split
	// them: SQLite renames a column but cannot change its definition.
	OpRenameColumn
	OpAlterColumn
	OpDropColumn
	OpRenameTable
	OpRenameView
	OpDropTable
	OpDropView
	OpTruncateTable
	OpCreateIndex
	OpDropIndex
)

var schemaOpNames = map[SchemaOp]string{
	OpCreateTable:   "create table",
	OpAddColumn:     "add column",
	OpRenameColumn:  "rename column",
	OpAlterColumn:   "alter column",
	OpDropColumn:    "drop column",
	OpRenameTable:   "rename table",
	OpRenameView:    "rename view",
	OpDropTable:     "drop table",
	OpDropView:      "drop view",
	OpTruncateTable: "truncate table",
	OpCreateIndex:   "create index",
	OpDropIndex:     "drop index",
}

func (o SchemaOp) String() string { return schemaOpNames[o] }

// unsupportedError is ErrUnsupported with the engine's reason attached.
// errors.Is(err, ErrUnsupported) holds, and the message is the reason
// alone, which is what the UI shows.
type unsupportedError struct{ reason string }

func (e unsupportedError) Error() string { return e.reason }
func (e unsupportedError) Unwrap() error { return ErrUnsupported }

// unsupported builds the ErrUnsupported a dialect answers with.
func unsupported(format string, args ...any) error {
	return unsupportedError{reason: fmt.Sprintf(format, args...)}
}

// ErrNoSchemaChange is what an alter that changes nothing renders to: an
// ALTER TABLE with no clauses is not a statement, and staging a no-op
// would only clutter the commit.
var ErrNoSchemaChange = errors.New("nothing to change")

// TransactionalDDL reports whether the engine runs DDL inside a
// transaction. MySQL and MariaDB do not: every DDL statement commits
// implicitly, so a commit that fails half-way through keeps what ran
// before the failure. The commit preview says so rather than promising
// an all-or-nothing it cannot keep.
func TransactionalDDL(e Engine) bool {
	switch e {
	case EngineMySQL, EngineMariaDB:
		return false
	default:
		return true
	}
}

// DefaultKind says how a column's DEFAULT clause is spelled.
type DefaultKind int

const (
	// DefaultNone is no DEFAULT clause at all; on an alter it drops the
	// column's default.
	DefaultNone DefaultKind = iota
	// DefaultText is a string literal, escaped per dialect.
	DefaultText
	// DefaultNumber is a numeric literal, validated before it is
	// rendered.
	DefaultNumber
	// DefaultNull is DEFAULT NULL.
	DefaultNull
	// DefaultExpr is a SQL expression — CURRENT_TIMESTAMP, now(), a
	// function call — rendered as typed. It is checked to be a single
	// expression that cannot end the statement or the column definition
	// it sits in.
	DefaultExpr
	// DefaultKeep is an alter's "leave the default as it is".
	DefaultKeep
)

// ColumnDefault is a DEFAULT clause. Value is ignored for DefaultNone,
// DefaultNull and DefaultKeep.
type ColumnDefault struct {
	Kind  DefaultKind
	Value string
}

// ColumnDef describes a column to create. Type is the engine's own type
// name (varchar(40), numeric(10, 2), timestamp with time zone): it is
// validated to be a type expression and nothing else, since no engine
// accepts a type as a bound parameter.
type ColumnDef struct {
	Name       string
	Type       string
	NotNull    bool
	Default    ColumnDefault
	PrimaryKey bool // CREATE TABLE only
}

// SchemaChange is one staged DDL operation. Like every Change its methods
// are unexported, so no UI package can put SQL of its own into a commit.
type SchemaChange interface {
	Change
	// ops are the operation kinds the change needs. A change that needs
	// one its engine does not support never renders.
	ops() []SchemaOp
	// Describe is the one-line summary the UI lists the change by.
	Describe() string
}

// CreateTable is CREATE TABLE with its columns and primary key.
type CreateTable struct {
	Database string
	Table    string
	Columns  []ColumnDef
}

// AddColumn is ALTER TABLE … ADD COLUMN.
type AddColumn struct {
	Database string
	Table    string
	Column   ColumnDef
}

// AlterColumn changes an existing column. Old is the column as the engine
// described it; every field that still equals it is left alone, so the
// rendered statement touches only what the user changed.
type AlterColumn struct {
	Database string
	Table    string
	Old      Column
	NewName  string
	Type     string
	NotNull  bool
	Default  ColumnDefault
}

// DropColumn is ALTER TABLE … DROP COLUMN.
type DropColumn struct {
	Database string
	Table    string
	Column   string
}

// RenameRelation renames a table or a view.
type RenameRelation struct {
	Database string
	Name     string
	NewName  string
	Kind     RelationKind
}

// DropRelation drops a table or a view.
type DropRelation struct {
	Database string
	Name     string
	Kind     RelationKind
}

// TruncateTable empties a table.
type TruncateTable struct {
	Database string
	Table    string
}

// CreateIndex is CREATE [UNIQUE] INDEX over one or more columns.
type CreateIndex struct {
	Database string
	Table    string
	Name     string
	Columns  []string
	Unique   bool
}

// DropIndex drops one index of a table.
type DropIndex struct {
	Database string
	Table    string
	Name     string
}

// ---------- Change plumbing ----------

func ddlKey(kind, database, table string, rest ...string) string {
	return "ddl\x00" + kind + "\x00" + database + "\x00" + table + "\x00" + strings.Join(rest, "\x00")
}

func (c CreateTable) key() string              { return ddlKey("create-table", c.Database, c.Table) }
func (c CreateTable) target() (string, string) { return c.Database, c.Table }
func (c CreateTable) ops() []SchemaOp          { return []SchemaOp{OpCreateTable} }
func (c CreateTable) Describe() string {
	return fmt.Sprintf("create table %s (%d columns)", c.Table, len(c.Columns))
}

func (c AddColumn) key() string              { return ddlKey("add-column", c.Database, c.Table, c.Column.Name) }
func (c AddColumn) target() (string, string) { return c.Database, c.Table }
func (c AddColumn) ops() []SchemaOp          { return []SchemaOp{OpAddColumn} }
func (c AddColumn) Describe() string {
	return fmt.Sprintf("add column %s.%s %s", c.Table, c.Column.Name, c.Column.Type)
}

func (c AlterColumn) key() string              { return ddlKey("alter-column", c.Database, c.Table, c.Old.Name) }
func (c AlterColumn) target() (string, string) { return c.Database, c.Table }
func (c AlterColumn) ops() []SchemaOp {
	var out []SchemaOp
	if c.renamed() {
		out = append(out, OpRenameColumn)
	}
	if c.retyped() || c.nullChanged() || c.defaultChanged() {
		out = append(out, OpAlterColumn)
	}
	return out
}
func (c AlterColumn) Describe() string {
	var parts []string
	if c.renamed() {
		parts = append(parts, "rename to "+c.NewName)
	}
	if c.retyped() {
		parts = append(parts, "type "+c.Type)
	}
	if c.nullChanged() {
		if c.NotNull {
			parts = append(parts, "not null")
		} else {
			parts = append(parts, "nullable")
		}
	}
	if c.defaultChanged() {
		parts = append(parts, "default "+describeDefault(c.Default))
	}
	return fmt.Sprintf("alter column %s.%s: %s", c.Table, c.Old.Name, strings.Join(parts, ", "))
}

// newName is the name the column ends up with.
func (c AlterColumn) newName() string {
	if strings.TrimSpace(c.NewName) == "" {
		return c.Old.Name
	}
	return c.NewName
}

func (c AlterColumn) renamed() bool { return c.newName() != c.Old.Name }
func (c AlterColumn) retyped() bool {
	return strings.TrimSpace(c.Type) != "" &&
		!strings.EqualFold(strings.TrimSpace(c.Type), strings.TrimSpace(c.Old.DataType))
}
func (c AlterColumn) nullChanged() bool    { return c.NotNull == c.Old.Nullable }
func (c AlterColumn) defaultChanged() bool { return c.Default.Kind != DefaultKeep }

// newType is the type the column ends up with.
func (c AlterColumn) newType() string {
	if c.retyped() {
		return strings.TrimSpace(c.Type)
	}
	return c.Old.DataType
}

func (c DropColumn) key() string              { return ddlKey("drop-column", c.Database, c.Table, c.Column) }
func (c DropColumn) target() (string, string) { return c.Database, c.Table }
func (c DropColumn) ops() []SchemaOp          { return []SchemaOp{OpDropColumn} }
func (c DropColumn) Describe() string         { return fmt.Sprintf("drop column %s.%s", c.Table, c.Column) }

func (c RenameRelation) key() string              { return ddlKey("rename", c.Database, c.Name) }
func (c RenameRelation) target() (string, string) { return c.Database, c.Name }
func (c RenameRelation) ops() []SchemaOp {
	if c.Kind == RelationView {
		return []SchemaOp{OpRenameView}
	}
	return []SchemaOp{OpRenameTable}
}
func (c RenameRelation) Describe() string {
	return fmt.Sprintf("rename %s %s to %s", relationWord(c.Kind), c.Name, c.NewName)
}

func (c DropRelation) key() string              { return ddlKey("drop", c.Database, c.Name) }
func (c DropRelation) target() (string, string) { return c.Database, c.Name }
func (c DropRelation) ops() []SchemaOp {
	if c.Kind == RelationView {
		return []SchemaOp{OpDropView}
	}
	return []SchemaOp{OpDropTable}
}
func (c DropRelation) Describe() string {
	return fmt.Sprintf("drop %s %s", relationWord(c.Kind), c.Name)
}

func (c TruncateTable) key() string              { return ddlKey("truncate", c.Database, c.Table) }
func (c TruncateTable) target() (string, string) { return c.Database, c.Table }
func (c TruncateTable) ops() []SchemaOp          { return []SchemaOp{OpTruncateTable} }
func (c TruncateTable) Describe() string         { return "truncate table " + c.Table }

func (c CreateIndex) key() string              { return ddlKey("create-index", c.Database, c.Table, c.Name) }
func (c CreateIndex) target() (string, string) { return c.Database, c.Table }
func (c CreateIndex) ops() []SchemaOp          { return []SchemaOp{OpCreateIndex} }
func (c CreateIndex) Describe() string {
	kind := "index"
	if c.Unique {
		kind = "unique index"
	}
	return fmt.Sprintf("create %s %s on %s (%s)", kind, c.Name, c.Table, strings.Join(c.Columns, ", "))
}

func (c DropIndex) key() string              { return ddlKey("drop-index", c.Database, c.Table, c.Name) }
func (c DropIndex) target() (string, string) { return c.Database, c.Table }
func (c DropIndex) ops() []SchemaOp          { return []SchemaOp{OpDropIndex} }
func (c DropIndex) Describe() string         { return fmt.Sprintf("drop index %s on %s", c.Name, c.Table) }

// statements renders a schema change: every operation it needs is asked
// about first, the values it carries are validated, and only then does the
// dialect spell it.
func schemaStatements(d Dialect, c SchemaChange) ([]Statement, error) {
	ops := c.ops()
	if len(ops) == 0 {
		return nil, ErrNoSchemaChange
	}
	for _, op := range ops {
		if err := d.schemaSupport(op); err != nil {
			return nil, err
		}
	}
	if err := validateSchemaChange(d.Engine(), c); err != nil {
		return nil, err
	}
	return d.schemaSQL(c)
}

func (c CreateTable) statements(d Dialect) ([]Statement, error)    { return schemaStatements(d, c) }
func (c AddColumn) statements(d Dialect) ([]Statement, error)      { return schemaStatements(d, c) }
func (c AlterColumn) statements(d Dialect) ([]Statement, error)    { return schemaStatements(d, c) }
func (c DropColumn) statements(d Dialect) ([]Statement, error)     { return schemaStatements(d, c) }
func (c RenameRelation) statements(d Dialect) ([]Statement, error) { return schemaStatements(d, c) }
func (c DropRelation) statements(d Dialect) ([]Statement, error)   { return schemaStatements(d, c) }
func (c TruncateTable) statements(d Dialect) ([]Statement, error)  { return schemaStatements(d, c) }
func (c CreateIndex) statements(d Dialect) ([]Statement, error)    { return schemaStatements(d, c) }
func (c DropIndex) statements(d Dialect) ([]Statement, error)      { return schemaStatements(d, c) }

// SchemaSQL renders a schema change for a dialect without a session —
// the preview a test or a stage-time log line wants. Driver.SchemaSQL is
// the session-aware version that also refuses on a read-only connection.
func SchemaSQL(d Dialect, c SchemaChange) ([]Statement, error) { return c.statements(d) }

func relationWord(k RelationKind) string {
	if k == RelationView {
		return "view"
	}
	return "table"
}

func describeDefault(def ColumnDefault) string {
	switch def.Kind {
	case DefaultNone:
		return "none"
	case DefaultNull:
		return "NULL"
	case DefaultText:
		return "'" + def.Value + "'"
	default:
		return def.Value
	}
}

// ---------- validation ----------

// validateSchemaChange checks every user-supplied value a change carries
// before any of it reaches statement text. Identifiers are quoted by the
// dialect whatever they hold, so they only need to be present; a type and
// a default expression are SQL fragments and are held to a grammar.
func validateSchemaChange(e Engine, c SchemaChange) error {
	switch c := c.(type) {
	case CreateTable:
		if err := needName("table", c.Table); err != nil {
			return err
		}
		if len(c.Columns) == 0 {
			return errors.New("a table needs at least one column")
		}
		seen := map[string]bool{}
		for _, col := range c.Columns {
			if err := validateColumnDef(e, col); err != nil {
				return err
			}
			if seen[col.Name] {
				return fmt.Errorf("column %q is listed twice", col.Name)
			}
			seen[col.Name] = true
		}
	case AddColumn:
		if err := needName("table", c.Table); err != nil {
			return err
		}
		return validateColumnDef(e, c.Column)
	case AlterColumn:
		if err := needName("column", c.newName()); err != nil {
			return err
		}
		if c.retyped() {
			if err := ValidateTypeName(c.Type); err != nil {
				return err
			}
		}
		if c.defaultChanged() {
			return validateDefault(e, c.Default)
		}
	case DropColumn:
		return needName("column", c.Column)
	case RenameRelation:
		if err := needName(relationWord(c.Kind), c.NewName); err != nil {
			return err
		}
		if c.NewName == c.Name {
			return ErrNoSchemaChange
		}
	case DropRelation:
		return needName(relationWord(c.Kind), c.Name)
	case TruncateTable:
		return needName("table", c.Table)
	case CreateIndex:
		if err := needName("index", c.Name); err != nil {
			return err
		}
		if len(c.Columns) == 0 {
			return errors.New("an index needs at least one column")
		}
		for _, col := range c.Columns {
			if err := needName("column", col); err != nil {
				return err
			}
		}
	case DropIndex:
		return needName("index", c.Name)
	}
	return nil
}

func validateColumnDef(e Engine, c ColumnDef) error {
	if err := needName("column", c.Name); err != nil {
		return err
	}
	if err := ValidateTypeName(c.Type); err != nil {
		return fmt.Errorf("column %s: %w", c.Name, err)
	}
	if c.Default.Kind == DefaultKeep {
		return fmt.Errorf("column %s: a new column has no default to keep", c.Name)
	}
	if err := validateDefault(e, c.Default); err != nil {
		return fmt.Errorf("column %s: %w", c.Name, err)
	}
	return nil
}

// needName rejects an empty identifier and one carrying a NUL byte, which
// no engine can store and some drivers truncate at. Everything else is
// the engine's to accept or refuse: QuoteIdent makes any text one
// identifier.
func needName(what, name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("the %s name is required", what)
	}
	if strings.ContainsRune(name, 0) {
		return fmt.Errorf("the %s name contains a NUL byte", what)
	}
	return nil
}

// typeNameChars is everything a type expression may contain: words,
// digits, spaces, the parentheses of a length or precision, the commas
// between arguments, array brackets and a dot for a schema-qualified
// type. No quote, no semicolon, no comment opener is among them.
var typeNameChars = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_ (),.\[\]]*$`)

// ValidateTypeName checks that a column type is a type expression and
// nothing more. Parentheses must balance and a comma may only appear
// inside them: a top-level comma would end the column definition and
// start another one the preview never named.
func ValidateTypeName(t string) error {
	t = strings.TrimSpace(t)
	if t == "" {
		return errors.New("a column type is required")
	}
	if !typeNameChars.MatchString(t) {
		return fmt.Errorf("type %q may only contain letters, digits, spaces, _ ( ) , . [ ]", t)
	}
	depth := 0
	for _, r := range t {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return fmt.Errorf("type %q has an unbalanced ')'", t)
			}
		case ',':
			if depth == 0 {
				return fmt.Errorf("type %q has a comma outside parentheses", t)
			}
		}
	}
	if depth != 0 {
		return fmt.Errorf("type %q has an unclosed '('", t)
	}
	return nil
}

var numberLiteral = regexp.MustCompile(`^[+-]?(\d+(\.\d*)?|\.\d+)([eE][+-]?\d+)?$`)

func validateDefault(e Engine, def ColumnDefault) error {
	switch def.Kind {
	case DefaultNumber:
		if !numberLiteral.MatchString(strings.TrimSpace(def.Value)) {
			return fmt.Errorf("default %q is not a number", def.Value)
		}
	case DefaultExpr:
		return ValidateExpression(e, def.Value)
	case DefaultText:
		if strings.ContainsRune(def.Value, 0) {
			return errors.New("the default contains a NUL byte")
		}
	}
	return nil
}

// ValidateExpression checks that a default expression is one expression
// that stays inside the clause it is put in. It is read with the same
// tokenizer the rest of lazysql uses, so a `;` or a `--` inside a string
// literal is data while one outside it is refused:
//
//   - no comment (it would swallow the rest of the statement);
//   - no `;` outside a literal (it would end the statement);
//   - parentheses that balance without ever closing more than they
//     opened, and no comma outside them (either would end the column
//     definition early);
//   - no literal or quoted identifier left open, which is checked by
//     appending a sentinel and making sure it is still a token of its own.
func ValidateExpression(e Engine, expr string) error {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return errors.New("a default expression is required")
	}
	const sentinel = " )"
	src := expr + sentinel
	toks := sqlhl.Tokenize(sqlhl.For(string(e)), src)
	if n := len(toks); n == 0 || toks[n-1].Kind != sqlhl.Operator || toks[n-1].Text(src) != ")" ||
		toks[n-1].End != len(src) {
		return fmt.Errorf("expression %q leaves a string, identifier or comment open", expr)
	}
	depth := 0
	for _, t := range toks[:len(toks)-1] {
		switch t.Kind {
		case sqlhl.Comment:
			return fmt.Errorf("expression %q contains a comment", expr)
		case sqlhl.Operator:
			for _, r := range t.Text(src) {
				switch r {
				case ';':
					return fmt.Errorf("expression %q contains a ';'", expr)
				case '(':
					depth++
				case ')':
					depth--
					if depth < 0 {
						return fmt.Errorf("expression %q closes a parenthesis it never opened", expr)
					}
				case ',':
					if depth == 0 {
						return fmt.Errorf("expression %q has a comma outside parentheses", expr)
					}
				}
			}
		}
	}
	if depth != 0 {
		return fmt.Errorf("expression %q leaves a parenthesis open", expr)
	}
	return nil
}

// ---------- shared rendering ----------

// ddl wraps one statement text; DDL carries no bound parameters, because
// no engine accepts them there — every value is an escaped literal.
func ddl(sql string) Statement { return Statement{SQL: sql} }

// defaultClause renders " DEFAULT …" including its leading space, or ""
// for DefaultNone. Text goes through QuoteLiteral, the same per-dialect
// escaping the copy and export flows use.
func defaultClause(d Dialect, def ColumnDefault) string {
	if v, ok := defaultValue(d, def); ok {
		return " DEFAULT " + v
	}
	return ""
}

// defaultValue renders the value part of a DEFAULT clause, reporting
// false when there is none.
func defaultValue(d Dialect, def ColumnDefault) (string, bool) {
	switch def.Kind {
	case DefaultText:
		return QuoteLiteral(d, def.Value), true
	case DefaultNumber:
		return strings.TrimSpace(def.Value), true
	case DefaultExpr:
		if e := engineOf(d); e == EngineMySQL || e == EngineMariaDB {
			return mysqlExprDefault(def.Value), true
		}
		return strings.TrimSpace(def.Value), true
	case DefaultNull:
		return "NULL", true
	}
	return "", false
}

// columnDefSQL renders one column definition: name, type, NOT NULL and
// DEFAULT. The primary key is a table constraint, rendered by the caller.
func columnDefSQL(d Dialect, c ColumnDef) string {
	var b strings.Builder
	b.WriteString(d.QuoteIdent(c.Name))
	b.WriteString(" ")
	b.WriteString(strings.TrimSpace(c.Type))
	if c.NotNull {
		b.WriteString(" NOT NULL")
	}
	b.WriteString(defaultClause(d, c.Default))
	return b.String()
}

func createTableSQL(d Dialect, c CreateTable) Statement {
	var b strings.Builder
	b.WriteString("CREATE TABLE ")
	b.WriteString(qualifiedTable(d, c.Database, c.Table))
	b.WriteString(" (")
	var pk []string
	for i, col := range c.Columns {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(columnDefSQL(d, col))
		if col.PrimaryKey {
			pk = append(pk, col.Name)
		}
	}
	if len(pk) > 0 {
		b.WriteString(", PRIMARY KEY (" + quoteAll(d, pk) + ")")
	}
	b.WriteString(")")
	return ddl(b.String())
}

func addColumnSQL(d Dialect, c AddColumn) Statement {
	return ddl("ALTER TABLE " + qualifiedTable(d, c.Database, c.Table) +
		" ADD COLUMN " + columnDefSQL(d, c.Column))
}

func dropColumnSQL(d Dialect, c DropColumn) Statement {
	return ddl("ALTER TABLE " + qualifiedTable(d, c.Database, c.Table) +
		" DROP COLUMN " + d.QuoteIdent(c.Column))
}

func renameColumnSQL(d Dialect, database, table, from, to string) Statement {
	return ddl("ALTER TABLE " + qualifiedTable(d, database, table) +
		" RENAME COLUMN " + d.QuoteIdent(from) + " TO " + d.QuoteIdent(to))
}

// alterRenameSQL is the ALTER TABLE/VIEW … RENAME TO spelling. The new
// name is never qualified: every engine that spells it this way keeps the
// relation in the namespace it was in and rejects a qualified target.
func alterRenameSQL(d Dialect, c RenameRelation) Statement {
	return ddl("ALTER " + strings.ToUpper(relationWord(c.Kind)) + " " +
		qualifiedTable(d, c.Database, c.Name) + " RENAME TO " + d.QuoteIdent(c.NewName))
}

func dropRelationSQL(d Dialect, c DropRelation) Statement {
	return ddl("DROP " + strings.ToUpper(relationWord(c.Kind)) + " " +
		qualifiedTable(d, c.Database, c.Name))
}

func truncateSQL(d Dialect, c TruncateTable) Statement {
	return ddl("TRUNCATE TABLE " + qualifiedTable(d, c.Database, c.Table))
}

// createIndexSQL is CREATE INDEX with the table qualified and the index
// name bare — the index lands in the table's namespace.
func createIndexSQL(d Dialect, c CreateIndex) Statement {
	return ddl(createIndexHead(c) + d.QuoteIdent(c.Name) + " ON " +
		qualifiedTable(d, c.Database, c.Table) + " (" + quoteAll(d, c.Columns) + ")")
}

func createIndexHead(c CreateIndex) string {
	if c.Unique {
		return "CREATE UNIQUE INDEX "
	}
	return "CREATE INDEX "
}

// dropQualifiedIndexSQL is DROP INDEX namespace.index, for the engines
// whose indexes live in a namespace rather than on a table.
func dropQualifiedIndexSQL(d Dialect, c DropIndex) Statement {
	return ddl("DROP INDEX " + qualifiedTable(d, c.Database, c.Name))
}

// schemaSQLCommon renders the operations every engine spells the same
// way. A dialect's schemaSQL handles its own differences and hands the
// rest to this.
func schemaSQLCommon(d Dialect, c SchemaChange) ([]Statement, error) {
	switch c := c.(type) {
	case CreateTable:
		return []Statement{createTableSQL(d, c)}, nil
	case AddColumn:
		return []Statement{addColumnSQL(d, c)}, nil
	case DropColumn:
		return []Statement{dropColumnSQL(d, c)}, nil
	case RenameRelation:
		return []Statement{alterRenameSQL(d, c)}, nil
	case DropRelation:
		return []Statement{dropRelationSQL(d, c)}, nil
	case TruncateTable:
		return []Statement{truncateSQL(d, c)}, nil
	case CreateIndex:
		return []Statement{createIndexSQL(d, c)}, nil
	case DropIndex:
		return []Statement{dropQualifiedIndexSQL(d, c)}, nil
	}
	return nil, unsupported("%s: %T is not a schema change this dialect knows", d.DisplayName(), c)
}

// alterColumnSeparate renders an alter as one statement per aspect, the
// shape DuckDB needs (one action per ALTER TABLE): type, nullability and
// default on the old name first, the rename last, so no statement refers
// to a name that does not exist yet.
func alterColumnSeparate(d Dialect, c AlterColumn, setType string) []Statement {
	table := qualifiedTable(d, c.Database, c.Table)
	col := d.QuoteIdent(c.Old.Name)
	var out []Statement
	if c.retyped() {
		out = append(out, ddl("ALTER TABLE "+table+" ALTER COLUMN "+col+" "+setType+" "+c.newType()))
	}
	if c.nullChanged() {
		verb := "DROP NOT NULL"
		if c.NotNull {
			verb = "SET NOT NULL"
		}
		out = append(out, ddl("ALTER TABLE "+table+" ALTER COLUMN "+col+" "+verb))
	}
	if c.defaultChanged() {
		out = append(out, ddl("ALTER TABLE "+table+" ALTER COLUMN "+col+" "+setDefaultClause(d, c.Default)))
	}
	if c.renamed() {
		out = append(out, renameColumnSQL(d, c.Database, c.Table, c.Old.Name, c.newName()))
	}
	return out
}

// setDefaultClause is the SET DEFAULT / DROP DEFAULT half of an ALTER
// COLUMN.
func setDefaultClause(d Dialect, def ColumnDefault) string {
	if v, ok := defaultValue(d, def); ok {
		return "SET DEFAULT " + v
	}
	return "DROP DEFAULT"
}

// ChangeTarget names the relation a staged change applies to — for a
// CREATE TABLE the table it will create, for a rename the old name. The
// UI uses it to mark staged relations and to know which namespaces to
// re-read after a commit.
func ChangeTarget(c Change) (database, table string) { return c.target() }
