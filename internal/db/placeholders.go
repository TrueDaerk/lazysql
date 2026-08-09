package db

import (
	"fmt"
	"strings"

	"lazysql/internal/sqlhl"
)

// Placeholder is one parameter hole in a statement the UI has to ask a
// value for: a positional `?` or a named `:name`. The other spellings the
// tokenizer knows — PostgreSQL's `$1`, MySQL's `@var` — are not prompted
// for: `$1` belongs to already-parameterized SQL and `@var` is a server
// variable, not a hole.
type Placeholder struct {
	// Name is the identifier of a `:name` placeholder, "" for `?`.
	Name string
	// Label names the placeholder for a prompt: the name, or "?" with
	// its 1-based position for positional ones.
	Label string
}

// ExtractPlaceholders returns the prompt-able placeholders of one
// statement in order of first appearance, with repeated `:name`s
// deduplicated — one prompt, bound to every position it appears in.
//
// Detection is tokenizer-based: `?` and `:name` inside string literals,
// comments or quoted identifiers are text, and `::type` is a cast, all of
// which sqlhl already refuses to tokenize as placeholders.
func ExtractPlaceholders(engine Engine, sql string) []Placeholder {
	var out []Placeholder
	seen := map[string]bool{}
	positional := 0
	for _, t := range sqlhl.Tokenize(sqlhl.For(string(engine)), sql) {
		name, ok := promptName(t, sql)
		if !ok {
			continue
		}
		if name == "" {
			positional++
			out = append(out, Placeholder{Label: fmt.Sprintf("? (%d)", positional)})
			continue
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, Placeholder{Name: name, Label: ":" + name})
	}
	return out
}

// BindPlaceholders rewrites sql so every prompt-able placeholder becomes
// the dialect's own parameter marker, and returns the argument list to
// bind. values are the entered strings, ordered like ExtractPlaceholders
// returned the placeholders; a repeated `:name` takes its one value at
// every position. The values never enter the statement text — they only
// ever travel as bound parameters.
func BindPlaceholders(d Dialect, engine Engine, sql string, values []string) (string, []any, error) {
	phs := ExtractPlaceholders(engine, sql)
	if len(values) != len(phs) {
		return "", nil, fmt.Errorf("db: %d placeholder values for %d placeholders", len(values), len(phs))
	}
	byName := map[string]string{}
	for i, p := range phs {
		if p.Name != "" {
			byName[p.Name] = values[i]
		}
	}

	var b strings.Builder
	var args []any
	positional := 0
	last := 0
	for _, t := range sqlhl.Tokenize(sqlhl.For(string(engine)), sql) {
		name, ok := promptName(t, sql)
		if !ok {
			continue
		}
		var v string
		if name == "" {
			// Positional values are consumed left to right, the order
			// ExtractPlaceholders listed them in.
			v = values[positionalIndex(phs, positional)]
			positional++
		} else {
			v = byName[name]
		}
		args = append(args, v)
		b.WriteString(sql[last:t.Start])
		b.WriteString(d.Placeholder(len(args)))
		last = t.End
	}
	b.WriteString(sql[last:])
	return b.String(), args, nil
}

// positionalIndex maps the n-th positional placeholder (0-based) onto its
// slot in the ExtractPlaceholders order, where positional and named
// placeholders interleave.
func positionalIndex(phs []Placeholder, n int) int {
	seen := 0
	for i, p := range phs {
		if p.Name != "" {
			continue
		}
		if seen == n {
			return i
		}
		seen++
	}
	return 0
}

// promptName classifies one token: ok reports a prompt-able placeholder,
// and name is its identifier ("" for positional `?`).
func promptName(t sqlhl.Token, sql string) (string, bool) {
	if t.Kind != sqlhl.Placeholder {
		return "", false
	}
	text := t.Text(sql)
	switch {
	case strings.HasPrefix(text, "?"):
		return "", true
	case strings.HasPrefix(text, ":"):
		return text[1:], true
	}
	return "", false
}
