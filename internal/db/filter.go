package db

import (
	"fmt"
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

// FilterOp is one comparison the structured filter modal offers. The
// values are the SQL spelling, so an operator needs no translation on
// its way into a statement — every supported engine accepts all of
// them.
type FilterOp string

const (
	OpEq        FilterOp = "="
	OpNe        FilterOp = "!="
	OpLt        FilterOp = "<"
	OpGt        FilterOp = ">"
	OpLe        FilterOp = "<="
	OpGe        FilterOp = ">="
	OpLike      FilterOp = "LIKE"
	OpIsNull    FilterOp = "IS NULL"
	OpIsNotNull FilterOp = "IS NOT NULL"
)

// FilterOps lists the operators in the order the modal cycles them.
func FilterOps() []FilterOp {
	return []FilterOp{
		OpEq, OpNe, OpLt, OpGt, OpLe, OpGe, OpLike, OpIsNull, OpIsNotNull,
	}
}

// NeedsValue reports whether the operator compares against a value. The
// two NULL tests do not, which is why the modal hides its value field
// for them.
func (op FilterOp) NeedsValue() bool {
	return op != OpIsNull && op != OpIsNotNull
}

// FilterCond is one condition of a structured filter: the column, the
// operator and the value exactly as the user typed it. Type is the
// column's declared data type when the caller knows it — it decides
// whether the value binds as a number, a boolean or a string, which is
// what keeps `intcol = $1` from reaching PostgreSQL with a text
// parameter.
type FilterCond struct {
	Column string
	Op     FilterOp
	Value  string
	Type   string
}

// BuildFilter turns structured conditions into a parameterized Filter.
// Identifiers go through Dialect.QuoteIdent and every value becomes a
// Dialect.Placeholder bound as a query parameter, so a value containing
// quotes or wildcards is data and can never be SQL. Conditions are
// joined with AND. No conditions yields nil, meaning "no filter".
func BuildFilter(d Dialect, conds []FilterCond) (*Filter, error) {
	if len(conds) == 0 {
		return nil, nil
	}
	exprs := make([]string, 0, len(conds))
	raws := make([]string, 0, len(conds))
	var args []any
	for _, c := range conds {
		if strings.TrimSpace(c.Column) == "" {
			return nil, fmt.Errorf("filter: no column selected")
		}
		if !validOp(c.Op) {
			return nil, fmt.Errorf("filter: unknown operator %q", string(c.Op))
		}
		expr := d.QuoteIdent(c.Column) + " " + string(c.Op)
		if c.Op.NeedsValue() {
			v, err := bindValue(c)
			if err != nil {
				return nil, err
			}
			args = append(args, v)
			expr += " " + d.Placeholder(len(args))
		}
		exprs = append(exprs, expr)
		raws = append(raws, c.String())
	}
	return &Filter{
		Expr: strings.Join(exprs, " AND "),
		Args: args,
		Raw:  strings.Join(raws, " AND "),
	}, nil
}

// String renders the condition the way the status line shows it. The
// quoting is display only — the statement itself binds the value — and
// follows what the value binds as, so a number reads as a number.
func (c FilterCond) String() string {
	if !c.Op.NeedsValue() {
		return c.Column + " " + string(c.Op)
	}
	shown := "'" + strings.ReplaceAll(c.Value, "'", "''") + "'"
	if v, err := bindValue(c); err == nil {
		if _, isText := v.(string); !isText {
			shown = strings.TrimSpace(c.Value)
		}
	}
	return c.Column + " " + string(c.Op) + " " + shown
}

func validOp(op FilterOp) bool {
	for _, o := range FilterOps() {
		if o == op {
			return true
		}
	}
	return false
}

// bindValue decides what Go type the value travels as. A LIKE pattern is
// always text — `id LIKE '1%'` is a string match even on an integer
// column. Otherwise the column's declared type decides, and an
// unparseable value is an error the modal reports inline rather than a
// statement the engine rejects later.
func bindValue(c FilterCond) (any, error) {
	v := c.Value
	if c.Op == OpLike {
		return v, nil
	}
	switch typeClass(c.Type) {
	case classInt:
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil {
			// A float typed against an integer column is still a number
			// the engine can compare, so it is bound rather than refused.
			if f, ferr := strconv.ParseFloat(strings.TrimSpace(v), 64); ferr == nil {
				return f, nil
			}
			return nil, fmt.Errorf("%s: %q is not a number", c.Column, v)
		}
		return n, nil
	case classFloat:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return nil, fmt.Errorf("%s: %q is not a number", c.Column, v)
		}
		return f, nil
	case classBool:
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		if err != nil {
			return nil, fmt.Errorf("%s: %q is not true or false", c.Column, v)
		}
		return b, nil
	case classText:
		return v, nil
	}
	// Type unknown: sniff a number, because a bare integer bound as text
	// is what PostgreSQL rejects. Booleans are not sniffed — "true" is a
	// plausible text value and no engine needs the hint.
	s := strings.TrimSpace(v)
	if intRe.MatchString(s) {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n, nil
		}
	}
	if floatRe.MatchString(s) {
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f, nil
		}
	}
	return v, nil
}

type valueClass int

const (
	classUnknown valueClass = iota
	classInt
	classFloat
	classBool
	classText
)

// intTypes and floatTypes are the numeric type names of the four
// supported engines, MySQL's unsigned spellings and DuckDB's own widths
// included. The name is matched whole, after the length/precision and
// any trailing modifier are cut off, so PostgreSQL's `point` and
// `interval` are not mistaken for integers by a substring test.
var intTypes = map[string]bool{
	"int": true, "int2": true, "int4": true, "int8": true, "integer": true,
	"bigint": true, "smallint": true, "mediumint": true, "tinyint": true,
	"hugeint": true, "utinyint": true, "usmallint": true, "uinteger": true,
	"ubigint": true, "uhugeint": true,
	"serial": true, "serial2": true, "serial4": true, "serial8": true,
	"smallserial": true, "bigserial": true,
}

var floatTypes = map[string]bool{
	"float": true, "float4": true, "float8": true, "real": true,
	"double": true, "decimal": true, "numeric": true, "dec": true, "fixed": true,
}

var boolTypes = map[string]bool{"bool": true, "boolean": true}

var textTypes = map[string]bool{
	"char": true, "varchar": true, "text": true, "tinytext": true,
	"mediumtext": true, "longtext": true, "bpchar": true, "character": true,
	"varying": true, "string": true, "uuid": true, "json": true, "jsonb": true,
	"name": true, "citext": true, "clob": true, "nvarchar": true, "nchar": true,
}

// typeClass maps a declared column type to what its values bind as.
func typeClass(t string) valueClass {
	base := baseTypeName(t)
	switch {
	case base == "":
		return classUnknown
	case intTypes[base]:
		return classInt
	case floatTypes[base]:
		return classFloat
	case boolTypes[base]:
		return classBool
	case textTypes[base]:
		return classText
	}
	return classUnknown
}

// baseTypeName reduces a declared type to its leading word without its
// length: `VARCHAR(20)` → `varchar`, `DOUBLE PRECISION` → `double`,
// `INT UNSIGNED` → `int`.
func baseTypeName(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	if i := strings.IndexAny(t, "( \t"); i >= 0 {
		t = t[:i]
	}
	return t
}

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
