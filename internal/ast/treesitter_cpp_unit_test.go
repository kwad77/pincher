// SPDX-License-Identifier: MIT

package ast

import (
	"context"
	"testing"
)

func TestTreeSitterCpp_InlineExtract(t *testing.T) {
	const src = `#include <vector>
#include "engine.h"

namespace app {

class Service : public Base {
public:
    Service();
    int run(int n) { return load(n); }
};

struct Point { int x; int y; };

enum class Status { Active, Inactive };

}

int Service::compute(int x) {
    helper();
    return x * 2;
}

void freefn() {
    obj.go();
    run(1);
}
`
	x, err := newCppTSExtractor(context.Background())
	if err != nil {
		t.Fatalf("newCppTSExtractor: %v", err)
	}
	fr, ok := x.extractChecked([]byte(src), "src/Service.cpp")
	if !ok {
		t.Fatal("C++ parse hit an ERROR node on valid source")
	}

	byKind := map[string]map[string]bool{}
	qn := map[string]string{}
	parent := map[string]string{}
	for _, s := range fr.Symbols {
		if byKind[s.Kind] == nil {
			byKind[s.Kind] = map[string]bool{}
		}
		byKind[s.Kind][s.Name] = true
		qn[s.Name] = s.QualifiedName
		parent[s.Name] = s.Parent
		if s.ExtractionConfidence != 1.0 {
			t.Errorf("symbol %s: confidence %v, want 1.0", s.Name, s.ExtractionConfidence)
		}
	}
	for name, kind := range map[string]string{
		"Service": "Class", "Point": "Class", "Status": "Enum", "freefn": "Function",
	} {
		if !byKind[kind][name] {
			t.Errorf("missing %s %q; got %v", kind, name, byKind[kind])
		}
	}
	// in-class method (with return type) + out-of-line method.
	for _, m := range []string{"run", "compute"} {
		if !byKind["Method"][m] {
			t.Errorf("missing Method %q; got %v", m, byKind["Method"])
		}
	}
	// constructor `Service()` (no return type) must NOT be a Method shadowing
	// the class.
	if byKind["Method"]["Service"] {
		t.Error("constructor Service emitted as Method (should be skipped)")
	}
	// namespace-blind QN: Service keyed on moduleQN, not app::Service.
	if qn["Service"] != "src.Service.Service" && qn["Service"] != "src::Service::Service" {
		t.Errorf("Service QN = %q, want moduleQN-based src::Service::Service", qn["Service"])
	}
	// out-of-line `Service::compute` binds to its class via the qualifier.
	if parent["compute"] == "" {
		t.Errorf("out-of-line method compute has empty Parent; want the Service class QN")
	}

	imports := map[string]bool{}
	calls := map[string]bool{}
	for _, e := range fr.Edges {
		switch e.Kind {
		case "IMPORTS":
			imports[e.ToName] = true
		case "CALLS":
			calls[e.ToName] = true
		}
	}
	for _, want := range []string{"vector", "engine.h"} {
		if !imports[want] {
			t.Errorf("#include %q missing from IMPORTS; got %v", want, imports)
		}
	}
	for _, want := range []string{"helper", "go", "run"} {
		if !calls[want] {
			t.Errorf("CALLS edge to %q missing; got %v", want, calls)
		}
	}
}
