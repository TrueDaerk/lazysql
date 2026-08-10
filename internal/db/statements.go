package db

import (
	"strings"

	"lazysql/internal/sqlhl"
)

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
	spans := SplitStatementSpans(engine, script)
	if len(spans) == 0 {
		return nil
	}
	out := make([]string, 0, len(spans))
	for _, s := range spans {
		out = append(out, s.SQL)
	}
	return out
}

// StatementSpan is one statement of a script together with where it sits
// in it. Start and End are rune offsets of the trimmed statement text, so
// a caret anywhere between them is inside that statement — which is how
// `ctrl+e` decides what to explain in a multi-statement buffer.
type StatementSpan struct {
	SQL        string
	Start, End int
}

// SplitStatementSpans is SplitStatements with the offsets kept.
func SplitStatementSpans(engine Engine, script string) []StatementSpan {
	r := []rune(script)
	mysqlish := engine == EngineMySQL || engine == EngineMariaDB
	dollar := engine == EnginePostgres

	var out []StatementSpan
	start, i := 0, 0
	push := func(end int) {
		lo, hi := start, end
		for lo < hi && isSpaceRune(r[lo]) {
			lo++
		}
		for hi > lo && isSpaceRune(r[hi-1]) {
			hi--
		}
		if lo < hi {
			out = append(out, StatementSpan{SQL: string(r[lo:hi]), Start: lo, End: hi})
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

func isSpaceRune(c rune) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

// StatementAt returns the statement the caret sits in, as a rune offset
// into the script. A caret inside a statement picks that one; a caret in
// the whitespace or on the `;` after one picks the statement it just
// left, which is where it is after typing a statement out. ok is false
// only for a script with no statements at all.
func StatementAt(engine Engine, script string, offset int) (StatementSpan, bool) {
	spans := SplitStatementSpans(engine, script)
	if len(spans) == 0 {
		return StatementSpan{}, false
	}
	pick := spans[0]
	for _, s := range spans {
		if offset >= s.Start {
			pick = s
		}
		if offset >= s.Start && offset <= s.End {
			return s, true
		}
	}
	return pick, true
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
var writeInCTE = map[string]bool{
	"INSERT": true, "UPDATE": true, "DELETE": true, "MERGE": true,
	"REPLACE": true, "TRUNCATE": true,
}

// ClassifyStatement decides whether a statement reads or writes, reading
// the most portable spelling of it. ClassifyStatementFor is the same
// question asked with an engine's own lexical rules — backticks, `#`
// comments, dollar-quoted bodies — and is what the read-only guard uses.
func ClassifyStatement(sql string) StatementKind {
	return ClassifyStatementFor("", sql)
}

// ClassifyStatementFor decides whether a statement reads or writes. It
// errs towards StatementWrite: an unrecognized statement is confirmed
// before it runs and executed through Exec, which is the safe way to be
// wrong.
//
// The decision runs on the tokenizer, never on substring search: a
// keyword inside a comment, a string literal or a quoted identifier is
// data. That is what keeps `WITH x AS (…) DELETE FROM t` a write while
// `SELECT 'delete me'` stays a read.
func ClassifyStatementFor(engine Engine, sql string) StatementKind {
	toks := significantTokens(engine, sql)
	if len(toks) == 0 {
		// Nothing but comments and whitespace: no leading keyword to
		// recognize, so it falls in with everything else unrecognized.
		return StatementWrite
	}
	kw := strings.ToUpper(toks[0].text)
	if !readKeywords[kw] {
		return StatementWrite
	}
	switch kw {
	case "WITH":
		// A data-modifying CTE is spelled with its verb as a bare word;
		// one inside a literal or a quoted identifier is not it.
		for _, t := range toks[1:] {
			if t.word && writeInCTE[strings.ToUpper(t.text)] {
				return StatementWrite
			}
		}
	case "PRAGMA":
		// `PRAGMA x = y` sets, `PRAGMA x` reads. The `=` has to be a real
		// operator: `PRAGMA table_info('a=b')` still only reads.
		for _, t := range toks[1:] {
			if t.op && strings.Contains(t.text, "=") {
				return StatementWrite
			}
		}
	}
	return StatementRead
}

// sqlToken is one significant token of a statement: its text plus the two
// facts classification asks about it. word marks a bare word — a keyword
// or an unquoted identifier, the two kinds a verb can be scanned as —
// and op marks an operator run.
type sqlToken struct {
	text string
	word bool
	op   bool
}

// significantTokens tokenizes sql and drops whitespace and comments, so
// index 0 is the statement's leading keyword however it was written.
func significantTokens(engine Engine, sql string) []sqlToken {
	d := sqlhl.For(string(engine))
	var out []sqlToken
	for _, t := range sqlhl.Tokenize(d, sql) {
		switch t.Kind {
		case sqlhl.Plain, sqlhl.Comment:
			continue
		case sqlhl.Keyword, sqlhl.Ident:
			out = append(out, sqlToken{text: t.Text(sql), word: true})
		case sqlhl.Operator:
			out = append(out, sqlToken{text: t.Text(sql), op: true})
		default:
			out = append(out, sqlToken{text: t.Text(sql)})
		}
	}
	return out
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

func isWordRune(c rune) bool { return isTagRune(c) }
