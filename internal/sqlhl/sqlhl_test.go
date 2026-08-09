package sqlhl

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// kindOf reports the kind the tokenizer gave the first occurrence of sub
// in src, and fails if sub is not covered by exactly one token.
func kindOf(t *testing.T, d Dialect, src, sub string) Kind {
	t.Helper()
	at := strings.Index(src, sub)
	if at < 0 {
		t.Fatalf("%q is not in %q", sub, src)
	}
	for _, tok := range Tokenize(d, src) {
		if tok.Start <= at && at+len(sub) <= tok.End {
			return tok.Kind
		}
	}
	t.Fatalf("no single token covers %q in %q; tokens: %s", sub, src, dump(d, src))
	return Plain
}

func dump(d Dialect, src string) string {
	var b strings.Builder
	for _, tok := range Tokenize(d, src) {
		b.WriteString(kindNames[tok.Kind] + "(" + tok.Text(src) + ") ")
	}
	return b.String()
}

var kindNames = map[Kind]string{
	Plain: "plain", Keyword: "keyword", Ident: "ident", QuotedIdent: "quoted",
	String: "string", Number: "number", Operator: "op", Comment: "comment",
	Placeholder: "placeholder",
}

// The contract the renderer depends on: tokens tile the input exactly.
// Every other test in this file is only meaningful if this one holds.
func TestTokensCoverTheWholeInputWithoutGaps(t *testing.T) {
	inputs := []string{
		"",
		"SELECT 1",
		"SELECT * FROM t WHERE a = 'x' -- note\nAND b = 2;",
		"/* unterminated",
		"'unterminated",
		"`weird` \"quoted\" $$body$$ :name ? $1 @@v",
		"sëlect ünïcode FROM tãble",
		"...;;;###",
		"\n\n\t  \n",
	}
	for _, d := range []Dialect{Generic, MySQL, Postgres, SQLite, DuckDB} {
		for _, src := range inputs {
			toks := Tokenize(d, src)
			want := 0
			for _, tok := range toks {
				if tok.Start != want {
					t.Fatalf("%s %q: token starts at %d, want %d", d, src, tok.Start, want)
				}
				if tok.End <= tok.Start {
					t.Fatalf("%s %q: empty token at %d", d, src, tok.Start)
				}
				want = tok.End
			}
			if want != len(src) {
				t.Fatalf("%s %q: tokens end at %d, want %d", d, src, want, len(src))
			}
			if got := len(Kinds(d, src)); got != utf8.RuneCountInString(src) {
				t.Fatalf("%s %q: %d kinds for %d runes", d, src, got, utf8.RuneCountInString(src))
			}
		}
	}
}

func TestKeywordsAreDialectAware(t *testing.T) {
	// The core is shared: SELECT is a keyword everywhere.
	for _, d := range []Dialect{Generic, MySQL, Postgres, SQLite, DuckDB} {
		if got := kindOf(t, d, "select 1", "select"); got != Keyword {
			t.Errorf("%s: `select` = %v, want a keyword (case-insensitive)", d, kindNames[got])
		}
	}
	cases := []struct {
		d    Dialect
		word string
	}{
		{SQLite, "AUTOINCREMENT"},
		{MySQL, "AUTO_INCREMENT"},
		{Postgres, "ILIKE"},
		{DuckDB, "QUALIFY"},
	}
	for _, c := range cases {
		src := "x " + c.word + " y"
		if got := kindOf(t, c.d, src, c.word); got != Keyword {
			t.Errorf("%s: %s = %v, want a keyword", c.d, c.word, kindNames[got])
		}
		if got := kindOf(t, SQLite, src, c.word); c.d != SQLite && got != Ident {
			t.Errorf("SQLite: %s = %v, want a plain identifier", c.word, kindNames[got])
		}
	}
}

