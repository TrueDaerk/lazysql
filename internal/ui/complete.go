package ui

import (
	"sort"
	"strings"
	"unicode"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"lazysql/internal/db"
	"lazysql/internal/sqlhl"
)

// Autocomplete in the query editor is a floating list anchored under the
// caret, composited by the same lipgloss layer mechanism as the modals —
// but it is *not* a modal: a modal swallows every key, and this one only
// claims five of them so typing keeps narrowing the list.
//
// Three decisions shape the rest of this file:
//
//   - The suggestion list is derived, never accumulated. Every refresh
//     rebuilds it from the buffer, the relation list and the column
//     cache, so there is no stale state to reconcile with an edit made
//     somewhere else.
//   - The replaced text is recomputed at accept time, not remembered
//     from the refresh. The popup's *items* may be a keystroke old; the
//     region an accepted item overwrites never is.
//   - Buffer analysis is a token scan, not a parse. `sqlhl.Tokenize`
//     already reads half-written SQL without failing, and matching its
//     identifier tokens against the known relation names is enough to
//     know which tables a statement mentions.

const (
	// minCompletionPrefix is how many identifier characters typing must
	// produce before the popup opens by itself. One character matches
	// most of the schema and would make the popup a permanent fixture;
	// ctrl+space still opens it on nothing at all.
	minCompletionPrefix = 2
	// maxCompletionItems caps the ranked list. Nothing below it is
	// reachable in a popup this size, and ranking a whole catalog on
	// every keystroke is work with no reader.
	maxCompletionItems = 200
	// completionRows is how many suggestions are visible at once.
	completionRows = 8
	// completionWidth caps the popup's text column.
	completionWidth = 44
)

// ---------- items and ranking ----------

// completionKind is where a suggestion came from. It is the tag the popup
// shows and the tie-breaker in the ranking: what a user cannot remember
// is the schema, not the keyword list, so schema names sort first.
type completionKind int

const (
	completeColumn completionKind = iota
	completeTable
	completeView
	completeKeyword
)

var completionTags = [...]string{"col", "table", "view", "kw"}

func (k completionKind) tag() string {
	if int(k) < len(completionTags) {
		return completionTags[k]
	}
	return ""
}

// completionItem is one suggestion. detail is the extra the popup shows
// to the right — the relation a column belongs to — and is empty for
// everything else.
type completionItem struct {
	text   string
	kind   completionKind
	detail string
}

// rankCompletions filters items by word and orders the survivors. The
// order is, in this priority:
//
//  1. prefix matches before substring matches — a user typing `us` wants
//     `users` above `campus_id`;
//  2. schema before keywords, by kind;
//  3. the shorter identifier, then alphabetically, so the order is
//     stable and a repeated keystroke never reshuffles the list.
//
// Matching is case-insensitive throughout: SQL identifiers are typed in
// whatever case the writer feels like, and the catalog's casing is not a
// thing anyone remembers.
func rankCompletions(word string, items []completionItem, limit int) []completionItem {
	lw := strings.ToLower(word)
	type scored struct {
		item  completionItem
		class int
	}
	out := make([]scored, 0, len(items))
	seen := make(map[string]bool, len(items))
	for _, it := range items {
		lt := strings.ToLower(it.text)
		class := -1
		switch {
		case lw == "":
			class = 0
		case strings.HasPrefix(lt, lw):
			class = 0
		case strings.Contains(lt, lw):
			class = 1
		}
		if class < 0 {
			continue
		}
		// The same name can reach here twice — a column of two joined
		// tables, a table whose name is also a keyword. The first one
		// wins, which is the highest-ranked source it came from.
		key := string(rune(it.kind)) + "\x00" + lt
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, scored{item: it, class: class})
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.class != b.class {
			return a.class < b.class
		}
		if a.item.kind != b.item.kind {
			return a.item.kind < b.item.kind
		}
		if len(a.item.text) != len(b.item.text) {
			return len(a.item.text) < len(b.item.text)
		}
		return strings.ToLower(a.item.text) < strings.ToLower(b.item.text)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	ranked := make([]completionItem, 0, len(out))
	for _, s := range out {
		ranked = append(ranked, s.item)
	}
	return ranked
}

// ---------- reading the buffer ----------

// completionContext is what the buffer says about the word under the
// caret: which line it is on, the rune range it occupies, and the
// relation it is qualified by, if any.
type completionContext struct {
	line      int
	start     int // rune index in the line where the word starts
	col       int // rune index of the caret
	word      string
	qualifier string // the identifier before a `.`, "" when unqualified
}

// completable reports whether there is anything for the popup to work
// from. `tab` uses it to decide between completing and typing a tab.
func (c completionContext) completable() bool { return c.word != "" || c.qualifier != "" }

