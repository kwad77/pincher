// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// v0.65 description-honesty audit (continuation of v0.64 work):
// the architecture tool's description said hotspots were "functions"
// — the response actually surfaces Function/Method/Class/Interface/
// Type/Module (isHotspotKind). And the description named four
// response fields but the response includes six (missed `project`
// metadata and didn't explicitly name `node_kinds` + `edge_kinds`).
// Agents reading the description got an under-specified picture
// of what `architecture` actually returns.
//
// Table-from-the-start (#1152):
//   - Positive: description names every response field the
//     handler actually emits.
//   - Negative: stale "hotspot functions" framing — undersells
//     what hotspots contains.
//   - Control: the handler's hotspot kind filter still covers all
//     six kinds named in the description; description-vs-runtime
//     parity.
//   - Cross-check: a real architecture call returns a response
//     containing each of the six top-level fields the description
//     promises.

func findArchitectureToolDescription(t *testing.T) string {
	t.Helper()
	srv, _, _ := newRichTestServer(t)
	tool := srv.tools["architecture"]
	if tool == nil {
		t.Fatal("architecture tool not registered")
	}
	return tool.Description
}

// Positive: description names every response field the handler emits.
func TestArchitectureDescription_NamesAllResponseFields(t *testing.T) {
	desc := findArchitectureToolDescription(t)
	mustContain := []string{
		"languages",     // language breakdown map
		"entry_points",  // entry point array
		"hotspots",      // hotspot array
		"node_kinds",    // per-kind symbol count
		"edge_kinds",    // per-kind edge count
	}
	for _, want := range mustContain {
		if !strings.Contains(desc, want) {
			t.Errorf("architecture description missing response field %q\nGOT:\n%s", want, desc)
		}
	}
}

// Negative: description must NOT say hotspots are functions only —
// they include Method/Class/Interface/Type/Module too. Pre-fix the
// description undersold the response.
func TestArchitectureDescription_DoesNotClaimHotspotsAreFunctionsOnly(t *testing.T) {
	desc := findArchitectureToolDescription(t)
	// The stale phrasing was "hotspot functions". The fix replaces
	// that with the kind list explicitly. Pin against a regression.
	if strings.Contains(desc, "hotspot functions (most-called") {
		t.Errorf("architecture description still uses stale 'hotspot functions' framing\nGOT:\n%s", desc)
	}
}

// Control: description-vs-runtime parity. The description claims
// hotspots cover six kinds; the runtime filter (isHotspotKind) must
// match exactly. Pre-fix isHotspotKind could drift independently of
// the description and the only catch would be a user-facing
// "but I thought architecture showed classes too" puzzle.
func TestArchitectureHotspotKinds_MatchDescriptionContract(t *testing.T) {
	// The kinds named in the description.
	mentioned := []string{"Function", "Method", "Class", "Interface", "Type", "Module"}
	for _, k := range mentioned {
		if !isHotspotKind(k) {
			t.Errorf("description names %q as a hotspot kind but isHotspotKind(%q)=false",
				k, k)
		}
	}
	// Negative direction: kinds NOT mentioned should NOT pass the
	// filter. Pin a representative non-hotspot kind from the v0.64
	// audit work.
	for _, k := range []string{"Setting", "Section", "Variable", "Document"} {
		if isHotspotKind(k) {
			t.Errorf("isHotspotKind(%q)=true but description doesn't name it as a hotspot kind",
				k)
		}
	}
}

// Cross-check: a real architecture call returns a response
// containing each of the six top-level fields the description
// promises. Pre-fix a future refactor could drop one of the fields
// without the contract test noticing.
func TestHandleArchitecture_ResponseShapeMatchesDescription(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "p-arch-shape"
	store.UpsertProject(db.Project{
		ID: pid, Path: t.TempDir(), Name: pid, IndexedAt: time.Now(),
	})
	srv.sessionID = pid

	mustUpsertSymbols(t, store, []db.Symbol{
		{ID: pid + "::pkg.Main#Function", ProjectID: pid, FilePath: "main.go",
			Name: "Main", QualifiedName: "pkg.Main", Kind: "Function", Language: "Go",
			IsEntryPoint: true, ExtractionConfidence: 1.0},
	})

	result, err := srv.handleArchitecture(context.Background(), makeReq(map[string]any{}))
	if err != nil {
		t.Fatalf("handleArchitecture: %v", err)
	}
	body := decode(t, result)

	// Description promises these top-level keys; the response must
	// contain every one of them (even if some are empty arrays/maps).
	wantKeys := []string{
		"project",
		"languages",
		"entry_points",
		"hotspots",
		"node_kinds",
		"edge_kinds",
	}
	for _, k := range wantKeys {
		if _, ok := body[k]; !ok {
			t.Errorf("architecture response missing key %q promised by description\nGOT keys:", k)
			for got := range body {
				t.Logf("  %s", got)
			}
		}
	}
}
