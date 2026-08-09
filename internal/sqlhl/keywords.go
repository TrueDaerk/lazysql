package sqlhl

import (
	"sort"
	"strings"
	"sync"
)

// The keyword sets are a shared core plus per-dialect extras. The core is
// what every engine lazysql speaks understands; the extras are the words
// that would otherwise read as ordinary identifiers on that engine
// (`AUTOINCREMENT` on SQLite, `QUALIFY` on DuckDB, `ILIKE` on Postgres).
//
// They are highlighting lists, not grammars: type names are in them
// because a `CREATE TABLE` reads better with the types picked out, and
// nothing downstream treats membership as "this word is reserved".

var coreKeywords = strings.Fields(`
ADD ALL ALTER ANALYZE AND ANY AS ASC ATTACH BEGIN BETWEEN BY CASCADE CASE
CAST CHECK COLLATE COLUMN COMMENT COMMIT CONFLICT CONSTRAINT CREATE CROSS
CURRENT CURRENT_DATE CURRENT_TIME CURRENT_TIMESTAMP DATABASE DEFAULT
DEFERRABLE DELETE DESC DETACH DISTINCT DO DROP ELSE END ESCAPE EXCEPT
EXCLUDE EXISTS EXPLAIN FALSE FETCH FILTER FIRST FOLLOWING FOR FOREIGN FROM
FULL GRANT GROUP GROUPS HAVING IF IN INDEX INNER INSERT INTERSECT INTO IS
ISNULL JOIN KEY LAST LATERAL LEFT LIKE LIMIT MATERIALIZED NATURAL NO NOT
NOTHING NOTNULL NULL NULLS OFFSET ON ONLY OR ORDER OUTER OVER PARTITION
PRECEDING PRIMARY RANGE RECURSIVE REFERENCES RENAME REPLACE RESTRICT
RETURNING REVOKE RIGHT ROLLBACK ROW ROWS SAVEPOINT SCHEMA SELECT SET SOME
TABLE TEMPORARY THEN TO TRANSACTION TRIGGER TRUE TRUNCATE UNBOUNDED UNION
UNIQUE UPDATE USING VACUUM VALUES VIEW WHEN WHERE WINDOW WITH

BIGINT BLOB BOOL BOOLEAN CHAR CHARACTER DATE DATETIME DECIMAL DOUBLE FLOAT
INT INTEGER INTERVAL JSON NUMERIC PRECISION REAL SMALLINT TEXT TIME
TIMESTAMP TINYINT UUID VARCHAR
`)

var dialectKeywords = map[Dialect][]string{
	MySQL: strings.Fields(`
AUTO_INCREMENT CHANGE CHARSET COLLATION DATABASES DESCRIBE DUAL DUPLICATE
ENGINE ENUM FULLTEXT IGNORE INDEXES LOCK LONGBLOB LONGTEXT MEDIUMINT
MEDIUMTEXT MODIFY SEPARATOR SHOW SPATIAL STRAIGHT_JOIN TABLES TINYTEXT
UNLOCK UNSIGNED YEAR ZEROFILL
`),
	Postgres: strings.Fields(`
ALWAYS ARRAY BIGSERIAL BYTEA CONCURRENTLY EXTENSION GENERATED IDENTITY
ILIKE INHERITS JSONB LANGUAGE OWNER PROCEDURE RETURNS SEQUENCE SERIAL
SETOF SIMILAR TABLESAMPLE TEXTSEARCH TIMESTAMPTZ TSQUERY TSVECTOR UNLOGGED
`),
	SQLite: strings.Fields(`
ABORT AUTOINCREMENT DEFERRED EXCLUSIVE FAIL GLOB IMMEDIATE INDEXED
INSTEAD PRAGMA RAISE REGEXP REINDEX ROWID TEMP VIRTUAL WITHOUT
`),
	DuckDB: strings.Fields(`
ANTI ASOF COPY EXPORT IMPORT INSTALL LIST LOAD MACRO MAP PIVOT POSITIONAL
PRAGMA QUALIFY SAMPLE SEMI SEQUENCE STRUCT SUMMARIZE UNPIVOT VARARGS
`),
}

var (
	keywordOnce sync.Once
	keywordSets map[Dialect]map[string]bool
)

// keywords returns the (shared, read-only) keyword set for a dialect.
// The sets are built once: highlighting runs on every keystroke, and
// rebuilding four maps per redraw would be the most expensive thing the
// tokenizer does.
func keywords(d Dialect) map[string]bool {
	keywordOnce.Do(func() {
		keywordSets = map[Dialect]map[string]bool{}
		for _, dialect := range []Dialect{Generic, MySQL, Postgres, SQLite, DuckDB} {
			set := make(map[string]bool, len(coreKeywords)+len(dialectKeywords[dialect]))
			for _, w := range coreKeywords {
				set[w] = true
			}
			for _, w := range dialectKeywords[dialect] {
				set[w] = true
			}
			keywordSets[dialect] = set
		}
	})
	if set, ok := keywordSets[d]; ok {
		return set
	}
	return keywordSets[Generic]
}

var (
	keywordListOnce sync.Once
	keywordLists    map[Dialect][]string
)

// Keywords returns the dialect's keyword set as a sorted, uppercase list.
// It is the same set the highlighter matches against: a word worth
// colouring is a word worth offering in the editor's completion popup,
// and keeping one list means a dialect gains both at once.
//
// The result is shared and must not be modified.
func Keywords(d Dialect) []string {
	keywordListOnce.Do(func() {
		keywordLists = map[Dialect][]string{}
		for _, dialect := range []Dialect{Generic, MySQL, Postgres, SQLite, DuckDB} {
			set := keywords(dialect)
			list := make([]string, 0, len(set))
			for w := range set {
				list = append(list, w)
			}
			sort.Strings(list)
			keywordLists[dialect] = list
		}
	})
	if list, ok := keywordLists[d]; ok {
		return list
	}
	return keywordLists[Generic]
}

// IsKeyword reports whether word is a keyword of the dialect, ignoring
// case. The completion popup asks before it inserts an identifier bare:
// a table called `order` has to come back quoted.
//
// Like the set behind it this is generous — it holds type names too — so
// it can say yes to a word no engine actually reserves. Quoting an
// identifier that did not need it is still valid SQL; the other mistake
// is a syntax error.
func IsKeyword(d Dialect, word string) bool { return keywords(d)[strings.ToUpper(word)] }