// A quote inside a literal must not end it, or every statement after it
// would be highlighted inside-out.
func TestStringsWithEmbeddedQuotes(t *testing.T) {
	src := `SELECT 'it''s here', 'a' FROM t`
	if got := kindOf(t, Generic, src, `'it''s here'`); got != String {
		t.Fatalf("doubled quote ended the literal: %s", dump(Generic, src))
	}
	// The literal ends where it should: what follows is code again.
	if got := kindOf(t, Generic, src, "FROM"); got != Keyword {
		t.Fatalf("`FROM` after the literal = %v, want a keyword: %s", kindNames[got], dump(Generic, src))
	}

	// MySQL honours backslash escapes; the others read the backslash as
	// an ordinary character, so the quote after it closes the literal.
	esc := `SELECT 'a\' , 'b' FROM t`
	if got := kindOf(t, MySQL, esc, `'a\' , '`); got != String {
		t.Fatalf("MySQL: backslash did not escape the quote: %s", dump(MySQL, esc))
	}
	if got := kindOf(t, Postgres, esc, `'a\'`); got != String {
		t.Fatalf("Postgres: backslash should not escape the quote: %s", dump(Postgres, esc))
	}

	// A quoted identifier has its own doubling rule, and MySQL reads a
	// double-quoted word as a string instead.
	if got := kindOf(t, Postgres, `SELECT "a""b" FROM t`, `"a""b"`); got != QuotedIdent {
		t.Errorf(`Postgres: "a""b" = %v, want a quoted identifier`, kindNames[got])
	}
	if got := kindOf(t, MySQL, `SELECT "a" FROM t`, `"a"`); got != String {
		t.Errorf(`MySQL: "a" = %v, want a string literal`, kindNames[got])
	}
	if got := kindOf(t, MySQL, "SELECT `a` FROM t", "`a`"); got != QuotedIdent {
		t.Errorf("MySQL: `a` = %v, want a quoted identifier", kindNames[got])
	}
	// Dollar quoting is Postgres/DuckDB only.
	if got := kindOf(t, Postgres, `SELECT $tag$ 'x' $tag$ FROM t`, `$tag$ 'x' $tag$`); got != String {
		t.Errorf("Postgres: dollar-quoted body = %v, want a string", kindNames[got])
	}
}

// An unterminated literal must not lose the rest of the buffer, and must
// not loop the scanner.
func TestUnterminatedConstructsRunToTheEnd(t *testing.T) {
	for _, src := range []string{"SELECT 'oops", "SELECT /* oops", "SELECT $$oops", `SELECT "oops`} {
		toks := Tokenize(Postgres, src)
		if last := toks[len(toks)-1]; last.End != len(src) {
			t.Errorf("%q: last token ends at %d, want %d", src, last.End, len(src))
		}
	}
}

// Everything after `--` is text, keywords included — the case that makes
// a naive word-first highlighter look broken.
func TestCommentsContainingKeywords(t *testing.T) {
	src := "SELECT 1 -- DROP TABLE users;\nDELETE 2"
	if got := kindOf(t, Generic, src, "-- DROP TABLE users;"); got != Comment {
		t.Fatalf("line comment = %v: %s", kindNames[got], dump(Generic, src))
	}
	// The comment stops at the newline: the next line is code again.
	if got := kindOf(t, Generic, src, "DELETE"); got != Keyword {
		t.Fatalf("the line after the comment = %v, want code: %s", kindNames[got], dump(Generic, src))
	}

	block := "SELECT /* FROM WHERE 'x' */ 1"
	if got := kindOf(t, Generic, block, "/* FROM WHERE 'x' */"); got != Comment {
		t.Fatalf("block comment = %v: %s", kindNames[got], dump(Generic, block))
	}
	// Postgres nests block comments; MySQL closes at the first `*/`.
	nested := "SELECT /* a /* b */ c */ 1"
	if got := kindOf(t, Postgres, nested, "/* a /* b */ c */"); got != Comment {
		t.Errorf("Postgres: nested block comment = %v: %s", kindNames[got], dump(Postgres, nested))
	}
	if got := kindOf(t, MySQL, nested, "/* a /* b */"); got != Comment {
		t.Errorf("MySQL: block comment should close at the first `*/`: %s", dump(MySQL, nested))
	}
	// `#` is a comment in MySQL and an operator elsewhere.
	hash := "SELECT 1 # DROP TABLE t"
	if got := kindOf(t, MySQL, hash, "# DROP TABLE t"); got != Comment {
		t.Errorf("MySQL: `#` comment = %v", kindNames[got])
	}
	if got := kindOf(t, Postgres, hash, "DROP"); got != Keyword {
		t.Errorf("Postgres: `#` should not start a comment: %s", dump(Postgres, hash))
	}
}

