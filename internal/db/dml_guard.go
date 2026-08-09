package db

import (
	"strings"

	"lazysql/internal/sqlhl"
)

// A DELETE or UPDATE with neither a WHERE nor a LIMIT touches every row
// in its table — the one shape of DML from the query editor worth an
// extra confirmation. Detection runs on sqlhl's tokenizer rather than
// substring search, so a WHERE inside a comment or a string literal does
// not suppress the warning, and a WHERE inside a parenthesized subquery
// does not count for the statement around it.

// UnguardedWrite is one DELETE or UPDATE statement that has no WHERE or
// LIMIT at its own level.
type UnguardedWrite struct {
	Statement string
	Verb      string // "DELETE" or "UPDATE"
	// Table is a best-effort read of the statement's target, for naming
	// in the confirm modal. It is display only.
	Table string
}

// FindUnguardedWrites scans stmts and returns every DELETE/UPDATE among
// them that lacks a top-level WHERE or LIMIT clause. Statements of any
// other kind, and guarded DELETE/UPDATE statements, are not reported.
func FindUnguardedWrites(engine Engine, stmts []string) []UnguardedWrite {
	d := sqlhl.For(string(engine))
	var out []UnguardedWrite
	for _, s := range stmts {
		verb := strings.ToUpper(firstWord(TrimLeadingComments(s)))
		if verb != "DELETE" && verb != "UPDATE" {
			continue
		}
		if hasTopLevelGuard(d, s) {
			continue
		}
		out = append(out, UnguardedWrite{Statement: s, Verb: verb, Table: targetTable(d, s, verb)})
	}
	return out
}

// hasTopLevelGuard reports whether sql has a WHERE or LIMIT keyword
// outside of any parentheses. Depth is tracked over every '(' and ')'
// rune inside Operator tokens rather than by comparing whole tokens,
// because the scanner merges adjacent operator runes into one token
// (e.g. "()" or "),") and a whole-token match would miss them.
func hasTopLevelGuard(d sqlhl.Dialect, sql string) bool {
	depth := 0
	for _, t := range sqlhl.Tokenize(d, sql) {
		switch t.Kind {
		case sqlhl.Operator:
			for _, r := range t.Text(sql) {
				switch r {
				case '(':
					depth++
				case ')':
					if depth > 0 {
						depth--
					}
				}
			}
		case sqlhl.Keyword:
			if depth == 0 {
				switch strings.ToUpper(t.Text(sql)) {
				case "WHERE", "LIMIT":
					return true
				}
			}
		}
	}
	return false
}

// targetTable reads the identifier right after UPDATE, or after
// DELETE's FROM, as a dotted name (schema.table). It gives up — an
// empty string — on anything it does not recognize, which is fine for a
// display-only best effort.
func targetTable(d sqlhl.Dialect, sql string, verb string) string {
	toks := sqlhl.Tokenize(d, sql)
	i := 0
	for i < len(toks) && !(toks[i].Kind == sqlhl.Keyword && strings.EqualFold(toks[i].Text(sql), verb)) {
		i++
	}
	if i >= len(toks) {
		return ""
	}
	i = skipTrivia(toks, i+1)
	if verb == "DELETE" {
		if i < len(toks) && toks[i].Kind == sqlhl.Keyword && strings.EqualFold(toks[i].Text(sql), "FROM") {
			i = skipTrivia(toks, i+1)
		}
	}

	var b strings.Builder
	for i < len(toks) {
		t := toks[i]
		switch {
		case t.Kind == sqlhl.Ident || t.Kind == sqlhl.QuotedIdent:
			b.WriteString(unquoteIdent(t.Text(sql)))
		case t.Kind == sqlhl.Operator && t.Text(sql) == ".":
			b.WriteString(".")
		default:
			return b.String()
		}
		i++
	}
	return b.String()
}

func skipTrivia(toks []sqlhl.Token, i int) int {
	for i < len(toks) && (toks[i].Kind == sqlhl.Plain || toks[i].Kind == sqlhl.Comment) {
		i++
	}
	return i
}
