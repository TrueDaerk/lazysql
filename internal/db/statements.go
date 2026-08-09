package db

import "strings"

// Free-form SQL arrives from the query editor as one script that may hold
// several statements. Splitting it on `;` needs a lexer rather than
// strings.Split: a semicolon inside a string literal, an identifier or a
// comment is data, not a separator. The lexer is dialect-aware because the
// three constructs that carry a semicolon differ per engine — backticked
// identifiers and `#` comments are MySQL's, dollar-quoted bodies are
// PostgreSQL's, and only MySQL reads a backslash as an escape inside a
// string literal.

// SplitStatements splits a SQL script into its statements. Comments and
// surrounding whitespace are kept inside the statement they belong to;
// empty statements (a stray `;`, a trailing newline) are dropped, so
// running an empty script yields no statements at all.
//
// The final statement does not need a trailing semicolon.
func SplitStatements(engine Engine, script string) []string {
	r := []rune(script)
	mysqlish := engine == EngineMySQL || engine == EngineMariaDB
	dollar := engine == EnginePostgres

	var out []string
	start, i := 0, 0
	push := func(end int) {
		if s := strings.TrimSpace(string(r[start:end])); s != "" {
			out = append(out, s)
		}
	}
	for i < len(r) {
		switch c := r[i]; {
		case c == '-' && i+1 < len(r) && r[i+1] == '-':
			i = skipToEOL(r, i)
		case mysqlish && c == '#':
			i = skipToEOL(r, i)
		case c == '/' && i+1 < len(r) && r[i+1] == '*':
			i = skipBlockComment(r, i)
		case c == '\'':
			i = skipQuoted(r, i, '\'', mysqlish)
		case c == '"':
			// Standard quoted identifier everywhere lazysql supports;
			// MySQL only reads it as a string in ANSI_QUOTES mode, and
			// either way a `"` pairs with the next unescaped `"`.
			i = skipQuoted(r, i, '"', false)
		case mysqlish && c == '`':
			i = skipQuoted(r, i, '`', false)
		case dollar && c == '$':
			i = skipDollarQuoted(r, i)
		case c == ';':
			push(i)
			i++
			start = i
		default:
			i++
		}
	}
	push(len(r))
	return out
}

func skipToEOL(r []rune, i int) int {
	for i < len(r) && r[i] != '\n' {
		i++
	}
	return i
}

// skipBlockComment steps over `/* … */`. Nesting is not honoured: only
// PostgreSQL nests block comments, and an unterminated one runs to the
// end of the script either way.
func skipBlockComment(r []rune, i int) int {
	for i += 2; i+1 < len(r); i++ {
		if r[i] == '*' && r[i+1] == '/' {
			return i + 2
		}
	}
	return len(r)
}

// skipQuoted steps over a quoted run starting at the opening quote. A
// doubled quote is the portable escape; backslash escapes are MySQL's
// and are only honoured when the caller asks for them. An unterminated
// run consumes the rest of the script, which keeps a half-typed literal
// from splitting into nonsense statements.
func skipQuoted(r []rune, i int, quote rune, backslash bool) int {
	for i++; i < len(r); {
		switch {
		case backslash && r[i] == '\\':
			i += 2
		case r[i] == quote:
			if i+1 < len(r) && r[i+1] == quote {
				i += 2
				continue
			}
			return i + 1
		default:
			i++
		}
	}
	return len(r)
}

// skipDollarQuoted steps over a PostgreSQL `$tag$ … $tag$` body. A `$`
// that does not open a valid tag is an ordinary character (it starts a
// positional parameter such as `$1`), so the caller advances by one.
func skipDollarQuoted(r []rune, i int) int {
	tag, after, ok := dollarTag(r, i)
	if !ok {
		return i + 1
	}
	for j := after; j < len(r); j++ {
		if r[j] != '$' {
			continue
		}
		if closeTag, end, ok := dollarTag(r, j); ok && closeTag == tag {
			return end
		}
	}
	return len(r)
}

