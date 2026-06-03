// SPDX-License-Identifier: MIT

package ast

import (
	"strings"
	"testing"
)

// #1177 v0.72: TS receiver-type stamping on CALLS edges. Pre-fix the
// TS extractor emitted ToName=<bare method name> for every call site
// (including `this.X()` and `varname.X()`). The resolver fell back to
// a name-only Method lookup that picks the wrong target whenever two
// classes both define a method named X — the dead_code FP family this
// stack closes.
//
// Table shape (#1152): positive (ReceiverType stamped correctly),
// negative (un-typed locals stay unstamped), control (free calls
// unchanged), cross-check (every relevant shape carries the regex-
// tier confidence).

// Positive — `this.X()` inside a method carries ReceiverType =
// enclosing class.
func TestExtractTypeScript_ReceiverType_ThisMethodStampsClass(t *testing.T) {
	src := `export class Service {
		process(): void {
			this.validate();
			this.persist();
		}
		validate(): void {}
		persist(): void {}
	}
`
	r := Extract([]byte(src), "TypeScript", "src/svc.ts")
	if r == nil {
		t.Fatal("nil result")
	}
	want := map[string]bool{
		"this.validate": false,
		"this.persist":  false,
	}
	for _, e := range r.Edges {
		if e.Kind != "CALLS" || !strings.HasSuffix(e.FromQN, ".process") {
			continue
		}
		if _, ok := want[e.ToName]; !ok {
			continue
		}
		if e.ReceiverType != "Service" {
			t.Errorf("edge %q→%q ReceiverType=%q, want Service",
				e.FromQN, e.ToName, e.ReceiverType)
		}
		want[e.ToName] = true
	}
	for k, v := range want {
		if !v {
			t.Errorf("missing CALLS edge from process → %q", k)
		}
	}
}

// Positive — typed-parameter binding: `function f(c: Cart) { c.add() }`
// emits ToName=`c.add` ReceiverType=Cart.
func TestExtractTypeScript_ReceiverType_TypedParamBinding(t *testing.T) {
	src := `export function handle(c: Cart): void {
		c.add(1);
		c.remove(1);
	}
`
	r := Extract([]byte(src), "TypeScript", "src/handle.ts")
	if r == nil {
		t.Fatal("nil result")
	}
	want := map[string]bool{"c.add": false, "c.remove": false}
	for _, e := range r.Edges {
		if e.Kind != "CALLS" || !strings.HasSuffix(e.FromQN, ".handle") {
			continue
		}
		if _, ok := want[e.ToName]; !ok {
			continue
		}
		if e.ReceiverType != "Cart" {
			t.Errorf("edge %q→%q ReceiverType=%q, want Cart",
				e.FromQN, e.ToName, e.ReceiverType)
		}
		want[e.ToName] = true
	}
	for k, v := range want {
		if !v {
			t.Errorf("missing CALLS edge from handle → %q", k)
		}
	}
}

// Positive — typed-local binding: `const cart: Cart = new Cart();
// cart.X()` stamps ReceiverType=Cart.
func TestExtractTypeScript_ReceiverType_TypedLocalBinding(t *testing.T) {
	src := `export function bootstrap(): void {
		const cart: Cart = new Cart();
		cart.add(1);
	}
`
	r := Extract([]byte(src), "TypeScript", "src/boot.ts")
	if r == nil {
		t.Fatal("nil result")
	}
	var got *ExtractedEdge
	for i := range r.Edges {
		e := &r.Edges[i]
		if e.Kind == "CALLS" && strings.HasSuffix(e.FromQN, ".bootstrap") && e.ToName == "cart.add" {
			got = e
			break
		}
	}
	if got == nil {
		t.Fatalf("missing CALLS edge bootstrap → cart.add; edges: %+v", r.Edges)
	}
	if got.ReceiverType != "Cart" {
		t.Errorf("ReceiverType=%q, want Cart", got.ReceiverType)
	}
}

