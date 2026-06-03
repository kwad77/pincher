// SPDX-License-Identifier: MIT

package ast

import (
	"testing"
)

// #1423 v0.72: Go READS-pass binding-pass falsely emits a CALLS edge
// when a parameter (or other local) name shadows a project Function
// with the same name. Pre-fix repro: `func finishIndexSpan(...,
// totalSymbols int, ...)` referencing `totalSymbols` in the body
// produced a READS edge with ToName="totalSymbols"; the #565 binding-
// pass then converted that to a phantom CALLS edge to the test
// helper `func totalSymbols(...)`. Real impact: polluted `trace`
// (test helper appears called from production), polluted `dead_code`
// (test helper looks "live" via the spurious caller), and any param
// name that matches a project Function (`count`, `value`, `data`,
// `result`, `total*`, `err`, `index`) cross-binds the same way.
//
// Table shape (#1152): positive (shadowed param doesn't emit READS),
// negative (true call to same-named function still emits CALLS),
// control (unrelated reads still flow through correctly), cross-check
// (selector reads `local.Field` still emit so #760's field-type path
// keeps working).

// Positive — parameter `totalSymbols` shadows a hypothetical
// project Function named totalSymbols. The body reads the parameter;
// no READS edge to it should leave the extractor (and so no spurious
// CALLS edge from the #565 binding-pass can manufacture).
func TestExtractGoReads_ParamShadow_NoFalseRead(t *testing.T) {
	src := `package svc
func finishIndexSpan(totalFiles, totalSymbols int) {
	_ = totalFiles
	_ = totalSymbols
}
`
	r := Extract([]byte(src), "Go", "svc/svc.go")
	if r == nil {
		t.Fatal("nil result")
	}
	for _, e := range r.Edges {
		if e.Kind != "READS" {
			continue
		}
		if e.ToName == "totalSymbols" || e.ToName == "totalFiles" {
			t.Errorf("extractor emitted READS to %q (parameter, should be skipped — #1423 bug)", e.ToName)
		}
	}
}

// Positive — receiver shadow: receiver name `s` reads with no
// selector must NOT emit READS.
func TestExtractGoReads_ReceiverShadow_NoFalseRead(t *testing.T) {
	src := `package svc
type S struct{}
func (s *S) M() {
	_ = s
}
`
	r := Extract([]byte(src), "Go", "svc/svc.go")
	if r == nil {
		t.Fatal("nil result")
	}
	for _, e := range r.Edges {
		if e.Kind == "READS" && e.ToName == "s" {
			t.Errorf("extractor emitted READS to receiver %q (should be skipped — #1423)", e.ToName)
		}
	}
}

// Positive — in-body local: `var local int` references must NOT
// emit READS for `local`.
func TestExtractGoReads_InBodyLocal_NoFalseRead(t *testing.T) {
	src := `package svc
func f() {
	var local int
	_ = local
}
`
	r := Extract([]byte(src), "Go", "svc/svc.go")
	if r == nil {
		t.Fatal("nil result")
	}
	for _, e := range r.Edges {
		if e.Kind == "READS" && e.ToName == "local" {
			t.Errorf("extractor emitted READS to in-body local %q (should be skipped — #1423)", e.ToName)
		}
	}
}

// Negative — when a parameter shadows a Function name, the extractor
// suppresses BOTH the READS edge for the param reference AND any
// CALLS edge whose callee matches the shadowed name. The latter is
// required for #1429: pre-fix the body's `helper()` call site emitted
// a CALLS edge that resolved to the project Function `helper` even
// though Go's scope rules would error (can't call an int). Suppressing
// both is the correct behaviour — fold sequence #1423 (READS) then
// #1429 (CALLS).
func TestExtractGoReads_ParamShadow_NoCALLSOrREADSToShadowedName(t *testing.T) {
	src := `package svc
func helper() {}
func f(helper int) {
	helper()
	_ = helper
}
`
	r := Extract([]byte(src), "Go", "svc/svc.go")
	if r == nil {
		t.Fatal("nil result")
	}
	for _, e := range r.Edges {
		if e.FromQN != "svc.f" {
			continue
		}
		if e.Kind == "CALLS" && e.ToName == "helper" {
			t.Error("CALLS edge to helper surfaced — shadowed param should suppress (#1429)")
		}
		if e.Kind == "READS" && e.ToName == "helper" {
			t.Error("READS edge to helper present — shadowed param should suppress (#1423)")
		}
	}
}

// Control — package-level Variable reads still emit READS. The
// shadowing filter only fires when the name is a known local; a
// genuine cross-function package-var read must not be suppressed.
func TestExtractGoReads_PackageVarRead_StillEmits(t *testing.T) {
	src := `package svc
var Cache int
func f() {
	_ = Cache
}
`
	r := Extract([]byte(src), "Go", "svc/svc.go")
	if r == nil {
		t.Fatal("nil result")
	}
	var found bool
	for _, e := range r.Edges {
		if e.Kind == "READS" && e.ToName == "Cache" && e.FromQN == "svc.f" {
			found = true
			break
		}
	}
	if !found {
		t.Error("package-var Cache READS missing — non-local names must still emit (regression guard)")
	}
}

// Cross-check — selector read `local.Field` still emits the
// `.Field` READS even though `local` is a parameter. The selector
// path stamps BaseType so the #760 / binding-pass logic can do
// type-aware filtering further downstream.
func TestExtractGoReads_SelectorOnLocal_StillEmitsField(t *testing.T) {
	src := `package svc
type T struct{ Field int }
func f(local *T) {
	_ = local.Field
}
`
	r := Extract([]byte(src), "Go", "svc/svc.go")
	if r == nil {
		t.Fatal("nil result")
	}
	var fieldRead bool
	for _, e := range r.Edges {
		if e.Kind == "READS" && e.ToName == "Field" && e.FromQN == "svc.f" {
			fieldRead = true
			if e.BaseType == "" {
				t.Errorf("selector READS on Field missing BaseType — #760 path broken")
			}
		}
		if e.Kind == "READS" && e.ToName == "local" {
			t.Errorf("bare-name READS to local parameter %q surfaced — #1423 filter regressed", e.ToName)
		}
	}
	if !fieldRead {
		t.Error("selector READS on Field missing — #760 path broken")
	}
}
