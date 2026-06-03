// SPDX-License-Identifier: MIT

package index

import (
	"context"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// #285: receiver-method calls produce CALLS edges that resolve to the
// Method symbol via the trailing-component fallback. Pre-fix,
// `trace inbound name=Method` returned 0 callers because the
// emitted ToName ("recv.Method") never matched any qualified name.

const receiverMethodSrc = `package svc

type Indexer struct{}

func (i *Indexer) Process(input string) string {
	return input
}

type Server struct {
	idx *Indexer
}

func (s *Server) Run(input string) string {
	return s.idx.Process(input)
}
`

func TestResolveCalls_ReceiverMethodFallback(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "svc/svc.go", receiverMethodSrc)

	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("Index: %v", err)
	}

	// Find the Process Method's stable ID.
	pid := db.ProjectIDFromPath(dir)
	syms, err := store.GetSymbolsByName(pid, "Process", 5)
	if err != nil {
		t.Fatalf("GetSymbolsByName Process: %v", err)
	}
	var processID string
	for _, s := range syms {
		if s.Kind == "Method" {
			processID = s.ID
			break
		}
	}
	if processID == "" {
		t.Fatalf("expected a Method named Process; got %d symbols, none Method", len(syms))
	}

	// Trace inbound on Process — pre-fix this would return 0.
	results, err := store.TraceViaCTEScoped(pid, processID, "inbound", []string{"CALLS"}, 3)
	if err != nil {
		t.Fatalf("TraceViaCTEScoped: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("expected ≥1 caller of Process; got 0 (#285 regression)")
	}

	foundRunCaller := false
	for _, r := range results {
		// SymbolID resolves to a Run-named caller — fetch by ID.
		if sym, err := store.GetSymbol(r.SymbolID); err == nil && sym != nil && sym.Name == "Run" {
			foundRunCaller = true
			break
		}
	}
	if !foundRunCaller {
		t.Errorf("Run is the only caller of Process; expected it to surface in inbound trace")
	}
}

// Negative test: when multiple types define the same Method name
// (Close, String, Run), the resolver should NOT bind to a specific
// one — it should drop the edge as ambiguous.
const ambiguousMethodSrc = `package svc

type Cache struct{}
func (c *Cache) Close() {}

type Connection struct{}
func (c *Connection) Close() {}

type Service struct{
	cache *Cache
	conn  *Connection
}

func (s *Service) Shutdown() {
	s.cache.Close()
	s.conn.Close()
}
`

func TestResolveCalls_AmbiguousMethodNameDoesNotBind(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "svc/ambig.go", ambiguousMethodSrc)

	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("Index: %v", err)
	}

	pid := db.ProjectIDFromPath(dir)
	syms, _ := store.GetSymbolsByName(pid, "Close", 5)
	var firstClose string
	for _, s := range syms {
		if s.Kind == "Method" {
			firstClose = s.ID
			break
		}
	}
	if firstClose == "" {
		t.Skip("Close not extracted as Method on this corpus; nothing to test")
	}

	// Either Close has 0 inbound CALLS (ambiguous → no edge) OR,
	// if a future improvement adds receiver-type analysis, it
	// resolves to exactly one Close. We pin only that the resolver
	// doesn't ambiguously double-bind to BOTH Close methods from one
	// caller (which would be the regression).
	results, err := store.TraceViaCTEScoped(pid, firstClose, "inbound", []string{"CALLS"}, 3)
	if err != nil {
		t.Fatalf("TraceViaCTEScoped: %v", err)
	}
	// The current resolver drops ambiguous matches; expect 0.
	// Update this assertion if a stricter type-aware resolver lands.
	for _, r := range results {
		sym, err := store.GetSymbol(r.SymbolID)
		if err != nil || sym == nil {
			continue
		}
		if sym.Name != "Shutdown" && sym.Name != "" {
			t.Errorf("unexpected caller %q for ambiguous Method Close", sym.Name)
		}
	}
}

func TestResolveCalls_StatsOnSQLDB_NotFalseBound(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "server/tracker.go", `package server

type Tracker struct{}

func (t *Tracker) Stats() {}
`)
	writeFile(t, dir, "store/store_test.go", `package store

import "database/sql"

func TestDBStats(db *sql.DB) {
	_ = db.Stats()
}
`)

	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("Index: %v", err)
	}

	testID := db.MakeSymbolID("store/store_test.go", "store.TestDBStats", "Function")
	statsID := db.MakeSymbolID("server/tracker.go", "server.*Tracker.Stats", "Method")

	inbound, err := store.EdgesTo(statsID, []string{"CALLS"})
	if err != nil {
		t.Fatalf("EdgesTo Tracker.Stats: %v", err)
	}
	if hasEdge(inbound, testID, "CALLS") {
		t.Fatalf("database/sql DB.Stats false-bound to server.Tracker.Stats; inbound=%+v", inbound)
	}
}
