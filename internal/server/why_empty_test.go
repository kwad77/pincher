// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
)

// #1391 v0.85 Phase 4 audit suite for why_empty. The composite is
// stateless (no DB queries) — tests don't need a fixture server.

// TestWhyEmpty_MissingPriorReason — negative-control: empty input
// returns a rich error listing every known reason value.
func TestWhyEmpty_MissingPriorReason(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)

	res, err := srv.handleWhyEmpty(context.Background(), makeReq(map[string]any{}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatal("expected IsError=true for missing prior_empty_reason")
	}
	text := textOf(t, res)
	if !strings.Contains(text, "why_empty requires `prior_empty_reason`") {
		t.Errorf("expected rich-error message; got %s", text)
	}
}

// TestWhyEmpty_KnownReasonReturnsCatalogEntry — positive happy path:
// passing a known empty_reason returns the structured catalog entry.
func TestWhyEmpty_KnownReasonReturnsCatalogEntry(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)

	res, err := srv.handleWhyEmpty(context.Background(), makeReq(map[string]any{
		"prior_empty_reason": EmptyReasonNoResultsInCorpus,
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success; got %s", textOf(t, res))
	}
	body := decode(t, res)

	if body["empty_reason"] != EmptyReasonNoResultsInCorpus {
		t.Errorf("empty_reason = %v; want %s", body["empty_reason"], EmptyReasonNoResultsInCorpus)
	}
	for _, key := range []string{"title", "when_it_fires", "recovery_action", "recovery_steps", "catalog_anchor"} {
		if _, ok := body[key]; !ok {
			t.Errorf("missing %q in catalog response", key)
		}
	}
	steps, _ := body["recovery_steps"].([]any)
	if len(steps) == 0 {
		t.Error("recovery_steps must not be empty")
	}
}

// TestWhyEmpty_UnknownReasonRichError — negative: unknown reason
// returns a rich error listing every known value (helpful for typo
// recovery).
func TestWhyEmpty_UnknownReasonRichError(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)

	res, err := srv.handleWhyEmpty(context.Background(), makeReq(map[string]any{
		"prior_empty_reason": "totally_made_up_code",
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError=true for unknown reason")
	}
	text := textOf(t, res)
	if !strings.Contains(text, "unknown empty_reason") {
		t.Errorf("expected unknown-reason rich error; got %s", text)
	}
	// Should mention at least one known reason for typo recovery.
	if !strings.Contains(text, EmptyReasonNoResultsInCorpus) {
		t.Errorf("expected known-reason list in error; got %s", text)
	}
}

// TestWhyEmpty_UnknownReasonListIsDeterministic — regression for #1580.
// Go map iteration is randomised, so a naive `for k := range catalog`
// produces a different "Known values:" ordering per call. This test
// invokes the unknown-reason path five times and asserts the error
// message is byte-identical across calls — pinning the sort.Strings
// fix.
func TestWhyEmpty_UnknownReasonListIsDeterministic(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)

	var first string
	for i := 0; i < 5; i++ {
		res, err := srv.handleWhyEmpty(context.Background(), makeReq(map[string]any{
			"prior_empty_reason": "totally_made_up_code",
		}))
		if err != nil {
			t.Fatalf("call %d: handler error: %v", i, err)
		}
		text := textOf(t, res)
		if i == 0 {
			first = text
			continue
		}
		if text != first {
			t.Errorf("call %d: error text differs from call 0 — map iteration order is leaking (#1580 regression). Call 0 had %q; call %d had %q",
				i, first, i, text)
		}
	}
}

// TestWhyEmpty_CatalogCoversEveryConstant — cross-check: every
// EmptyReason* constant declared in empty_reason.go has a matching
// catalog entry. Pairing gate — adding a constant without bumping
// the catalog fails here.
func TestWhyEmpty_CatalogCoversEveryConstant(t *testing.T) {
	t.Parallel()
	allReasons := []string{
		EmptyReasonNoProjectIndexed,
		EmptyReasonStaleIndex,
		EmptyReasonUnsupportedLanguage,
		EmptyReasonLowConfidenceExtractor,
		EmptyReasonSameFileOnly,
		EmptyReasonCrossFileUnavailable,
		EmptyReasonQueryTooNarrow,
		EmptyReasonNoResultsInCorpus,
		EmptyReasonCapDroppedAll,
		EmptyReasonIncrementalNoChange,
		EmptyReasonAllFilesBlocked,
		EmptyReasonExtractorEmittedNothing,
		EmptyReasonTargetNotResolved,
	}
	for _, reason := range allReasons {
		entry, ok := whyEmptyCatalog[reason]
		if !ok {
			t.Errorf("constant %q has no whyEmptyCatalog entry — add one to internal/server/why_empty.go and a row to docs/empty-reasons.md", reason)
			continue
		}
		if entry.Title == "" {
			t.Errorf("%q catalog entry missing Title", reason)
		}
		if entry.WhenItFires == "" {
			t.Errorf("%q catalog entry missing WhenItFires", reason)
		}
		if entry.RecoveryAction == "" {
			t.Errorf("%q catalog entry missing RecoveryAction", reason)
		}
		if len(entry.RecoverySteps) == 0 {
			t.Errorf("%q catalog entry has no RecoverySteps", reason)
		}
		if entry.CatalogAnchor == "" {
			t.Errorf("%q catalog entry missing CatalogAnchor", reason)
		}
	}
}

// TestWhyEmpty_NoUnknownCatalogEntries — inverse: every catalog
// entry's Reason field matches a declared constant. Catches typos in
// the catalog map keys.
func TestWhyEmpty_NoUnknownCatalogEntries(t *testing.T) {
	t.Parallel()
	known := map[string]bool{
		EmptyReasonNoProjectIndexed:        true,
		EmptyReasonStaleIndex:              true,
		EmptyReasonUnsupportedLanguage:     true,
		EmptyReasonLowConfidenceExtractor:  true,
		EmptyReasonSameFileOnly:            true,
		EmptyReasonCrossFileUnavailable:    true,
		EmptyReasonQueryTooNarrow:          true,
		EmptyReasonNoResultsInCorpus:       true,
		EmptyReasonCapDroppedAll:           true,
		EmptyReasonIncrementalNoChange:     true,
		EmptyReasonAllFilesBlocked:         true,
		EmptyReasonExtractorEmittedNothing: true,
		EmptyReasonTargetNotResolved:       true,
	}
	for key, entry := range whyEmptyCatalog {
		if !known[key] {
			t.Errorf("whyEmptyCatalog has entry %q that isn't a declared EmptyReason* constant", key)
		}
		if entry.Reason != key {
			t.Errorf("whyEmptyCatalog[%q].Reason = %q (mismatch — entry self-claims a different reason)", key, entry.Reason)
		}
	}
}

// TestWhyEmpty_RecoveryStepsShape — control: every recovery step
// follows the {tool, args, why} contract. Stamping a malformed step
// would confuse the agent's parser.
func TestWhyEmpty_RecoveryStepsShape(t *testing.T) {
	t.Parallel()
	for reason, entry := range whyEmptyCatalog {
		for i, step := range entry.RecoverySteps {
			if step["tool"] == "" {
				t.Errorf("%s recovery_steps[%d] missing tool", reason, i)
			}
			if step["why"] == "" {
				t.Errorf("%s recovery_steps[%d] missing why", reason, i)
			}
			// args may be empty string for tools that take no args (e.g. list, doctor)
			if _, ok := step["args"]; !ok {
				t.Errorf("%s recovery_steps[%d] missing args key", reason, i)
			}
		}
	}
}

// TestWhyEmpty_NoDBQueryRequired — control: the composite must not
// require a project_id or any DB state. Statelessness is part of the
// contract (it's a catalog lookup, not a graph query).
func TestWhyEmpty_NoDBQueryRequired(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)

	// No project arg, no session project. Should still succeed.
	res, err := srv.handleWhyEmpty(context.Background(), makeReq(map[string]any{
		"prior_empty_reason": EmptyReasonQueryTooNarrow,
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Errorf("composite should not require project — it's stateless; got error: %s", textOf(t, res))
	}
}

// TestWhyEmpty_IsRegistered — gate.
func TestWhyEmpty_IsRegistered(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	tool, ok := srv.tools["why_empty"]
	if !ok {
		t.Fatal("why_empty not registered in srv.tools")
	}
	desc := strings.ToLower(tool.Description)
	for _, want := range []string{"empty", "recovery"} {
		if !strings.Contains(desc, want) {
			t.Errorf("description should mention %q; got %q", want, tool.Description)
		}
	}
}
