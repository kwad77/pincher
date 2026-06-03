// SPDX-License-Identifier: MIT

package ast

import (
	"testing"
)

// #1429 v0.72: extractGoCalls emits false CALLS edges for calls through
// local function-typed variables that shadow a project Function with
// the same name. Sibling shape to #1423 (param-shadow READS-to-false-
// CALLS), but for the CALLS pass directly rather than the READS-pass
// binding-pass conversion.
//
// Repro on this repo (pre-fix): classifyTaskShape in
// internal/server/server.go declares `contains := func(needles ...string) bool {...}`
// and then calls `contains("dead code", ...)` multiple times. The Go
// extractor's body walk saw each `contains(...)` CallExpr and emitted
// ToName="contains"; the resolver bound it to the Function `contains`
// in cmd/pinch/init_hook.go — a phantom edge across packages that
// Go's scope rules wouldn't allow.
//
// Table shape (#1152): positive (closure-shadow suppresses CALLS),
// negative (real call to un-shadowed Function still emits), control
// (params shadow callable names too), cross-check (selector calls
// like obj.Method are unaffected because the resolver's receiver-
// type / receiver-method paths can disambiguate them).

// Positive — body-local closure shadows a sibling Function. The
// `contains()` call site must NOT emit a CALLS edge to the project
// Function `contains`.
func TestExtractGoCalls_LocalClosureShadowsProjectFn_NoFalseCALLS(t *testing.T) {
	src := `package svc
func contains() bool { return false }
func f() {
	contains := func() bool { return true }
	_ = contains()
}
`
	r := Extract([]byte(src), "Go", "svc/svc.go")
	if r == nil {
		t.Fatal("nil result")
	}
	for _, e := range r.Edges {
		if e.Kind == "CALLS" && e.FromQN == "svc.f" && e.ToName == "contains" {
			t.Errorf("CALLS edge to contains surfaced — local closure shadows project Fn (#1429)")
		}
	}
}

// Positive — `var name FuncT = ...` form (top-level pattern inside
// the function body). Same suppression as the := form.
func TestExtractGoCalls_VarFuncTypedLocal_NoFalseCALLS(t *testing.T) {
	src := `package svc
func contains() bool { return false }
func f() {
	var contains = func() bool { return true }
	_ = contains()
}
`
	r := Extract([]byte(src), "Go", "svc/svc.go")
	if r == nil {
		t.Fatal("nil result")
	}
	for _, e := range r.Edges {
		if e.Kind == "CALLS" && e.FromQN == "svc.f" && e.ToName == "contains" {
			t.Errorf("CALLS edge to contains surfaced — var-form local shadow (#1429)")
		}
	}
}

// Positive — function-typed parameter. The parameter could be called
// inside the body (call through the parameter); shouldn't bind to a
// same-named project Function.
func TestExtractGoCalls_FuncTypedParam_NoFalseCALLS(t *testing.T) {
	src := `package svc
func handler() {}
func f(handler func()) {
	handler()
}
`
	r := Extract([]byte(src), "Go", "svc/svc.go")
	if r == nil {
		t.Fatal("nil result")
	}
	for _, e := range r.Edges {
		if e.Kind == "CALLS" && e.FromQN == "svc.f" && e.ToName == "handler" {
			t.Errorf("CALLS edge to handler surfaced — function-typed param shadow (#1429)")
		}
	}
}

// Negative — un-shadowed call still emits. Without this guard the
// #1429 filter could over-suppress.
func TestExtractGoCalls_UnshadowedCall_StillEmits(t *testing.T) {
	src := `package svc
func helper() {}
func contains() bool { return false }
func f() {
	contains := func() bool { return true }
	_ = contains()
	helper()
}
`
	r := Extract([]byte(src), "Go", "svc/svc.go")
	if r == nil {
		t.Fatal("nil result")
	}
	var sawHelper bool
	for _, e := range r.Edges {
		if e.Kind != "CALLS" || e.FromQN != "svc.f" {
			continue
		}
		if e.ToName == "helper" {
			sawHelper = true
		}
		if e.ToName == "contains" {
			t.Errorf("CALLS to contains surfaced — shadow filter regressed")
		}
	}
	if !sawHelper {
		t.Error("CALLS to helper missing — un-shadowed call must still emit")
	}
}

// Cross-check — selector calls (`obj.Method`, `pkg.Fn`) are unaffected
// by the bare-name shadow filter. They route through the receiver-type
// / receiver-method resolver paths which have their own disambiguation
// (the filter only fires on dotless callees).
func TestExtractGoCalls_SelectorCalls_NotFiltered(t *testing.T) {
	src := `package svc
type T struct{}
func (t T) Run() {}
func f(contains func()) {
	t := T{}
	t.Run()
}
`
	r := Extract([]byte(src), "Go", "svc/svc.go")
	if r == nil {
		t.Fatal("nil result")
	}
	var sawTRun bool
	for _, e := range r.Edges {
		if e.Kind == "CALLS" && e.FromQN == "svc.f" && e.ToName == "t.Run" {
			sawTRun = true
		}
	}
	if !sawTRun {
		t.Error("selector call t.Run missing — shadow filter must not affect dotted callees")
	}
}

// Cross-check — the dogfood repro shape: a body-local `contains` closure
// is invoked many times. Pre-#1429 every call site emitted a CALLS edge
// to the project Function `contains`. Post-fix, zero CALLS to `contains`
// from this function.
func TestExtractGoCalls_DogfoodRepro_ClassifyTaskShape(t *testing.T) {
	src := `package svc
func contains() bool { return false }
func classifyTaskShape(input string) string {
	contains := func(needles ...string) bool {
		for _, n := range needles {
			_ = n
		}
		return false
	}
	switch {
	case contains("dead code", "dead-code"):
		return "dead-code"
	case contains("test", "spec", "coverage"):
		return "test"
	}
	return ""
}
`
	r := Extract([]byte(src), "Go", "svc/svc.go")
	if r == nil {
		t.Fatal("nil result")
	}
	for _, e := range r.Edges {
		if e.Kind == "CALLS" && e.FromQN == "svc.classifyTaskShape" && e.ToName == "contains" {
			t.Error("CALLS to contains surfaced — dogfood repro shape (#1429)")
		}
	}
}
