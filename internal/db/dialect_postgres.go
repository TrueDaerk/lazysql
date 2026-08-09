package db

import (
	"context"
	"fmt"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func init() {
	register(postgresDialect{})
}

type postgresDialect struct{}

func (postgresDialect) Engine() Engine      { return EnginePostgres }
func (postgresDialect) DisplayName() string { return "PostgreSQL" }
func (postgresDialect) driverName() string  { return "pgx" }

func (postgresDialect) QuoteIdent(ident string) string {
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}

func (postgresDialect) Placeholder(n int) string { return fmt.Sprintf("$%d", n) }

func (postgresDialect) LimitOffset(limit, offset int) string {
	return fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)
}

// PostgreSQL cannot query across databases on one connection, so the
// browsable namespaces are the schemas of the connected database.
func (postgresDialect) listDatabases(ctx context.Context, q querier) ([]string, error) {
	return scanStrings(ctx, q,
		`SELECT nspname FROM pg_catalog.pg_namespace
		 WHERE nspname NOT LIKE 'pg\_%' AND nspname <> 'information_schema'
		 ORDER BY nspname`)
}

func pgSchema(database string) string {
	if database == "" {
		return "public"
	}
	return database
}

func (postgresDialect) listRelations(ctx context.Context, q querier, database string) ([]Relation, error) {
	return scanRelations(ctx, q,
		`SELECT table_name, table_type FROM information_schema.tables
		 WHERE table_schema = $1 ORDER BY table_name`,
		pgSchema(database))
}

func (d postgresDialect) tableColumns(ctx context.Context, q querier, database, table string) ([]Column, error) {
	schema := pgSchema(database)
	rows, err := q.QueryContext(ctx,
		`SELECT c.column_name, c.data_type, c.is_nullable, c.column_default,
		        c.is_identity, c.identity_generation, c.is_generated,
		        EXISTS (
		          SELECT 1 FROM pg_index i
		          JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY (i.indkey)
		          WHERE i.indrelid = to_regclass($3) AND i.indisprimary
		            AND a.attname = c.column_name
		        )
		 FROM information_schema.columns c
		 WHERE c.table_schema = $1 AND c.table_name = $2
		 ORDER BY c.ordinal_position`,
		schema, table, d.QuoteIdent(schema)+"."+d.QuoteIdent(table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cols []Column
	for rows.Next() {
		var name, typ, nullable string
		var def, identityGen *string
		var isIdentity, isGenerated string
		var pk bool
		if err := rows.Scan(&name, &typ, &nullable, &def,
			&isIdentity, &identityGen, &isGenerated, &pk); err != nil {
			return nil, err
		}
		cols = append(cols, Column{
			Name:       name,
			DataType:   typ,
			Nullable:   nullable == "YES",
			Default:    def,
			PrimaryKey: pk,
			Extra:      pgColumnExtra(isIdentity, derefOr(identityGen, ""), isGenerated, def),
		})
	}
	return cols, rows.Err()
}

// pgColumnExtra spells out how PostgreSQL produces a column's value.
// Identity columns and generated columns say so directly; the older
// serial types only show up as a nextval() default.
func pgColumnExtra(isIdentity, identityGen, isGenerated string, def *string) string {
	switch {
	case isIdentity == "YES":
		if identityGen != "" {
			return "identity " + strings.ToLower(identityGen)
		}
		return "identity"
	case isGenerated == "ALWAYS":
		return "generated"
	case def != nil && strings.HasPrefix(strings.ToLower(*def), "nextval("):
		return "serial"
	}
	return ""
}

// pgReferentialActions maps pg_constraint's single-char confupdtype and
// confdeltype codes onto their SQL spelling.
var pgReferentialActions = map[string]string{
	"a": "NO ACTION",
	"r": "RESTRICT",
	"c": "CASCADE",
	"n": "SET NULL",
	"d": "SET DEFAULT",
}

func (d postgresDialect) tableForeignKeys(ctx context.Context, q querier, database, table string) ([]ForeignKey, error) {
	schema := pgSchema(database)
	// conkey and confkey are positionally paired arrays, so they are
	// unnested together: row i of the pair is key column i.
	rows, err := q.QueryContext(ctx,
		// confupdtype/confdeltype are of the internal "char" type; the
		// cast keeps the pgx decoder on the plain-string path.
		`SELECT c.conname, a.attname, ns.nspname, cl.relname, af.attname,
		        c.confupdtype::text, c.confdeltype::text
		 FROM pg_constraint c
		 JOIN LATERAL unnest(c.conkey, c.confkey) WITH ORDINALITY AS k(att, fatt, ord) ON true
		 JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = k.att
		 JOIN pg_class cl ON cl.oid = c.confrelid
		 JOIN pg_namespace ns ON ns.oid = cl.relnamespace
		 JOIN pg_attribute af ON af.attrelid = c.confrelid AND af.attnum = k.fatt
		 WHERE c.conrelid = to_regclass($1) AND c.contype = 'f'
		 ORDER BY c.conname, k.ord`,
		d.QuoteIdent(schema)+"."+d.QuoteIdent(table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var fks []ForeignKey
	for rows.Next() {
		var name, col, refSchema, refTable, refCol, upd, del string
		if err := rows.Scan(&name, &col, &refSchema, &refTable, &refCol, &upd, &del); err != nil {
			return nil, err
		}
		if refSchema != schema {
			refTable = refSchema + "." + refTable
		}
		fk := ForeignKey{
			Name:     name,
			RefTable: refTable,
			OnUpdate: pgReferentialActions[upd],
			OnDelete: pgReferentialActions[del],
		}
		fks = appendFKColumn(fks, fk, col, refCol)
	}
	return fks, rows.Err()
}

func (d postgresDialect) tableIndexes(ctx context.Context, q querier, database, table string) ([]Index, error) {
	schema := pgSchema(database)
	rows, err := q.QueryContext(ctx,
		`SELECT ci.relname, a.attname, i.indisunique, i.indisprimary
		 FROM pg_index i
		 JOIN pg_class ci ON ci.oid = i.indexrelid
		 JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY (i.indkey)
		 WHERE i.indrelid = to_regclass($1)
		 ORDER BY ci.relname, a.attnum`,
		d.QuoteIdent(schema)+"."+d.QuoteIdent(table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var idx []Index
	for rows.Next() {
		var name, col string
		var unique, primary bool
		if err := rows.Scan(&name, &col, &unique, &primary); err != nil {
			return nil, err
		}
		if n := len(idx); n > 0 && idx[n-1].Name == name {
			idx[n-1].Columns = append(idx[n-1].Columns, col)
			continue
		}
		idx = append(idx, Index{Name: name, Columns: []string{col}, Unique: unique, Primary: primary})
	}
	return idx, rows.Err()
}

// PostgreSQL does not keep the original CREATE TABLE text, so the DDL
// is synthesized from the introspected columns, indexes and foreign
// keys. It is a faithful description, not a byte-identical replay of
// what the table was created with.
func (d postgresDialect) tableDDL(ctx context.Context, q querier, database, table string) (string, error) {
	cols, err := d.tableColumns(ctx, q, database, table)
	if err != nil {
		return "", err
	}
	if len(cols) == 0 {
		return "", fmt.Errorf("db: no DDL for table %q", table)
	}
	idx, err := d.tableIndexes(ctx, q, database, table)
	if err != nil {
		return "", err
	}
	fks, err := d.tableForeignKeys(ctx, q, database, table)
	if err != nil {
		return "", err
	}
	return synthesizeDDL(d, pgSchema(database), table, cols, idx, fks), nil
}
