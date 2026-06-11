// SPDX-License-Identifier: MIT

package server

import "testing"

// #2011: ToolAdvertised is the toolset-resolution primitive hook-check
// uses to keep advisory recommendations callable. It must follow
// parseToolsetEnv's rule exactly: only the canonical "full" widens the
// advertisement; unset / "core" / typos resolve to coreToolset.
func TestToolAdvertised(t *testing.T) {
	cases := []struct {
		env  string
		tool string
		want bool
	}{
		{"", "search", true},                // core member, default env
		{"", "onboard_module", false},       // non-core, default env
		{"core", "onboard_module", false},   // non-core, explicit core
		{"core", "guide", true},             // core member, explicit core
		{"full", "onboard_module", true},    // full advertises everything
		{"full", "search", true},            // full advertises everything
		{"FULL", "onboard_module", true},    // canonical match is case-insensitive
		{" full ", "onboard_module", true},  // and trims whitespace
		{"fulll", "onboard_module", false},  // typo lands on the core default
		{"", "no_such_tool_ever", false},    // unknown names are never core-advertised
		{"full", "no_such_tool_ever", true}, // full mode doesn't validate names (advertisement check only)
	}
	for _, c := range cases {
		t.Run(c.env+"/"+c.tool, func(t *testing.T) {
			t.Setenv("PINCHER_TOOLSET", c.env)
			if got := ToolAdvertised(c.tool); got != c.want {
				t.Errorf("ToolAdvertised(%q) under PINCHER_TOOLSET=%q = %v, want %v", c.tool, c.env, got, c.want)
			}
		})
	}
}
