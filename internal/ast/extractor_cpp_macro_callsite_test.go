// SPDX-License-Identifier: MIT

package ast

import (
	"strings"
	"testing"
)

// #1204 v0.66 DOGFOOD: cMacroRE pre-fix matched indented SCREAM_CASE
// macro CALL SITES (e.g. UE_LOG inside function bodies) as if they
// were column-0 bare-prefix DECLARATIONS. extractCBareMacros then
// emitted one Symbol per call site, producing N copies of the first
// macro argument's name with identical qualified_name (one per call
// site). On Unreal Engine corpora that mean 4-11 LogTemp symbols per
// file and a qualified_name_collision in `doctor`.

// Positive: indented UE_LOG(LogTemp, ...) inside a function body does
// NOT emit a Symbol — call sites are not declarations. Uses a plain
// (non-scope-qualified) function declaration since the C extractor's
// funcRE doesn't recognize `Bridge::SendCommand` — that's a separate
// pre-existing C++-extractor limitation outside #1204's scope.
func TestExtractC_IndentedMacroCallSite_NoSymbol(t *testing.T) {
	t.Parallel()
	source := []byte(`
void do_stuff(int a) {
    UE_LOG(LogTemp, Warning, TEXT("first"));
    UE_LOG(LogTemp, Warning, TEXT("second"));
    UE_LOG(LogTemp, Error,   TEXT("third"));
}
`)
	result := extractC(source, "Bridge.cpp")
	for _, s := range result.Symbols {
		if s.Name == "LogTemp" {
			t.Errorf("indented UE_LOG call site must not emit a LogTemp Symbol; got %+v", s)
		}
	}
	// Confirm the real declaration (do_stuff) survived as a Function.
	var sawFn bool
	for _, s := range result.Symbols {
		if s.Kind == "Function" && strings.Contains(s.Name, "do_stuff") {
			sawFn = true
		}
	}
	if !sawFn {
		t.Errorf("do_stuff Function symbol should still be extracted; got symbols=%+v", result.Symbols)
	}
}

// Negative-positive: a column-0 bare-prefix declaration macro
// (EXPORT_SYMBOL, DEFINE_LOG_CATEGORY) STILL emits a Symbol named for
// its first argument — this is the documented #74 use case.
func TestExtractC_ColumnZeroBareMacro_StillEmits(t *testing.T) {
	t.Parallel()
	source := []byte(`
EXPORT_SYMBOL(my_kernel_func);
DEFINE_LOG_CATEGORY(LogCategoryRoot);
`)
	result := extractC(source, "module.c")
	wantNames := map[string]bool{"my_kernel_func": false, "LogCategoryRoot": false}
	for _, s := range result.Symbols {
		if _, ok := wantNames[s.Name]; ok {
			wantNames[s.Name] = true
		}
	}
	for n, found := range wantNames {
		if !found {
			t.Errorf("expected column-0 bare-prefix macro %q to be extracted; got symbols=%+v", n, result.Symbols)
		}
	}
}

// Cross-check: rewriteCMacroSymbols path still works — column-0
// `static DEVICE_ATTR(keys, ...)` (matched by funcRE) gets renamed to
// the first arg `keys`, not left as DEVICE_ATTR.
func TestExtractC_StaticDeviceAttrRewrite_StillWorks(t *testing.T) {
	t.Parallel()
	source := []byte(`
static DEVICE_ATTR(keys, 0644, show_keys, store_keys);
static DEVICE_ATTR(values, 0644, show_values, store_values);
`)
	result := extractC(source, "driver.c")
	wantNames := map[string]bool{"keys": false, "values": false}
	for _, s := range result.Symbols {
		if _, ok := wantNames[s.Name]; ok {
			wantNames[s.Name] = true
		}
		if s.Name == "DEVICE_ATTR" {
			t.Errorf("rewriteCMacroSymbols should rename DEVICE_ATTR to its first arg; got %+v", s)
		}
	}
	for n, found := range wantNames {
		if !found {
			t.Errorf("expected DEVICE_ATTR rewrite to produce Symbol named %q; got symbols=%+v", n, result.Symbols)
		}
	}
}

// Cross-check: the qualified_name_collision count for the Unreal
// repro shape (N UE_LOG calls in one file) drops to zero. This is the
// directly-observable acceptance criterion in #1204.
func TestExtractC_UnrealLogTempRepro_NoQNCollision(t *testing.T) {
	t.Parallel()
	source := []byte(`
void EpicUnrealMCPBridge::DoStuff() {
    UE_LOG(LogTemp, Warning, TEXT("a"));
    UE_LOG(LogTemp, Warning, TEXT("b"));
    UE_LOG(LogTemp, Warning, TEXT("c"));
    UE_LOG(LogTemp, Warning, TEXT("d"));
    UE_LOG(LogTemp, Warning, TEXT("e"));
    UE_LOG(LogTemp, Warning, TEXT("f"));
    UE_LOG(LogTemp, Warning, TEXT("g"));
    UE_LOG(LogTemp, Warning, TEXT("h"));
    UE_LOG(LogTemp, Warning, TEXT("i"));
    UE_LOG(LogTemp, Warning, TEXT("j"));
    UE_LOG(LogTemp, Warning, TEXT("k"));
}
`)
	result := extractC(source, "EpicUnrealMCPBridge.cpp")
	if n, dup := result.QNCollisions["EpicUnrealMCPBridge::LogTemp"]; dup {
		t.Errorf("expected zero LogTemp QN collisions; got %d", n)
	}
	logTempCount := 0
	for _, s := range result.Symbols {
		if s.Name == "LogTemp" {
			logTempCount++
		}
	}
	if logTempCount > 0 {
		t.Errorf("expected zero LogTemp Symbols (call sites are not declarations); got %d", logTempCount)
	}
}
