package db

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Server activity is the one read that is about the server rather than
// about its data: who is connected, what each session is running, how
// long it has been at it, and who is waiting on whom. The engines
// disagree about every part of that — the catalog (`SHOW PROCESSLIST`'s
// table form vs. `pg_stat_activity`), the column names, the unit of the
// duration, and whether lock waits are reported at all — so the
// differences are settled here and the UI only ever sees a []Process.
//
// Three rules hold for every dialect:
//
//   - The listing is a plain read. It never changes anything, so it is
//     safe on a read-only session and needs no confirmation.
//   - Killing is not. It goes through KillProcess, which a read-only
//     session refuses like any other write, and the UI confirms before
//     calling it.
//   - Both run through the same querier/Logger as every other statement,
//     so the process list and the KILL land in the command log without
//     anything re-formatting them by hand.

// Process is one session (MySQL/MariaDB) or backend (PostgreSQL) the
// server currently has. Every string field is empty rather than a
// placeholder when the engine reports nothing for it, so the UI decides
// how "unknown" reads.
//
// Duration is how long the session has been doing what State says: the
// running statement's age for a working session, and nothing at all
// (HasDuration false) for an idle one — an idle connection's age says
// nothing about server load, and sorting by it would bury the long
// query the view exists to surface.
type Process struct {
	// ID is the engine's own session identifier — MySQL's connection id,
	// PostgreSQL's backend pid — as decimal text. It is what
	// KillProcessSQL builds its statement from.
	ID       string
	User     string
	Database string
	// Client is the address the session connected from, empty for a
	// local/socket connection the engine reports no address for.
	Client string
	// State is the engine's own wording: "Query: Sending data",
	// "active", "idle in transaction", …
	State       string
	Duration    time.Duration
	HasDuration bool
	// Query is the statement the session is running (or last ran), as
	// the engine reports it — possibly truncated by the server itself.
	Query string
	// BlockedBy holds the IDs of the sessions this one is waiting for,
	// empty when it is not blocked or when the engine cannot say.
	BlockedBy []string
	// Self marks lazysql's own session. Killing it would drop the
	// connection the view is being read through, so the UI refuses.
	Self bool
}

// Blocked reports whether the session is waiting on another one.
func (p Process) Blocked() bool { return len(p.BlockedBy) > 0 }

// BlockedByText renders the blocker IDs for display, empty when the
// session is not blocked.
func (p Process) BlockedByText() string { return strings.Join(p.BlockedBy, ",") }

// KillStatement is what ends one session on a given engine.
//
// The session ID is validated as a decimal integer and rendered into the
// statement rather than bound as a parameter: MySQL's KILL takes no
// placeholder at all outside a stored program, and a half-parameterized
// pair of dialects would be harder to audit than one rule that holds for
// both. Nothing else in the statement comes from outside this package.
type KillStatement struct {
	SQL string
	// ReturnsRow marks a statement that answers with a row rather than
	// only a status: PostgreSQL's pg_terminate_backend reports whether
	// the signal was delivered, and a false there means the backend was
	// already gone (or the session may not signal it).
	ReturnsRow bool
}

// ProcessListSQL returns the statement ListProcesses runs on an engine,
// or ErrUnsupported for an engine that has no server to ask.
func ProcessListSQL(d Dialect) (string, error) { return d.processListSQL() }

// KillProcessSQL returns the statement that ends session id, or
// ErrUnsupported for an engine with no sessions. An id that is not a
// decimal integer is an error, not an escaped literal.
func KillProcessSQL(d Dialect, id string) (KillStatement, error) { return d.killProcessSQL(id) }

// processID validates a session identifier for use in a KILL statement:
// a plain decimal integer, nothing else. Everything lazysql passes here
// came out of the engine's own process list, so a rejection means the
// list was not what it claimed to be — which is exactly when a literal
// must not be built.
func processID(id string) (string, error) {
	s := strings.TrimSpace(id)
	if s == "" {
		return "", fmt.Errorf("db: empty process id")
	}
	if _, err := strconv.ParseUint(s, 10, 64); err != nil {
		return "", fmt.Errorf("db: %q is not a process id", id)
	}
	return s, nil
}