// Negative — un-typed local (no `: TypeName` annotation) emits the
// dotted edge but ReceiverType stays empty. The resolver's existing
// #285 receiver-method fallback can still try a best-effort name
// lookup — we don't lose any pre-#1177 behaviour, we just don't gain
// the precise binding.
func TestExtractTypeScript_ReceiverType_UntypedLocalLeavesReceiverEmpty(t *testing.T) {
	src := `export function bootstrap(): void {
		const cart = createCart();
		cart.add(1);
	}
`
	r := Extract([]byte(src), "TypeScript", "src/boot.ts")
	if r == nil {
		t.Fatal("nil result")
	}
	var got *ExtractedEdge
	for i := range r.Edges {
		e := &r.Edges[i]
		if e.Kind == "CALLS" && strings.HasSuffix(e.FromQN, ".bootstrap") && e.ToName == "cart.add" {
			got = e
			break
		}
	}
	if got == nil {
		t.Fatalf("missing CALLS edge bootstrap → cart.add; edges: %+v", r.Edges)
	}
	if got.ReceiverType != "" {
		t.Errorf("ReceiverType=%q, want empty (no type annotation on cart)", got.ReceiverType)
	}
}

// Control — free calls (no receiver chain) still emit bare-name edges
// with ReceiverType=empty. Pre-#1177 shape preserved for everything
// outside the `this.` / `varname.` paths.
func TestExtractTypeScript_ReceiverType_BareCallsUnchanged(t *testing.T) {
	src := `export function bootstrap(): void {
		loadConfig();
		render();
	}
`
	r := Extract([]byte(src), "TypeScript", "src/boot.ts")
	if r == nil {
		t.Fatal("nil result")
	}
	want := map[string]bool{"loadConfig": false, "render": false}
	for _, e := range r.Edges {
		if e.Kind != "CALLS" || !strings.HasSuffix(e.FromQN, ".bootstrap") {
			continue
		}
		if _, ok := want[e.ToName]; !ok {
			continue
		}
		if e.ReceiverType != "" {
			t.Errorf("free call %q ReceiverType=%q, want empty", e.ToName, e.ReceiverType)
		}
		want[e.ToName] = true
	}
	for k, v := range want {
		if !v {
			t.Errorf("missing free-call edge bootstrap → %q", k)
		}
	}
}

// Negative — TS bottom/top types (`void`, `any`, `unknown`, `never`,
// `null`, `undefined`) are NOT classes the resolver can bind against,
// so a parameter `n: any` must NOT register `n` as having type "any"
// — otherwise an `n.X()` call would emit ReceiverType="any" and the
// resolver would chase a phantom Class.
func TestExtractTypeScript_ReceiverType_BottomTypesDropped(t *testing.T) {
	src := `export function handle(n: any, u: unknown, v: void): void {
		n.fire();
	}
`
	r := Extract([]byte(src), "TypeScript", "src/h.ts")
	if r == nil {
		t.Fatal("nil result")
	}
	for _, e := range r.Edges {
		if e.Kind == "CALLS" && e.ToName == "n.fire" {
			if e.ReceiverType != "" {
				t.Errorf("ReceiverType=%q for bottom-typed receiver, want empty", e.ReceiverType)
			}
			return
		}
	}
	t.Errorf("missing CALLS edge handle → n.fire; edges: %+v", r.Edges)
}

// Cross-check — receiver-aware edges keep the regex-tier confidence
// (0.6) for parity with regexCallScan. An accidental promotion to
// AST-tier (1.0) would mislead downstream tools that bias on
// extraction confidence.
func TestExtractTypeScript_ReceiverType_KeepsRegexTierConfidence(t *testing.T) {
	src := `export class S {
		run(): void { this.helper(); }
		helper(): void {}
	}
`
	r := Extract([]byte(src), "TypeScript", "src/s.ts")
	if r == nil {
		t.Fatal("nil result")
	}
	for _, e := range r.Edges {
		if e.Kind != "CALLS" {
			continue
		}
		if e.Confidence != 0.6 {
			t.Errorf("edge %q→%q confidence=%v, want 0.6 (regex-tier)",
				e.FromQN, e.ToName, e.Confidence)
		}
	}
}
