// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Tests for the --dco-hook install path. Covers the fresh-repo write,
// the non-git-dir skip, idempotent re-runs, the stale-managed-hook
// refresh, and the hard refusal to overwrite a non-pincher hook (no
// --force escape — sign-off is an attestation, a user's existing
// prepare-commit-msg policy must never be silently replaced).
//
// A second group executes the generated script against sample
// COMMIT_EDITMSG files to pin the runtime behavior: trailer appended
// when absent, untouched when present, merge/squash sources skipped,
// cherry-picked foreign sign-offs preserved.

// TestInstallDCOHook_WritesHook_OnEmptyRepo pins the positive path: a
// repo with no existing prepare-commit-msg gets the managed hook,
// executable, carrying the marker and the sign-off machinery.
func TestInstallDCOHook_WritesHook_OnEmptyRepo(t *testing.T) {
	dir := makeGitRepo(t)
	var out bytes.Buffer
	if err := installDCOHook(&out, dir, false); err != nil {
		t.Fatalf("installDCOHook: %v", err)
	}
	p := filepath.Join(dir, ".git", "hooks", "prepare-commit-msg")
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("expected hook at %s: %v", p, err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o100 == 0 {
		t.Errorf("hook is not owner-executable: mode %v", info.Mode())
	}
	body, _ := os.ReadFile(p)
	if !strings.Contains(string(body), gitHookMarker) {
		t.Errorf("hook missing marker %q in body:\n%s", gitHookMarker, body)
	}
	if !strings.Contains(string(body), "Signed-off-by:") {
		t.Errorf("hook missing Signed-off-by machinery; body:\n%s", body)
	}
	if !strings.Contains(out.String(), "wrote") {
		t.Errorf("output didn't report the write; got:\n%s", out.String())
	}
}

// TestInstallDCOHook_NotAGitRepo_SkipsWithoutError pins the safety
// branch: a non-git directory must not error (same contract as
// --git-hooks — safe on loose Claude Code workspaces).
func TestInstallDCOHook_NotAGitRepo_SkipsWithoutError(t *testing.T) {
	dir := t.TempDir() // no .git inside
	var out bytes.Buffer
	if err := installDCOHook(&out, dir, false); err != nil {
		t.Fatalf("installDCOHook on non-git dir errored: %v", err)
	}
	if !strings.Contains(out.String(), "not a git repository") {
		t.Errorf("expected 'not a git repository' skip message; got:\n%s", out.String())
	}
}

// TestInstallDCOHook_IdempotentReinstall pins that a re-run on an
// already-managed repo reports no-change and leaves the file alone.
func TestInstallDCOHook_IdempotentReinstall(t *testing.T) {
	dir := makeGitRepo(t)
	var out1, out2 bytes.Buffer
	if err := installDCOHook(&out1, dir, false); err != nil {
		t.Fatalf("first install: %v", err)
	}
	if err := installDCOHook(&out2, dir, false); err != nil {
		t.Fatalf("re-install: %v", err)
	}
	if !strings.Contains(out2.String(), "already up to date") {
		t.Errorf("re-install should report 'already up to date'; got:\n%s", out2.String())
	}
}

// TestInstallDCOHook_RefreshesStaleManagedHook pins the upgrade path:
// a pincher-managed hook (marker present) with an out-of-date body is
// replaced in place.
func TestInstallDCOHook_RefreshesStaleManagedHook(t *testing.T) {
	dir := makeGitRepo(t)
	p := filepath.Join(dir, ".git", "hooks", "prepare-commit-msg")
	stale := "#!/bin/sh\n# " + gitHookMarker + ": old pincher hook body\n"
	if err := os.WriteFile(p, []byte(stale), 0o755); err != nil {
		t.Fatalf("seed stale managed hook: %v", err)
	}
	var out bytes.Buffer
	if err := installDCOHook(&out, dir, false); err != nil {
		t.Fatalf("installDCOHook: %v", err)
	}
	body, _ := os.ReadFile(p)
	if string(body) != dcoHookBody() {
		t.Errorf("stale managed hook was not refreshed; body:\n%s", body)
	}
}

// TestInstallDCOHook_NeverOverwritesUserHook pins the hard refusal:
// an existing non-pincher prepare-commit-msg is preserved byte-for-byte
// and the skip is surfaced with a notice. There is intentionally no
// --force escape for this hook.
func TestInstallDCOHook_NeverOverwritesUserHook(t *testing.T) {
	dir := makeGitRepo(t)
	p := filepath.Join(dir, ".git", "hooks", "prepare-commit-msg")
	userBody := "#!/bin/sh\n# user's own message policy — must not be clobbered\nexit 0\n"
	if err := os.WriteFile(p, []byte(userBody), 0o755); err != nil {
		t.Fatalf("seed user hook: %v", err)
	}
	var out bytes.Buffer
	if err := installDCOHook(&out, dir, false); err != nil {
		t.Fatalf("installDCOHook: %v", err)
	}
	body, _ := os.ReadFile(p)
	if string(body) != userBody {
		t.Errorf("user hook was overwritten; body now:\n%s", body)
	}
	if !strings.Contains(out.String(), "refusing to overwrite") {
		t.Errorf("expected refusal notice; got:\n%s", out.String())
	}
}

// TestInstallDCOHook_DryRunWritesNothing pins the preview branch.
func TestInstallDCOHook_DryRunWritesNothing(t *testing.T) {
	dir := makeGitRepo(t)
	var out bytes.Buffer
	if err := installDCOHook(&out, dir, true); err != nil {
		t.Fatalf("installDCOHook --dry-run: %v", err)
	}
	if !strings.Contains(out.String(), "would write") {
		t.Errorf("dry-run should preview with 'would write'; got:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", "hooks", "prepare-commit-msg")); !os.IsNotExist(err) {
		t.Errorf("dry-run wrote a hook to disk: %v", err)
	}
}

// TestDCOHookBody_StructuralInvariants pins the properties downstream
// tooling depends on: shebang, marker, merge/squash skip, config-driven
// identity, absent-only append.
func TestDCOHookBody_StructuralInvariants(t *testing.T) {
	body := dcoHookBody()
	if !strings.HasPrefix(body, "#!/bin/sh\n") {
		t.Error("hook body must start with #!/bin/sh")
	}
	if !strings.Contains(body, gitHookMarker) {
		t.Errorf("hook body must contain marker %q", gitHookMarker)
	}
	if !strings.Contains(body, "merge | squash)") {
		t.Error("hook body must skip merge/squash COMMIT_SOURCE values")
	}
	if !strings.Contains(body, "git config user.name") || !strings.Contains(body, "git config user.email") {
		t.Error("hook body must derive identity from git config user.name/user.email")
	}
	if !strings.Contains(body, `grep -qsF "$SIGNOFF"`) {
		t.Error("hook body must check for an existing identical trailer before appending")
	}
}

// ── script-execution tests ──────────────────────────────────────────
//
// These run the generated hook under `sh` against sample COMMIT_EDITMSG
// files inside a real `git init` repo (the hook shells out to
// `git config` and `git interpret-trailers`). Skipped when git or sh
// is unavailable.

const (
	dcoTestName  = "Test User"
	dcoTestEmail = "test@example.com"
)

// runDCOHookScript installs the hook in a real git repo with a known
// identity, writes msg to COMMIT_EDITMSG, executes the hook with the
// given COMMIT_SOURCE, and returns the resulting message file content.
func runDCOHookScript(t *testing.T, msg, source string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("hook execution test requires POSIX sh")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not on PATH")
	}

	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.name", dcoTestName},
		{"config", "user.email", dcoTestEmail},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if outBytes, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, outBytes)
		}
	}

	var out bytes.Buffer
	if err := installDCOHook(&out, dir, false); err != nil {
		t.Fatalf("installDCOHook: %v", err)
	}

	msgFile := filepath.Join(dir, ".git", "COMMIT_EDITMSG")
	if err := os.WriteFile(msgFile, []byte(msg), 0o644); err != nil {
		t.Fatalf("write COMMIT_EDITMSG: %v", err)
	}

	hook := filepath.Join(dir, ".git", "hooks", "prepare-commit-msg")
	args := []string{hook, msgFile}
	if source != "" {
		args = append(args, source)
	}
	cmd := exec.Command("sh", args...)
	cmd.Dir = dir
	if outBytes, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("hook execution failed: %v\n%s", err, outBytes)
	}

	updated, err := os.ReadFile(msgFile)
	if err != nil {
		t.Fatalf("read COMMIT_EDITMSG after hook: %v", err)
	}
	return string(updated)
}

