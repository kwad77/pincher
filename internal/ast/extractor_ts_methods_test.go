// SPDX-License-Identifier: MIT

package ast

import (
	"strings"
	"testing"
)

// #1158 (v0.61): TS class methods extract as Method symbols with
// Parent = enclosing class. Pre-fix, the TS extractor emitted Class
// symbols but the methods inside were invisible — `class Cart { add()
// {} }` produced only `Class:Cart`, no `Method:Cart.add`. Foundation
// of the TS receiver-type stack: without Method symbols there is
// nothing for the resolver to bind `X.method` calls to in later
// releases.
//
// Tests follow the table-from-the-start shape (#1152): positive
// (methods extract), negative (control-flow keywords filtered),
// control (real free-function-not-in-class behaviour preserved),
// and cross-check (Parent points at the class QN, not bare name).

const tsClassMethodSrc = `export class Cart {
	add(item: Item): void {
		this.items.push(item);
	}

	total(): number {
		return this.items.reduce((s, i) => s + i.price, 0);
	}

	private validate(): boolean {
		return this.items.length > 0;
	}

	static fromJSON(json: string): Cart {
		return new Cart();
	}

	async submit(): Promise<void> {
		await this.api.post(this);
	}
}
`

// Positive: every class method becomes a Method symbol with
// Parent = "<module>.Cart". Five methods in the fixture; all must
// land.
func TestExtractTypeScript_ClassMethods_AllExtracted(t *testing.T) {
	r := Extract([]byte(tsClassMethodSrc), "TypeScript", "src/cart.ts")
	if r == nil {
		t.Fatal("nil result")
	}
	wantMethods := map[string]bool{
		"add":      false,
		"total":    false,
		"validate": false,
		"fromJSON": false,
		"submit":   false,
	}
	for _, s := range r.Symbols {
		if s.Kind != "Method" {
			continue
		}
		if _, expected := wantMethods[s.Name]; expected {
			wantMethods[s.Name] = true
		}
	}
	for name, found := range wantMethods {
		if !found {
			t.Errorf("class method %q not extracted as Method symbol", name)
		}
	}
}

// Positive: Method.Parent points at the class's qualified name so
// the resolver can later bind X.method calls. The exact Parent format
// is `<module-qn>.Cart` per the regex extractor's moduleQN pattern.
func TestExtractTypeScript_ClassMethods_ParentIsClassQN(t *testing.T) {
	r := Extract([]byte(tsClassMethodSrc), "TypeScript", "src/cart.ts")
	if r == nil {
		t.Fatal("nil result")
	}
	for _, s := range r.Symbols {
		if s.Kind != "Method" || s.Name != "add" {
			continue
		}
		if !strings.HasSuffix(s.Parent, ".Cart") {
			t.Errorf("Method add Parent = %q; want a value ending in .Cart (the enclosing class QN)", s.Parent)
		}
		if s.QualifiedName != s.Parent+".add" {
			t.Errorf("Method add QualifiedName = %q; want %q",
				s.QualifiedName, s.Parent+".add")
		}
		return
	}
	t.Error("Method add not found")
}

// Negative (control): the keyword filter drops control-flow false
// positives. Constructing a class body that uses `if`, `for`,
// `while`, `return` at line start verifies the filter catches the
// methodRE's regex-shape match for those keywords.
const tsControlFlowSrc = `export class Worker {
	if (true) { console.log("not a method"); }
	work(): void {
		for (let i = 0; i < 10; i++) {
			while (i < 5) { break; }
			if (i === 0) { return; }
		}
	}
}
`

func TestExtractTypeScript_ClassMethods_FiltersControlFlowKeywords(t *testing.T) {
	r := Extract([]byte(tsControlFlowSrc), "TypeScript", "src/worker.ts")
	if r == nil {
		t.Fatal("nil result")
	}
	for _, s := range r.Symbols {
		if s.Kind != "Method" && s.Kind != "Function" {
			continue
		}
		switch s.Name {
		case "if", "for", "while", "return", "break", "continue", "switch":
			t.Errorf("control-flow keyword %q emitted as %s symbol — should have been filtered", s.Name, s.Kind)
		}
	}
	// And: `work` must still extract — the filter is keyword-only.
	hasWork := false
	for _, s := range r.Symbols {
		if s.Kind == "Method" && s.Name == "work" {
			hasWork = true
		}
	}
	if !hasWork {
		t.Error("Method work not extracted; the keyword filter must not drop real methods")
	}
}

// Negative (control): free functions outside any class are still
// emitted as Function (not Method). The pre-#1158 behaviour for
// top-level `function` declarations must not regress.
const tsFreeFuncSrc = `export function greet(name: string): string {
	return "hello, " + name;
}

export const add = (a: number, b: number): number => a + b;
`

func TestExtractTypeScript_FreeFunctions_StillFunctionNotMethod(t *testing.T) {
	r := Extract([]byte(tsFreeFuncSrc), "TypeScript", "src/util.ts")
	if r == nil {
		t.Fatal("nil result")
	}
	hits := map[string]string{"greet": "", "add": ""}
	for _, s := range r.Symbols {
		if _, expected := hits[s.Name]; expected {
			hits[s.Name] = s.Kind
		}
	}
	for name, kind := range hits {
		if kind != "Function" {
			t.Errorf("free function %q extracted as %q; want Function", name, kind)
		}
	}
}

// Cross-check: dropTSKeywordFalsePositives operates on the symbol
// slice directly. Hand-built input exercises the Function+Method
// drop and the order-preservation, decoupling from the full
// extractor pipeline so a future pipeline change can't accidentally
// silence this filter.
func TestDropTSKeywordFalsePositives_Direct(t *testing.T) {
	in := []ExtractedSymbol{
		{Name: "realMethod", Kind: "Method", QualifiedName: "m.C.realMethod"},
		{Name: "if", Kind: "Method", QualifiedName: "m.C.if"},
		{Name: "for", Kind: "Function", QualifiedName: "m.for"},
		// Class with keyword-shaped name (unusual but parseable) — survives.
		{Name: "if", Kind: "Class", QualifiedName: "m.if"},
		{Name: "anotherReal", Kind: "Function", QualifiedName: "m.anotherReal"},
	}
	out := dropTSKeywordFalsePositives(in)
	wantNames := []string{"realMethod", "if", "anotherReal"}
	if len(out) != len(wantNames) {
		t.Fatalf("len(out) = %d, want %d; got names: %v",
			len(out), len(wantNames), tsNamesOf(out))
	}
	for i, s := range out {
		if s.Name != wantNames[i] {
			t.Errorf("out[%d].Name = %q, want %q", i, s.Name, wantNames[i])
		}
	}
	// Sanity: the Class survived.
	hasClass := false
	for _, s := range out {
		if s.Kind == "Class" && s.Name == "if" {
			hasClass = true
		}
	}
	if !hasClass {
		t.Error("Class kind with keyword-shaped name dropped — filter must be Function/Method-only")
	}
}

func tsNamesOf(syms []ExtractedSymbol) []string {
	out := make([]string, 0, len(syms))
	for _, s := range syms {
		out = append(out, s.Name)
	}
	return out
}
