// SPDX-License-Identifier: MIT

package server

import "testing"

// #275: developer-scratch files at project root must be filtered out
// of architecture's entry_points list. Files nested under any
// directory (e.g. testdata/corpus/foo/scratch.go) are kept visible
// because they're typically real test fixtures.
func TestIsDeveloperScratchPath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		path string
		want bool
	}{
		// At project root → filtered
		{"scratch_lang.go", "scratch_lang.go", true},
		{"scratch_idx.go", "scratch_idx.go", true},
		{".scratch_lang_test.go", ".scratch_lang_test.go", true},
		{"tmp_repro.go", "tmp_repro.go", true},
		{"_scratch.go", "_scratch.go", true},
		{"scratch.go", "scratch.go", true},
		{".scratch.go", ".scratch.go", true},
		// Real entry points → kept
		{"main.go", "main.go", false},
		{"cmd/pinch/main.go", "cmd/pinch/main.go", false},
		// Nested scratch → kept (likely test fixture)
		{"testdata/corpus/foo/scratch.go", "testdata/corpus/foo/scratch.go", false},
		{"internal/scratch_pad.go", "internal/scratch_pad.go", false},
		// Unrelated names at root → kept (we only filter known scratch shapes)
		{"playground.go", "playground.go", false},
		{"notes.go", "notes.go", false},
		// Backslash separator (Windows path)
		{"windows scratch", `dir\scratch.go`, false}, // nested → kept
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isDeveloperScratchPath(c.path); got != c.want {
				t.Errorf("isDeveloperScratchPath(%q) = %v, want %v", c.path, got, c.want)
			}
		})
	}
}
