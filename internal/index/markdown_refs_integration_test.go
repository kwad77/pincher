package index

import (
	"context"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// #1481 — Markdown REFERENCES edges are emitted by the extractor (per
// internal/ast/markdown.go:240-277) but reported as zero in production
// despite real corpora with hundreds of internal links. This probe
// exercises the full pipeline (extract → resolve → persist) to lock
// down whether the link-walker reaches the DB.

const markdownIntraDocLinks = `# Project

Welcome.

## Installation

See [Configuration](#configuration) for the next step.

## Configuration

After [Installation](#installation), set up env vars.

## Usage

Refer back to [Configuration](#configuration) when troubleshooting.
`

func TestIndex_MarkdownIntraDocREFERENCES_PersistedToDB_1481(t *testing.T) {
	// Positive shape. Three sections with cross-references via
	// [text](#anchor) intra-doc links. Should emit REFERENCES
	// edges (Installation → Configuration, Configuration → Installation,
	// Usage → Configuration).
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "README.md", markdownIntraDocLinks)

	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("Index: %v", err)
	}

	projectID := db.ProjectIDFromPath(dir)

	// Find the Configuration Section. Section QNs root on the H1
	// heading slug (here: "project" from "# Project"), NOT the
	// filename — so the QN is "project.configuration".
	syms, err := store.GetSymbolsByQN(projectID, "project.configuration")
	if err != nil || len(syms) == 0 {
		t.Fatalf("expected project.configuration Section symbol; got %d syms err=%v", len(syms), err)
	}
	configID := syms[0].ID

	edges, err := store.EdgesTo(configID, []string{"REFERENCES"})
	if err != nil {
		t.Fatalf("EdgesTo: %v", err)
	}
	if len(edges) == 0 {
		t.Errorf("expected ≥1 REFERENCES edge into the Configuration section (Installation links to it via [Configuration](#configuration)); got 0. #1481 reproduces.")
	}
}

func TestIndex_MarkdownParentRelativeREFERENCES_PersistedToDB_1868(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "docs/adr-migration-plan.md", `# Migration Plan

See [Phase 0](../adr/0009-phase-0-tool-validation.md#decision) before continuing.
`)
	writeFile(t, dir, "adr/0009-phase-0-tool-validation.md", `# Phase 0 Tool Validation

## Decision

Use the validated tool path.
`)

	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("Index: %v", err)
	}

	projectID := db.ProjectIDFromPath(dir)
	targetSyms, err := store.GetSymbolsForFile(projectID, "adr/0009-phase-0-tool-validation.md")
	if err != nil {
		t.Fatalf("GetSymbolsForFile target: %v", err)
	}
	var targetID string
	for _, s := range targetSyms {
		if s.QualifiedName == "phase_0_tool_validation.decision" {
			targetID = s.ID
			break
		}
	}
	if targetID == "" {
		t.Fatalf("target Decision section missing; syms=%+v", targetSyms)
	}

	edges, err := store.EdgesTo(targetID, []string{"REFERENCES"})
	if err != nil {
		t.Fatalf("EdgesTo: %v", err)
	}
	if len(edges) == 0 {
		t.Fatal("expected parent-relative Markdown link to persist as REFERENCES edge; got 0")
	}
}