// dollarTag reads a `$tag$` delimiter at i, returning the tag text and
// the index just after the closing `$`.
func dollarTag(r []rune, i int) (tag string, after int, ok bool) {
	j := i + 1
	for j < len(r) && isTagRune(r[j]) {
		j++
	}
	if j >= len(r) || r[j] != '$' {
		return "", 0, false
	}
	return string(r[i+1 : j]), j + 1, true
}

func isTagRune(c rune) bool {
	return c == '_' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// StatementKind says whether a statement gives rows back or changes
// something. The query editor routes on it twice: a read goes through
// Query and renders in the grid, a write goes through Exec and needs the
// user to confirm it first.
type StatementKind int

const (
	StatementRead StatementKind = iota
	StatementWrite
)

// readKeywords are the leading keywords that only ever produce rows.
// EXPLAIN is one of them by convention even though `EXPLAIN ANALYZE`
// really does execute its statement on PostgreSQL — see the wiki note.
var readKeywords = map[string]bool{
	"SELECT":   true,
	"WITH":     true,
	"SHOW":     true,
	"EXPLAIN":  true,
	"DESCRIBE": true,
	"DESC":     true,
	"VALUES":   true,
	"TABLE":    true,
	"PRAGMA":   true,
}

// writeInCTE are the keywords that turn a `WITH` into a writing
// statement: PostgreSQL allows data-modifying CTEs, so the leading
// keyword alone does not settle it.
var writeInCTE = []string{"INSERT", "UPDATE", "DELETE", "MERGE"}

// ClassifyStatement decides whether a statement reads or writes. It errs
// towards StatementWrite: an unrecognized statement is confirmed before
// it runs and executed through Exec, which is the safe way to be wrong.
func ClassifyStatement(sql string) StatementKind {
	body := TrimLeadingComments(sql)
	kw := strings.ToUpper(firstWord(body))
	if !readKeywords[kw] {
		return StatementWrite
	}
	switch kw {
	case "WITH":
		if containsKeyword(body, writeInCTE...) {
			return StatementWrite
		}
	case "PRAGMA":
		// `PRAGMA x = y` sets, `PRAGMA x` reads.
		if strings.Contains(body, "=") {
			return StatementWrite
		}
	}
	return StatementRead
}

// TrimLeadingComments strips whitespace and leading `--`, `#` and `/* */`
// comments so the caller sees the statement's first real keyword.
func TrimLeadingComments(sql string) string {
	s := strings.TrimSpace(sql)
	for {
		switch {
		case strings.HasPrefix(s, "--"), strings.HasPrefix(s, "#"):
			if i := strings.IndexByte(s, '\n'); i >= 0 {
				s = strings.TrimSpace(s[i+1:])
				continue
			}
			return ""
		case strings.HasPrefix(s, "/*"):
			i := strings.Index(s[2:], "*/")
			if i < 0 {
				return ""
			}
			s = strings.TrimSpace(s[2+i+2:])
			continue
		default:
			return s
		}
	}
}

// FirstKeyword is the statement's leading keyword, upper-cased, for log
// lines and modal titles. It is empty for a statement that is only
// comments.
func FirstKeyword(sql string) string {
	return strings.ToUpper(firstWord(TrimLeadingComments(sql)))
}

func firstWord(s string) string {
	for i, c := range s {
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '(' || c == ';' {
			return s[:i]
		}
	}
	return s
}

// containsKeyword reports whether any of the words appears in sql as a
// whole word, ignoring case. It is a heuristic over statement text, used
// only to decide whether a CTE writes.
func containsKeyword(sql string, words ...string) bool {
	up := strings.ToUpper(sql)
	for _, w := range words {
		for i := 0; ; {
			j := strings.Index(up[i:], w)
			if j < 0 {
				break
			}
			at := i + j
			before := at == 0 || !isWordRune(rune(up[at-1]))
			afterAt := at + len(w)
			after := afterAt >= len(up) || !isWordRune(rune(up[afterAt]))
			if before && after {
				return true
			}
			i = at + len(w)
		}
	}
	return false
}

func isWordRune(c rune) bool { return isTagRune(c) }
