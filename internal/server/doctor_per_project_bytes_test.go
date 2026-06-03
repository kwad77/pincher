// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"testing"

	"github.com/kwad77/pincher/internal/db"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// #1220: doctor's projects array now carries a per-project
// db_bytes_estimate so multi-GB DB users can see WHICH project to delete
// first. The estimate uses count-based table weights; the load-bearing
// property is *relative ordering*, not absolute byte precision.

// Positive: a seeded project shows non-zero db_bytes_estimate that
// scales with symbol payload size.
func TestHandleDoctor_DBBytesEstimate_Positive(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	mustUpsertProject(t, store, "p-sized", "/tmp/p-sized", "sized")
	mustUpsertSymbols(t, store, []db.Symbol{
		{
			ID: "a.go::pkg.A#Function", ProjectID: "p-sized",
			FilePath: "a.go", Name: "A", QualifiedName: "pkg.A",
			Kind: "Function", Language: "Go",
			Signature: "func A() error",
			Docstring: "doc string for A",
		},
		{
			ID: "b.go::pkg.B#Function", ProjectID: "p-sized",
			FilePath: "b.go", Name: "B", QualifiedName: "pkg.B",
			Kind: "Function", Language: "Go",
			Signature: "func B(ctx context.Context, payload []byte) (*Result, error)",
			Docstring: "B does a much longer thing with a much longer docstring",
		},
	})

	body := decode(t, doctorResult(t, srv))
	projects, _ := body["projects"].([]any)
	if len(projects) != 1 {
		t.Fatalf("want 1 project, got %d", len(projects))
	}
	m, _ := projects[0].(map[string]any)
	est, ok := m["db_bytes_estimate"].(float64)
	if !ok {
		t.Fatalf("db_bytes_estimate must be numeric; got %T %v", m["db_bytes_estimate"], m["db_bytes_estimate"])
	}
	if est <= 0 {
		t.Errorf("db_bytes_estimate must be > 0 for a seeded project; got %v", est)
	}
}

// Negative: a project with zero symbols reports db_bytes_estimate=0
// (the field is always present, never omitted — JSON consumers can
// rely on its existence).
func TestHandleDoctor_DBBytesEstimate_EmptyProjectIsZero(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	mustUpsertProject(t, store, "p-empty", "/tmp/p-empty", "empty")
	// No symbols seeded.

	body := decode(t, doctorResult(t, srv))
	projects, _ := body["projects"].([]any)
	if len(projects) != 1 {
		t.Fatalf("want 1 project, got %d", len(projects))
	}
	m, _ := projects[0].(map[string]any)
	est, ok := m["db_bytes_estimate"].(float64)
	if !ok {
		t.Fatalf("db_bytes_estimate must be present even for empty projects; got %T %v", m["db_bytes_estimate"], m["db_bytes_estimate"])
	}
	if est != 0 {
		t.Errorf("empty project db_bytes_estimate = %v; want 0", est)
	}
}

// Cross-check (the load-bearing one): relative ordering. A project
// with bigger/more symbols has a strictly higher estimate than one
// with fewer/smaller symbols. This is what users actually act on
// ("delete the biggest one first") — absolute byte precision is
// secondary.
func TestHandleDoctor_DBBytesEstimate_RelativeOrderingMatchesPayload(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	mustUpsertProject(t, store, "p-small", "/tmp/p-small", "small")
	mustUpsertProject(t, store, "p-big", "/tmp/p-big", "big")

	mustUpsertSymbols(t, store, []db.Symbol{{
		ID: "s.go::s.S#Function", ProjectID: "p-small",
		FilePath: "s.go", Name: "S", QualifiedName: "s.S",
		Kind: "Function", Language: "Go", Signature: "func S()",
	}})
	// p-big: 5 symbols, each with longer signature + docstring.
	bigSyms := []db.Symbol{}
	for i := 0; i < 5; i++ {
		bigSyms = append(bigSyms, db.Symbol{
			ID:            "big.go::big.Fn" + string(rune('A'+i)) + "#Function",
			ProjectID:     "p-big",
			FilePath:      "big.go",
			Name:          "Fn" + string(rune('A'+i)),
			QualifiedName: "big.Fn" + string(rune('A'+i)),
			Kind:          "Function", Language: "Go",
			Signature: "func Fn(ctx context.Context, lots of args here) (*VeryLongReturnTypeName, error)",
			Docstring: "an intentionally lengthy docstring to push the byte total — paragraph one, paragraph two, paragraph three of explanatory text",
		})
	}
	mustUpsertSymbols(t, store, bigSyms)

	body := decode(t, doctorResult(t, srv))
	projects, _ := body["projects"].([]any)
	if len(projects) != 2 {
		t.Fatalf("want 2 projects, got %d", len(projects))
	}
	byName := map[string]float64{}
	for _, p := range projects {
		m, _ := p.(map[string]any)
		name, _ := m["name"].(string)
		est, _ := m["db_bytes_estimate"].(float64)
		byName[name] = est
	}
	if byName["big"] <= byName["small"] {
		t.Errorf("relative ordering violated: big=%v small=%v — big project must estimate higher", byName["big"], byName["small"])
	}
}

// Control: empty DB → projects array is empty (no panic, no nil-slice
// regression on the new field). The pre-#1220 healthy-empty test
// covered the surface; this pins the new field's absence path.
func TestHandleDoctor_DBBytesEstimate_EmptyDBNoCrash(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)

	body := decode(t, doctorResult(t, srv))
	projects, ok := body["projects"].([]any)
	if !ok {
		t.Fatalf("projects must be []; got %T", body["projects"])
	}
	if len(projects) != 0 {
		t.Errorf("want empty projects on empty DB; got %d", len(projects))
	}
}

func doctorResult(t *testing.T, srv *Server) *mcp.CallToolResult {
	t.Helper()
	res, err := srv.handleDoctor(context.Background(), makeReq(map[string]any{}))
	if err != nil {
		t.Fatalf("handleDoctor: %v", err)
	}
	return res
}
