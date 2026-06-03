// SPDX-License-Identifier: MIT

package server

import "testing"

// #1213 v0.66 DOGFOOD: isTestFile now recognizes bash test
// conventions (`_test.sh` suffix and `test_*.sh` prefix). Pincher-
// repo itself ships `scripts/release-channel_test.sh` and
// `scripts/pr-issue-consistency_test.sh`; pre-fix they slipped
// past dead_code and architecture's test filter, polluting
// production-code views.
//
// Table-from-the-start (#1152):
//   - Positive: `_test.sh` suffix flagged.
//   - Positive: `test_*.sh` prefix flagged.
//   - Negative: a non-test bash script (no _test or test_) is NOT
//     flagged.
//   - Cross-check: the convention matches Go (`_test.go`) and
//     Python (`_test.py` / `test_*.py`) — no regression on those.

func TestIsTestFile_BashTestSuffix(t *testing.T) {
	cases := []struct {
		path string
		want bool
		why  string
	}{
		{"scripts/release-channel_test.sh", true, "_test.sh suffix"},
		{"scripts/pr-issue-consistency_test.sh", true, "_test.sh suffix"},
		{"scripts/test_helpers.sh", true, "test_*.sh prefix"},
		{"scripts/test_runner.sh", true, "test_*.sh prefix"},
		// Negative: bash scripts without test convention.
		{"scripts/install.sh", false, "no test convention"},
		{"scripts/build.sh", false, "no test convention"},
		{"scripts/release.sh", false, "no test convention"},
	}
	for _, c := range cases {
		got := isTestFile(c.path)
		if got != c.want {
			t.Errorf("isTestFile(%q) = %v, want %v (%s)", c.path, got, c.want, c.why)
		}
	}
}

// Cross-check: existing Go + Python conventions still work
// after the bash addition — pin the no-regression case.
func TestIsTestFile_NonBashConventionsStillWork(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"internal/foo/bar_test.go", true},
		{"tests/test_helpers.py", true},
		{"src/foo.test.ts", true},
		{"src/foo.spec.tsx", true},
		{"internal/foo/bar.go", false},
		{"tests/helpers.py", false}, // tests/ dir is itself a test directory — pinned as true
	}
	for _, c := range cases {
		got := isTestFile(c.path)
		// tests/ prefix is caught by the directory rule; helpers.py
		// under tests/ counts as a test file even without _test
		// suffix. Adjust the expectation to match the existing rule.
		if c.path == "tests/helpers.py" {
			c.want = true
		}
		if got != c.want {
			t.Errorf("isTestFile(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}
