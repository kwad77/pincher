// SPDX-License-Identifier: MIT

package server

// Guide-coaching PR-15/17: phase-aware shape routing. The front door
// used to return the generic architecture+search fallback for nuanced
// tasks and never recommended the composites. These tests pin the new
// routing table:
//
//	stack-trace-looking task → investigate_failure
//	directory-shaped task    → onboard_module
//	pre-edit intent          → plan_change
//	caller question          → trace
//	resumption               → loop resume (ledger-gated) else adr list
//	multi-question task      → batch (only when registered)
//	everything else          → legacy keyword path (preserved)

import (
	"strings"
	"testing"
)

func TestClassifyTaskShape_CompositeShapes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		task string
		want guideShape
	}{
		// Stack-trace-looking: file:line frame fires on its own.
		{"panic at internal/server/server.go:12423 in handleGuide", shapeFailure},
		{"goroutine 1 [running]: main.main()", shapeFailure},
		{"panic: runtime error: nil deref\nat handleGuide\nexit status 2", shapeFailure},
		{"investigate this traceback from CI", shapeFailure},
		// A DESCRIBED bug (single line, no frame) stays a fix task.
		{"fix panic in the indexer", shapeFix},
		// Directory-shaped.
		{"onboard me on internal/ast", shapeOnboard},
		{"orient in the supervisor package", shapeOnboard},
		{"internal/server/", shapeOnboard},
		// Bare extensionless path, no verb → onboard via the keyword
		// default branch.
		{"internal/server", shapeOnboard},
		// A verb-ful path task keeps its keyword routing.
		{"find pkg/sub", shapeFind},
		// Pre-edit intent.
		{"change the retry policy in flushSession", shapeRefactor},
		{"what breaks if I touch flushSession", shapeRefactor},
		{"modify the FTS sync triggers", shapeRefactor},
		// "change" as a mid-sentence noun must NOT hijack a fix task.
		{"fix the change detection bug", shapeFix},
		// Caller questions.
		{"blast radius of flushSession", shapeTraceIn},
		{"check blast radius of this change", shapeReview}, // diff-scoped stays review
		// Resumption: leading verb or idiomatic phrase.
		{"continue where I left off", shapeResume},
		{"resume the migration work", shapeResume},
		{"where was I", shapeResume},
		{"pick up where we stopped yesterday", shapeResume},
		// "continue" mid-sentence is not resumption.
		{"fix the loop so it can continue after errors", shapeFix},
		// Multi-question.
		{"what is the indexer? how does search work?", shapeBatch},
		{"1. explain flushSession 2. explain handleStats", shapeBatch},
		// Legacy fallback preserved.
		{"something completely vague", shapeUnknown},
		{"fix the login timeout bug", shapeFix},
	}
	for _, c := range cases {
		if got := classifyTaskShape(c.task); got != c.want {
			t.Errorf("classifyTaskShape(%q) = %v, want %v", c.task, got, c.want)
		}
	}
}

// TestComputeGuide_ShapeRouting_FirstRecommendation pins the shipped
// shape → first-recommendation table end-to-end through computeGuide
// (runtime gates included; the test server registers the production
// tool surface, which has the three composites but no batch/loop).
func TestComputeGuide_ShapeRouting_FirstRecommendation(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	cases := []struct {
		task      string
		wantShape guideShape
		wantFirst string
	}{
		{"panic at internal/server/server.go:12423 in handleGuide", shapeFailure, "investigate_failure"},
		{"onboard me on internal/ast", shapeOnboard, "onboard_module"},
		{"internal/server/", shapeOnboard, "onboard_module"},
		{"refactor the auth middleware", shapeRefactor, "plan_change"},
		{"what breaks if I touch flushSession", shapeRefactor, "plan_change"},
		{"who calls flushSession", shapeTraceIn, "trace"},
		{"blast radius of flushSession", shapeTraceIn, "trace"},
		// No loop ledger in a fresh test store (and no loop tool
		// registered) → resumption falls back to adr list.
		{"continue where I left off", shapeResume, "adr"},
		// batch is NOT registered on this branch → multi-question
		// tasks fall back to the keyword path ("what is" → understand).
		{"what is the indexer? how does search work?", shapeUnderstand, "architecture"},
		// Legacy fallback preserved: unknown → architecture+search.
		{"something completely vague", shapeUnknown, "architecture"},
		// Legacy fix flow preserved: search leads.
		{"fix the login timeout bug", shapeFix, "search"},
	}
	for _, c := range cases {
		shape, _, recs, _ := srv.computeGuide(c.task, "")
		if shape != c.wantShape {
			t.Errorf("computeGuide(%q) shape = %v, want %v", c.task, shape, c.wantShape)
		}
		if len(recs) == 0 {
			t.Errorf("computeGuide(%q) returned no recommendations", c.task)
			continue
		}
		if recs[0]["tool"] != c.wantFirst {
			t.Errorf("computeGuide(%q) first rec = %q, want %q (recs=%v)", c.task, recs[0]["tool"], c.wantFirst, recs)
		}
		for i, r := range recs {
			if r["tool"] == "" || r["why"] == "" || r["args"] == "" {
				t.Errorf("computeGuide(%q) rec %d missing tool/args/why: %v", c.task, i, r)
			}
		}
	}
}