func TestPlaceholders(t *testing.T) {
	src := "SELECT * FROM t WHERE a = ? AND b = :name AND c = :2"
	for _, sub := range []string{"?", ":name", ":2"} {
		if got := kindOf(t, Generic, src, sub); got != Placeholder {
			t.Errorf("%s = %v, want a placeholder: %s", sub, kindNames[got], dump(Generic, src))
		}
	}
	if got := kindOf(t, Postgres, "SELECT $1, $12", "$12"); got != Placeholder {
		t.Errorf("Postgres: $12 = %v, want a placeholder", kindNames[got])
	}
	// A cast is not a placeholder, however much it looks like one.
	if got := kindOf(t, Postgres, "SELECT a::text", "::"); got != Operator {
		t.Errorf("Postgres: `::` = %v, want an operator", kindNames[got])
	}
	if got := kindOf(t, Postgres, "SELECT a::text", "text"); got != Keyword {
		t.Errorf("Postgres: the type after `::` = %v, want a keyword", kindNames[got])
	}
	// MySQL user variables read as placeholders; `@` elsewhere does not.
	if got := kindOf(t, MySQL, "SET @@id = 1", "@@id"); got != Placeholder {
		t.Errorf("MySQL: @@id = %v, want a placeholder", kindNames[got])
	}
	// A `?` inside a literal or a comment stays where it is.
	if got := kindOf(t, Generic, "SELECT '?' -- ?", "'?'"); got != String {
		t.Errorf("a `?` inside a literal = %v", kindNames[got])
	}
}

func TestNumbers(t *testing.T) {
	src := "SELECT 1, 2.5, .5, 1e10, 1.5e-3, 0xFF FROM t"
	for _, sub := range []string{"1,", "2.5", ".5", "1e10", "1.5e-3", "0xFF"} {
		want := strings.TrimSuffix(sub, ",")
		if got := kindOf(t, Generic, src, want); got != Number {
			t.Errorf("%s = %v, want a number: %s", want, kindNames[got], dump(Generic, src))
		}
	}
	// An identifier that starts with a digit-free prefix is not a number.
	if got := kindOf(t, Generic, "SELECT a1 FROM t", "a1"); got != Ident {
		t.Errorf("a1 = %v, want an identifier", kindNames[got])
	}
}

func TestTypedStringPrefixes(t *testing.T) {
	if got := kindOf(t, Postgres, `SELECT E'a\'b' FROM t`, `E'a\'b'`); got != String {
		t.Errorf("E'' = %v, want a string", kindNames[got])
	}
	if got := kindOf(t, MySQL, "SELECT X'1F' FROM t", "X'1F'"); got != String {
		t.Errorf("X'' = %v, want a string", kindNames[got])
	}
	// A word longer than one letter before a quote is still an identifier
	// followed by a literal.
	if got := kindOf(t, Generic, "SELECT foo'a' FROM t", "foo"); got != Ident {
		t.Errorf("foo'a' should split into an identifier and a string: %s",
			dump(Generic, "SELECT foo'a' FROM t"))
	}
}

func TestForMapsEngineNames(t *testing.T) {
	cases := map[string]Dialect{
		"mysql": MySQL, "mariadb": MySQL, "MariaDB": MySQL,
		"postgres": Postgres, "sqlite": SQLite, "duckdb": DuckDB,
		"": Generic, "oracle": Generic,
	}
	for name, want := range cases {
		if got := For(name); got != want {
			t.Errorf("For(%q) = %q, want %q", name, got, want)
		}
	}
}
