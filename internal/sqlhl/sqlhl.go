// Package sqlhl tokenizes SQL for syntax highlighting.
//
// It is deliberately not a parser. Highlighting runs on every keystroke
// over a buffer that is usually half-written, so the scanner has exactly
// one hard rule: it never fails and never loses a byte. Every token it
// emits is contiguous with the previous one, so the tokens of a string
// always reassemble into that string — the property the renderer relies
// on to map tokens onto runes without a second pass over the source.
//
// Unterminated constructs (a quote or a block comment the user has not
// closed yet) run to the end of the input rather than being reported as
// an error: while typing, everything after the opening quote *is* inside
// the literal, and colouring it that way is what an editor should show.
package sqlhl

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Kind is what a token is. The renderer maps these onto themed styles;
// Plain (whitespace), Ident and Operator deliberately have no colour of
// their own so ordinary text keeps the terminal's foreground.
type Kind uint8

const (
	Plain Kind = iota
	Keyword
	Ident
	QuotedIdent
	String
	Number
	Operator
	Comment
	Placeholder
)

// Token is one lexical run, as byte offsets into the source. Tokens
// returned by Tokenize cover the whole input with no gaps and no overlap.
type Token struct {
	Kind  Kind
	Start int
	End   int
}

// Text returns the token's source text.
func (t Token) Text(src string) string { return src[t.Start:t.End] }

// Dialect selects the keyword set and the handful of lexical rules that
// differ per engine. The zero value is Generic: the shared core, and the
// most portable reading of every ambiguous construct.
type Dialect string

const (
	Generic  Dialect = ""
	MySQL    Dialect = "mysql"
	Postgres Dialect = "postgres"
	SQLite   Dialect = "sqlite"
	DuckDB   Dialect = "duckdb"
)

// For maps a db.Engine name onto a dialect. MariaDB shares MySQL's
// lexis, the same way it shares its dialect in internal/db. An unknown
// name yields Generic rather than an error: highlighting is decoration,
// and a connection to something unrecognized should still be readable.
func For(engine string) Dialect {
	switch strings.ToLower(strings.TrimSpace(engine)) {
	case "mysql", "mariadb":
		return MySQL
	case "postgres", "postgresql":
		return Postgres
	case "sqlite", "sqlite3":
		return SQLite
	case "duckdb":
		return DuckDB
	}
	return Generic
}

// Tokenize scans src and returns its tokens in order. The result covers
// every byte of src exactly once.
func Tokenize(d Dialect, src string) []Token {
	s := &scanner{d: d, src: src, kw: keywords(d)}
	s.scan()
	return s.out
}

// Kinds returns one Kind per rune of src — the shape a renderer that
// wraps, slices or overlays a cursor on the text needs, since none of
// those operations can be expressed in byte offsets.
func Kinds(d Dialect, src string) []Kind {
	out := make([]Kind, 0, len(src))
	for _, t := range Tokenize(d, src) {
		for range t.Text(src) { // range over a string steps by rune
			out = append(out, t.Kind)
		}
	}
	return out
}

type scanner struct {
	d   Dialect
	src string
	kw  map[string]bool
	i   int
	out []Token
}

// emit records a token from the current position to end and advances.
// It clamps end into (i, len(src)] so no rule can stall the scanner or
// run off the input, whatever the source looks like.
func (s *scanner) emit(k Kind, end int) {
	if end > len(s.src) {
		end = len(s.src)
	}
	if end <= s.i {
		_, size := utf8.DecodeRuneInString(s.src[s.i:])
		end = s.i + max(size, 1)
	}
	// Runs of the same kind are merged, so whitespace between two plain
	// stretches does not cost three tokens.
	if n := len(s.out); n > 0 && s.out[n-1].Kind == k && s.out[n-1].End == s.i {
		s.out[n-1].End = end
		s.i = end
		return
	}
	s.out = append(s.out, Token{Kind: k, Start: s.i, End: end})
	s.i = end
}

func (s *scanner) at(pos int) byte {
	if pos < 0 || pos >= len(s.src) {
		return 0
	}
	return s.src[pos]
}

