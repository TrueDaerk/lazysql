package ui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"lazysql/internal/db"
)

// blockingDriver is what a slow table looks like from the UI's side: its
// page and count queries never return on their own, only when their
// context is cancelled. Every other Driver method is left to the
// embedded nil interface — reaching one is a bug in the test, and a
// panic says so louder than a stub would.
type blockingDriver struct {
	db.Driver
	// pages and counts hand each query's context back to the test, kept
	// apart so a cancelled page query's trailing count cannot be
	// mistaken for the next request's page. Both are buffered so a query
	// never blocks on a test that is not reading.
	pages, counts chan context.Context
	log           *db.Logger
}

func (d *blockingDriver) QueryPage(ctx context.Context, _, _ string, _ *db.Filter, _ *db.Sort, _, _ int) (*db.ResultSet, error) {
	d.pages <- ctx
	<-ctx.Done()
	return nil, ctx.Err()
}

func (d *blockingDriver) CountRows(ctx context.Context, _, _ string, _ *db.Filter) (int64, error) {
	d.counts <- ctx
	<-ctx.Done()
	return 0, ctx.Err()
}

// The renderers and the command log reach for these on every frame.
func (d *blockingDriver) ReadOnly() bool     { return false }
func (d *blockingDriver) Logger() *db.Logger { return d.log }

// blocked returns a browsing model whose driver never answers, so a
// reload can be inspected while it is still in flight.
func blocked(t *testing.T) (Model, *blockingDriver) {
	t.Helper()
	m := dataBrowsing(t)
	drv := &blockingDriver{
		pages:  make(chan context.Context, 8),
		counts: make(chan context.Context, 8),
		log:    db.NewLogger(),
	}
	m.driver = drv
	return m, drv
}

// A newer request cancels the context of the page and count queries it
// supersedes, rather than leaving them to run to completion.
func TestSupersededPageQueryIsCancelled(t *testing.T) {
	m, drv := blocked(t)

	first := m.reloadPage()
	firstReq := m.data.req
	replies := make(chan []tea.Msg, 1)
	go func() { replies <- drain(first) }()
	pageCtx := <-drv.pages

	// A second `s` while the first page query is still running.
	second := m.reloadPage()
	if m.data.req == firstReq {
		t.Fatalf("req = %d, want a newer one than %d", m.data.req, firstReq)
	}
	if err := pageCtx.Err(); !errors.Is(err, context.Canceled) {
		t.Fatalf("superseded page query context: %v, want %v", err, context.Canceled)
	}

	// Both replies of the superseded request come back cancelled, and
	// both carry the old req.
	for _, msg := range <-replies {
		switch msg := msg.(type) {
		case pageLoadedMsg:
			if msg.req != firstReq || !errors.Is(msg.err, context.Canceled) {
				t.Fatalf("page reply = req %d, err %v; want req %d cancelled", msg.req, msg.err, firstReq)
			}
		case rowCountMsg:
			if msg.req != firstReq || !errors.Is(msg.err, context.Canceled) {
				t.Fatalf("count reply = req %d, err %v; want req %d cancelled", msg.req, msg.err, firstReq)
			}
		default:
			t.Fatalf("unexpected reply %T", msg)
		}
	}

	// The request that replaced it is the one still running, and it is
	// running for real: exactly one page query is in flight.
	go drain(second)
	if err := (<-drv.pages).Err(); err != nil {
		t.Fatalf("new page query starts cancelled: %v", err)
	}
	m.stopPageQueries()
}

// Three presses of `s` in a row leave one page query in flight, not
// three: every press cancels the pair before it.
func TestRepeatedSortsCoalesceToOneInFlightPair(t *testing.T) {
	m, drv := blocked(t)

	var ctxs []context.Context
	for i := 0; i < 3; i++ {
		cmd := m.toggleSort()
		if !m.data.loading {
			t.Fatalf("press %d: loading = false, want a load in flight", i+1)
		}
		go drain(cmd)
		// Only the page query gets that far: the count is issued after
		// it in the same batch, and drain runs a batch in order.
		ctxs = append(ctxs, <-drv.pages)
	}
	live := 0
	for i, ctx := range ctxs {
		if ctx.Err() == nil {
			live++
			continue
		}
		if i == len(ctxs)-1 {
			t.Fatalf("the newest page query was cancelled: %v", ctx.Err())
		}
	}
	if live != 1 {
		t.Fatalf("page queries still running = %d, want 1", live)
	}
	m.stopPageQueries()
}

