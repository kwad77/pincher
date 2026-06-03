// SPDX-License-Identifier: MIT

package index

import "testing"

// TestIsDefaultTraceKinds verifies the gate that decides whether the
// closure-table fast-path is safe to take for a given caller's edge-kind
// filter (#652 phase 1). Closure rows don't store per-hop edge kind, so
// only the canonical default trace set is fast-pathable. Anything else
// must fall through to the recursive-CTE path which respects the kind
// filter.
func TestIsDefaultTraceKinds(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want bool
	}{
		{"default in canonical order", []string{"CALLS", "HTTP_CALLS", "ASYNC_CALLS"}, true},
		{"default in different order", []string{"HTTP_CALLS", "ASYNC_CALLS", "CALLS"}, true},
		{"empty slice", nil, false},
		{"single CALLS", []string{"CALLS"}, false},
		{"two of three", []string{"CALLS", "HTTP_CALLS"}, false},
		{"superset", []string{"CALLS", "HTTP_CALLS", "ASYNC_CALLS", "READS"}, false},
		{"unrelated set", []string{"READS", "WRITES", "IMPORTS"}, false},
		{"three but wrong one", []string{"CALLS", "HTTP_CALLS", "READS"}, false},
		{"case-sensitive — lowercase rejected", []string{"calls", "http_calls", "async_calls"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isDefaultTraceKinds(tc.in)
			if got != tc.want {
				t.Errorf("isDefaultTraceKinds(%v) = %v; want %v", tc.in, got, tc.want)
			}
		})
	}
}
