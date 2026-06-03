// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #879: max_rows / limit out of range used to silently clamp inside the
// cypher executor (max_rows > 10000 → 10000) or the dead_code handler
// (limit > 500 → 500) with no signal — same silent-confidently-wrong
// shape as the depth / min_confidence clamps. Now both surface a
// `_meta.warnings` entry.

func TestHandleQuery_MaxRowsOver10000_ClampsAndWarns(t *testing.T) {
	t.Parallel()
	srv, _ := setupSeededProject(t)

	result, err := srv.handleQuery(context.Background(), makeReq(map[string]any{
		"pinchql":  `MATCH (n:Function) RETURN n.name`,
		"max_rows": float64(99999),
	}))
	if err != nil {
		t.Fatalf("handleQuery: %v", err)
	}
	body := decode(t, result)
	ws := warningsFromMeta(body)
	if !warningContains(ws, "max_rows=99999 clamped to 10000") {
		t.Errorf("expected max_rows clamp warning; got warnings=%v", ws)
	}
}

func TestHandleQuery_MaxRowsZero_ClampsAndWarns(t *testing.T) {
	t.Parallel()
	srv, _ := setupSeededProject(t)

	result, err := srv.handleQuery(context.Background(), makeReq(map[string]any{
		"pinchql":  `MATCH (n:Function) RETURN n.name`,
		"max_rows": float64(0),
	}))
	if err != nil {
		t.Fatalf("handleQuery: %v", err)
	}
	body := decode(t, result)
	ws := warningsFromMeta(body)
	if !warningContains(ws, "max_rows=0 clamped to 1") {
		t.Errorf("expected max_rows zero-clamp warning; got warnings=%v", ws)
	}
}

func TestHandleDeadCode_LimitOver500_ClampsAndWarns(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "dc-limit-high"
	store.UpsertProject(db.Project{ID: pid, Path: "/tmp/" + pid, Name: pid, IndexedAt: time.Now()})
	srv.sessionID = pid
	mustUpsertSymbols(t, store, []db.Symbol{
		{ID: pid + "::pkg.x#Function", ProjectID: pid, FilePath: "f.go",
			Name: "x", QualifiedName: "pkg.x", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1.0},
	})

	result, err := srv.handleDeadCode(context.Background(), makeReq(map[string]any{
		"limit": float64(9999),
	}))
	if err != nil {
		t.Fatalf("handleDeadCode: %v", err)
	}
	body := decode(t, result)
	ws := warningsFromMeta(body)
	if !warningContains(ws, "limit=9999 clamped to 500") {
		t.Errorf("expected limit clamp warning; got warnings=%v", ws)
	}
}

func TestHandleDeadCode_LimitNegative_ClampsAndWarns(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "dc-limit-neg"
	store.UpsertProject(db.Project{ID: pid, Path: "/tmp/" + pid, Name: pid, IndexedAt: time.Now()})
	srv.sessionID = pid
	mustUpsertSymbols(t, store, []db.Symbol{
		{ID: pid + "::pkg.x#Function", ProjectID: pid, FilePath: "f.go",
			Name: "x", QualifiedName: "pkg.x", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1.0},
	})

	result, err := srv.handleDeadCode(context.Background(), makeReq(map[string]any{
		"limit": float64(-3),
	}))
	if err != nil {
		t.Fatalf("handleDeadCode: %v", err)
	}
	body := decode(t, result)
	ws := warningsFromMeta(body)
	if !warningContains(ws, "limit=-3 clamped to 1") {
		t.Errorf("expected limit negative-clamp warning; got warnings=%v", ws)
	}
}

// Control: in-range values don't warn.
func TestHandlers_InRangeLimitAndMaxRows_NoClamp(t *testing.T) {
	t.Parallel()
	srv, _ := setupSeededProject(t)

	result, err := srv.handleQuery(context.Background(), makeReq(map[string]any{
		"pinchql":  `MATCH (n:Function) RETURN n.name`,
		"max_rows": float64(50),
	}))
	if err != nil {
		t.Fatalf("handleQuery: %v", err)
	}
	body := decode(t, result)
	for _, w := range warningsFromMeta(body) {
		if s, ok := w.(string); ok && (containsAny(s, "max_rows", "limit")) && containsAny(s, "clamped") {
			t.Errorf("in-range max_rows=50 must not warn; got %q", s)
		}
	}
}

func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if len(n) > 0 && len(s) >= len(n) {
			for i := 0; i+len(n) <= len(s); i++ {
				if s[i:i+len(n)] == n {
					return true
				}
			}
		}
	}
	return false
}
