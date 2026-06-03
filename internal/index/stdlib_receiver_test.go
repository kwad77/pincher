// SPDX-License-Identifier: MIT

package index

import (
	"testing"
)

// #410: pin the stdlib-receiver stoplist. The bug repro case was
// `extractTextFromHTML` calling `strings.Index(...)` which the
// receiver-method fallback (#285) bound to `*Indexer.Index` because
// only one Method named `Index` exists in pincher-repo.
func TestIsStdlibReceiver(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		// Stdlib package names — should match.
		{"strings", true},
		{"bytes", true},
		{"time", true},
		{"os", true},
		{"fmt", true},
		{"io", true},
		{"errors", true},
		{"context", true},
		{"sync", true},
		{"http", true},
		{"slog", true},
		{"strconv", true},
		// In-project receiver-style names — should NOT match.
		{"idx", false},
		{"s", false},
		{"srv", false},
		{"db", false},
		{"store", false},
		{"projectStore", false},
		{"req", false},
		// Empty + edge cases.
		{"", false},
		{"strings.Builder", false}, // dotted — only the leaf is checked elsewhere
		{"STRINGS", false},         // case-sensitive intentionally
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isStdlibReceiver(c.name); got != c.want {
				t.Errorf("isStdlibReceiver(%q) = %v, want %v", c.name, got, c.want)
			}
		})
	}
}
