package db

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// EXPLAIN is the one read that is about a statement rather than about
// data, and the four engines disagree about all of it: the prefix, the
// shape of the answer, and whether there is a machine-readable form at
// all. The differences are settled here so the UI only ever sees a Plan
// and asks it for lines.
//
// Two rules hold for every dialect:
//
//   - ANALYZE is never added. On PostgreSQL and MySQL it *executes* the
//     statement, which for a DELETE means the rows are gone; a plan the
//     user asked to look at must not change anything. Explaining a write
//     is therefore safe on every engine lazysql supports.
//   - The plan is read through the same querier as every other
//     statement, so the EXPLAIN itself lands in the command log without
//     anything re-formatting it by hand.

// PlanFormat says which of a Plan's three renderings carries its
// content. Which one an engine produces is fixed per dialect, except on
// MySQL, where it depends on whether the server has FORMAT=JSON.
type PlanFormat int

const (
	// PlanTree is a node tree: PostgreSQL, SQLite and MySQL's JSON form.
	PlanTree PlanFormat = iota
	// PlanGrid is a tabular result: MySQL's classic EXPLAIN.
	PlanGrid
	// PlanText is the engine's own preformatted output: DuckDB.
	PlanText
)

// PlanNode is one node of a query plan tree. Label names the operation,
// Detail is the engine's cost/row estimate for it (empty when it has
// none), and Notes are the conditions that belong to the node — filters,
// join and index conditions, sort keys — rendered under it.
type PlanNode struct {
	Label    string
	Detail   string
	Notes    []string
	Children []PlanNode
}

// Plan is a dialect-agnostic query plan. Exactly one of Nodes, Grid and
// Raw is filled, per Format; Lines renders whichever it is.
type Plan struct {
	Engine Engine
	// SQL is the EXPLAIN statement as it was executed, for the log line
	// and the view's header.
	SQL    string
	Format PlanFormat

	Nodes []PlanNode
	Grid  *ResultSet
	Raw   string

	// Note is a remark about how the plan was obtained — currently only
	// MySQL's fallback from FORMAT=JSON to the tabular form. Empty when
	// there is nothing to say.
	Note string
}

// Lines renders the plan as display lines, indented tree first. It never
// styles anything: the UI decides how a line looks.
func (p *Plan) Lines() []string {
	if p == nil {
		return nil
	}
	var out []string
	if p.Note != "" {
		out = append(out, p.Note, "")
	}
	switch p.Format {
	case PlanGrid:
		out = append(out, gridLines(p.Grid)...)
	case PlanText:
		out = append(out, strings.Split(strings.TrimRight(p.Raw, "\n"), "\n")...)
	default:
		for _, n := range p.Nodes {
			out = append(out, nodeLines(n, 0)...)
		}
	}
	return out
}

// RenderText is the plan as one block of text, for `y` and for a file.
func (p *Plan) RenderText() string {
	return strings.Join(p.Lines(), "\n")
}

// nodeLines renders one node and its subtree. Children are prefixed with
// `->` at their own indent, the way PostgreSQL's own text format marks
// them, so a deep plan stays readable in a narrow main view.
func nodeLines(n PlanNode, depth int) []string {
	indent := strings.Repeat("  ", depth)
	head := indent
	if depth > 0 {
		head += "-> "
	}
	head += n.Label
	if n.Detail != "" {
		head += "  " + n.Detail
	}
	out := []string{head}
	noteIndent := indent + "   "
	if depth == 0 {
		noteIndent = indent + "  "
	}
	for _, note := range n.Notes {
		out = append(out, noteIndent+note)
	}
	for _, c := range n.Children {
		out = append(out, nodeLines(c, depth+1)...)
	}
	return out
}

