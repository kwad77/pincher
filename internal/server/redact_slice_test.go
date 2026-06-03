// SPDX-License-Identifier: MIT

package server

import (
	"testing"
)

// #607: redactSensitiveArgs recurses into []any via redactSensitiveSlice.
// The slice path was 100% uncovered pre-#607 even though it's reachable
// from every tool response via maybeRecordSlowQuery → jsonResultWithMeta /
// textResultWithMeta. A bug in the recursion would silently leak
// credentials into the slow_queries.args_json column.
//
// These tests cover the three slice-recursion shapes:
//  1. slice of maps containing sensitive keys
//  2. slice of slices of maps (deeper recursion)
//  3. mixed slice with non-map values that should pass through unchanged

func TestRedactSensitiveArgs_SliceOfMaps(t *testing.T) {
	t.Parallel()
	in := map[string]any{
		"items": []any{
			map[string]any{"name": "alice", "password": "hunter2"},
			map[string]any{"name": "bob", "token": "abc123"},
		},
	}
	out := redactSensitiveArgs(in)

	items, ok := out["items"].([]any)
	if !ok {
		t.Fatalf("items not []any after redaction: %T", out["items"])
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(items))
	}

	first, _ := items[0].(map[string]any)
	if first["password"] != "[redacted]" {
		t.Errorf("slice-of-maps password not redacted: %v", first["password"])
	}
	if first["name"] != "alice" {
		t.Errorf("non-sensitive name was clobbered: %v", first["name"])
	}

	second, _ := items[1].(map[string]any)
	if second["token"] != "[redacted]" {
		t.Errorf("slice-of-maps token not redacted: %v", second["token"])
	}
}

func TestRedactSensitiveArgs_NestedSliceOfSliceOfMaps(t *testing.T) {
	t.Parallel()
	in := map[string]any{
		"deeply": []any{
			[]any{
				map[string]any{"secret": "shh"},
				map[string]any{"normal": "ok"},
			},
			[]any{
				map[string]any{"authorization": "Bearer xxx"},
			},
		},
	}
	out := redactSensitiveArgs(in)

	outer, _ := out["deeply"].([]any)
	if len(outer) != 2 {
		t.Fatalf("outer len %d, want 2", len(outer))
	}

	inner0, _ := outer[0].([]any)
	first, _ := inner0[0].(map[string]any)
	if first["secret"] != "[redacted]" {
		t.Errorf("nested slice-of-slice secret not redacted: %v", first)
	}
	second, _ := inner0[1].(map[string]any)
	if second["normal"] != "ok" {
		t.Errorf("non-sensitive sibling clobbered: %v", second)
	}

	inner1, _ := outer[1].([]any)
	third, _ := inner1[0].(map[string]any)
	if third["authorization"] != "[redacted]" {
		t.Errorf("nested slice-of-slice authorization not redacted: %v", third)
	}
}

func TestRedactSensitiveArgs_SliceWithMixedValues(t *testing.T) {
	t.Parallel()
	// Slice contains scalars + a single map. Scalars must pass through;
	// the map's sensitive key must redact. Pre-#607 the slice traversal
	// was untested — this asserts both branches of the type-switch.
	in := map[string]any{
		"mixed": []any{
			"plain string",
			42,
			true,
			map[string]any{"api_key": "leaked"},
			nil,
		},
	}
	out := redactSensitiveArgs(in)
	mixed, _ := out["mixed"].([]any)
	if len(mixed) != 5 {
		t.Fatalf("mixed len %d, want 5", len(mixed))
	}
	if mixed[0] != "plain string" || mixed[1] != 42 || mixed[2] != true {
		t.Errorf("scalar passthrough broken: %v", mixed[:3])
	}
	m, _ := mixed[3].(map[string]any)
	if m["api_key"] != "[redacted]" {
		t.Errorf("map inside mixed slice not redacted: %v", m)
	}
	if mixed[4] != nil {
		t.Errorf("nil passthrough broken: %v", mixed[4])
	}
}

// Sanity: redactSensitiveArgs returns nil when the input is nil — this
// prevents the slow-query writer from dereferencing nil. Existed pre-#607
// but pinning it here so the helper's contract is documented in tests.
func TestRedactSensitiveArgs_NilInput(t *testing.T) {
	t.Parallel()
	if got := redactSensitiveArgs(nil); got != nil {
		t.Errorf("nil input should return nil; got %v", got)
	}
}