// SortProcesses orders a process list the way the activity view shows
// it: the longest-running session first, sessions with no duration
// (idle ones) last, and equal durations by ascending ID so a repeated
// refresh does not shuffle rows under the cursor.
func SortProcesses(ps []Process) {
	sort.SliceStable(ps, func(i, j int) bool {
		a, b := ps[i], ps[j]
		if a.HasDuration != b.HasDuration {
			return a.HasDuration
		}
		if a.HasDuration && a.Duration != b.Duration {
			return a.Duration > b.Duration
		}
		return processIDLess(a.ID, b.ID)
	})
}

// processIDLess orders two session IDs numerically where both are
// numbers, and lexically otherwise.
func processIDLess(a, b string) bool {
	na, erra := strconv.ParseUint(strings.TrimSpace(a), 10, 64)
	nb, errb := strconv.ParseUint(strings.TrimSpace(b), 10, 64)
	if erra == nil && errb == nil {
		return na < nb
	}
	return a < b
}

// ProcessIDLess is processIDLess, exported for the activity view's own
// column sort: its PID column and its tie-break for every other column
// order sessions the same way SortProcesses does.
func ProcessIDLess(a, b string) bool { return processIDLess(a, b) }

// scanProcesses runs a process-list query and reads its fixed column
// contract, which every dialect's processListSQL produces in this order:
//
//	id, user, database, client, state, duration_seconds, query,
//	blocked_by, is_self
//
// duration_seconds and blocked_by may be NULL — "not running anything"
// and "not blocked (or the engine cannot say)" — and blocked_by is a
// comma-separated list rather than an array, so no dialect has to agree
// with another about array decoding.
func scanProcesses(ctx context.Context, q querier, query string, args ...any) ([]Process, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Process
	for rows.Next() {
		var id, user, database, client, state, secs, stmt, blockers, self any
		if err := rows.Scan(
			&id, &user, &database, &client, &state, &secs, &stmt, &blockers, &self); err != nil {
			return nil, err
		}
		p := Process{
			ID:        processText(id),
			User:      processText(user),
			Database:  processText(database),
			Client:    processText(client),
			State:     processText(state),
			Query:     processText(stmt),
			BlockedBy: splitProcessIDs(processText(blockers)),
			Self:      processBool(self),
		}
		if s, ok := processSeconds(secs); ok && s >= 0 {
			p.Duration = time.Duration(s * float64(time.Second))
			p.HasDuration = true
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// processText renders one scanned column as display text. Every driver
// spells these columns differently ([]byte on MySQL, string on pgx), so
// they go through the same normalization as a browsed cell.
func processText(v any) string {
	switch x := normalizeValue(v).(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(x)
	default:
		return FormatValue(x, "")
	}
}

// processSeconds reads a duration column, which arrives as an integer
// (MySQL's TIME), a float (PostgreSQL's EXTRACT) or decimal text
// depending on the driver.
func processSeconds(v any) (float64, bool) {
	switch x := normalizeValue(v).(type) {
	case int64:
		return float64(x), true
	case float64:
		return x, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		if err != nil {
			return 0, false
		}
		return f, true
	}
	return 0, false
}

// processBool reads a boolean column, which MySQL answers with 0/1 and
// PostgreSQL with a real boolean.
func processBool(v any) bool {
	switch x := normalizeValue(v).(type) {
	case bool:
		return x
	case int64:
		return x != 0
	case float64:
		return x != 0
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "t", "true", "1", "yes", "on":
			return true
		}
	}
	return false
}

// splitProcessIDs turns the comma-separated blocker column into IDs,
// dropping the empty strings an engine's own join may leave behind.
func splitProcessIDs(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// applyBlockers merges a waiter → blockers map into a process list. It
// is what the engines that need a second query for lock waits use;
// dialects that report blockers in the listing itself never call it.
func applyBlockers(ps []Process, blockers map[string][]string) {
	if len(blockers) == 0 {
		return
	}
	for i := range ps {
		if b := blockers[ps[i].ID]; len(b) > 0 {
			ps[i].BlockedBy = b
		}
	}
}
