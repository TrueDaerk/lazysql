package db

import (
	"strings"
)

// Per-engine DDL. Each dialect answers two questions: can it do a kind of
// operation at all (schemaSupport), and how does it spell the ones whose
// syntax differs from the shared forms in ddl.go (schemaSQL). The
// differences are collected in this one file so they read side by side;
// see wiki/reference/ddl-per-dialect.md.

// ---------- PostgreSQL ----------

func (postgresDialect) schemaSupport(SchemaOp) error { return nil }

func (d postgresDialect) schemaSQL(c SchemaChange) ([]Statement, error) {
	if c, ok := c.(AlterColumn); ok {
		return pgAlterColumn(d, c), nil
	}
	// DROP INDEX schema.index and ALTER TABLE/VIEW … RENAME TO are the
	// shared spellings already.
	return schemaSQLCommon(d, c)
}

// pgAlterColumn folds type, nullability and default into one ALTER TABLE
// with comma-separated actions — PostgreSQL applies them together — and
// renames last, in a statement of its own: RENAME COLUMN cannot be
// combined with other actions.
func pgAlterColumn(d Dialect, c AlterColumn) []Statement {
	table := qualifiedTable(d, c.Database, c.Table)
	col := d.QuoteIdent(c.Old.Name)
	var actions []string
	if c.retyped() {
		actions = append(actions, "ALTER COLUMN "+col+" TYPE "+c.newType())
	}
	if c.nullChanged() {
		if c.NotNull {
			actions = append(actions, "ALTER COLUMN "+col+" SET NOT NULL")
		} else {
			actions = append(actions, "ALTER COLUMN "+col+" DROP NOT NULL")
		}
	}
	if c.defaultChanged() {
		actions = append(actions, "ALTER COLUMN "+col+" "+setDefaultClause(d, c.Default))
	}
	var out []Statement
	if len(actions) > 0 {
		out = append(out, ddl("ALTER TABLE "+table+" "+strings.Join(actions, ", ")))
	}
	if c.renamed() {
		out = append(out, renameColumnSQL(d, c.Database, c.Table, c.Old.Name, c.newName()))
	}
	return out
}

// ---------- MySQL / MariaDB ----------

// AlterRewritesColumn reports whether changing a column's type or
// nullability restates the whole column definition, dropping whatever the
// new definition does not repeat. The alter form warns about it.
func AlterRewritesColumn(e Engine) bool { return e == EngineMySQL || e == EngineMariaDB }

func (mysqlDialect) schemaSupport(SchemaOp) error { return nil }

func (d mysqlDialect) schemaSQL(c SchemaChange) ([]Statement, error) {
	switch c := c.(type) {
	case AlterColumn:
		return mysqlAlterColumn(d, c), nil
	case RenameRelation:
		// RENAME TABLE covers views too, and keeps the relation in its
		// database only when the target says so — hence the qualified
		// new name.
		return []Statement{ddl("RENAME TABLE " + qualifiedTable(d, c.Database, c.Name) +
			" TO " + qualifiedTable(d, c.Database, c.NewName))}, nil
	case DropIndex:
		// A MySQL index belongs to its table, not to the schema.
		return []Statement{ddl("DROP INDEX " + d.QuoteIdent(c.Name) +
			" ON " + qualifiedTable(d, c.Database, c.Table))}, nil
	}
	return schemaSQLCommon(d, c)
}

// mysqlAlterColumn touches as little of the column as it can. MySQL has
// no way to change only a column's type or nullability: MODIFY and CHANGE
// replace the whole definition, and whatever the new one leaves out —
// the default, AUTO_INCREMENT, ON UPDATE — is gone. So:
//
//   - a default-only or rename-only change uses ALTER COLUMN … SET/DROP
//     DEFAULT and RENAME COLUMN, which leave the rest of the column alone;
//   - a type or nullability change uses one CHANGE COLUMN (which renames
//     as well), re-stating the kept default, AUTO_INCREMENT and ON UPDATE
//     from what introspection reported. The column's comment, character
//     set and collation are not re-stated; the form warns about that.
func mysqlAlterColumn(d mysqlDialect, c AlterColumn) []Statement {
	table := qualifiedTable(d, c.Database, c.Table)
	col := d.QuoteIdent(c.Old.Name)
	if c.retyped() || c.nullChanged() {
		var b strings.Builder
		b.WriteString("ALTER TABLE " + table + " CHANGE COLUMN " + col + " " +
			d.QuoteIdent(c.newName()) + " " + c.newType())
		if c.NotNull {
			b.WriteString(" NOT NULL")
		} else {
			b.WriteString(" NULL")
		}
		if c.defaultChanged() {
			b.WriteString(defaultClause(d, c.Default))
		} else {
			b.WriteString(mysqlKeptDefault(d, c.Old))
		}
		extra := strings.ToLower(c.Old.Extra)
		if strings.Contains(extra, "auto_increment") {
			b.WriteString(" AUTO_INCREMENT")
		}
		if i := strings.Index(extra, "on update "); i >= 0 {
			b.WriteString(" ON UPDATE " + strings.TrimSpace(c.Old.Extra[i+len("on update "):]))
		}
		return []Statement{ddl(b.String())}
	}
	var out []Statement
	if c.defaultChanged() {
		out = append(out, ddl("ALTER TABLE "+table+" ALTER COLUMN "+col+" "+setDefaultClause(d, c.Default)))
	}
	if c.renamed() {
		out = append(out, renameColumnSQL(d, c.Database, c.Table, c.Old.Name, c.newName()))
	}
	return out
}

