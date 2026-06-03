// SPDX-License-Identifier: MIT

package main

import (
	"strings"
	"testing"
)

// shouldOrientInteractive fires only for a human at a terminal — never
// when an MCP client (piped stdin), the HTTP server, or a detached
// --no-stdio child is what's running.
func TestShouldOrientInteractive(t *testing.T) {
	cases := []struct {
		name      string
		httpAddr  string
		noStdio   bool
		stdinTTY  bool
		want      bool
	}{
		{"human at a terminal", "", false, true, true},
		{"MCP client — stdin is a pipe", "", false, false, false},
		{"HTTP server requested", ":8080", false, true, false},
		{"detached --no-stdio child", "", true, true, false},
		{"piped + http + no-stdio", ":0", true, false, false},
	}
	for _, c := range cases {
		got := shouldOrientInteractive(c.httpAddr, c.noStdio, c.stdinTTY)
		if got != c.want {
			t.Errorf("%s: shouldOrientInteractive(%q,%v,%v) = %v, want %v",
				c.name, c.httpAddr, c.noStdio, c.stdinTTY, got, c.want)
		}
	}
}

// printInteractiveOrientation names the wizard and the help command so
// a lost human has a next step.
func TestPrintInteractiveOrientation(t *testing.T) {
	var b strings.Builder
	printInteractiveOrientation(&b)
	out := b.String()
	for _, want := range []string{"pincher setup", "pincher --help", "MCP server"} {
		if !strings.Contains(out, want) {
			t.Errorf("orientation text missing %q; got:\n%s", want, out)
		}
	}
}
