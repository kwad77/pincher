// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #1780: Class/Type/Interface/Enum kinds have no inbound type-reference
// edges in the graph — a struct used only as `&T{}` looks dead. When a
// caller widens `kinds` to a type-shaped kind, dead_code / audit_unused
// must stamp a _meta warning so a type-kind "dead" row isn't read as a
// safe-to-delete verdict.

func seed1780Project(t *testing.T, srv *Server, store *db.Store, pid string) {
	t.Helper()
	store.UpsertProject(db.Project{
		ID: pid, Path: t.TempDir(), Name: pid, IndexedAt: time.Now(),
		FileCount: 1, SymCount: 2, EdgeCount: 1,
	})
	srv.sessionID = pid
	// Unexported so they qualify as dead-code candidates — the type-kind
	// warning fires on the non-empty success path.
	mustUpsertSymbols(t, store, []db.Symbol{
		{
			ID: pid + "::pkg.helper#Function", ProjectID: pid, FilePath: "a.go",
			Name: "helper", QualifiedName: "pkg.helper", Kind: "Function",
			Language: "Go", ExtractionConfidence: 1.0,
		},
		{
			ID: pid + "::pkg.widget#Class", ProjectID: pid, FilePath: "a.go",
			Name: "widget", QualifiedName: "pkg.widget", Kind: "Class",
			Language: "Go", ExtractionConfidence: 1.0,
		},
	})
}

func metaWarningsHave1780(meta map[string]any, needle string) bool {
	ws, _ := meta["warnings"].([]any)
	for _, w := range ws {
		if s, ok := w.(string); ok && strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

func TestHandleDeadCode_TypeKinds_StampWarning_1780(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	seed1780Project(t, srv, store, "p-dc-typekind")

	res, err := srv.handleDeadCode(context.Background(), makeReq(map[string]any{
		"kinds": "Function,Class",
	}))
	if err != nil {
		t.Fatalf("handleDeadCode: %v", err)
	}
	meta, _ := decode(t, res)["_meta"].(map[string]any)
	if meta == nil {
		t.Fatalf("expected _meta envelope")
	}
	if !metaWarningsHave1780(meta, "type-reference edges") {
		t.Errorf("kinds=Function,Class must stamp a type-reference-edge warning; warnings=%v", meta["warnings"])
	}
}

// Control: the default-shape kinds set (Function,Method) must NOT stamp
// the type-kind warning — it would be noise on every normal call.
func TestHandleDeadCode_FunctionMethodKinds_NoTypeWarning_1780(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	seed1780Project(t, srv, store, "p-dc-nokind")

	res, err := srv.handleDeadCode(context.Background(), makeReq(map[string]any{
		"kinds": "Function,Method",
	}))
	if err != nil {
		t.Fatalf("handleDeadCode: %v", err)
	}
	meta, _ := decode(t, res)["_meta"].(map[string]any)
	if meta != nil && metaWarningsHave1780(meta, "type-reference edges") {
		t.Errorf("Function,Method kinds must not stamp the type-kind warning; warnings=%v", meta["warnings"])
	}
}

func TestHandleAuditUnused_TypeKinds_StampWarning_1780(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	seed1780Project(t, srv, store, "p-au-typekind")

	res, err := srv.handleAuditUnused(context.Background(), makeReq(map[string]any{
		"kinds": "Class",
	}))
	if err != nil {
		t.Fatalf("handleAuditUnused: %v", err)
	}
	meta, _ := decode(t, res)["_meta"].(map[string]any)
	if meta == nil {
		t.Fatalf("expected _meta envelope")
	}
	w2, _ := meta["warnings_v2"].([]any)
	found := false
	for _, w := range w2 {
		if m, ok := w.(map[string]any); ok && m["code"] == "type_kinds_no_reference_edges" {
			found = true
		}
	}
	if !found {
		t.Errorf("audit_unused kinds=Class must stamp warnings_v2 type_kinds_no_reference_edges; got %v", meta["warnings_v2"])
	}
}