// mysqlKeptDefault re-states a column's current default for a CHANGE
// COLUMN that is not meant to touch it. The two engines report it
// differently in information_schema.COLUMNS.COLUMN_DEFAULT:
//
//   - MariaDB (10.2.7+) reports SQL — a quoted literal, NULL, or an
//     expression — so it is re-stated as reported;
//   - MySQL reports a literal *unquoted*, and an expression bare with
//     DEFAULT_GENERATED in EXTRA; the literal is re-quoted here and the
//     expression goes through the same spelling a typed one does.
func mysqlKeptDefault(d mysqlDialect, old Column) string {
	if old.Default == nil {
		return ""
	}
	v := *old.Default
	if d.engine == EngineMariaDB {
		return " DEFAULT " + v
	}
	if strings.Contains(strings.ToLower(old.Extra), "default_generated") {
		return " DEFAULT " + mysqlExprDefault(v)
	}
	return " DEFAULT " + QuoteLiteral(d, v)
}

// mysqlExprDefault spells an expression default the way MySQL 8 wants
// it: parenthesized, except the CURRENT_TIMESTAMP family, which has been
// a bare default for DATETIME/TIMESTAMP since long before expression
// defaults existed and is still the spelling older servers accept.
// MariaDB reads the parenthesized form as the same expression.
func mysqlExprDefault(expr string) string {
	expr = strings.TrimSpace(expr)
	up := strings.ToUpper(expr)
	for _, bare := range []string{"CURRENT_TIMESTAMP", "NOW(", "LOCALTIME", "CURRENT_DATE", "CURRENT_TIME"} {
		if strings.HasPrefix(up, bare) {
			return expr
		}
	}
	if strings.HasPrefix(expr, "(") && strings.HasSuffix(expr, ")") {
		return expr
	}
	return "(" + expr + ")"
}

// ---------- SQLite ----------

func (sqliteDialect) schemaSupport(op SchemaOp) error {
	switch op {
	case OpAlterColumn:
		return unsupported("SQLite's ALTER TABLE cannot change a column's type, nullability or default — only rename it")
	case OpTruncateTable:
		return unsupported("SQLite has no TRUNCATE — stage row deletes, or run DELETE FROM in the query editor")
	case OpRenameView:
		return unsupported("SQLite cannot rename a view — drop it and create it again")
	}
	return nil
}

func (d sqliteDialect) schemaSQL(c SchemaChange) ([]Statement, error) {
	switch c := c.(type) {
	case AlterColumn:
		// schemaSupport let only a rename through.
		return []Statement{renameColumnSQL(d, c.Database, c.Table, c.Old.Name, c.newName())}, nil
	case CreateIndex:
		// SQLite qualifies the index, not the table: the index is created
		// in the named attached database, on a table of that database.
		name := d.QuoteIdent(c.Name)
		if c.Database != "" {
			name = d.QuoteIdent(c.Database) + "." + name
		}
		return []Statement{ddl(createIndexHead(c) + name + " ON " + d.QuoteIdent(c.Table) +
			" (" + quoteAll(d, c.Columns) + ")")}, nil
	}
	return schemaSQLCommon(d, c)
}

// ---------- DuckDB ----------

func (duckdbDialect) schemaSupport(SchemaOp) error { return nil }

func (d duckdbDialect) schemaSQL(c SchemaChange) ([]Statement, error) {
	switch c := c.(type) {
	case AlterColumn:
		// DuckDB takes one action per ALTER TABLE.
		return alterColumnSeparate(d, c, "TYPE"), nil
	case TruncateTable:
		return []Statement{ddl("TRUNCATE " + qualifiedTable(d, c.Database, c.Table))}, nil
	}
	return schemaSQLCommon(d, c)
}
