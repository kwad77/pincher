package main

import (
	"bytes"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestCompletion_ListsEveryDispatchedSubcommand is the drift gate:
// pincherSubcommands (the source of truth for the completion scripts)
// must equal the set of subcommands main() actually dispatches. Adding
// a subcommand without adding it here fails this test — so the
// completion scripts never go stale. #1710 v0.92.
func TestCompletion_ListsEveryDispatchedSubcommand(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	// main() dispatches with `os.Args[1] == "<name>"`.
	re := regexp.MustCompile(`os\.Args\[1\] == "([a-z][a-z-]*)"`)
	dispatched := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		dispatched[m[1]] = true
	}
	if len(dispatched) == 0 {
		t.Fatal("found no `os.Args[1] == \"...\"` dispatch lines — regex drift?")
	}

	listed := map[string]bool{}
	for _, s := range pincherSubcommands {
		listed[s] = true
	}

	var missing, extra []string
	for cmd := range dispatched {
		if !listed[cmd] {
			missing = append(missing, cmd)
		}
	}
	for cmd := range listed {
		if !dispatched[cmd] {
			extra = append(extra, cmd)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("pincherSubcommands is missing dispatched subcommand(s): %v — add them so completion stays current", missing)
	}
	if len(extra) > 0 {
		t.Errorf("pincherSubcommands lists %v which main() does not dispatch — stale entry", extra)
	}
}

// completionScript emits a usable script for each supported shell and
// rejects anything else.
func TestCompletionScript(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		script, ok := completionScript(shell)
		if !ok {
			t.Errorf("completionScript(%q) returned ok=false", shell)
			continue
		}
		// Every script must mention pincher and a known subcommand.
		if !strings.Contains(script, "pincher") {
			t.Errorf("%s script does not mention pincher", shell)
		}
		if !strings.Contains(script, "doctor") || !strings.Contains(script, "completion") {
			t.Errorf("%s script is missing expected subcommands", shell)
		}
	}
	if _, ok := completionScript("powershell"); ok {
		t.Error("completionScript should reject an unsupported shell")
	}
}

// TestCompletionCLI exercises the runCompletionCLI core: a good shell
// prints the script to stdout and returns 0; a bad shell or wrong arg
// count prints usage to stderr and returns 1.
func TestCompletionCLI(t *testing.T) {
	t.Run("good shell", func(t *testing.T) {
		var out, errb bytes.Buffer
		if code := completionCLI([]string{"bash"}, &out, &errb); code != 0 {
			t.Fatalf("exit code = %d, want 0", code)
		}
		if !strings.Contains(out.String(), "_pincher_complete") {
			t.Errorf("stdout missing the bash script:\n%s", out.String())
		}
		if errb.Len() != 0 {
			t.Errorf("stderr should be empty, got: %s", errb.String())
		}
	})
	t.Run("bad shell", func(t *testing.T) {
		var out, errb bytes.Buffer
		if code := completionCLI([]string{"powershell"}, &out, &errb); code != 1 {
			t.Fatalf("exit code = %d, want 1", code)
		}
		if !strings.Contains(errb.String(), "unsupported shell") {
			t.Errorf("stderr missing the error, got: %s", errb.String())
		}
	})
	t.Run("no arg", func(t *testing.T) {
		var out, errb bytes.Buffer
		if code := completionCLI(nil, &out, &errb); code != 1 {
			t.Fatalf("exit code = %d, want 1", code)
		}
		if !strings.Contains(errb.String(), "usage:") {
			t.Errorf("stderr missing usage, got: %s", errb.String())
		}
	})
}

// TestNearestSubcommand checks the typo `did you mean` hint.
func TestNearestSubcommand(t *testing.T) {
	cases := []struct{ in, want string }{
		{"doctr", "doctor"},
		{"docter", "doctor"},
		{"stat", "stats"},
		{"setp", "setup"},
		{"indx", "index"},
		{"completon", "completion"},
		{"wholly-unrelated-token", ""}, // too far from anything
	}
	for _, c := range cases {
		if got := nearestSubcommand(c.in); got != c.want {
			t.Errorf("nearestSubcommand(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
