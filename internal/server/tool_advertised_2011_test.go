// SPDX-License-Identifier: MIT

package server

import "testing"

// #2011: ToolAdvertised is the toolset-resolution primitive hook-check
// uses to keep advisory recommendations callable. It must follow
// parseToolsetEnv's rule exactly (#2054): the default is `full`, so
// unset / "full" / typos resolve to the full advertisement; only the
// explicit canonical "core" narrows to coreToolset.
func TestToolAdvertised(t *testing.T) {
	cases := []struct {
		env  string
		tool string
		want bool
	}{
		{"", "search", true},                // default env = full advertises everything (#2054)
		{"", "onboard_module", true},        // non-core tool now advertised under the full default
		{"core", "onboard_module", false},   // non-core, explicit core opt-in
		{"core", "guide", true},             // core member, explicit core
		{"full", "onboard_module", true},    // full advertises everything
		{"full", "search", true},            // full advertises everything
		{"FULL", "onboard_module", true},    // canonical match is case-insensitive
		{" full ", "onboard_module", true},  // and trims whitespace
		{"fulll", "onboard_module", true},   // typo lands on the full default (#2054)
		{"coree", "onboard_module", true},   // typo of core also lands on full
		{"core", "no_such_tool_ever", false}, // unknown names are never core-advertised
		{"", "no_such_tool_ever", true},     // full default doesn't validate names (advertisement check only)
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
