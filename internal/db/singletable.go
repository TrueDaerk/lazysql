package db

import "strings"

// A query-result export can only emit SQL/INSERT statements when the
// rows on screen still correspond 1:1 to rows of one real table and each
// selected item is that table's own column, not a computed one. This
// file decides that conservatively: anything it cannot prove safe it
// reports false for, and the export flow then simply does not offer the
// SQL format — see wiki/design/copy-and-export.md.

// SingleTableSelect reports the one table a plain
// `SELECT <cols> FROM <table> [WHERE ...] [ORDER BY ...] [LIMIT ...]`
// statement reads from, when every selected item is a bare (optionally
// qualified) column reference or `*`. It reports false for a JOIN, a
// comma-separated FROM list, a subquery, a UNION/INTERSECT/EXCEPT, a
// GROUP BY, a DISTINCT, or any selected item that is a function call, an
// expression or an alias — each of those breaks either the row
// correspondence or the column-to-table correspondence an INSERT needs.
func SingleTableSelect(engine Engine, sql string) (table string, ok bool) {
	toks := sqlTokens(engine, sql)
	i := 0
	if !eqKW(tokAt(toks, i), "SELECT") {
		return "", false
	}
	i++
	if eqKW(tokAt(toks, i), "DISTINCT") {
		return "", false
	}

	selStart := i
	fromIdx := -1
	depth := 0
	for ; i < len(toks); i++ {
		switch toks[i] {
		case "(":
			depth++
		case ")":
			depth--
		}
		if depth == 0 && eqKW(toks[i], "FROM") {
			fromIdx = i
		}
		if fromIdx >= 0 {
			break
		}
	}
	if fromIdx < 0 || !validSelectList(toks[selStart:fromIdx]) {
		return "", false
	}

	j := fromIdx + 1
	if tokAt(toks, j) == "(" {
		return "", false // subquery in FROM
	}
	table, j, ok = readQualifiedName(toks, j)
	if !ok {
		return "", false
	}

	// An optional table alias — `AS x` or a bare trailing identifier —
	// does not affect which columns map to which table, so it is
	// consumed rather than rejected.
	if eqKW(tokAt(toks, j), "AS") {
		j++
		if isIdent(tokAt(toks, j)) {
			j++
		}
	} else if isIdent(tokAt(toks, j)) && !isReservedFollowKeyword(tokAt(toks, j)) {
		j++
	}

	depth = 0
	for ; j < len(toks); j++ {
		t := toks[j]
		switch t {
		case "(":
			depth++
			continue
		case ")":
			depth--
			continue
		}
		if depth != 0 {
			continue
		}
		if t == "," || forbiddenAfterFrom[strings.ToUpper(t)] && !isQuoted(t) {
			return "", false
		}
	}
	return table, true
}

// forbiddenAfterFrom are the top-level keywords that, anywhere after the
// FROM table, mean the result no longer maps to that one table: a join
// brings in a second table, a set operator or GROUP BY changes what a
// row is.
var forbiddenAfterFrom = map[string]bool{
	"JOIN": true, "INNER": true, "LEFT": true, "RIGHT": true, "FULL": true,
	"CROSS": true, "NATURAL": true,
	"UNION": true, "INTERSECT": true, "EXCEPT": true,
	"GROUP": true,
}

// followKeywords are the clause keywords that can legally follow a table
// reference with no alias between them — WHERE, ORDER BY and so on.
// Anything else bare in that position is read as a trailing alias.
var followKeywords = map[string]bool{
	"WHERE": true, "GROUP": true, "ORDER": true, "LIMIT": true, "HAVING": true,
	"JOIN": true, "INNER": true, "LEFT": true, "RIGHT": true, "FULL": true,
	"CROSS": true, "NATURAL": true,
	"UNION": true, "INTERSECT": true, "EXCEPT": true,
	"WINDOW": true, "OFFSET": true, "FETCH": true, "FOR": true, "USING": true, "ON": true,
}

func isReservedFollowKeyword(tok string) bool {
	return !isQuoted(tok) && tok != "" && followKeywords[strings.ToUpper(tok)]
}