// TestDCOHookScript_AppendsSignoffWhenAbsent pins the core behavior:
// a plain message gains exactly one trailer with the repo identity.
func TestDCOHookScript_AppendsSignoffWhenAbsent(t *testing.T) {
	got := runDCOHookScript(t, "fix: handle empty index\n", "message")
	want := "Signed-off-by: " + dcoTestName + " <" + dcoTestEmail + ">"
	if strings.Count(got, want) != 1 {
		t.Errorf("expected exactly one %q trailer; message now:\n%s", want, got)
	}
	if !strings.HasPrefix(got, "fix: handle empty index\n") {
		t.Errorf("original message body was altered:\n%s", got)
	}
}

// TestDCOHookScript_ExistingSignoffUnchanged pins idempotence at the
// script layer: a message already carrying the committer's trailer is
// returned byte-identical.
func TestDCOHookScript_ExistingSignoffUnchanged(t *testing.T) {
	msg := "fix: handle empty index\n\nSigned-off-by: " + dcoTestName + " <" + dcoTestEmail + ">\n"
	got := runDCOHookScript(t, msg, "message")
	if got != msg {
		t.Errorf("already-signed message was modified.\nbefore:\n%s\nafter:\n%s", msg, got)
	}
}

// TestDCOHookScript_MergeSourceUntouched pins the COMMIT_SOURCE guard:
// merge messages are git-generated and must pass through unmodified.
func TestDCOHookScript_MergeSourceUntouched(t *testing.T) {
	msg := "Merge branch 'feature' into master\n"
	got := runDCOHookScript(t, msg, "merge")
	if got != msg {
		t.Errorf("merge message was modified.\nbefore:\n%s\nafter:\n%s", msg, got)
	}
}