// isIdentRune is the character class of an unquoted SQL identifier, wide
// enough for the dialects lazysql speaks (`$` is legal inside one on
// MySQL and DuckDB).
func isIdentRune(r rune) bool {
	return r == '_' || r == '$' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// completionContextAt reads the word ending at col on one line. It never
// looks at other lines: a SQL identifier cannot span a newline, and a
// qualifier written across one is not worth the ambiguity.
func completionContextAt(line string, col int) completionContext {
	runes := []rune(line)
	if col > len(runes) {
		col = len(runes)
	}
	if col < 0 {
		col = 0
	}
	start := col
	for start > 0 && isIdentRune(runes[start-1]) {
		start--
	}
	// An unquoted identifier cannot start with a digit, so a run that
	// does is part of a number, not a word being completed. Leaving
	// start at the caret also keeps an accepted item from overwriting
	// the digits.
	if start < col && unicode.IsDigit(runes[start]) {
		start = col
	}
	ctx := completionContext{start: start, col: col, word: string(runes[start:col])}
	// A dot immediately before the word makes it a member of whatever
	// identifier precedes the dot: `users.na` completes the columns of
	// `users` and nothing else.
	if start > 0 && runes[start-1] == '.' {
		ctx.qualifier = qualifierBefore(runes, start-1)
	}
	return ctx
}

// qualifierBefore reads the identifier ending at end — the position of
// the dot — delimited or bare. It returns "" when there is nothing that
// could name a relation there, which is also what `1.5` gives: a numeric
// literal qualifies nothing.
func qualifierBefore(runes []rune, end int) string {
	start := end
	if end > 0 && isQuoteRune(runes[end-1]) {
		// A delimited qualifier runs back to its matching opener, which
		// is the only way `"my table".col` can be read as one name.
		q := runes[end-1]
		i := end - 1
		for i > 0 && runes[i-1] != q {
			i--
		}
		if i == 0 {
			return ""
		}
		start = i - 1
	} else {
		for start > 0 && isIdentRune(runes[start-1]) {
			start--
		}
		if start < end && unicode.IsDigit(runes[start]) {
			return ""
		}
	}
	return unquoteIdent(string(runes[start:end]))
}

func isQuoteRune(r rune) bool { return r == '"' || r == '`' }

// unquoteIdent strips the delimiters a user may have typed around an
// identifier, so `"users".` qualifies the same relation as `users.`.
func unquoteIdent(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if q := s[0]; (q == '"' || q == '`') && s[len(s)-1] == q {
			return strings.ReplaceAll(s[1:len(s)-1], string(q)+string(q), string(q))
		}
	}
	return s
}

