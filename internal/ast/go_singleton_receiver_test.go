// SPDX-License-Identifier: MIT

package ast

import "testing"

// #1747: method calls through a package-level singleton var
// (`var pyRE = &regexExtractor{}`; `pyRE.extract(...)`) must carry the
// var's type as ExtractedEdge.ReceiverType so the resolver's #423
// receiver-type binding can resolve them. Before this fix the edge
// carried only the enclosing method's receiver type (empty for a free
// function), so calls through package-global singletons dropped — and
// a type only ever instantiated as a singleton looked 100% dead.

// receiverTypeOf returns the ReceiverType of the first CALLS edge
// matching fromQN→toName, or a sentinel if no such edge exists.
func receiverTypeOf(edges []ExtractedEdge, fromQN, toName string) (string, bool) {
	for _, e := range edges {
		if e.Kind == "CALLS" && e.FromQN == fromQN && e.ToName == toName {
			return e.ReceiverType, true
		}
	}
	return "", false
}

// Positive: pointer singleton via `&T{...}` → ReceiverType "*Worker".
func TestExtractGoCalls_PtrSingletonReceiverType(t *testing.T) {
	t.Parallel()
	src := `package p
type Worker struct{}
func (w *Worker) Do() {}
var inst = &Worker{}
func run() { inst.Do() }`
	r := Extract([]byte(src), "Go", "p/p.go")
	if r == nil {
		t.Fatal("nil result")
	}
	got, ok := receiverTypeOf(r.Edges, "p.run", "inst.Do")
	if !ok {
		t.Fatal("no CALLS edge p.run → inst.Do")
	}
	if got != "*Worker" {
		t.Errorf("ReceiverType = %q, want *Worker", got)
	}
}

// Positive: value-composite singleton `T{...}` → ReceiverType "Config".
func TestExtractGoCalls_ValueSingletonReceiverType(t *testing.T) {
	t.Parallel()
	src := `package p
type Config struct{}
func (c Config) Load() {}
var cfg = Config{}
func f() { cfg.Load() }`
	r := Extract([]byte(src), "Go", "p/p.go")
	got, ok := receiverTypeOf(r.Edges, "p.f", "cfg.Load")
	if !ok {
		t.Fatal("no CALLS edge p.f → cfg.Load")
	}
	if got != "Config" {
		t.Errorf("ReceiverType = %q, want Config", got)
	}
}

// Positive: `new(T)` singleton → ReceiverType "*Engine".
func TestExtractGoCalls_NewSingletonReceiverType(t *testing.T) {
	t.Parallel()
	src := `package p
type Engine struct{}
func (e *Engine) Start() {}
var eng = new(Engine)
func g() { eng.Start() }`
	r := Extract([]byte(src), "Go", "p/p.go")
	got, ok := receiverTypeOf(r.Edges, "p.g", "eng.Start")
	if !ok {
		t.Fatal("no CALLS edge p.g → eng.Start")
	}
	if got != "*Engine" {
		t.Errorf("ReceiverType = %q, want *Engine", got)
	}
}

// Positive: explicit type, no initializer — `var x *Engine`.
func TestExtractGoCalls_ExplicitTypeSingletonReceiverType(t *testing.T) {
	t.Parallel()
	src := `package p
type Engine struct{}
func (e *Engine) Stop() {}
var x *Engine
func h() { x.Stop() }`
	r := Extract([]byte(src), "Go", "p/p.go")
	got, ok := receiverTypeOf(r.Edges, "p.h", "x.Stop")
	if !ok {
		t.Fatal("no CALLS edge p.h → x.Stop")
	}
	if got != "*Engine" {
		t.Errorf("ReceiverType = %q, want *Engine", got)
	}
}

// Control: a param shadowing the package var name must NOT pick up the
// package-var type — the param is a different binding. The enclosing
// function is free (no receiver), so ReceiverType stays empty.
func TestExtractGoCalls_ParamShadowsSingleton(t *testing.T) {
	t.Parallel()
	src := `package p
type Worker struct{}
func (w *Worker) Do() {}
type Other struct{}
func (o *Other) Do() {}
var inst = &Worker{}
func run(inst *Other) { inst.Do() }`
	r := Extract([]byte(src), "Go", "p/p.go")
	got, ok := receiverTypeOf(r.Edges, "p.run", "inst.Do")
	if !ok {
		t.Fatal("no CALLS edge p.run → inst.Do")
	}
	if got == "*Worker" {
		t.Errorf("ReceiverType = %q — a param shadowing the package var "+
			"must not inherit the singleton's type", got)
	}
}

// Control: a qualified-type singleton (`&bytes.Buffer{}`) is not a
// same-package type — no ReceiverType stamp (the resolver can't map a
// foreign package without import-graph awareness anyway).
func TestExtractGoCalls_QualifiedTypeSingletonNotStamped(t *testing.T) {
	t.Parallel()
	src := `package p
import "bytes"
var buf = &bytes.Buffer{}
func f() { buf.Write(nil) }`
	r := Extract([]byte(src), "Go", "p/p.go")
	got, ok := receiverTypeOf(r.Edges, "p.f", "buf.Write")
	if !ok {
		t.Fatal("no CALLS edge p.f → buf.Write")
	}
	if got != "" {
		t.Errorf("ReceiverType = %q, want empty (qualified type not same-package)", got)
	}
}

// Control: a 3-segment `recv.field.method` call keeps the enclosing
// method's receiver type — resolveByReceiverType case-3 needs it to
// look the field type up. The #1747 stamp only acts on 2-segment
// `IDENT.method` callees.
func TestExtractGoCalls_FieldChainKeepsEnclosingReceiver(t *testing.T) {
	t.Parallel()
	src := `package p
type Dep struct{}
func (d *Dep) Run() {}
type Host struct{ dep *Dep }
func (h *Host) Go() { h.dep.Run() }`
	r := Extract([]byte(src), "Go", "p/p.go")
	got, ok := receiverTypeOf(r.Edges, "p.*Host.Go", "h.dep.Run")
	if !ok {
		t.Fatal("no CALLS edge p.*Host.Go → h.dep.Run")
	}
	if got != "*Host" {
		t.Errorf("ReceiverType = %q, want *Host (field-chain keeps enclosing receiver)", got)
	}
}