// gridLines renders a tabular plan as aligned columns with a header.
func gridLines(rs *ResultSet) []string {
	if rs == nil || len(rs.Columns) == 0 {
		return nil
	}
	widths := make([]int, len(rs.Columns))
	for i, c := range rs.Columns {
		widths[i] = len(c.Name)
	}
	cells := make([][]string, 0, len(rs.Rows))
	for _, row := range rs.Rows {
		line := make([]string, len(rs.Columns))
		for i := range rs.Columns {
			if i < len(row) {
				line[i] = FormatValue(row[i], "NULL")
			}
			if n := len(line[i]); n > widths[i] {
				widths[i] = n
			}
		}
		cells = append(cells, line)
	}
	pad := func(vals []string) string {
		parts := make([]string, len(vals))
		for i, v := range vals {
			parts[i] = v + strings.Repeat(" ", widths[i]-len(v))
		}
		return strings.TrimRight(strings.Join(parts, "  "), " ")
	}
	header := make([]string, len(rs.Columns))
	rule := make([]string, len(rs.Columns))
	for i, c := range rs.Columns {
		header[i] = c.Name
		rule[i] = strings.Repeat("-", widths[i])
	}
	out := []string{pad(header), pad(rule)}
	for _, line := range cells {
		out = append(out, pad(line))
	}
	return out
}

// explainBody strips the trailing semicolon and whitespace a buffer's
// statement carries: `EXPLAIN SELECT 1;` is fine, but a statement that
// is only a semicolon is not something to send.
func explainBody(sql string) (string, error) {
	s := strings.TrimSpace(sql)
	for strings.HasSuffix(s, ";") {
		s = strings.TrimSpace(strings.TrimSuffix(s, ";"))
	}
	if s == "" {
		return "", fmt.Errorf("db: nothing to explain")
	}
	return s, nil
}

// ---------- PostgreSQL ----------

// pgPlanRoot is one entry of `EXPLAIN (FORMAT JSON)`'s top-level array.
type pgPlanRoot struct {
	Plan pgPlanNode `json:"Plan"`
}

// pgPlanNode is the subset of PostgreSQL's JSON plan lazysql renders.
// Unknown keys are ignored: the format grows with every release and a
// new one is not a reason to fail.
type pgPlanNode struct {
	NodeType     string  `json:"Node Type"`
	Operation    string  `json:"Operation"`
	JoinType     string  `json:"Join Type"`
	RelationName string  `json:"Relation Name"`
	Alias        string  `json:"Alias"`
	IndexName    string  `json:"Index Name"`
	CTEName      string  `json:"CTE Name"`
	StartupCost  float64 `json:"Startup Cost"`
	TotalCost    float64 `json:"Total Cost"`
	PlanRows     float64 `json:"Plan Rows"`
	PlanWidth    int     `json:"Plan Width"`

	IndexCond   string   `json:"Index Cond"`
	RecheckCond string   `json:"Recheck Cond"`
	Filter      string   `json:"Filter"`
	HashCond    string   `json:"Hash Cond"`
	MergeCond   string   `json:"Merge Cond"`
	JoinFilter  string   `json:"Join Filter"`
	SortKey     []string `json:"Sort Key"`
	GroupKey    []string `json:"Group Key"`

	Plans []pgPlanNode `json:"Plans"`
}

// ParsePostgresPlan turns `EXPLAIN (FORMAT JSON)` output into plan
// nodes. It is exported so the JSON shape can be tested against captured
// server output without a live PostgreSQL.
func ParsePostgresPlan(raw string) ([]PlanNode, error) {
	var roots []pgPlanRoot
	if err := json.Unmarshal([]byte(raw), &roots); err != nil {
		return nil, fmt.Errorf("db: unreadable PostgreSQL plan: %w", err)
	}
	if len(roots) == 0 {
		return nil, fmt.Errorf("db: empty PostgreSQL plan")
	}
	out := make([]PlanNode, 0, len(roots))
	for _, r := range roots {
		out = append(out, pgNode(r.Plan))
	}
	return out, nil
}

