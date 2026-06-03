// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #1040: handleArchitecture's "truly empty" diagnosis used to default
// to "likely config/docs-only (no Functions)" whenever hotspots and
// entry-points were empty AND symCount > 0. For ghost-extraction
// projects (symbols extracted, resolver phase produced no edges) this
// directly contradicted the langs histogram in the same response —
// a project with 327 Functions across Go/TypeScript was confidently
// reported as having "no Functions." Now: a code-corpus language
// (Go/Python/JS/etc.) with edgeCount==0 routes to a ghost-extraction
// diagnosis pointing at re-index + doctor.

func TestHandleArchitecture_GhostExtraction_DiagnosisNamesIt(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "p-ghost"
	store.UpsertProject(db.Project{
		ID: pid, Path: t.TempDir(), Name: pid, IndexedAt: time.Now(),
		FileCount: 50, SymCount: 300, EdgeCount: 0,
	})
	srv.sessionID = pid

	// Seed callable Go symbols so the langs histogram includes "Go" but
	// the graph has no CALLS edges (mimicking a half-extracted project).
	syms := []db.Symbol{}
	for i := 0; i < 5; i++ {
		syms = append(syms, db.Symbol{
			ID:                   pid + "::pkg.Func" + string(rune('A'+i)) + "#Function",
			ProjectID:            pid,
			FilePath:             "a.go",
			Name:                 "Func" + string(rune('A'+i)),
			QualifiedName:        "pkg.Func" + string(rune('A'+i)),
			Kind:                 "Function",
			Language:             "Go",
			ExtractionConfidence: 1.0,
		})
	}
	mustUpsertSymbols(t, store, syms)

	result, err := srv.handleArchitecture(context.Background(), makeReq(map[string]any{}))
	if err != nil {
		t.Fatalf("handleArchitecture: %v", err)
	}
	body := decode(t, result)
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil {
		t.Fatal("_meta missing")
	}
	diagnosis, _ := meta["diagnosis"].(string)
	if !strings.Contains(diagnosis, "ghost-extraction") {
		t.Errorf("expected ghost-extraction diagnosis; got %q", diagnosis)
	}
	// Pre-fix said "no Functions" — must not regress.
	if strings.Contains(diagnosis, "no Functions") {
		t.Errorf("diagnosis must not claim no Functions for a project with code-corpus symbols; got %q", diagnosis)
	}
}

// Control: a project that genuinely is config/docs-only (no code-corpus
// language) still hits the original "config/docs-only" diagnosis.
func TestHandleArchitecture_TrulyConfigOnly_KeepsOriginalDiagnosis(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "p-cfg-only"
	store.UpsertProject(db.Project{
		ID: pid, Path: t.TempDir(), Name: pid, IndexedAt: time.Now(),
		FileCount: 5, SymCount: 30, EdgeCount: 0,
	})
	srv.sessionID = pid

	// Only YAML Settings — no code-corpus language present.
	syms := []db.Symbol{}
	for i := 0; i < 5; i++ {
		syms = append(syms, db.Symbol{
			ID:                   pid + "::a.yaml::root.setting" + string(rune('A'+i)) + "#Setting",
			ProjectID:            pid,
			FilePath:             "a.yaml",
			Name:                 "setting" + string(rune('A'+i)),
			QualifiedName:        "root.setting" + string(rune('A'+i)),
			Kind:                 "Setting",
			Language:             "YAML",
			ExtractionConfidence: 1.0,
		})
	}
	mustUpsertSymbols(t, store, syms)

	result, err := srv.handleArchitecture(context.Background(), makeReq(map[string]any{}))
	if err != nil {
		t.Fatalf("handleArchitecture: %v", err)
	}
	body := decode(t, result)
	meta, _ := body["_meta"].(map[string]any)
	diagnosis, _ := meta["diagnosis"].(string)
	if strings.Contains(diagnosis, "ghost-extraction") {
		t.Errorf("must not claim ghost-extraction for a YAML-only project; got %q", diagnosis)
	}
	if !strings.Contains(diagnosis, "config/docs-only") {
		t.Errorf("expected the config/docs-only diagnosis; got %q", diagnosis)
	}
}
