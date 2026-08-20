package sqlhl

import (
	"sort"
	"strings"
	"sync"
)

// The function catalog is a shared core plus per-dialect extras, the same
// shape as the keyword lists — but it is a *different* list, kept apart on
// purpose. A function name is not a keyword: `LENGTH` still has to read as
// an ordinary identifier when it names a column, so it must never enter
// the set sqlhl.IsKeyword or the highlighter's keyword-matching consult.
// This file exists only to feed the completion popup's function
// suggestions.
//
// Coverage is the commonly used built-ins per dialect — aggregates,
// string, numeric, date/time, conditional, cast, JSON where the dialect
// has it — sourced from each engine's function reference, not guessed.
// Completeness beyond that is a non-goal: see
// wiki/reference/sql-function-catalog.md for the source list and the
// coverage decisions per dialect.
// CAST and REPLACE are left out of the core list even though every
// dialect has them as functions: both are already core keywords (`CAST(x
// AS int)` is keyword syntax; `REPLACE` doubles as the upsert clause), so
// they already reach the popup — as keyword suggestions — without an
// entry here. The same reasoning drops LEFT/RIGHT/TRUNCATE (MySQL),
// LEFT/RIGHT (Postgres) and DATE/TIME/DATETIME/GLOB (SQLite) below: each
// collides with a keyword already offered for that dialect.
var coreFunctions = strings.Fields(`
COUNT SUM AVG MIN MAX
COALESCE NULLIF
UPPER LOWER LENGTH TRIM LTRIM RTRIM SUBSTR CONCAT
ABS ROUND CEIL FLOOR POWER SQRT MOD
`)

var dialectFunctions = map[Dialect][]string{
	MySQL: strings.Fields(`
GROUP_CONCAT IFNULL CONVERT
DATE_FORMAT DATE_ADD DATE_SUB DATEDIFF NOW CURDATE CURTIME
MONTH DAY HOUR MINUTE SECOND STR_TO_DATE UNIX_TIMESTAMP FROM_UNIXTIME
LPAD RPAD LOCATE INSTR REPEAT REVERSE FORMAT CHAR_LENGTH
RAND GREATEST LEAST
JSON_EXTRACT JSON_OBJECT JSON_ARRAY JSON_VALID
`),
	Postgres: strings.Fields(`
STRING_AGG ARRAY_AGG IFNULL
TO_CHAR TO_DATE TO_TIMESTAMP TO_NUMBER DATE_TRUNC DATE_PART EXTRACT AGE NOW
GENERATE_SERIES LPAD RPAD POSITION SPLIT_PART
REGEXP_REPLACE REGEXP_MATCH GREATEST LEAST UNNEST ARRAY_LENGTH
JSON_BUILD_OBJECT JSONB_BUILD_OBJECT JSON_AGG JSONB_AGG
`),
	SQLite: strings.Fields(`
GROUP_CONCAT IFNULL IIF INSTR
RANDOM RANDOMBLOB HEX QUOTE ZEROBLOB TYPEOF
STRFTIME JULIANDAY UNIXEPOCH
JSON_EXTRACT JSON_ARRAY JSON_OBJECT JSON_VALID
`),
	DuckDB: strings.Fields(`
LIST STRING_AGG ARRAY_AGG IFNULL LIST_VALUE UNNEST
STRFTIME STRPTIME DATE_TRUNC DATE_PART DATEDIFF EPOCH EPOCH_MS
MAKE_DATE MAKE_TIME MAKE_TIMESTAMP
REGEXP_MATCHES REGEXP_REPLACE REGEXP_EXTRACT SPLIT_PART
LIST_EXTRACT LIST_CONTAINS STRUCT_PACK GREATEST LEAST
TO_JSON JSON_EXTRACT ARRAY_LENGTH
`),
}

var (
	functionListOnce sync.Once
	functionLists    map[Dialect][]string
)

// Functions returns the dialect's built-in function catalog as a sorted,
// uppercase list — the shared core plus that dialect's extras. The result
// is shared and must not be modified.
func Functions(d Dialect) []string {
	functionListOnce.Do(func() {
		functionLists = map[Dialect][]string{}
		for _, dialect := range []Dialect{Generic, MySQL, Postgres, SQLite, DuckDB} {
			set := make(map[string]bool, len(coreFunctions)+len(dialectFunctions[dialect]))
			for _, w := range coreFunctions {
				set[w] = true
			}
			for _, w := range dialectFunctions[dialect] {
				set[w] = true
			}
			list := make([]string, 0, len(set))
			for w := range set {
				list = append(list, w)
			}
			sort.Strings(list)
			functionLists[dialect] = list
		}
	})
	if list, ok := functionLists[d]; ok {
		return list
	}
	return functionLists[Generic]
}
