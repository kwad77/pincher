package init

import "testing"

// DetectHostFromEnv keys on CLAUDECODE. t.Setenv (so no t.Parallel)
// makes the probe deterministic regardless of the host the test runs
// under.
func TestDetectHostFromEnv(t *testing.T) {
	t.Setenv("CLAUDECODE", "1")
	if target, reason := DetectHostFromEnv(); target != "claude" || reason == "" {
		t.Errorf("CLAUDECODE set → got (%q,%q), want claude", target, reason)
	}

	t.Setenv("CLAUDECODE", "")
	if target, _ := DetectHostFromEnv(); target != "" {
		t.Errorf("CLAUDECODE cleared → got %q, want \"\" (no host)", target)
	}
}

// resolveInitTarget is the pure decision — every branch with synthetic
// inputs, no dependence on the real environment or installed editors.
func TestResolveInitTarget(t *testing.T) {
	// Env signal wins outright — markers are not even consulted.
	got := resolveInitTarget("claude", "CLAUDECODE is set", []Target{{Name: "cursor"}, {Name: "zed"}})
	if !got.Decided || got.Target != "claude" {
		t.Errorf("env signal should win: got %+v", got)
	}

	// No env signal, exactly one marker → that target.
	got = resolveInitTarget("", "", []Target{{Name: "codex"}})
	if !got.Decided || got.Target != "codex" {
		t.Errorf("single marker hit: got %+v", got)
	}

	// No env signal, no markers → undecided. The caller MUST refuse —
	// this is the #1862 fix: never silently fall back to claude.
	got = resolveInitTarget("", "", nil)
	if got.Decided {
		t.Errorf("no host + no markers must be undecided, not a guess: got %+v", got)
	}
	if got.Target == "claude" {
		t.Error("undecided resolution must NOT default to claude (the bug)")
	}

	// No env signal, multiple markers → detect (configure all).
	got = resolveInitTarget("", "", []Target{{Name: "claude"}, {Name: "cursor"}})
	if !got.Decided || got.Target != "detect" {
		t.Errorf("multiple marker hits should resolve to detect: got %+v", got)
	}
}

// AutoResolveInitTarget end-to-end with the env short-circuit — the one
// path that's deterministic without controlling the machine's editors.
func TestAutoResolveInitTarget_EnvShortCircuit(t *testing.T) {
	t.Setenv("CLAUDECODE", "1")
	res := AutoResolveInitTarget(t.TempDir())
	if !res.Decided || res.Target != "claude" {
		t.Errorf("under Claude Code AutoResolveInitTarget should pick claude; got %+v", res)
	}
}