// referencedRelations returns the relations of known that the buffer
// mentions, in the order they first appear. It is a token scan, not a
// parse: every identifier in the buffer is looked up in the relation
// list, so `FROM users u JOIN orders` finds both without knowing what
// FROM or JOIN mean, and a column that happens to share a table's name
// costs one extra column list.
func referencedRelations(d sqlhl.Dialect, src string, known []db.Relation) []string {
	if len(known) == 0 || strings.TrimSpace(src) == "" {
		return nil
	}
	byName := make(map[string]string, len(known))
	for _, r := range known {
		byName[strings.ToLower(r.Name)] = r.Name
	}
	var out []string
	seen := map[string]bool{}
	for _, t := range sqlhl.Tokenize(d, src) {
		if t.Kind != sqlhl.Ident && t.Kind != sqlhl.QuotedIdent {
			continue
		}
		name, ok := byName[strings.ToLower(unquoteIdent(t.Text(src)))]
		if !ok || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// ---------- quoting ----------

// quoteCompletion spells an identifier the way it has to be written to
// resolve back to itself, and bare whenever it does not have to be
// written any other way. Over-quoting is not free: `"users"` in a buffer
// the user then edits by hand is noise, and on MySQL a backtick where
// none was needed is a portability trap.
//
// It quotes when the name is not a plain `[A-Za-z_][A-Za-z0-9_]*` word,
// when it collides with a keyword, and — on PostgreSQL only — when it is
// not already lower-case, because Postgres folds an unquoted identifier
// and a table created as "Users" would otherwise resolve to `users`.
func quoteCompletion(dialect db.Dialect, hl sqlhl.Dialect, name string) string {
	if !needsQuoting(hl, name) || dialect == nil {
		return name
	}
	return dialect.QuoteIdent(name)
}

func needsQuoting(hl sqlhl.Dialect, name string) bool {
	if name == "" {
		return true
	}
	for i, r := range name {
		switch {
		case r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return true
		}
	}
	if hl == sqlhl.Postgres && name != strings.ToLower(name) {
		return true
	}
	return sqlhl.IsKeyword(hl, name)
}

// ---------- popup state ----------

// completion is the popup's state. The scroll offset is deliberately
// absent: it is derived from the cursor at render time, the same rule the
// editor's own scroll window follows, so there is no second position that
// can drift out of step with the list.
type completion struct {
	open   bool
	items  []completionItem
	cursor int
	// loading marks a column fetch in flight for a relation the buffer
	// mentions, so the popup can say that more is coming rather than
	// looking complete while it is not.
	loading bool
	// explicit records that ctrl+space asked for this popup. It has to
	// survive a rebuild: a request on a prefix too short to auto-trigger
	// would otherwise close itself the moment the metadata it is waiting
	// for arrives.
	explicit bool
}

// selected returns the item the popup would insert.
func (c completion) selected() (completionItem, bool) {
	if !c.open || c.cursor < 0 || c.cursor >= len(c.items) {
		return completionItem{}, false
	}
	return c.items[c.cursor], true
}

// ---------- assembling the suggestions ----------

// editorContext reads the word under the editor's caret.
func (m Model) editorContext() completionContext {
	lines := strings.Split(m.script(), "\n")
	row := m.editor.area.Line()
	if row < 0 || row >= len(lines) {
		return completionContext{line: row}
	}
	ctx := completionContextAt(lines[row], m.editor.area.Column())
	ctx.line = row
	return ctx
}

// completionSources builds the unranked suggestion list and the relations
// whose columns it would like fetched. The two come back together
// because they are read from the same scan of the buffer.
func (m Model) completionSources(ctx completionContext) (items []completionItem, want []string) {
	dialect := m.sqlDialect()
	referenced := referencedRelations(dialect, m.script(), m.relations)

	// A qualified word is a member access: only that relation's columns
	// can complete it, and nothing else belongs in the list.
	if ctx.qualifier != "" {
		rel, ok := m.relationByName(ctx.qualifier)
		if !ok {
			return nil, referenced
		}
		for _, c := range m.schemaColumns(rel.Name) {
			items = append(items, completionItem{text: c, kind: completeColumn, detail: rel.Name})
		}
		return items, append(referenced, rel.Name)
	}

	for _, name := range referenced {
		for _, c := range m.schemaColumns(name) {
			items = append(items, completionItem{text: c, kind: completeColumn, detail: name})
		}
	}
	for _, r := range m.relations {
		kind := completeTable
		if r.Kind == db.RelationView {
			kind = completeView
		}
		items = append(items, completionItem{text: r.Name, kind: kind})
	}
	for _, w := range sqlhl.Keywords(dialect) {
		items = append(items, completionItem{text: w, kind: completeKeyword})
	}
	return items, referenced
}

// refreshCompletion rebuilds the popup from the buffer. explicit is
// ctrl+space (or tab on a word): it opens the popup on a prefix too short
// to trigger it by itself, including no prefix at all.
//
// It returns the metadata fetches the new list wants; it never waits for
// them. The popup shows what is cached and the reply re-renders it.
func (m *Model) refreshCompletion(explicit bool) tea.Cmd {
	// The cursor returns to the best match: after a keystroke the list is
	// a different list, and keeping an index into the old one would
	// select whatever happened to land at that position.
	return m.rebuildCompletion(explicit, "")
}

// restackCompletion rebuilds an open popup after a column list landed.
// It is the other half of "never block": the popup opened on what was
// cached, and this is what makes the rest appear without a keystroke.
// The selection is carried across by name — the user did not move, so
// neither should the highlight.
func (m *Model) restackCompletion() tea.Cmd {
	if !m.completion.open && !m.completion.loading {
		return nil
	}
	keep := ""
	if it, ok := m.completion.selected(); ok {
		keep = it.text
	}
	return m.rebuildCompletion(m.completion.explicit, keep)
}

// rebuildCompletion is the shared body: derive the list, start the
// fetches it wants, and place the cursor.
func (m *Model) rebuildCompletion(explicit bool, keep string) tea.Cmd {
	if m.focus != panelQuery || !m.editor.editing {
		m.completion = completion{}
		return nil
	}
	ctx := m.editorContext()
	if !explicit && len([]rune(ctx.word)) < minCompletionPrefix {
		m.completion = completion{}
		return nil
	}
	items, want := m.completionSources(ctx)
	cmd, loading := m.ensureSchemaColumns(want)
	ranked := rankCompletions(ctx.word, items, maxCompletionItems)
	if len(ranked) == 0 {
		// Nothing matches. An open popup with no rows would be a border
		// drawn over the buffer for no reason — but a fetch may still be
		// in flight, and its reply comes back through here.
		m.completion = completion{loading: loading, explicit: explicit}
		return cmd
	}
	next := completion{open: true, items: ranked, loading: loading, explicit: explicit}
	for i, it := range ranked {
		if keep != "" && it.text == keep {
			next.cursor = i
			break
		}
	}
	m.completion = next
	return cmd
}

// moveCompletion walks the list. It clamps rather than wraps: a popup
// that jumps from the last row back to the first hides how long the list
// is, and `down` held down should stop at the end.
func (m *Model) moveCompletion(delta int) {
	if !m.completion.open {
		return
	}
	c := m.completion.cursor + delta
	if c < 0 {
		c = 0
	}
	if c >= len(m.completion.items) {
		c = len(m.completion.items) - 1
	}
	m.completion.cursor = c
}

// closeCompletion is `esc` while the popup is open: it closes the popup
// and touches nothing else — not the buffer, not insert mode, not the
// focus.
func (m *Model) closeCompletion() { m.completion = completion{} }

// acceptCompletion inserts the selected item over the word under the
// caret and closes the popup. Keywords go in as they are; identifiers go
// through the dialect's quoting rules.
func (m *Model) acceptCompletion() {
	it, ok := m.completion.selected()
	m.completion = completion{}
	if !ok {
		return
	}
	text := it.text
	if it.kind != completeKeyword {
		var dialect db.Dialect
		if m.driver != nil {
			dialect = m.driver.Dialect()
		}
		text = quoteCompletion(dialect, m.sqlDialect(), text)
	}
	m.replaceEditorWord(text)
}

// replaceEditorWord overwrites the word under the caret with text and
// leaves the caret at its end. The region is read from the editor now,
// not from what the popup recorded when it opened.
func (m *Model) replaceEditorWord(text string) {
	ctx := m.editorContext()
	lines := strings.Split(m.script(), "\n")
	if ctx.line < 0 || ctx.line >= len(lines) {
		return
	}
	runes := []rune(lines[ctx.line])
	col := ctx.col
	if col > len(runes) {
		col = len(runes)
	}
	start := ctx.start
	if start > col {
		start = col
	}
	lines[ctx.line] = string(runes[:start]) + text + string(runes[col:])
	m.editor.area.SetValue(strings.Join(lines, "\n"))
	moveEditorCursor(&m.editor.area, ctx.line, start+len([]rune(text)))
}

// moveEditorCursor puts the textarea's caret on a logical line and
// column. The component has no absolute "go to row" call, so this walks
// there — which is exact only because the editor pins the textarea wide
// enough never to soft-wrap (see internal/ui/highlight.go), making one
// CursorDown one logical line.
func moveEditorCursor(ta *textarea.Model, row, col int) {
	ta.MoveToBegin()
	for i := 0; i < row; i++ {
		ta.CursorDown()
	}
	ta.SetCursorColumn(col)
}

// ---------- rendering ----------

// completionPopup renders the floating list. It returns "" when there is
// nothing to draw, so the caller can skip the layer entirely.
func (m Model) completionPopup(maxW, maxH int) string {
	c := m.completion
	if !c.open || len(c.items) == 0 {
		return ""
	}
	s := m.style
	// The box costs two cells of border in each direction.
	textW := min(completionWidth, maxInt(maxW-2, 1))
	rows := min(completionRows, len(c.items))
	if h := maxH - 2; h < rows {
		rows = maxInt(h, 1)
	}
	if c.loading {
		rows = maxInt(rows-1, 1)
	}

	// The window scrolls only as far as it must to keep the selection
	// visible, so the offset is a function of the cursor alone.
	start := 0
	if c.cursor >= rows {
		start = c.cursor - rows + 1
	}
	if max := len(c.items) - rows; start > max {
		start = maxInt(max, 0)
	}

	lines := make([]string, 0, rows+1)
	for i := start; i < start+rows && i < len(c.items); i++ {
		it := c.items[i]
		right := it.kind.tag()
		if it.detail != "" {
			right = it.detail + " " + right
		}
		lines = append(lines, completionRow(s, it.text, right, textW, i == c.cursor))
	}
	if c.loading {
		lines = append(lines, s.muted.Render(truncate("loading columns…", textW)))
	}
	return s.popup.Render(strings.Join(lines, "\n"))
}

// completionRow lays one suggestion out as `name … tag`, padded to the
// popup's width so the selection highlight is a full bar rather than a
// tint on the text.
func completionRow(s styles, text, right string, w int, selected bool) string {
	tag := ""
	if right != "" {
		tag = " " + right
	}
	name := truncate(text, maxInt(w-lipgloss.Width(tag), 1))
	gap := w - lipgloss.Width(name) - lipgloss.Width(tag)
	if gap < 0 {
		gap = 0
	}
	if selected {
		return s.popupSelected.Render(name + strings.Repeat(" ", gap) + tag)
	}
	return name + strings.Repeat(" ", gap) + s.popupTag.Render(tag)
}
