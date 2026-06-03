// SPDX-License-Identifier: MIT

package ast

import "testing"

// #1753: dartRE.classRE only matched `(abstract)? class`, so a Dart 3
// class carrying any of the modifiers sealed / final / base /
// interface / mixin was never extracted — the type vanished and its
// methods degraded from Method (parented) to orphan Function. These
// tests pin every Dart 3 class-modifier shape.

// TestExtractDart_ClassModifiers_AllExtractAsClass covers each Dart 3
// class modifier (and a plain class control), asserting the type
// surfaces with kind=Class.
func TestExtractDart_ClassModifiers_AllExtractAsClass(t *testing.T) {
	t.Parallel()
	src := []byte(`class Plain {}
abstract class Abstract {}
sealed class Sealed {}
final class Final {}
base class Base {}
interface class Iface {}
mixin class MixinClass {}
abstract base class AbstractBase {}
`)
	r := Extract(src, "Dart", "modifiers.dart")
	if r == nil {
		t.Fatal("nil result")
	}
	kind := map[string]string{}
	for _, s := range r.Symbols {
		kind[s.Name] = s.Kind
	}
	for _, name := range []string{
		"Plain", "Abstract", "Sealed", "Final", "Base",
		"Iface", "MixinClass", "AbstractBase",
	} {
		if kind[name] != "Class" {
			t.Errorf("%q kind = %q, want Class", name, kind[name])
		}
	}
}

// TestExtractDart_SealedClassMethodsParentCorrectly is the headline
// fix: a method inside a `sealed class` must extract as Method with
// the sealed type as its Parent — not as an orphan Function. This is
// the silent-wrong symptom (`trace` / `dead_code` saw orphan funcs).
func TestExtractDart_SealedClassMethodsParentCorrectly(t *testing.T) {
	t.Parallel()
	src := []byte(`sealed class Shape {
  double area() => 0;
}

final class Circle extends Shape {
  double area() => 3.14;
}
`)
	r := Extract(src, "Dart", "shapes.dart")
	if r == nil {
		t.Fatal("nil result")
	}
	var areaMethods int
	for _, s := range r.Symbols {
		if s.Name != "area" {
			continue
		}
		if s.Kind != "Method" {
			t.Errorf("area kind = %q, want Method (a method of a sealed/final class must not orphan as Function)", s.Kind)
		}
		if s.Parent != "shapes.Shape" && s.Parent != "shapes.Circle" {
			t.Errorf("area parent = %q, want shapes.Shape or shapes.Circle", s.Parent)
		}
		areaMethods++
	}
	if areaMethods != 2 {
		t.Errorf("got %d `area` symbols, want 2 (one per class)", areaMethods)
	}
}

// TestExtractDart_InterfaceAndMixinClass_NoDoubleEmission is the
// control: `interface class` / `mixin class` must extract once, as
// Class — not also as an Interface via interfaceRE. interfaceRE's
// name capture requires `[A-Z]…` and the keyword `class` is
// lowercase, so it cannot match these lines.
func TestExtractDart_InterfaceAndMixinClass_NoDoubleEmission(t *testing.T) {
	t.Parallel()
	src := []byte(`interface class Repo {}
mixin class Service {}
mixin PlainMixin {}
`)
	r := Extract(src, "Dart", "kinds.dart")
	count := map[string]int{}
	kind := map[string]string{}
	for _, s := range r.Symbols {
		count[s.Name]++
		kind[s.Name] = s.Kind
	}
	for _, name := range []string{"Repo", "Service"} {
		if count[name] != 1 {
			t.Errorf("%q emitted %d times, want exactly 1 (no class/interface double-emission)", name, count[name])
		}
		if kind[name] != "Class" {
			t.Errorf("%q kind = %q, want Class", name, kind[name])
		}
	}
	// Control: a standalone `mixin` (no `class`) is still an Interface.
	if kind["PlainMixin"] != "Interface" {
		t.Errorf("PlainMixin kind = %q, want Interface (standalone mixin)", kind["PlainMixin"])
	}
}