func (s *scanner) scan() {
	for s.i < len(s.src) {
		r, size := utf8.DecodeRuneInString(s.src[s.i:])
		switch {
		case unicode.IsSpace(r):
			s.emit(Plain, s.spaceEnd())

		case r == '-' && s.at(s.i+1) == '-':
			s.emit(Comment, s.lineEnd(s.i))

		// `#` starts a comment in MySQL only; everywhere else it is an
		// operator (Postgres XOR, DuckDB list indexing).
		case r == '#' && s.d == MySQL:
			s.emit(Comment, s.lineEnd(s.i))

		case r == '/' && s.at(s.i+1) == '*':
			s.emit(Comment, s.blockCommentEnd())

		case r == '\'':
			s.emit(String, s.quotedEnd(s.i, '\'', s.backslashEscapes()))

		// A double-quoted word is a delimited identifier in every engine
		// lazysql speaks except MySQL, whose default sql_mode reads it as
		// a string literal (see wiki/reference/sqlite-double-quoted-strings
		// for the neighbouring SQLite quirk).
		case r == '"':
			k := QuotedIdent
			if s.d == MySQL {
				k = String
			}
			s.emit(k, s.quotedEnd(s.i, '"', k == String && s.backslashEscapes()))

		case r == '`':
			s.emit(QuotedIdent, s.quotedEnd(s.i, '`', false))

		case r == '$':
			s.scanDollar()

		case r == '?':
			s.emit(Placeholder, s.identEnd(s.i+1))

		case r == ':':
			switch {
			case s.at(s.i+1) == ':': // Postgres cast, not a placeholder
				s.emit(Operator, s.i+2)
			// `:name` and the numbered `:1` spelling drivers also accept.
			case isIdentStartAt(s.src, s.i+1) || isDigitAt(s.src, s.i+1):
				s.emit(Placeholder, s.identEnd(s.i+1))
			default:
				s.emit(Operator, s.i+1)
			}

		// MySQL user variables read as placeholders: they are the same
		// kind of hole in an otherwise complete statement.
		case r == '@' && s.d == MySQL:
			j := s.i + 1
			if s.at(j) == '@' {
				j++
			}
			if isIdentStartAt(s.src, j) {
				s.emit(Placeholder, s.identEnd(j))
			} else {
				s.emit(Operator, s.i+1)
			}

		case unicode.IsDigit(r) || (r == '.' && isDigitAt(s.src, s.i+1)):
			s.emit(Number, s.numberEnd())

		case isIdentStart(r):
			s.scanWord()

		default:
			s.emit(Operator, s.i+size)
		}
	}
}

func (s *scanner) spaceEnd() int {
	j := s.i
	for j < len(s.src) {
		r, size := utf8.DecodeRuneInString(s.src[j:])
		if !unicode.IsSpace(r) {
			break
		}
		j += size
	}
	return j
}

// lineEnd is the offset of the newline that ends the line at pos, or the
// end of input. The newline itself stays out of the token so a line
// comment never swallows the row break.
func (s *scanner) lineEnd(pos int) int {
	if n := strings.IndexByte(s.src[pos:], '\n'); n >= 0 {
		return pos + n
	}
	return len(s.src)
}

// blockCommentEnd closes a `/* … */`. Postgres and DuckDB nest block
// comments, so an inner `/*` there needs its own `*/`; MySQL and SQLite
// close at the first one.
func (s *scanner) blockCommentEnd() int {
	nests := s.d == Postgres || s.d == DuckDB
	depth := 1
	j := s.i + 2
	for j < len(s.src) {
		switch {
		case s.src[j] == '*' && s.at(j+1) == '/':
			depth--
			j += 2
			if depth == 0 {
				return j
			}
		case nests && s.src[j] == '/' && s.at(j+1) == '*':
			depth++
			j += 2
		default:
			j++
		}
	}
	return len(s.src)
}

// quotedEnd closes the literal opening at pos. A doubled quote is always
// an escaped quote, and in MySQL a backslash escapes the next byte too.
// An unterminated literal ends the input.
func (s *scanner) quotedEnd(pos int, q byte, backslash bool) int {
	for j := pos + 1; j < len(s.src); {
		switch {
		case backslash && s.src[j] == '\\':
			j += 2
		case s.src[j] == q && s.at(j+1) == q:
			j += 2
		case s.src[j] == q:
			return j + 1
		default:
			j++
		}
	}
	return len(s.src)
}