// TestDCOHookScript_SquashSourceUntouched pins the same guard for
// squash messages.
func TestDCOHookScript_SquashSourceUntouched(t *testing.T) {
	msg := "# This is a combination of 2 commits.\nfirst subject\n"
	got := runDCOHookScript(t, msg, "squash")
	if got != msg {
		t.Errorf("squash message was modified.\nbefore:\n%s\nafter:\n%s", msg, got)
	}
}

// TestDCOHookScript_PreservesForeignSignoff pins the cherry-pick
// contract: an existing trailer from a different identity is kept and
// the committer's own trailer is appended after it — never replaced.
func TestDCOHookScript_PreservesForeignSignoff(t *testing.T) {
	foreign := "Signed-off-by: Original Author <original@example.com>"
	msg := "feat: upstream change\n\n" + foreign + "\n"
	got := runDCOHookScript(t, msg, "message")
	if !strings.Contains(got, foreign) {
		t.Errorf("foreign sign-off was stomped; message now:\n%s", got)
	}
	own := "Signed-off-by: " + dcoTestName + " <" + dcoTestEmail + ">"
	if strings.Count(got, own) != 1 {
		t.Errorf("expected exactly one own trailer alongside the foreign one; message now:\n%s", got)
	}
	if strings.Index(got, foreign) > strings.Index(got, own) {
		t.Errorf("own trailer should follow the preserved foreign one; message now:\n%s", got)
	}
}

// TestDCOHookScript_TrailerPlacedAboveCommentBlock pins the
// interpret-trailers behavior the hook relies on: with the
// commented-out status block `git commit` appends, the trailer lands
// in the message body, not below the comments.
func TestDCOHookScript_TrailerPlacedAboveCommentBlock(t *testing.T) {
	msg := "fix: handle empty index\n\n# Please enter the commit message for your changes.\n# Changes to be committed:\n#\tmodified: foo.go\n"
	got := runDCOHookScript(t, msg, "message")
	own := "Signed-off-by: " + dcoTestName + " <" + dcoTestEmail + ">"
	sobIdx := strings.Index(got, own)
	commentIdx := strings.Index(got, "# Please enter")
	if sobIdx < 0 {
		t.Fatalf("trailer missing; message now:\n%s", got)
	}
	if commentIdx >= 0 && sobIdx > commentIdx {
		t.Errorf("trailer should be placed above the comment block; message now:\n%s", got)
	}
}
