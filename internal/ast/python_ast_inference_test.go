// SPDX-License-Identifier: MIT

package ast

import "testing"

// One-hop local type inference tests (#python-edge-resolution): the
// extractor tracks simple `x = ClassName(...)` assignments at function-body
// and module level and rewrites subsequent `x.method()` calls to the
// resolved class path. Inferred edges carry confidence 0.6 (below the 0.7
// of statically-written call paths) so consumers can distinguish them.

// callEdgesWithConfidence collects CALLS edges whose FromQN matches fromQN,
// returning ToName → Confidence for assertion on both fields.
func callEdgesWithConfidence(edges []ExtractedEdge, fromQN string) map[string]float64 {
	out := map[string]float64{}
	for _, e := range edges {
		if e.Kind == "CALLS" && e.FromQN == fromQN {
			out[e.ToName] = e.Confidence
		}
	}
	return out
}

func TestPyAST_InstanceMethodOneHopInference(t *testing.T) {
	pythonASTOrSkip(t)
	src := []byte(`from app.services.user_service import UserService

def caller():
    svc = UserService()
    return svc.get_user(1)
`)
	r, ok := extractPythonAST(src, "src/app/api.py")
	if !ok {
		t.Fatal("parse failed")
	}
	calls := callEdgesWithConfidence(r.Edges, "src.app.api.caller")
	// The constructor call keeps the static-path confidence.
	if got, ok := calls["app.services.user_service.UserService"]; !ok || got != 0.7 {
		t.Errorf("constructor edge = (%v, %v), want (0.7, present); calls=%v", got, ok, calls)
	}
	// The instance-method call is rewritten through the inferred type, at
	// the lower inferred confidence.
	if got, ok := calls["app.services.user_service.UserService.get_user"]; !ok || got != 0.6 {
		t.Errorf("inferred edge = (%v, %v), want (0.6, present); calls=%v", got, ok, calls)
	}
	// The raw attribute chain must NOT also be emitted.
	if _, ok := calls["svc.get_user"]; ok {
		t.Errorf("raw chain svc.get_user should be replaced by the inferred path; calls=%v", calls)
	}
}

func TestPyAST_ModuleLevelInstanceInference(t *testing.T) {
	pythonASTOrSkip(t)
	// Module-level `shared = UserService()` seeds every function body's
	// type map — functions run after the module body has executed.
	src := []byte(`from app.services.user_service import UserService

shared = UserService()

def caller():
    return shared.ping()
`)
	r, ok := extractPythonAST(src, "src/app/api.py")
	if !ok {
		t.Fatal("parse failed")
	}
	calls := callEdgesWithConfidence(r.Edges, "src.app.api.caller")
	if got, ok := calls["app.services.user_service.UserService.ping"]; !ok || got != 0.6 {
		t.Errorf("module-level inferred edge = (%v, %v), want (0.6, present); calls=%v", got, ok, calls)
	}
}

func TestPyAST_LocalClassInstanceInference(t *testing.T) {
	pythonASTOrSkip(t)
	// A module-level class in the same file resolves through its
	// module-qualified path, not the imports map.
	src := []byte(`class Worker:
    def run(self):
        pass

def caller():
    w = Worker()
    w.run()
`)
	r, ok := extractPythonAST(src, "pkg/jobs.py")
	if !ok {
		t.Fatal("parse failed")
	}
	calls := callEdgesWithConfidence(r.Edges, "pkg.jobs.caller")
	if got, ok := calls["pkg.jobs.Worker.run"]; !ok || got != 0.6 {
		t.Errorf("local-class inferred edge = (%v, %v), want (0.6, present); calls=%v", got, ok, calls)
	}
}

func TestPyAST_LastAssignmentWinsKillsStaleInference(t *testing.T) {
	pythonASTOrSkip(t)
	// Rebinding x to something we can't type must clear the inference —
	// the subsequent call falls back to the raw chain at 0.7, and no
	// stale Worker.huh edge is emitted.
	src := []byte(`class Worker:
    pass

def caller():
    x = Worker()
    x = compute()
    x.huh()
`)
	r, ok := extractPythonAST(src, "m.py")
	if !ok {
		t.Fatal("parse failed")
	}
	calls := callEdgesWithConfidence(r.Edges, "m.caller")
	if _, ok := calls["m.Worker.huh"]; ok {
		t.Errorf("stale inference: x was rebound, m.Worker.huh must not be emitted; calls=%v", calls)
	}
	if got, ok := calls["x.huh"]; !ok || got != 0.7 {
		t.Errorf("fallback raw chain = (%v, %v), want (0.7, present); calls=%v", got, ok, calls)
	}
}

