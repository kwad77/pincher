// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #1799: the symbols batch tool resolved IDs unscoped — a symbol ID is
// path+QN+kind, so an indexed mirror of the session repo carries
// identical IDs and an unscoped lookup could return the wrong project's
// row. symbols now session-scopes by default, like symbol / context /
// trace / neighborhood (#1232 / #1408).

func seed1799TwoProjects(t *testing.T, srv *Server, store *db.Store) (sessionID, mirrorID string) {
	t.Helper()
	sessionID, mirrorID = "p-session-1799", "p-mirror-1799"
	for _, pid := range []string{sessionID, mirrorID} {
		store.UpsertProject(db.Project{
			ID: pid, Path: t.TempDir(), Name: pid, IndexedAt: time.Now(),
			FileCount: 1, SymCount: 1, EdgeCount: 1,
		})
	}
	srv.sessionID = sessionID
	// Same ID in both projects, distinguishable by signature.
	collidingID := "pkg/x.go::pkg.Foo#Function"
	mustUpsertSymbols(t, store, []db.Symbol{
		{
			ID: collidingID, ProjectID: sessionID, FilePath: "pkg/x.go",
			Name: "Foo", QualifiedName: "pkg.Foo", Kind: "Function",
			Language: "Go", Signature: "func Foo() // SESSION", ExtractionConfidence: 1.0,
		},
		{
			ID: collidingID, ProjectID: mirrorID, FilePath: "pkg/x.go",
			Name: "Foo", QualifiedName: "pkg.Foo", Kind: "Function",
			Language: "Go", Signature: "func Foo() // MIRROR", ExtractionConfidence: 1.0,
		},
		{
			ID: "pkg/y.go::pkg.MirrorOnly#Function", ProjectID: mirrorID, FilePath: "pkg/y.go",
			Name: "MirrorOnly", QualifiedName: "pkg.MirrorOnly", Kind: "Function",
			Language: "Go", Signature: "func MirrorOnly()", ExtractionConfidence: 1.0,
		},
	})
	return sessionID, mirrorID
}

func TestHandleSymbols_SessionScopedByDefault_1799(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	seed1799TwoProjects(t, srv, store)

	// No `project` arg — must resolve the colliding ID from the SESSION
	// project, not the mirror.
	res, err := srv.handleSymbols(context.Background(), makeReq(map[string]any{
		"ids":    []any{"pkg/x.go::pkg.Foo#Function"},
		"fields": "id,signature",
	}))
	if err != nil {
		t.Fatalf("handleSymbols: %v", err)
	}
	syms, _ := decode(t, res)["symbols"].([]any)
	if len(syms) != 1 {
		t.Fatalf("expected 1 symbol, got %d", len(syms))
	}
	got, _ := syms[0].(map[string]any)["signature"].(string)
	if got != "func Foo() // SESSION" {
		t.Errorf("#1799: batch resolved the colliding ID from the wrong project — signature = %q, want the SESSION row", got)
	}
}

func TestHandleSymbols_MirrorOnlyID_NotFoundWithoutCrossProject_1799(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	seed1799TwoProjects(t, srv, store)

	// An ID present only in the mirror must surface as not_found —
	// not silently served from the mirror.
	res, err := srv.handleSymbols(context.Background(), makeReq(map[string]any{
		"ids": []any{"pkg/y.go::pkg.MirrorOnly#Function"},
	}))
	if err != nil {
		t.Fatalf("handleSymbols: %v", err)
	}
	body := decode(t, res)
	nf, _ := body["not_found_ids"].([]any)
	if len(nf) != 1 {
		t.Errorf("#1799: a mirror-only ID must be not_found under default session scope; not_found_ids=%v", body["not_found_ids"])
	}
}

func TestHandleSymbols_CrossProjectOptIn_ResolvesMirrorID_1799(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	seed1799TwoProjects(t, srv, store)

	// cross_project=true falls back to the unscoped lookup for the
	// session-missed ID.
	res, err := srv.handleSymbols(context.Background(), makeReq(map[string]any{
		"ids":           []any{"pkg/y.go::pkg.MirrorOnly#Function"},
		"cross_project": true,
		"fields":        "id,signature",
	}))
	if err != nil {
		t.Fatalf("handleSymbols: %v", err)
	}
	syms, _ := decode(t, res)["symbols"].([]any)
	if len(syms) != 1 {
		t.Fatalf("expected 1 symbol, got %d", len(syms))
	}
	if errStr, _ := syms[0].(map[string]any)["error"].(string); errStr != "" {
		t.Errorf("cross_project=true must resolve the mirror-only ID; got error %q", errStr)
	}
}

func TestHandleSymbols_CrossProjectOptIn_ReadsSourceFromOwningProject(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	sessionRoot := t.TempDir()
	mirrorRoot := t.TempDir()
	sessionID, mirrorID := "p-session-source", "p-mirror-source"
	mustUpsertProject(t, store, sessionID, sessionRoot, sessionID)
	mustUpsertProject(t, store, mirrorID, mirrorRoot, mirrorID)
	srv.sessionID = sessionID
	srv.sessionRoot = sessionRoot

	rel := filepath.Join("pkg", "x.go")
	sessionSource := "package pkg\nfunc Marker() string { return \"SESSION\" }\n"
	mirrorSource := "package pkg\nfunc Marker() string { return \"MIRROR!\" }\n"
	for root, body := range map[string]string{
		sessionRoot: sessionSource,
		mirrorRoot:  mirrorSource,
	} {
		if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
			t.Fatalf("mkdir fixture: %v", err)
		}
		if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
	}

	start := strings.Index(mirrorSource, "func Marker")
	if start < 0 {
		t.Fatal("test fixture missing function")
	}
	id := "pkg/x.go::pkg.Marker#Function"
	mustUpsertSymbols(t, store, []db.Symbol{{
		ID: id, ProjectID: mirrorID, FilePath: filepath.ToSlash(rel),
		Name: "Marker", QualifiedName: "pkg.Marker", Kind: "Function",
		Language: "Go", StartByte: start, EndByte: len(mirrorSource),
		Signature: "func Marker() string", ExtractionConfidence: 1.0,
	}})

	res, err := srv.handleSymbols(context.Background(), makeReq(map[string]any{
		"ids":           []any{id},
		"cross_project": true,
		"fields":        "id,source",
	}))
	if err != nil {
		t.Fatalf("handleSymbols: %v", err)
	}
	syms, _ := decode(t, res)["symbols"].([]any)
	if len(syms) != 1 {
		t.Fatalf("expected 1 symbol, got %d", len(syms))
	}
	source, _ := syms[0].(map[string]any)["source"].(string)
	if !strings.Contains(source, "MIRROR!") || strings.Contains(source, "SESSION") {
		t.Fatalf("cross_project source must come from owning project root; got %q", source)
	}
}
