// SPDX-License-Identifier: MIT

package ast

import (
	"testing"
)

// #1344 v0.71: Makefile extractor now emits CALLS edges between
// rules and their prerequisites when the prerequisite name resolves
// to another in-file rule. Pre-fix the parser-backed Makefile
// extractor produced 0 edges across the entire indexed corpus.

func TestExtractMakefile_RulePrereqsEmitCALLS_1344(t *testing.T) {
	src := []byte(`
build: deps fmt
	go build ./...

install: build
	cp pincher $(BIN)

test: build
	go test ./...

deps:
	go mod download

fmt:
	go fmt ./...
`)
	result := extractMakefile(src, "Makefile")
	if result == nil {
		t.Fatal("nil result")
	}

	// Each of these edges must be present.
	want := []struct {
		from, to string
	}{
		{"build", "deps"},
		{"build", "fmt"},
		{"install", "build"},
		{"test", "build"},
	}
	for _, w := range want {
		var found bool
		for _, e := range result.Edges {
			if e.Kind == "CALLS" && e.FromQN == w.from && e.ToName == w.to {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected CALLS edge %s → %s; got edges: %+v", w.from, w.to, result.Edges)
		}
	}

	// No spurious edges: every CALLS edge must point at a real
	// in-file rule, and there must be exactly the 4 above (no
	// duplicates, no edges into non-existent symbols).
	var callsCount int
	for _, e := range result.Edges {
		if e.Kind == "CALLS" {
			callsCount++
		}
	}
	if callsCount != len(want) {
		t.Errorf("expected exactly %d CALLS edges; got %d (edges: %+v)", len(want), callsCount, result.Edges)
	}
}

// Negative: a prerequisite that does NOT name an in-file rule (e.g.
// a source file path) must NOT emit a CALLS edge — we'd be
// synthesizing an edge into a non-existent symbol.
func TestExtractMakefile_ExternalPrereq_NoEdge_1344(t *testing.T) {
	src := []byte(`
artifact.o: artifact.c artifact.h
	cc -c -o $@ $<
`)
	result := extractMakefile(src, "Makefile")
	if result == nil {
		t.Fatal("nil result")
	}
	for _, e := range result.Edges {
		if e.Kind == "CALLS" {
			t.Errorf("unexpected CALLS edge: %+v (no in-file rule named %q exists)", e, e.ToName)
		}
	}
}

// Negative: $(VAR) and %.o pattern-stem prerequisites must NOT emit
// CALLS edges — they can't be statically resolved to a rule name.
func TestExtractMakefile_VariableAndPatternPrereqs_NoEdge_1344(t *testing.T) {
	src := []byte(`
build: $(BUILD_DEPS) %.o
	echo build

deps:
	echo deps
`)
	result := extractMakefile(src, "Makefile")
	if result == nil {
		t.Fatal("nil result")
	}
	for _, e := range result.Edges {
		if e.Kind == "CALLS" {
			t.Errorf("variable/pattern prerequisites must NOT emit CALLS; got %+v", e)
		}
	}
}

// Negative: .PHONY: foo bar declarations must NOT emit CALLS edges
// from `.PHONY` to foo/bar — `.PHONY` is a directive, not a rule.
func TestExtractMakefile_PhonyDeclaration_NoEdge_1344(t *testing.T) {
	src := []byte(`
.PHONY: build test

build:
	echo build

test:
	echo test
`)
	result := extractMakefile(src, "Makefile")
	if result == nil {
		t.Fatal("nil result")
	}
	for _, e := range result.Edges {
		if e.Kind == "CALLS" && e.FromQN == ".PHONY" {
			t.Errorf(".PHONY declaration must not emit CALLS; got %+v", e)
		}
	}
}

// Cross-check: duplicate prereqs on the same rule (`build: deps deps`)
// emit at most one edge (the seen-map dedupe).
func TestExtractMakefile_DuplicatePrereq_DedupedToOneEdge_1344(t *testing.T) {
	src := []byte(`
build: deps deps deps
	echo build

deps:
	echo deps
`)
	result := extractMakefile(src, "Makefile")
	if result == nil {
		t.Fatal("nil result")
	}
	var count int
	for _, e := range result.Edges {
		if e.Kind == "CALLS" && e.FromQN == "build" && e.ToName == "deps" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 deduped CALLS edge; got %d (edges: %+v)", count, result.Edges)
	}
}
