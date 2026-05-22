package main

import (
	"strings"
	"testing"
)

// browserOpenCommand maps each OS to its shell-open helper.
func TestBrowserOpenCommand(t *testing.T) {
	const url = "http://127.0.0.1:7777/v1/dashboard"
	cases := []struct {
		goos     string
		wantName string
		wantArg  string // the arg that must carry the URL
	}{
		{"windows", "rundll32", url},
		{"darwin", "open", url},
		{"linux", "xdg-open", url},
		{"freebsd", "xdg-open", url}, // default branch
	}
	for _, c := range cases {
		name, args := browserOpenCommand(c.goos, url)
		if name != c.wantName {
			t.Errorf("%s: command = %q, want %q", c.goos, name, c.wantName)
		}
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, c.wantArg) {
			t.Errorf("%s: args %v must carry the URL %q", c.goos, args, c.wantArg)
		}
	}
}

// shouldOpenBrowser opens only for an interactive human — never in
// --json mode and never with --no-open.
func TestShouldOpenBrowser(t *testing.T) {
	cases := []struct {
		name     string
		stdoutTTY, jsonOut, noOpen bool
		want     bool
	}{
		{"interactive human", true, false, false, true},
		{"piped stdout", false, false, false, false},
		{"--json mode", true, true, false, false},
		{"--no-open", true, false, true, false},
		{"piped + json + no-open", false, true, true, false},
	}
	for _, c := range cases {
		if got := shouldOpenBrowser(c.stdoutTTY, c.jsonOut, c.noOpen); got != c.want {
			t.Errorf("%s: shouldOpenBrowser(%v,%v,%v) = %v, want %v",
				c.name, c.stdoutTTY, c.jsonOut, c.noOpen, got, c.want)
		}
	}
}