// validSelectList reports whether every top-level comma-separated item
// of a SELECT list is a bare `*`, a bare column, or a `table.column` /
// `table.*` pair — nothing with a function call, an operator or an
// alias, all of which stop the exported columns from being the real
// column names of one table.
func validSelectList(toks []string) bool {
	if len(toks) == 0 {
		return false
	}
	valid := func(item []string) bool {
		switch len(item) {
		case 1:
			return item[0] == "*" || isIdent(item[0])
		case 3:
			return isIdent(item[0]) && item[1] == "." && (item[2] == "*" || isIdent(item[2]))
		default:
			return false
		}
	}
	depth, start := 0, 0
	for i, t := range toks {
		switch t {
		case "(":
			depth++
		case ")":
			depth--
		}
		if depth == 0 && t == "," {
			if !valid(toks[start:i]) {
				return false
			}
			start = i + 1
		}
	}
	return valid(toks[start:])
}

// readQualifiedName reads a possibly dotted identifier chain
// (`schema.table`, `db.schema.table`) starting at i and returns its last
// segment — the table export cares about, since the database/schema
// portion is already carried separately as the connection's own current
// namespace.
func readQualifiedName(toks []string, i int) (name string, next int, ok bool) {
	if !isIdent(tokAt(toks, i)) {
		return "", i, false
	}
	name = unquoteIdent(toks[i])
	i++
	for tokAt(toks, i) == "." && isIdent(tokAt(toks, i+1)) {
		name = unquoteIdent(toks[i+1])
		i += 2
	}
	return name, i, true
}

func tokAt(toks []string, i int) string {
	if i < 0 || i >= len(toks) {
		return ""
	}
	return toks[i]
}

// eqKW reports whether tok is the keyword kw, case-insensitively. A
// quoted token never matches: a column genuinely named "from" must not
// be read as the FROM keyword.
func eqKW(tok, kw string) bool {
	return !isQuoted(tok) && strings.EqualFold(tok, kw)
}

func isQuoted(tok string) bool {
	return tok != "" && (tok[0] == '"' || tok[0] == '`')
}

// isIdent reports whether tok can stand as a column or table name: a
// quoted identifier (any content), or an unquoted word starting with a
// letter or underscore. A bare numeric or string-literal token
// (opaque-lexed as "?") is neither.
func isIdent(tok string) bool {
	if tok == "" {
		return false
	}
	if isQuoted(tok) {
		return true
	}
	c := tok[0]
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// sqlTokens breaks sql into a flat token stream for SingleTableSelect's
// shallow grammar check. A quoted identifier keeps its quotes (so a
// column literally named "join" is never mistaken for the JOIN
// keyword), a string literal collapses to one opaque "?" token, comments
// vanish, and `( ) , .` become one-character tokens of their own;
// everything else is either a run of word characters or a single
// operator rune.
func sqlTokens(engine Engine, sql string) []string {
	r := []rune(sql)
	mysqlish := engine == EngineMySQL || engine == EngineMariaDB
	dollar := engine == EnginePostgres

	var toks []string
	i := 0
	for i < len(r) {
		c := r[i]
		switch {
		case isSpaceRune(c):
			i++
		case c == '-' && i+1 < len(r) && r[i+1] == '-':
			i = skipToEOL(r, i)
		case mysqlish && c == '#':
			i = skipToEOL(r, i)
		case c == '/' && i+1 < len(r) && r[i+1] == '*':
			i = skipBlockComment(r, i)
		case c == '\'':
			i = skipQuoted(r, i, '\'', mysqlish)
			toks = append(toks, "?")
		case c == '"':
			start := i
			i = skipQuoted(r, i, '"', false)
			toks = append(toks, string(r[start:i]))
		case mysqlish && c == '`':
			start := i
			i = skipQuoted(r, i, '`', false)
			toks = append(toks, string(r[start:i]))
		case dollar && c == '$':
			start := i
			next := skipDollarQuoted(r, i)
			if next == i+1 {
				// Not a valid `$tag$` delimiter — a positional parameter
				// such as `$1` — so it stands as its own token.
				toks = append(toks, string(r[i]))
			} else {
				toks = append(toks, string(r[start:next]))
			}
			i = next
		case c == '(', c == ')', c == ',', c == '.', c == '*', c == ';':
			toks = append(toks, string(c))
			i++
		case isWordRune(c):
			start := i
			for i < len(r) && isWordRune(r[i]) {
				i++
			}
			toks = append(toks, string(r[start:i]))
		default:
			// An operator or anything else lazysql has no keyword for: a
			// one-rune token that will not satisfy isIdent, so an
			// arithmetic select item like `col + 1` still fails
			// validSelectList's shape check.
			toks = append(toks, string(c))
			i++
		}
	}
	return toks
}