// The composite recommendations must carry concrete args derived from
// the task — not bare placeholders.
func TestComputeGuide_CompositeArgsDerivedFromTask(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)

	// investigate_failure gets the raw task as error_text.
	trace := "panic: nil deref\nat internal/server/server.go:12423"
	_, _, recs, _ := srv.computeGuide(trace, "")
	if got := recs[0]["args"]; !strings.Contains(got, "error_text") || !strings.Contains(got, "server.go:12423") {
		t.Errorf("failure args = %q, want error_text carrying the pasted trace", got)
	}

	// onboard_module gets the directory extracted from the task.
	_, _, recs, _ = srv.computeGuide("onboard me on internal/ast", "")
	if got := recs[0]["args"]; !strings.Contains(got, `"directory":"internal/ast"`) {
		t.Errorf("onboard args = %q, want directory internal/ast", got)
	}

	// plan_change gets the task hint as target.
	_, _, recs, _ = srv.computeGuide("refactor the auth middleware", "")
	if got := recs[0]["args"]; !strings.Contains(got, `"target":"auth middleware"`) {
		t.Errorf("plan_change args = %q, want target from the task hint", got)
	}
}

// When the batch tool IS registered (post-merge of the batch PR), a
// multi-question task routes to it with one sub-call per question.
// The gate is the runtime registered-tools set, not a hardcode — pin
// that by registering a stub batch tool on the test server.
func TestComputeGuide_BatchShape_GatedOnRegistration(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	srv.tools["batch"] = nil // presence in the set is what toolRegistered checks

	task := "what is the indexer? how does search ranking work?"
	shape, _, recs, _ := srv.computeGuide(task, "")
	if shape != shapeBatch {
		t.Fatalf("shape = %v, want %v with batch registered", shape, shapeBatch)
	}
	if recs[0]["tool"] != "batch" {
		t.Fatalf("first rec = %q, want batch", recs[0]["tool"])
	}
	// One search sub-call per question, hints from each question.
	if args := recs[0]["args"]; !strings.Contains(args, "indexer") || !strings.Contains(args, "ranking") {
		t.Errorf("batch args = %q, want per-question hints (indexer / ranking)", args)
	}
}

// Resumption only recommends loop resume when BOTH gates pass: a
// registered loop tool and a non-empty loop ledger. A fresh store has
// no loop_checkpoints table (schema v40 isn't migrated here), so even
// with the tool registered the recommendation stays adr list.
func TestComputeGuide_ResumeShape_LedgerGated(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	srv.tools["loop"] = nil

	shape, _, recs, _ := srv.computeGuide("continue where I left off", "")
	if shape != shapeResume {
		t.Fatalf("shape = %v, want %v", shape, shapeResume)
	}
	if recs[0]["tool"] != "adr" {
		t.Errorf("first rec = %q, want adr (ledger empty/absent must not recommend loop resume)", recs[0]["tool"])
	}
	for _, r := range recs {
		if r["tool"] == "loop" {
			t.Errorf("loop resume recommended without a ledger: %v", recs)
		}
	}
}
