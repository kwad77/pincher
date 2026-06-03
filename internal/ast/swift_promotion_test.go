// SPDX-License-Identifier: MIT

package ast

import (
	"testing"
)

// #1450 v0.73 — Swift extractor promotion from 0.70 → 0.85 (stable
// regex tier, joining TS/TSX/Rust/Java). These tests pin every shape
// the promotion covers. Each test runs on a self-contained Swift
// source snippet representative of the real-world declaration shape.

func TestSwift_StructIsExtractedAsClass(t *testing.T) {
	src := []byte(`
public struct ContentView {
    var counter: Int = 0
    func render() -> String { "view" }
}
`)
	result := Extract(src, "Swift", "Sources/View.swift")
	if result == nil {
		t.Fatal("nil result")
	}
	byName := map[string]ExtractedSymbol{}
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	cv, ok := byName["ContentView"]
	if !ok {
		t.Fatalf("expected struct ContentView; got names %v", swiftKeysOf(byName))
	}
	if cv.Kind != "Class" {
		t.Errorf("ContentView.Kind = %q; want Class (Swift struct treated same as class in the symbol model)", cv.Kind)
	}
	render, ok := byName["render"]
	if !ok {
		t.Fatal("expected struct method render")
	}
	if render.Kind != "Method" {
		t.Errorf("render.Kind = %q; want Method (scoped to struct)", render.Kind)
	}
	if render.Parent == "" {
		t.Error("render.Parent empty; method should be scoped to ContentView")
	}
}

func TestSwift_ActorIsExtractedAsClass(t *testing.T) {
	src := []byte(`
public actor Counter {
    private var value: Int = 0
    func increment() async { value += 1 }
}
`)
	result := Extract(src, "Swift", "Sources/Counter.swift")
	if result == nil {
		t.Fatal("nil result")
	}
	byName := map[string]ExtractedSymbol{}
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	c, ok := byName["Counter"]
	if !ok {
		t.Fatalf("expected actor Counter; got %v", swiftKeysOf(byName))
	}
	if c.Kind != "Class" {
		t.Errorf("Counter.Kind = %q; want Class", c.Kind)
	}
	inc, ok := byName["increment"]
	if !ok {
		t.Fatal("expected actor method increment")
	}
	if inc.Kind != "Method" {
		t.Errorf("increment.Kind = %q; want Method", inc.Kind)
	}
}

func TestSwift_EnumIsExtractedAsEnum(t *testing.T) {
	src := []byte(`
public enum NetworkError {
    case offline
    case timeout(after: TimeInterval)

    func localizedDescription() -> String { "..." }
}
`)
	result := Extract(src, "Swift", "Sources/Errors.swift")
	if result == nil {
		t.Fatal("nil result")
	}
	byName := map[string]ExtractedSymbol{}
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	ne, ok := byName["NetworkError"]
	if !ok {
		t.Fatalf("expected enum NetworkError; got %v", swiftKeysOf(byName))
	}
	if ne.Kind != "Enum" {
		t.Errorf("NetworkError.Kind = %q; want Enum", ne.Kind)
	}
	// The enum's method should still come through as a Method scoped
	// to the enum (the framework's currentClass tracking covers this
	// — enumRE runs alongside classRE inside the same iteration).
	// Pre-#1450 the enum itself wasn't extracted at all.
	if _, ok := byName["localizedDescription"]; !ok {
		t.Error("expected enum method localizedDescription")
	}
}

func TestSwift_IndirectEnum(t *testing.T) {
	src := []byte(`public indirect enum Tree { case leaf, node(Tree, Tree) }`)
	result := Extract(src, "Swift", "x.swift")
	if result == nil {
		t.Fatal("nil result")
	}
	for _, s := range result.Symbols {
		if s.Name == "Tree" && s.Kind == "Enum" {
			return
		}
	}
	t.Error("expected indirect enum Tree as Enum")
}

func TestSwift_InitConstructor(t *testing.T) {
	src := []byte(`
public class Shape {
    init(color: String) {}
    public convenience init() {
        self.init(color: "red")
    }
    required init(decoder: NSCoder) {}
}
`)
	result := Extract(src, "Swift", "Sources/Shape.swift")
	if result == nil {
		t.Fatal("nil result")
	}
	initCount := 0
	for _, s := range result.Symbols {
		if s.Name == "init" {
			initCount++
			if s.Kind != "Method" {
				t.Errorf("init.Kind = %q; want Method (scoped to Shape class)", s.Kind)
			}
		}
	}
	// Three init forms above (plain / convenience / required).
	if initCount != 3 {
		t.Errorf("init count = %d; want 3 (plain + convenience + required)", initCount)
	}
}

func TestSwift_AtAttributePrefixedFunc(t *testing.T) {
	src := []byte(`
@MainActor
public class ViewModel {
    @objc func reload() {}
    @discardableResult func save() -> Bool { false }
    @available(iOS 15, *) func newAPI() {}
}
`)
	result := Extract(src, "Swift", "Sources/VM.swift")
	if result == nil {
		t.Fatal("nil result")
	}
	byName := map[string]ExtractedSymbol{}
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	if _, ok := byName["ViewModel"]; !ok {
		t.Errorf("expected @MainActor class ViewModel; got %v", swiftKeysOf(byName))
	}
	for _, m := range []string{"reload", "save", "newAPI"} {
		if _, ok := byName[m]; !ok {
			t.Errorf("expected @attribute-prefixed method %s", m)
		}
	}
}

