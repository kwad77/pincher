// SPDX-License-Identifier: MIT

package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestEdgeProvenanceTierColumn_PresentAndDefaultExtracted(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	var dflt sql.NullString
	if err := s.ro.QueryRow(`SELECT dflt_value FROM pragma_table_info('edges') WHERE name = 'provenance_tier'`).Scan(&dflt); err != nil {
		t.Fatalf("edges.provenance_tier column missing: %v", err)
	}
	if !dflt.Valid || dflt.String != "'EXTRACTED'" {
		t.Fatalf("edges.provenance_tier default = %v, want 'EXTRACTED'", dflt)
	}
}

func TestBulkUpsertEdges_RoundTripsProvenanceTier(t *testing.T) {
	s := newTestStore(t)
	projectID := "proj-provenance-tier"
	if err := s.UpsertProject(testProject(projectID)); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	if err := s.BulkUpsertSymbols([]Symbol{
		testSymbol("a", "A", "Function", projectID, "a.go"),
		testSymbol("b", "B", "Function", projectID, "b.go"),
	}); err != nil {
		t.Fatalf("BulkUpsertSymbols: %v", err)
	}
	if err := s.BulkUpsertEdges([]Edge{{
		ProjectID:       projectID,
		FromID:          "a",
		ToID:            "b",
		Kind:            "MAPS_TO",
		Confidence:      0.82,
		ProvenanceTier:  EdgeProvenanceInferred,
		Source:          "resolve_pass",
	}}); err != nil {
		t.Fatalf("BulkUpsertEdges: %v", err)
	}

	edges, err := s.ListEdgesForProject(projectID)
	if err != nil {
		t.Fatalf("ListEdgesForProject: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("len(edges) = %d, want 1", len(edges))
	}
	if got := edges[0].ProvenanceTier; got != EdgeProvenanceInferred {
		t.Fatalf("ProvenanceTier = %q, want %q", got, EdgeProvenanceInferred)
	}
}

func TestBulkUpsertEdges_DefaultsEmptyProvenanceTierToExtracted(t *testing.T) {
	s := newTestStore(t)
	projectID := "proj-provenance-default"
	if err := s.UpsertProject(testProject(projectID)); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	if err := s.BulkUpsertSymbols([]Symbol{
		testSymbol("a", "A", "Function", projectID, "a.go"),
		testSymbol("b", "B", "Function", projectID, "b.go"),
	}); err != nil {
		t.Fatalf("BulkUpsertSymbols: %v", err)
	}
	if err := s.BulkUpsertEdges([]Edge{{ProjectID: projectID, FromID: "a", ToID: "b", Kind: "CALLS", Confidence: 1.0}}); err != nil {
		t.Fatalf("BulkUpsertEdges: %v", err)
	}

	edges, err := s.ListEdgesForProject(projectID)
	if err != nil {
		t.Fatalf("ListEdgesForProject: %v", err)
	}
	if got := edges[0].ProvenanceTier; got != EdgeProvenanceExtracted {
		t.Fatalf("ProvenanceTier = %q, want %q", got, EdgeProvenanceExtracted)
	}
}
