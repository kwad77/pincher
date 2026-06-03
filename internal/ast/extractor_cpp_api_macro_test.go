// SPDX-License-Identifier: MIT

package ast

import (
	"testing"
)

// #1693 (#1389 cross-language sweep): C++ codebases using a
// `<MODULE>_API` dllexport macro — `class PYRITE_API AMyActor`, the
// Unreal Engine convention — had the macro token captured as the
// class name. Every `class <MODULE>_API X` in a file then collided
// on the macro name (`PYRITE_API appears 6 times`) and `pincher
// doctor` reported qualified_name_collision. classRE now skips a
// leading ALLCAPS-with-underscore export-macro token.
//
// Four-case shape (#1152): positive (real name captured past the
// macro), negative (no-macro classes unaffected), control (an
// ALLCAPS-underscore class name with no second identifier is kept),
// cross-check (struct + base-class clause still parse).

func TestExtractCpp_ApiMacroSkipped_RealNameCaptured_1693(t *testing.T) {
	t.Parallel()
	src := []byte(`#pragma once

class PYRITE_API AMyActor
{
public:
    void BeginPlay();
};

struct PYRITE_API FSeed
{
    int Value;
};
`)
	result := Extract(src, "C++", "Source/Pyrite/MyActor.h")
	if result == nil {
		t.Fatal("Extract returned nil")
	}
	names := map[string]string{} // name -> kind
	for _, s := range result.Symbols {
		if s.Kind == "Class" {
			names[s.Name] = s.Kind
		}
	}
	if _, ok := names["PYRITE_API"]; ok {
		t.Error("export macro PYRITE_API captured as a Class symbol — should be skipped (#1693)")
	}
	for _, want := range []string{"AMyActor", "FSeed"} {
		if names[want] != "Class" {
			t.Errorf("real type %q not captured as a Class — macro skip ate the real name; got symbols %v", want, names)
		}
	}
}

// Negative: a plain `class Foo` with no export macro must still be
// captured — the optional macro group must not disturb the common case.
func TestExtractCpp_NoMacroClassUnaffected_1693(t *testing.T) {
	t.Parallel()
	src := []byte(`class Widget
{
public:
    void Render();
};

struct list_head
{
    struct list_head *next;
};
`)
	result := Extract(src, "C++", "src/widget.h")
	got := map[string]bool{}
	for _, s := range result.Symbols {
		if s.Kind == "Class" {
			got[s.Name] = true
		}
	}
	for _, want := range []string{"Widget", "list_head"} {
		if !got[want] {
			t.Errorf("class %q missing — optional macro group disturbed the no-macro case; got %v", want, got)
		}
	}
}

// Control: an ALLCAPS-with-underscore class NAME with no second
// identifier (no export macro) must be captured as itself, not eaten
// by the macro group. RE2 takes the not-taken branch of the optional
// group when taking it would leave no name token.
func TestExtractCpp_AllCapsClassNameKept_1693(t *testing.T) {
	t.Parallel()
	src := []byte(`class API_REGISTRY
{
public:
    void Register();
};
`)
	result := Extract(src, "C++", "src/registry.h")
	var found bool
	for _, s := range result.Symbols {
		if s.Kind == "Class" && s.Name == "API_REGISTRY" {
			found = true
		}
	}
	if !found {
		t.Error("ALLCAPS-underscore class name API_REGISTRY was eaten by the macro-skip group — " +
			"the optional group must yield when there's no second identifier")
	}
}

// Cross-check: macro + base-class clause. `class PYRITE_API AThing :
// public AActor` must capture AThing as name and AActor as parent.
func TestExtractCpp_ApiMacroWithBaseClass_1693(t *testing.T) {
	t.Parallel()
	src := []byte(`class PYRITE_API AThing : public AActor
{
};
`)
	result := Extract(src, "C++", "src/thing.h")
	var thing ExtractedSymbol
	for _, s := range result.Symbols {
		if s.Kind == "Class" && s.Name == "AThing" {
			thing = s
		}
		if s.Kind == "Class" && s.Name == "PYRITE_API" {
			t.Error("PYRITE_API captured as Class despite the base-class clause (#1693)")
		}
	}
	if thing.Name == "" {
		t.Fatal("AThing class not captured with macro + base-class clause present")
	}
}