func TestSwift_ClassFuncStaticMethod(t *testing.T) {
	// `class func` is Swift's older static-method form, still common
	// in Apple frameworks. Pre-#1450 funcRE only matched `static`.
	src := []byte(`
public class Logger {
    class func shared() -> Logger { Logger() }
    static func reset() {}
}
`)
	result := Extract(src, "Swift", "Sources/Log.swift")
	if result == nil {
		t.Fatal("nil result")
	}
	byName := map[string]ExtractedSymbol{}
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	if _, ok := byName["shared"]; !ok {
		t.Errorf("expected `class func shared`; got names %v", swiftKeysOf(byName))
	}
	if _, ok := byName["reset"]; !ok {
		t.Errorf("expected `static func reset`; got names %v", swiftKeysOf(byName))
	}
}

func TestSwift_MutatingModifier(t *testing.T) {
	src := []byte(`
public struct Counter {
    var value: Int = 0
    public mutating func increment() { value += 1 }
    public nonmutating func describe() -> String { "..." }
}
`)
	result := Extract(src, "Swift", "Sources/Counter.swift")
	if result == nil {
		t.Fatal("nil result")
	}
	byName := map[string]ExtractedSymbol{}
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	for _, m := range []string{"increment", "describe"} {
		s, ok := byName[m]
		if !ok {
			t.Errorf("expected method %s; got names %v", m, swiftKeysOf(byName))
			continue
		}
		if s.Kind != "Method" || s.Parent == "" {
			t.Errorf("%s should be a Method scoped to Counter; got Kind=%q Parent=%q", m, s.Kind, s.Parent)
		}
	}
}

func TestSwift_GenericFunc(t *testing.T) {
	src := []byte(`
public func identity<T>(_ x: T) -> T { x }
public func bigger<T: Comparable>(_ a: T, _ b: T) -> T { a > b ? a : b }
`)
	result := Extract(src, "Swift", "Sources/Generic.swift")
	if result == nil {
		t.Fatal("nil result")
	}
	byName := map[string]ExtractedSymbol{}
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	for _, fn := range []string{"identity", "bigger"} {
		if _, ok := byName[fn]; !ok {
			t.Errorf("expected generic func %s; got names %v", fn, swiftKeysOf(byName))
		}
	}
}

func TestSwift_ExtensionWithWhereClause(t *testing.T) {
	// #1183 v0.67 scopeRE — extension Type. #1450 extends with
	// where-clause tolerance.
	src := []byte(`
public extension Array where Element: Hashable {
    public func uniq() -> [Element] { Array(Set(self)) }
}
`)
	result := Extract(src, "Swift", "Sources/Ext.swift")
	if result == nil {
		t.Fatal("nil result")
	}
	for _, s := range result.Symbols {
		if s.Name == "uniq" {
			if s.Parent == "" {
				t.Error("uniq.Parent empty; extension should scope method to Array")
			}
			return
		}
	}
	t.Error("expected method uniq inside extension")
}

func TestSwift_FilePrivateAccessModifier(t *testing.T) {
	src := []byte(`
fileprivate class Internal {}
fileprivate func helper() {}
`)
	result := Extract(src, "Swift", "Sources/Internal.swift")
	if result == nil {
		t.Fatal("nil result")
	}
	byName := map[string]ExtractedSymbol{}
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	if _, ok := byName["Internal"]; !ok {
		t.Errorf("expected fileprivate class Internal; got %v", swiftKeysOf(byName))
	}
	if _, ok := byName["helper"]; !ok {
		t.Errorf("expected fileprivate func helper; got %v", swiftKeysOf(byName))
	}
}

// Confidence promotion guard: Swift is now registered at 0.85
// (stable-regex tier). Pre-#1450 it was 0.70. The test pins the
// adapter's base confidence so a future registry edit that drops
// Swift back to 0.70 surfaces loudly. (Per-symbol stamped values
// can exceed 0.85 once the #34 signal-composition pass boosts
// individual symbols — the base is what defines the tier.)
func TestSwift_ExtractorConfidenceIs085(t *testing.T) {
	if c := RegisteredConfidence("Swift"); c != 0.85 {
		t.Errorf("Swift registry confidence = %v; want 0.85 (#1450 promotion)", c)
	}
}

// Existing v0.67 behaviour stays — `extension Type` still binds inner
// methods to Type (#1183).
func TestSwift_ExtensionStillScopesToReceiver_Regression(t *testing.T) {
	src := []byte(`
public class Shape {}
extension Shape {
    public func area() -> Double { 0 }
}
`)
	result := Extract(src, "Swift", "Sources/Shape.swift")
	if result == nil {
		t.Fatal("nil result")
	}
	for _, s := range result.Symbols {
		if s.Name == "area" {
			if s.Kind != "Method" {
				t.Errorf("area.Kind = %q; want Method (#1183 regression guard)", s.Kind)
			}
			return
		}
	}
	t.Error("expected method area inside extension")
}

func swiftKeysOf(m map[string]ExtractedSymbol) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