func pgNode(n pgPlanNode) PlanNode {
	label := n.NodeType
	if n.Operation != "" && !strings.Contains(label, n.Operation) {
		label += " " + n.Operation
	}
	if n.JoinType != "" && !strings.Contains(label, n.JoinType) {
		label += " (" + n.JoinType + ")"
	}
	if n.IndexName != "" {
		label += " using " + n.IndexName
	}
	switch {
	case n.RelationName != "":
		label += " on " + n.RelationName
		if n.Alias != "" && n.Alias != n.RelationName {
			label += " " + n.Alias
		}
	case n.CTEName != "":
		label += " on " + n.CTEName
	}

	out := PlanNode{
		Label: label,
		Detail: fmt.Sprintf("(cost=%.2f..%.2f rows=%s width=%d)",
			n.StartupCost, n.TotalCost, trimFloat(n.PlanRows), n.PlanWidth),
	}
	for _, note := range []struct{ key, val string }{
		{"Index Cond", n.IndexCond},
		{"Recheck Cond", n.RecheckCond},
		{"Filter", n.Filter},
		{"Hash Cond", n.HashCond},
		{"Merge Cond", n.MergeCond},
		{"Join Filter", n.JoinFilter},
		{"Sort Key", strings.Join(n.SortKey, ", ")},
		{"Group Key", strings.Join(n.GroupKey, ", ")},
	} {
		if note.val != "" {
			out.Notes = append(out.Notes, note.key+": "+note.val)
		}
	}
	for _, c := range n.Plans {
		out.Children = append(out.Children, pgNode(c))
	}
	return out
}

// trimFloat renders a row estimate without a trailing ".0": PostgreSQL
// reports whole rows as JSON numbers.
func trimFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func (d postgresDialect) explain(ctx context.Context, q querier, sql string) (*Plan, error) {
	body, err := explainBody(sql)
	if err != nil {
		return nil, err
	}
	stmt := "EXPLAIN (FORMAT JSON) " + body
	raw, err := scanSingleValue(ctx, q, stmt)
	if err != nil {
		return nil, err
	}
	nodes, err := ParsePostgresPlan(raw)
	if err != nil {
		return nil, err
	}
	return &Plan{Engine: EnginePostgres, SQL: stmt, Format: PlanTree, Nodes: nodes}, nil
}

// ---------- MySQL / MariaDB ----------

// ParseMySQLPlan turns `EXPLAIN FORMAT=JSON` output into plan nodes.
// MySQL's JSON plan has no fixed schema — every node is a bag of keys
// that differs by access method and server version — so it is walked
// generically: scalars become notes on their node, objects and arrays
// become children. Key order is the server's, which is the order that
// reads like a plan.
func ParseMySQLPlan(raw string) ([]PlanNode, error) {
	v, err := parseOrderedJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("db: unreadable MySQL plan: %w", err)
	}
	n := jsonNode("query plan", v)
	return []PlanNode{n}, nil
}

// jsonNode renders one ordered-JSON value as a plan node labelled name.
func jsonNode(name string, v *jsonValue) PlanNode {
	out := PlanNode{Label: name}
	switch {
	case v == nil:
		return out
	case v.arr != nil:
		for i, e := range v.arr {
			out.Children = append(out.Children, jsonNode(fmt.Sprintf("%s[%d]", name, i), e))
		}
	case v.obj != nil:
		for _, k := range v.keys {
			child := v.obj[k]
			if child.scalar != nil {
				out.Notes = append(out.Notes, k+": "+*child.scalar)
				continue
			}
			out.Children = append(out.Children, jsonNode(k, child))
		}
	case v.scalar != nil:
		out.Detail = *v.scalar
	}
	return out
}

