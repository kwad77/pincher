// SPDX-License-Identifier: MIT

package main

import "testing"

func TestShouldStartBackgroundWatcher(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		noStdio     bool
		mcpHTTPPath string
		want        bool
	}{
		{
			name:    "stdio MCP starts watcher",
			noStdio: false,
			want:    true,
		},
		{
			name:    "pure detached dashboard skips watcher",
			noStdio: true,
			want:    false,
		},
		{
			name:        "streamable HTTP MCP starts watcher",
			noStdio:     true,
			mcpHTTPPath: "/mcp",
			want:        true,
		},
		{
			name:        "blank HTTP MCP path is still pure dashboard",
			noStdio:     true,
			mcpHTTPPath: "   ",
			want:        false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := shouldStartBackgroundWatcher(c.noStdio, c.mcpHTTPPath); got != c.want {
				t.Errorf("shouldStartBackgroundWatcher(%v, %q) = %v, want %v",
					c.noStdio, c.mcpHTTPPath, got, c.want)
			}
		})
	}
}
