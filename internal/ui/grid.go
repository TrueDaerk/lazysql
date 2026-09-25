package ui

import (
	"lazysql/internal/db"
	"lazysql/internal/history"
)

// gridModel is the main view's Data tab as a sub-model: the page on
// screen, the queries fetching it, the inline filter line and its
// history, the staged changeset, and the foreign-key navigation state
// — caches, in-flight markers, the jump history and the action waiting
// on a fetch. The root Model owns one and routes to it; it does not
// implement tea.Model, because the keys that drive it are reduced by the
// root the same way the side panels' are. See
// wiki/design/tui-shell-architecture.md.
type gridModel struct {
	// data is the main view's Data tab: one page of the open relation.
	data dataView

	// inflight is the cancel handle of the page and count queries the
	// last reload put in flight, so a newer request — a second `s` on a
	// slow table — stops the server working on the one it supersedes
	// instead of only dropping its reply. It is a pointer so every copy
	// of the Model shares one handle, the way changes shares one
	// changeset. See wiki/design/page-query-cancellation.md.
	inflight *pageQueries

	// pageSize is the configured row limit for browsing a table page and
	// pagination — config.PageSize resolved to its default at New(). Every
	// dataView is constructed with this as its own pageSize so limit()
	// never has to reach back through the Model.
	pageSize int

	// filterInput is the grid's inline `/` line — the WHERE clause being
	// typed — nil when none is open. filters is the per-relation filter
	// history behind its recall keys, newest first, across every scope.
	filterInput *filterInput
	filters     []history.Entry

	// changes is the staged changeset: edits accumulate here and only
	// execute on explicit commit. It is a pointer so every copied Model
	// shares one changeset.
	changes *db.Changeset

	// Foreign-key navigation. fkCache holds one relation's constraints,
	// refsCache the whole namespace's (keyed by an fkKey with an empty
	// table) for the reverse direction, and fkLoading marks the fetches
	// in flight so a repeated key press does not stack round trips.
	// browseStack is the jump history `ctrl+o`/`esc` walk back, and
	// fkAfter is the action waiting for a fetch that has not landed yet.
	fkCache     map[fkKey][]db.ForeignKey
	refsCache   map[fkKey][]namespaceFK
	fkLoading   map[fkKey]bool
	browseStack []browseState
	fkAfter     actionID
}

// newGridModel builds an empty grid with fresh caches and a fresh
// changeset. pageSize is the configured row limit every page it opens
// is constructed with.
func newGridModel(pageSize int) gridModel {
	return gridModel{
		pageSize:  pageSize,
		changes:   db.NewChangeset(),
		fkCache:   map[fkKey][]db.ForeignKey{},
		refsCache: map[fkKey][]namespaceFK{},
		fkLoading: map[fkKey]bool{},
	}
}

// ---------- page queries ----------

// ensureInflight returns the grid's page-query handle, creating it on
// first use so a Model built by hand (a test fixture) behaves like one
// that came through New.
func (g *gridModel) ensureInflight() *pageQueries {
	if g.inflight == nil {
		g.inflight = &pageQueries{}
	}
	return g.inflight
}

// stopPageQueries cancels the page and count queries in flight, if any.
func (g *gridModel) stopPageQueries() { g.inflight.stop() }

// pageQueryDone reports one of the two replies of the current request to
// the cancel handle.
func (g *gridModel) pageQueryDone(req int, page bool) { g.inflight.done(req, page) }

// ---------- foreign-key caches ----------

// cacheFKs records one relation's foreign keys. The pointer receiver is
// what lets the lazily created map survive the Model copy Update works
// on.
func (g *gridModel) cacheFKs(k fkKey, fks []db.ForeignKey) {
	if k.table == "" {
		return
	}
	if g.fkCache == nil {
		g.fkCache = map[fkKey][]db.ForeignKey{}
	}
	// A relation without foreign keys caches as an empty non-nil slice,
	// so "known to have none" is distinguishable from "never fetched".
	if fks == nil {
		fks = []db.ForeignKey{}
	}
	g.fkCache[k] = fks
}

// ---------- jump history ----------

// pushBrowse records the open page, with tab as the main-view tab it was
// left on, so a jump can be undone. A page that is not browsing a
// relation is nothing to come back to and is not recorded.
func (g *gridModel) pushBrowse(tab mainTab) {
	if !g.data.browsing() {
		return
	}
	g.browseStack = append(g.browseStack, browseState{data: g.data, tab: tab})
	if n := len(g.browseStack); n > browseStackMax {
		g.browseStack = g.browseStack[n-browseStackMax:]
	}
}

// popBrowse takes the newest entry off the jump history. ok is false
// when there is none.
func (g *gridModel) popBrowse() (st browseState, ok bool) {
	n := len(g.browseStack)
	if n == 0 {
		return browseState{}, false
	}
	st = g.browseStack[n-1]
	g.browseStack = g.browseStack[:n-1]
	return st, true
}

// clearBrowse forgets the jump history and any action waiting on a
// foreign-key fetch: both describe a relation or namespace that is
// being left.
func (g *gridModel) clearBrowse() {
	g.browseStack = nil
	g.fkAfter = actNone
}