func TestPyAST_InferenceSkipsLowercaseImportedNames(t *testing.T) {
	pythonASTOrSkip(t)
	// `x = get_db()` — imported, but not class-shaped (lowercase). No
	// inference: rewriting x.execute() through get_db would be wrong.
	src := []byte(`from app.deps import get_db

def caller():
    x = get_db()
    x.execute()
`)
	r, ok := extractPythonAST(src, "m.py")
	if !ok {
		t.Fatal("parse failed")
	}
	calls := callEdgesWithConfidence(r.Edges, "m.caller")
	if _, ok := calls["app.deps.get_db.execute"]; ok {
		t.Errorf("lowercase imported callee must not be treated as a class; calls=%v", calls)
	}
	if _, ok := calls["x.execute"]; !ok {
		t.Errorf("expected raw-chain fallback x.execute; calls=%v", calls)
	}
}

func TestPyAST_SrcPrefixedCrossModuleCall(t *testing.T) {
	pythonASTOrSkip(t)
	// src-layout gap, pinned at the extractor level: the file's QNs carry
	// the `src.` path prefix but the import-rewritten to_name does not —
	// bridging the two is the resolver's job (source roots + unique-suffix
	// fallback).
	src := []byte(`from app.util import helper

def run():
    return helper()
`)
	r, ok := extractPythonAST(src, "src/app/main.py")
	if !ok {
		t.Fatal("parse failed")
	}
	calls := callEdgesWithConfidence(r.Edges, "src.app.main.run")
	if got, ok := calls["app.util.helper"]; !ok || got != 0.7 {
		t.Errorf("cross-module call edge = (%v, %v), want (0.7, present); calls=%v", got, ok, calls)
	}
}

func TestPyAST_RelativeImportCall(t *testing.T) {
	pythonASTOrSkip(t)
	// Relative imports keep their leading dots in the to_name; the
	// resolver anchors them against the calling file's path.
	src := []byte(`from .util import helper
from ..common import shared

def run():
    helper()
    shared()
`)
	r, ok := extractPythonAST(src, "src/app/sub/main.py")
	if !ok {
		t.Fatal("parse failed")
	}
	calls := callEdgesWithConfidence(r.Edges, "src.app.sub.main.run")
	if got, ok := calls[".util.helper"]; !ok || got != 0.7 {
		t.Errorf("single-dot relative call edge = (%v, %v), want (0.7, present); calls=%v", got, ok, calls)
	}
	if got, ok := calls["..common.shared"]; !ok || got != 0.7 {
		t.Errorf("double-dot relative call edge = (%v, %v), want (0.7, present); calls=%v", got, ok, calls)
	}
}

func TestPyAST_InferenceInsideMethodBody(t *testing.T) {
	pythonASTOrSkip(t)
	// Inference applies inside methods too, and coexists with the
	// self-rewrite.
	src := []byte(`from app.repo import Repo

class Service:
    def load(self):
        r = Repo()
        r.fetch()
        self.validate()

    def validate(self):
        pass
`)
	r, ok := extractPythonAST(src, "src/app/svc.py")
	if !ok {
		t.Fatal("parse failed")
	}
	calls := callEdgesWithConfidence(r.Edges, "src.app.svc.Service.load")
	if got, ok := calls["app.repo.Repo.fetch"]; !ok || got != 0.6 {
		t.Errorf("method-body inferred edge = (%v, %v), want (0.6, present); calls=%v", got, ok, calls)
	}
	if got, ok := calls["src.app.svc.Service.validate"]; !ok || got != 0.7 {
		t.Errorf("self-rewrite must be unaffected = (%v, %v), want (0.7, present); calls=%v", got, ok, calls)
	}
}