// The sort key reacts before its query returns: the requested direction
// is on the column header and the grid says it is loading.
func TestSortShowsPendingOrderAndLoadingBeforeTheReplyLands(t *testing.T) {
	m, drv := blocked(t)
	col := m.data.cols[m.data.col].Name

	cmd := m.toggleSort()
	go drain(cmd)
	<-drv.pages

	if !m.data.loading {
		t.Fatal("loading = false right after the sort key")
	}
	desc, ok := m.data.sortOn(col)
	if !ok || desc {
		t.Fatalf("sortOn(%q) = (%v, %v), want ascending", col, desc, ok)
	}
	cols, _ := m.buildGrid()
	if !strings.Contains(cols[m.data.col].header, "▲") {
		t.Fatalf("column header = %q, want the ascending marker", cols[m.data.col].header)
	}
	if got := m.dataStatus(); !strings.Contains(got, "loading…") {
		t.Fatalf("status line = %q, want a loading marker", got)
	}
	if got := m.mainTabBar(120); !strings.Contains(got, "loading…") {
		t.Fatalf("tab bar = %q, want a loading marker", got)
	}
	m.stopPageQueries()
}

// A reply that belongs to a request the user has already moved past is
// dropped: it may not put its rows, its error or its total on screen,
// and it may not clear the marker of the request that replaced it.
func TestLateReplyForOldRequestIsDropped(t *testing.T) {
	m, drv := blocked(t)
	rows := len(m.data.rows)

	go drain(m.reloadPage())
	<-drv.pages
	stale := m.data.req
	go drain(m.reloadPage())
	<-drv.pages

	m = send(t, m,
		pageLoadedMsg{req: stale, conn: m.active, table: m.data.table,
			result: &db.ResultSet{Columns: []db.Column{{Name: "ghost"}}, Rows: [][]any{{1}}}},
		rowCountMsg{req: stale, conn: m.active, table: m.data.table, total: 99999},
	)

	if len(m.data.rows) != rows {
		t.Fatalf("rows = %d, want the %d the fresh page left", len(m.data.rows), rows)
	}
	if m.data.total == 99999 {
		t.Fatal("a stale count overwrote the total")
	}
	if !m.data.loading {
		t.Fatal("a stale reply cleared the loading marker of the request that replaced it")
	}
	m.stopPageQueries()
}

// A cancelled reply for the request on screen — the view closed under
// it — clears the marker instead of leaving the grid loading forever,
// and is not reported as a failure.
func TestCancelledReplyClearsLoadingWithoutAnError(t *testing.T) {
	m, drv := blocked(t)

	go drain(m.reloadPage())
	<-drv.pages
	req := m.data.req

	m = send(t, m,
		pageLoadedMsg{req: req, conn: m.active, table: m.data.table, err: context.Canceled},
		rowCountMsg{req: req, conn: m.active, table: m.data.table, err: context.Canceled},
	)

	if m.data.loading {
		t.Fatal("loading is still set after the last query was cancelled")
	}
	if m.data.err != "" {
		t.Fatalf("data.err = %q, want a cancellation to read as no failure", m.data.err)
	}
	if logContains(m, "FAILED") {
		t.Fatalf("command log reports a cancellation as a failure: %v", m.commandLog)
	}
	m.stopPageQueries()
}

// The loading marker is set the moment the key is pressed and cleared by
// the reply that finally lands.
func TestLoadingIsSetOnPressAndClearedByTheFinalReply(t *testing.T) {
	m := dataBrowsing(t)
	if m.data.loading {
		t.Fatal("loading is set with nothing in flight")
	}
	pressed := m
	if cmd := pressed.toggleSort(); cmd == nil {
		t.Fatal("the sort key issued no query")
	}
	if !pressed.data.loading {
		t.Fatal("loading = false right after the sort key")
	}
	pressed.stopPageQueries()

	m = send(t, m, press('s'))
	if m.data.loading {
		t.Fatal("loading is still set after the page landed")
	}
	if m.data.sort == nil || m.data.sort.Desc {
		t.Fatalf("sort = %+v, want ascending", m.data.sort)
	}
	if got := m.dataStatus(); strings.Contains(got, "loading…") {
		t.Fatalf("status line = %q, want no loading marker once the page landed", got)
	}
}

// A cancelled statement is logged as superseded rather than as a failed
// one, so the command log never reads as if the sort key broke.
func TestCancelledStatementIsNotLoggedAsAFailure(t *testing.T) {
	entry := db.LogEntry{SQL: "SELECT * FROM grid ORDER BY name ASC", Err: context.Canceled}
	got := sqlEntryText(entry)
	if strings.Contains(got, "FAILED") {
		t.Fatalf("log text = %q, want no failure", got)
	}
	if !strings.Contains(got, "cancelled") {
		t.Fatalf("log text = %q, want it to say the statement was cancelled", got)
	}
}
