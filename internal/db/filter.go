package db

import (
	"regexp"
	"strconv"
	"strings"
)

// The quick filter of the data grid is free-form SQL: the user types a
// WHERE fragment and expects it to work. Interpolating that straight
// into the statement is exactly the injection shape lazysql must not
// have, so ParseFilter first tries to take the fragment apart into
// comparisons whose *values* can travel as bound parameters. Only a
// fragment it cannot recognise stays verbatim, and it is flagged so the
// UI can warn about it.

// comparisonRe matches one `column <op> <literal>` term. The column is a
// bare, double-quoted or backtick-quoted identifier; the literal is
// validated separately by parseLiteral.
var comparisonRe = regexp.MustCompile(
	`(?is)^\s*([A-Za-z_][A-Za-z0-9_$]*|"(?:[^"]|"")+"|` + "`(?:[^`]|``)+`" + `)\s*` +
		`(=|<>|!=|>=|<=|>|<|not\s+like|like|ilike)\s*(.+?)\s*$`)

// nullRe matches `column IS [NOT] NULL`, which takes no parameter.
var nullRe = regexp.MustCompile(
	`(?is)^\s*([A-Za-z_][A-Za-z0-9_$]*|"(?:[^"]|"")+"|` + "`(?:[^`]|``)+`" + `)\s*` +
		`is\s+(not\s+)?null\s*$`)

var (
	intRe   = regexp.MustCompile(`^[+-]?\d+$`)
	floatRe = regexp.MustCompile(`^[+-]?(\d+\.\d*|\.\d+|\d+)([eE][+-]?\d+)?$`)
)

// ParseFilter turns a user-typed WHERE fragment into a Filter. A leading
// `WHERE` is tolerated. An empty fragment yields nil, meaning "no
// filter". Anything that is not a chain of simple comparisons joined by
// AND is kept verbatim and marked Verbatim so callers can warn.
func ParseFilter(d Dialect, raw string) *Filter {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil
	}
	if rest, ok := cutKeyword(s, "where"); ok {
		s = strings.TrimSpace(rest)
	}
	verbatim := &Filter{Expr: s, Raw: raw, Verbatim: true}

	terms, ok := splitAnd(s)
	if !ok {
		return verbatim
	}
	exprs := make([]string, 0, len(terms))
	var args []any
	for _, t := range terms {
		expr, val, hasVal, ok := parseTerm(d, t, len(args)+1)
		if !ok {
			return verbatim
		}
		exprs = append(exprs, expr)
		if hasVal {
			args = append(args, val)
		}
	}
	return &Filter{Expr: strings.Join(exprs, " AND "), Args: args, Raw: raw}
}

// parseTerm renders one comparison with a quoted identifier and, unless
// it is an IS [NOT] NULL test, a placeholder for position n.
func parseTerm(d Dialect, term string, n int) (expr string, val any, hasVal, ok bool) {
	if mm := nullRe.FindStringSubmatch(term); mm != nil {
		op := "IS NULL"
		if strings.TrimSpace(mm[2]) != "" {
			op = "IS NOT NULL"
		}
		return d.QuoteIdent(unquoteIdent(mm[1])) + " " + op, nil, false, true
	}
	mm := comparisonRe.FindStringSubmatch(term)
	if mm == nil {
		return "", nil, false, false
	}
	lit, ok := parseLiteral(mm[3])
	if !ok {
		return "", nil, false, false
	}
	op := strings.ToUpper(strings.Join(strings.Fields(mm[2]), " "))
	return d.QuoteIdent(unquoteIdent(mm[1])) + " " + op + " " + d.Placeholder(n), lit, true, true
}

// parseLiteral recognises the value syntaxes worth binding: quoted
// strings, numbers and booleans. NULL is deliberately absent — `col =
// NULL` is never what the user means, so such a fragment falls through
// to the verbatim path instead of being silently rewritten.
func parseLiteral(s string) (any, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, false
	}
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		body := s[1 : len(s)-1]
		// A lone quote inside means the literal ended early and more SQL
		// follows, e.g. `'a' OR 1=1`; only doubled quotes are escapes.
		if strings.Contains(strings.ReplaceAll(body, "''", ""), "'") {
			return nil, false
		}
		return strings.ReplaceAll(body, "''", "'"), true
	}
	switch strings.ToUpper(s) {
	case "TRUE":
		return true, true
	case "FALSE":
		return false, true
	}
	if intRe.MatchString(s) {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			return v, true
		}
	}
	if floatRe.MatchString(s) {
		if v, err := strconv.ParseFloat(s, 64); err == nil {
			return v, true
		}
	}
	return nil, false
}

// splitAnd splits a fragment on top-level AND. It reports false when the
// fragment contains anything the naive splitter must not guess at:
// parentheses, OR, or an unterminated quote.
func splitAnd(s string) ([]string, bool) {
	var terms []string
	var cur strings.Builder
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			cur.WriteByte(c)
			if c == quote {
				// A doubled quote is an escape, not the end.
				if i+1 < len(s) && s[i+1] == quote {
					cur.WriteByte(quote)
					i++
					continue
				}
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"', '`':
			quote = c
			cur.WriteByte(c)
			continue
		case '(', ')', ';':
			return nil, false
		}
		if _, ok := cutKeywordAt(s, i, "or"); ok {
			return nil, false
		}
		if _, ok := cutKeywordAt(s, i, "and"); ok {
			terms = append(terms, cur.String())
			cur.Reset()
			i += len("and") - 1
			continue
		}
		cur.WriteByte(c)
	}
	if quote != 0 {
		return nil, false
	}
	terms = append(terms, cur.String())
	for _, t := range terms {
		if strings.TrimSpace(t) == "" {
			return nil, false
		}
	}
	return terms, true
}

// cutKeywordAt reports whether the word kw starts at s[i] on a word
// boundary, case-insensitively.
func cutKeywordAt(s string, i int, kw string) (string, bool) {
	if i+len(kw) > len(s) {
		return "", false
	}
	if !strings.EqualFold(s[i:i+len(kw)], kw) {
		return "", false
	}
	if i > 0 && isIdentByte(s[i-1]) {
		return "", false
	}
	if j := i + len(kw); j < len(s) && isIdentByte(s[j]) {
		return "", false
	}
	return s[i+len(kw):], true
}

// cutKeyword strips a leading keyword such as `WHERE`.
func cutKeyword(s, kw string) (string, bool) {
	return cutKeywordAt(s, 0, kw)
}

func isIdentByte(c byte) bool {
	return c == '_' || c == '$' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// unquoteIdent strips the quoting the user typed so the dialect can
// apply its own.
func unquoteIdent(s string) string {
	if len(s) < 2 {
		return s
	}
	switch s[0] {
	case '"':
		if s[len(s)-1] == '"' {
			return strings.ReplaceAll(s[1:len(s)-1], `""`, `"`)
		}
	case '`':
		if s[len(s)-1] == '`' {
			return strings.ReplaceAll(s[1:len(s)-1], "``", "`")
		}
	}
	return s
}