// backslashEscapes reports whether a string literal honours backslash
// escapes. Only MySQL does by default — see
// wiki/reference/sql-literal-escaping.
func (s *scanner) backslashEscapes() bool { return s.d == MySQL }

// scanDollar handles the three things `$` can start: a Postgres numbered
// placeholder (`$1`), a dollar-quoted string (`$tag$ … $tag$`), or, in
// every other position, a bare operator.
func (s *scanner) scanDollar() {
	if s.d != Postgres && s.d != DuckDB {
		s.emit(Operator, s.i+1)
		return
	}
	if isDigitAt(s.src, s.i+1) {
		j := s.i + 1
		for isDigitAt(s.src, j) {
			j++
		}
		s.emit(Placeholder, j)
		return
	}
	// The tag is an identifier (possibly empty, for `$$`), closed by a
	// second `$`. Anything else is not a dollar quote.
	j := s.i + 1
	for j < len(s.src) {
		r, size := utf8.DecodeRuneInString(s.src[j:])
		if !isIdentPart(r) || r == '$' {
			break
		}
		j += size
	}
	if s.at(j) != '$' {
		s.emit(Operator, s.i+1)
		return
	}
	tag := s.src[s.i : j+1]
	if n := strings.Index(s.src[j+1:], tag); n >= 0 {
		s.emit(String, j+1+n+len(tag))
		return
	}
	s.emit(String, len(s.src))
}

// stringPrefixes are the letters that turn an immediately following
// quote into a typed string literal rather than an identifier followed
// by one: E” (escape), X”/B” (blob and bit), N” (national).
const stringPrefixes = "EXBN"

func (s *scanner) scanWord() {
	end := s.identEnd(s.i)
	word := s.src[s.i:end]
	if len(word) == 1 && s.at(end) == '\'' &&
		strings.ContainsRune(stringPrefixes, unicode.ToUpper(rune(word[0]))) {
		s.emit(String, s.quotedEnd(end, '\'', true))
		return
	}
	if s.kw[strings.ToUpper(word)] {
		s.emit(Keyword, end)
		return
	}
	s.emit(Ident, end)
}

// identEnd walks the identifier characters starting at pos. It returns
// pos itself when there are none, which emit turns into a one-rune token
// — the case that keeps a bare `?` or `:` from stalling the scan.
func (s *scanner) identEnd(pos int) int {
	j := pos
	for j < len(s.src) {
		r, size := utf8.DecodeRuneInString(s.src[j:])
		if !isIdentPart(r) {
			break
		}
		j += size
	}
	return j
}

// numberEnd covers hex (0x…), decimals and exponents. It is generous on
// purpose: a half-typed `1.` or `1e` is still a number to the eye.
func (s *scanner) numberEnd() int {
	j := s.i
	if s.src[j] == '0' && (s.at(j+1) == 'x' || s.at(j+1) == 'X') {
		j += 2
		for isHexAt(s.src, j) {
			j++
		}
		return j
	}
	for isDigitAt(s.src, j) {
		j++
	}
	if s.at(j) == '.' {
		j++
		for isDigitAt(s.src, j) {
			j++
		}
	}
	if s.at(j) == 'e' || s.at(j) == 'E' {
		k := j + 1
		if s.at(k) == '+' || s.at(k) == '-' {
			k++
		}
		if isDigitAt(s.src, k) {
			j = k
			for isDigitAt(s.src, j) {
				j++
			}
		}
	}
	return j
}

func isIdentStart(r rune) bool { return r == '_' || unicode.IsLetter(r) }

// isIdentPart includes `$`, which MySQL and DuckDB allow inside
// identifiers. Where it is not part of one the scanner has already
// claimed it in scanDollar.
func isIdentPart(r rune) bool { return isIdentStart(r) || unicode.IsDigit(r) || r == '$' }

func isIdentStartAt(src string, pos int) bool {
	if pos < 0 || pos >= len(src) {
		return false
	}
	r, _ := utf8.DecodeRuneInString(src[pos:])
	return isIdentStart(r)
}

func isDigitAt(src string, pos int) bool {
	return pos >= 0 && pos < len(src) && src[pos] >= '0' && src[pos] <= '9'
}

func isHexAt(src string, pos int) bool {
	if pos < 0 || pos >= len(src) {
		return false
	}
	c := src[pos]
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}
