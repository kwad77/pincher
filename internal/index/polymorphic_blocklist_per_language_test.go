// SPDX-License-Identifier: MIT

package index

import "testing"

// #1158 v0.61: polymorphic-method-name blocklist generalized from
// Go-only `isPolymorphicInterfaceMethodName` to a per-language map
// `polymorphicMethodNamesByLanguage`. New `isPolymorphicMethodName(name,
// lang)` dispatches; the legacy Go-only function is preserved as a
// thin wrapper for backward-compat with the existing Go resolver call
// sites (indexer.go:2278, :2687).
//
// Tests follow the four-case shape (#1152): positive (Go entries
// preserved), positive (TS/Python entries land), negative (unknown
// language doesn't filter), and cross-check (legacy wrapper matches
// the language-aware dispatch for "Go").

// Positive: every name from pre-v0.61 isPolymorphicInterfaceMethodName
// still trips the new isPolymorphicMethodName("Go") path. Regression
// guard — generalization must not drop any of the v0.20+ entries.
func TestPolymorphicMethodName_GoSetPreserved(t *testing.T) {
	goNames := []string{
		// fmt.Stringer / error
		"String", "Error", "GoString",
		// io
		"Read", "Write", "Close", "Seek",
		"ReadAt", "WriteAt", "ReadFrom", "WriteTo",
		"ReadByte", "WriteByte", "ReadString", "WriteString",
		"ReadRune", "WriteRune", "UnreadByte", "UnreadRune",
		// sync
		"Lock", "Unlock", "RLock", "RUnlock", "TryLock",
		// sort.Interface
		"Len", "Less", "Swap",
		// http
		"ServeHTTP",
		// encoding
		"MarshalJSON", "UnmarshalJSON", "MarshalText", "UnmarshalText",
		"MarshalBinary", "UnmarshalBinary", "MarshalYAML", "UnmarshalYAML",
		// fmt
		"Format", "Scan",
		// time
		"Now", "Add", "Sub", "Before", "After", "Equal",
		// context
		"Deadline", "Done", "Err", "Value",
		// errors
		"Is", "As", "Unwrap",
		// #567
		"Run",
	}
	for _, name := range goNames {
		if !isPolymorphicMethodName(name, "Go") {
			t.Errorf("isPolymorphicMethodName(%q, %q) = false; want true (regression — Go entry dropped)",
				name, "Go")
		}
		// And the legacy wrapper must agree.
		if !isPolymorphicInterfaceMethodName(name) {
			t.Errorf("isPolymorphicInterfaceMethodName(%q) = false; want true (legacy wrapper dropped %q)",
				name, name)
		}
	}
}

// Positive: TS entries land per v0.61 generalization. Pre-v0.61
// these names had no filter — the future TS receiver-type resolver
// would over-bind `toString`/`then`/`catch`/etc. without this list.
func TestPolymorphicMethodName_TypeScriptEntriesPresent(t *testing.T) {
	tsNames := []string{
		// Object.prototype
		"toString", "valueOf", "hasOwnProperty",
		// Promise
		"then", "catch", "finally",
		// Iterator
		"next",
		// Map/Set/Array
		"size", "clear", "has", "get", "set", "add",
		"forEach", "map", "filter",
		// EventTarget
		"addEventListener", "removeEventListener", "emit", "on", "off",
		// Lifecycle
		"render", "constructor", "destroy",
	}
	for _, name := range tsNames {
		if !isPolymorphicMethodName(name, "TypeScript") {
			t.Errorf("isPolymorphicMethodName(%q, %q) = false; want true",
				name, "TypeScript")
		}
	}
}

// Positive: Python dunders land per v0.61 generalization. The
// v0.62 Python AST resolver will consume this list to avoid binding
// every print(x) call to the single user-defined __str__.
func TestPolymorphicMethodName_PythonEntriesPresent(t *testing.T) {
	pyNames := []string{
		"__init__", "__str__", "__repr__", "__hash__",
		"__eq__", "__lt__",
		"__getitem__", "__setitem__", "__iter__", "__next__",
		"__enter__", "__exit__",
		"__call__",
		"close", "read", "write",
	}
	for _, name := range pyNames {
		if !isPolymorphicMethodName(name, "Python") {
			t.Errorf("isPolymorphicMethodName(%q, %q) = false; want true",
				name, "Python")
		}
	}
}

// Negative: unknown language doesn't filter anything. A future
// extractor that lands without an entry in the map gets the
// pre-v0.61 "no filter" behaviour — fail open, not closed.
func TestPolymorphicMethodName_UnknownLanguageNoFilter(t *testing.T) {
	for _, name := range []string{"String", "toString", "__init__", "foo", "bar"} {
		if isPolymorphicMethodName(name, "Cobol") {
			t.Errorf("isPolymorphicMethodName(%q, %q) = true; want false (unknown lang must fail open)",
				name, "Cobol")
		}
	}
}

// Negative: TS-only names don't trip Go path (Go resolver must
// continue to bind `.then()` if it appeared in Go code, because Go
// doesn't have promise semantics).
func TestPolymorphicMethodName_LanguagesAreIsolated(t *testing.T) {
	// `then` is a TS entry only — Go must not consider it polymorphic.
	if isPolymorphicMethodName("then", "Go") {
		t.Error("isPolymorphicMethodName(\"then\", \"Go\") = true; want false (TS-only name leaked into Go set)")
	}
	// `__init__` is a Python entry only — Go must not consider it polymorphic.
	if isPolymorphicMethodName("__init__", "Go") {
		t.Error("isPolymorphicMethodName(\"__init__\", \"Go\") = true; want false")
	}
	// Conversely, `Run` is a Go-only entry that should NOT leak into TS
	// (TS could legitimately define a `run()` method without it being
	// the Go-stdlib false-positive case).
	if isPolymorphicMethodName("Run", "TypeScript") {
		t.Error("isPolymorphicMethodName(\"Run\", \"TypeScript\") = true; want false (Go-only entry leaked into TS set)")
	}
}

// Cross-check: legacy wrapper isPolymorphicInterfaceMethodName MUST
// match isPolymorphicMethodName(name, "Go") byte-for-byte across a
// representative sample, including names NOT in either set. Pins
// the backward-compat contract for the Go resolver call sites.
func TestPolymorphicMethodName_LegacyWrapperParity(t *testing.T) {
	cases := []string{
		"String", "Error", "Read", "Lock", "Len", "Run",
		"foo", "bar", "MyMethod", "process",
		"then", "toString", "__init__",
	}
	for _, name := range cases {
		legacy := isPolymorphicInterfaceMethodName(name)
		modern := isPolymorphicMethodName(name, "Go")
		if legacy != modern {
			t.Errorf("legacy/modern divergence on %q: legacy=%v, modern=%v", name, legacy, modern)
		}
	}
}