func (d mysqlDialect) explain(ctx context.Context, q querier, sql string) (*Plan, error) {
	body, err := explainBody(sql)
	if err != nil {
		return nil, err
	}
	// FORMAT=JSON is the richer answer and has been there since MySQL
	// 5.7 and MariaDB 10.1, but a plan is worth having on an older
	// server too — so a failure falls back to the tabular form rather
	// than costing the user the feature. Only the JSON attempt's error
	// is dropped; the fallback's is reported.
	jsonStmt := "EXPLAIN FORMAT=JSON " + body
	if raw, err := scanSingleValue(ctx, q, jsonStmt); err == nil {
		if nodes, perr := ParseMySQLPlan(raw); perr == nil {
			return &Plan{Engine: d.engine, SQL: jsonStmt, Format: PlanTree, Nodes: nodes}, nil
		}
	}
	stmt := "EXPLAIN " + body
	rows, err := q.QueryContext(ctx, stmt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rs, err := scanPlanGrid(rows)
	if err != nil {
		return nil, err
	}
	return &Plan{
		Engine: d.engine, SQL: stmt, Format: PlanGrid, Grid: rs,
		Note: "-- FORMAT=JSON unavailable: tabular EXPLAIN",
	}, nil
}

// ---------- SQLite ----------

// sqlitePlanRow is one row of `EXPLAIN QUERY PLAN`: a node id, its
// parent's id (0 at the top) and the text SQLite describes it with.
type sqlitePlanRow struct {
	ID     int64
	Parent int64
	Detail string
}

// BuildSQLitePlan assembles the id/parent rows of EXPLAIN QUERY PLAN
// into a tree. Rows arrive in document order, so children keep the order
// SQLite listed them in. A row whose parent is not in the result is
// attached at the top rather than dropped.
func BuildSQLitePlan(rows []sqlitePlanRow) []PlanNode {
	type entry struct {
		node     *PlanNode
		children []*entry
	}
	byID := map[int64]*entry{}
	order := make([]*entry, 0, len(rows))
	for _, r := range rows {
		e := &entry{node: &PlanNode{Label: r.Detail}}
		byID[r.ID] = e
		order = append(order, e)
	}
	var roots []*entry
	for i, r := range rows {
		e := order[i]
		if p, ok := byID[r.Parent]; ok && r.Parent != r.ID {
			p.children = append(p.children, e)
			continue
		}
		roots = append(roots, e)
	}
	var build func(e *entry) PlanNode
	build = func(e *entry) PlanNode {
		n := *e.node
		for _, c := range e.children {
			n.Children = append(n.Children, build(c))
		}
		return n
	}
	out := make([]PlanNode, 0, len(roots))
	for _, r := range roots {
		out = append(out, build(r))
	}
	return out
}

func (sqliteDialect) explain(ctx context.Context, q querier, sql string) (*Plan, error) {
	body, err := explainBody(sql)
	if err != nil {
		return nil, err
	}
	stmt := "EXPLAIN QUERY PLAN " + body
	rows, err := q.QueryContext(ctx, stmt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var plan []sqlitePlanRow
	for rows.Next() {
		var id, parent, notused any
		var detail any
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			return nil, err
		}
		plan = append(plan, sqlitePlanRow{
			ID:     asInt(id),
			Parent: asInt(parent),
			Detail: FormatValue(normalizeValue(detail), ""),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	nodes := BuildSQLitePlan(plan)
	if len(nodes) == 0 {
		nodes = []PlanNode{{Label: "(no plan — SQLite reported no steps)"}}
	}
	return &Plan{Engine: EngineSQLite, SQL: stmt, Format: PlanTree, Nodes: nodes}, nil
}

// asInt reads a driver value that should be an integer, tolerating the
// text form some drivers hand back.
func asInt(v any) int64 {
	switch x := normalizeValue(v).(type) {
	case int64:
		return x
	case float64:
		return int64(x)
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(x), 10, 64)
		return n
	}
	return 0
}

// ---------- DuckDB ----------

func (duckdbDialect) explain(ctx context.Context, q querier, sql string) (*Plan, error) {
	body, err := explainBody(sql)
	if err != nil {
		return nil, err
	}
	// DuckDB answers with (explain_key, explain_value) pairs whose value
	// is a finished ASCII box diagram. Re-parsing that into nodes would
	// only lose information, so it is passed through as preformatted
	// text — the one dialect lazysql does not render itself.
	stmt := "EXPLAIN " + body
	rows, err := q.QueryContext(ctx, stmt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rs, err := scanPlanGrid(rows)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	for _, row := range rs.Rows {
		if len(row) == 0 {
			continue
		}
		if len(row) > 1 {
			if key := FormatValue(row[0], ""); key != "" {
				b.WriteString(key + "\n")
			}
		}
		b.WriteString(FormatValue(row[len(row)-1], "") + "\n")
	}
	return &Plan{Engine: EngineDuckDB, SQL: stmt, Format: PlanText, Raw: b.String()}, nil
}

// ---------- shared scanning ----------

// scanSingleValue reads the one cell of a one-row, one-column answer —
// the shape both JSON EXPLAIN forms come back in.
func scanSingleValue(ctx context.Context, q querier, stmt string) (string, error) {
	rows, err := q.QueryContext(ctx, stmt)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return "", err
		}
		return "", fmt.Errorf("db: %s returned no rows", firstWord(stmt))
	}
	var v any
	if err := rows.Scan(&v); err != nil {
		return "", err
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return FormatValue(normalizeValue(v), ""), nil
}

// scanPlanGrid materializes a rowsScanner into a ResultSet. It is the
// dialect-side twin of scanResultSet, which needs a *sql.Rows the
// dialects never hold.
func scanPlanGrid(rows rowsScanner) (*ResultSet, error) {
	names, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	cols := make([]Column, len(names))
	for i, n := range names {
		cols[i] = Column{Name: n}
	}
	rs := &ResultSet{Columns: cols, Rows: [][]any{}}
	for rows.Next() {
		raw := make([]any, len(names))
		ptrs := make([]any, len(names))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make([]any, len(names))
		for i, v := range raw {
			row[i] = normalizeValue(v)
		}
		rs.Rows = append(rs.Rows, row)
	}
	return rs, rows.Err()
}

// ---------- ordered JSON ----------

// jsonValue is a JSON document with its object keys in document order.
// encoding/json's map[string]any loses that order, and a plan read in
// alphabetical order stops describing the query it came from — the
// access method would sort away from the table it belongs to.
type jsonValue struct {
	scalar *string
	keys   []string
	obj    map[string]*jsonValue
	arr    []*jsonValue
}

func parseOrderedJSON(raw string) (*jsonValue, error) {
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	return parseOrderedValue(dec, tok)
}

func parseOrderedValue(dec *json.Decoder, tok json.Token) (*jsonValue, error) {
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			v := &jsonValue{obj: map[string]*jsonValue{}}
			for {
				keyTok, err := dec.Token()
				if err != nil {
					return nil, err
				}
				if d, ok := keyTok.(json.Delim); ok && d == '}' {
					return v, nil
				}
				key, ok := keyTok.(string)
				if !ok {
					return nil, fmt.Errorf("json: object key is %T", keyTok)
				}
				valTok, err := dec.Token()
				if err != nil {
					return nil, err
				}
				child, err := parseOrderedValue(dec, valTok)
				if err != nil {
					return nil, err
				}
				// A repeated key keeps its first position and takes the
				// last value, which is what encoding/json does too.
				if _, seen := v.obj[key]; !seen {
					v.keys = append(v.keys, key)
				}
				v.obj[key] = child
			}
		case '[':
			v := &jsonValue{arr: []*jsonValue{}}
			for {
				elemTok, err := dec.Token()
				if err != nil {
					return nil, err
				}
				if d, ok := elemTok.(json.Delim); ok && d == ']' {
					return v, nil
				}
				child, err := parseOrderedValue(dec, elemTok)
				if err != nil {
					return nil, err
				}
				v.arr = append(v.arr, child)
			}
		}
		return nil, fmt.Errorf("json: unexpected delimiter %v", t)
	default:
		s := scalarText(tok)
		return &jsonValue{scalar: &s}, nil
	}
}

func scalarText(tok json.Token) string {
	switch t := tok.(type) {
	case nil:
		return "null"
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case json.Number:
		return t.String()
	default:
		return fmt.Sprintf("%v", t)
	}
}
