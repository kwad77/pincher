// SPDX-License-Identifier: MIT

package server

import (
	"testing"
)

// #1665 v0.87: branch-drift advisory must skip testdata corpora.
// Same dogfood-found pattern as #1644 for the nested-project
// advisory — those projects are static testdata, not real working
// trees, and the "switch branch" remediation churns the index
// without fixing anything.
//
// branchDriftSuppressedPath operates on a single path (project's
// own path), unlike isIntentionallyNested which needs an inner +
// outer pair.

func TestBranchDriftSuppressedPath_Matrix_1665(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path string
		want bool
		why  string
	}{
		// Testdata corpora — all expected to suppress.
		{`D:\ClaudeCode\pincher-repo\testdata\corpus\go-project`, true, "Windows testdata/corpus/<name>"},
		{`/home/user/pincher/testdata/corpus/k8s-ops`, true, "Unix testdata/corpus/<name>"},
		{`D:\ClaudeCode\pincher-repo\testdata\__fixtures__\trace`, true, "testdata/__fixtures__"},
		{`/repos/pincher/testdata/__fixtures__/trace`, true, "Unix __fixtures__"},

		// Worktree convention.
		{`D:\proj\.atrium\work\branch-x`, true, ".atrium/work/<branch>"},
		{`/proj/.atrium/work/branch-y`, true, "Unix .atrium/work"},

		// Real projects must NOT suppress.
		{`D:\ClaudeCode\pincher-repo`, false, "main repo root"},
		{`/repos/leaderflow`, false, "Unix unrelated project"},
		{`D:\ClaudeCode\other-project`, false, "Windows unrelated project"},
		{`D:\ClaudeCode\pincher-repo\cmd\pinch`, false, "subdir of main repo, not testdata"},

		// Substring overlaps must NOT match — defensive.
		{`D:\testdata-for-other-thing\proj`, false, "testdata- prefix without /corpus or /__fixtures__"},
		{`D:\corpus_data\foo`, false, "corpus_data is not corpus/"},
		{`/home/atrium/repos/foo`, false, "atrium without /.atrium/work/"},

		// Edge cases.
		{"", false, "empty path"},
		{`testdata/corpus/x`, true, "relative path testdata/corpus/x"},
	}
	for _, c := range cases {
		if got := branchDriftSuppressedPath(c.path); got != c.want {
			t.Errorf("branchDriftSuppressedPath(%q) = %v, want %v (%s)", c.path, got, c.want, c.why)
		}
	}
}
