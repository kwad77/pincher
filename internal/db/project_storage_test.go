// SPDX-License-Identifier: MIT

package db

import "testing"

func TestEstimateProjectBytes_AttributesSymbolsEdgesAndPendingEdges(t *testing.T) {
	s := newTestStore(t)
	if err := s.UpsertProject(testProject("storage-a")); err != nil {
		t.Fatalf("UpsertProject storage-a: %v", err)
	}
	if err := s.UpsertProject(testProject("storage-b")); err != nil {
		t.Fatalf("UpsertProject storage-b: %v", err)
	}

	if err := s.BulkUpsertSymbols([]Symbol{
		testSymbol("a::Caller#Function", "Caller", "Function", "storage-a", "caller.go"),
		testSymbol("a::Callee#Function", "Callee", "Function", "storage-a", "callee.go"),
		testSymbol("b::Only#Function", "Only", "Function", "storage-b", "only.go"),
	}); err != nil {
		t.Fatalf("BulkUpsertSymbols: %v", err)
	}
	if err := s.BulkUpsertEdges([]Edge{{
		ProjectID: "storage-a",
		FromID:    "a::Caller#Function",
		ToID:      "a::Callee#Function",
		Kind:      "CALLS",
		Source:    "resolve_pass",
	}}); err != nil {
		t.Fatalf("BulkUpsertEdges: %v", err)
	}
	if err := s.ReplacePendingEdgesForFile("storage-a", "caller.go", []PendingEdge{{
		ProjectID:  "storage-a",
		FromFile:   "caller.go",
		Kind:       "CALLS",
		FromQN:     "pkg.Caller",
		ToName:     "pkg.Callee",
		Confidence: 1,
	}}); err != nil {
		t.Fatalf("ReplacePendingEdgesForFile: %v", err)
	}

	got, err := s.EstimateProjectBytes()
	if err != nil {
		t.Fatalf("EstimateProjectBytes: %v", err)
	}
	if got["storage-a"] <= 0 {
		t.Fatalf("storage-a estimate = %d, want >0", got["storage-a"])
	}
	if got["storage-b"] <= 0 {
		t.Fatalf("storage-b estimate = %d, want >0", got["storage-b"])
	}
	if got["storage-a"] <= got["storage-b"] {
		t.Fatalf("storage-a estimate = %d, want > storage-b estimate %d", got["storage-a"], got["storage-b"])
	}
}

func TestProjectFileHelpers(t *testing.T) {
	s := newTestStore(t)
	if err := s.UpsertProject(testProject("files")); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	if err := s.SetFileHash("files", "caller.go", "hash-caller"); err != nil {
		t.Fatalf("SetFileHash caller: %v", err)
	}
	if err := s.SetFileHash("files", "callee.go", "hash-callee"); err != nil {
		t.Fatalf("SetFileHash callee: %v", err)
	}
	if err := s.BulkUpsertSymbols([]Symbol{
		testSymbol("files::Caller#Function", "Caller", "Function", "files", "caller.go"),
		testSymbol("files::Callee#Function", "Callee", "Function", "files", "callee.go"),
	}); err != nil {
		t.Fatalf("BulkUpsertSymbols: %v", err)
	}
	if err := s.BulkUpsertEdges([]Edge{{
		ProjectID: "files",
		FromID:    "files::Caller#Function",
		ToID:      "files::Callee#Function",
		Kind:      "CALLS",
		Source:    "resolve_pass",
	}}); err != nil {
		t.Fatalf("BulkUpsertEdges: %v", err)
	}

	files, err := s.ListFilesForProject("files")
	if err != nil {
		t.Fatalf("ListFilesForProject: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("ListFilesForProject len = %d, want 2: %v", len(files), files)
	}

	symbolFiles, err := s.ListSymbolFilePaths("files")
	if err != nil {
		t.Fatalf("ListSymbolFilePaths: %v", err)
	}
	if len(symbolFiles) != 2 {
		t.Fatalf("ListSymbolFilePaths len = %d, want 2: %v", len(symbolFiles), symbolFiles)
	}

	counts, err := s.SymbolCountsByFile("files")
	if err != nil {
		t.Fatalf("SymbolCountsByFile: %v", err)
	}
	if counts["caller.go"] != 1 || counts["callee.go"] != 1 {
		t.Fatalf("SymbolCountsByFile = %v, want one symbol per file", counts)
	}

	referrers, err := s.FilesWithEdgesToFile("files", "callee.go")
	if err != nil {
		t.Fatalf("FilesWithEdgesToFile: %v", err)
	}
	if len(referrers) != 1 || referrers[0] != "caller.go" {
		t.Fatalf("FilesWithEdgesToFile = %v, want [caller.go]", referrers)
	}
}
