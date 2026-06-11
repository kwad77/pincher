// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// installDCOHook writes a pincher-managed `prepare-commit-msg` hook
// into `.git/hooks/` that appends a DCO `Signed-off-by:` trailer
// (built from `git config user.name` / `user.email`) whenever the
// commit message doesn't already carry it. Projects that enforce
// per-commit DCO sign-off (the tim-actions/dco style check) fail PRs
// when a single commit lacks the trailer — and `git config
// format.signoff true` does NOT cover plain `git commit`, so the
// trailer is easy to forget. The hook makes compliance automatic at
// the only layer that sees every commit.
//
// Opt-in via `pincher init --dco-hook`, mirroring the `--git-hooks`
// gate (#1261): writes into `.git/hooks/` are always explicitly
// requested, never a side effect of seeding rules files. Sign-off is
// additionally a legal attestation (the Developer Certificate of
// Origin), so it must never be switched on without the user asking.
//
// Safety properties:
//   - Not a git repo → skip with a notice, exit 0 (same contract as
//     installGitHooks: safe on loose Claude Code workspaces).
//   - An existing NON-pincher prepare-commit-msg hook is NEVER
//     overwritten — not even with --force. A user's
//     prepare-commit-msg may implement their own message policy;
//     silently replacing it would change attestation behavior. The
//     install skips with a notice instead.
//   - Re-runs are idempotent: a byte-identical pincher-managed hook
//     reports "already up to date"; a stale pincher-managed hook
//     (older pincher version) is refreshed in place.
//   - Freshly written hooks are made executable (0o755).
func installDCOHook(out io.Writer, projectDir string, dryRun bool) error {
	if info, err := os.Stat(filepath.Join(projectDir, ".git")); err != nil || !info.IsDir() {
		// Not a git repo (or a linked worktree, where .git is a file
		// and hooks live in the main repo). No-op rather than error so
		// `pincher init --dco-hook` stays safe to script.
		fmt.Fprintf(out, "pincher init [dco-hook]: %s is not a git repository — skipping\n", projectDir)
		return nil
	}
	hooksDir := filepath.Join(projectDir, ".git", "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", hooksDir, err)
	}

	hookPath := filepath.Join(hooksDir, "prepare-commit-msg")
	newBody := dcoHookBody()

	existing, err := os.ReadFile(hookPath)
	switch {
	case err == nil:
		if !strings.Contains(string(existing), gitHookMarker) {
			// Hard refusal — unlike --git-hooks there is no --force
			// escape hatch for this hook (see doc comment above).
			fmt.Fprintf(out, "pincher init [dco-hook]: %s already exists and is not pincher-managed — refusing to overwrite. Add the Signed-off-by logic to your hook manually if you want automatic DCO sign-off.\n", hookPath)
			return nil
		}
		if string(existing) == newBody {
			fmt.Fprintf(out, "pincher init [dco-hook]: %s already up to date — no change\n", hookPath)
			return nil
		}
	case !os.IsNotExist(err):
		return fmt.Errorf("read %s: %w", hookPath, err)
	}

	if dryRun {
		fmt.Fprintf(out, "pincher init [dco-hook]: would write %s\n", hookPath)
		fmt.Fprintln(out, "--- new hook content ---")
		fmt.Fprintln(out, newBody)
		return nil
	}

	if err := os.WriteFile(hookPath, []byte(newBody), 0o755); err != nil {
		return fmt.Errorf("write %s: %w", hookPath, err)
	}
	// os.WriteFile's mode only applies on create (and is umask-filtered);
	// chmod explicitly so a replaced hook is guaranteed executable.
	if err := os.Chmod(hookPath, 0o755); err != nil {
		return fmt.Errorf("chmod %s: %w", hookPath, err)
	}
	fmt.Fprintf(out, "pincher init [dco-hook]: wrote %s\n", hookPath)
	fmt.Fprintln(out, "pincher init [dco-hook]: commits in this repo will now carry a Signed-off-by trailer automatically.")
	return nil
}

// dcoHookBody returns the POSIX-sh body of the prepare-commit-msg
// hook. Behavior (standard prepare-commit-msg semantics):
//
//   - $1 = path to the commit message file; $2 = COMMIT_SOURCE.
//   - merge / squash sources are left untouched — their messages are
//     git-generated and rewriting them mid-operation is surprising.
//   - The committer's own trailer is appended only when absent, so
//     re-edits and `--amend` never duplicate it.
//   - Existing trailers are never removed or rewritten: a cherry-picked
//     commit keeps the original author's Signed-off-by lines and gains
//     the committer's own (the DCO chain-of-custody model).
//   - Identity comes from `git config user.name` / `user.email`; when
//     either is unset the hook exits 0 silently — it must never break
//     the user's commit flow.
//   - `git interpret-trailers --in-place` places the trailer correctly
//     above the commented-out status block `git commit` appends; if
//     that ever fails, a plain append is the fallback.
func dcoHookBody() string {
	return `#!/bin/sh
# ` + gitHookMarker + `: pincher prepare-commit-msg hook — appends a DCO
# "Signed-off-by:" trailer (git config user.name / user.email) when the
# message doesn't already carry yours. Projects enforcing per-commit
# DCO sign-off fail PRs on a single unsigned commit, and
# "git config format.signoff true" does NOT cover plain "git commit";
# this hook closes that gap at the only layer that sees every commit.
#
# Safe to delete. "pincher init --dco-hook" reinstalls it, and refuses
# to clobber non-pincher hooks (no marker comment).

COMMIT_MSG_FILE="$1"
COMMIT_SOURCE="${2:-}"

# Merge and squash messages are git-generated — leave them alone.
case "$COMMIT_SOURCE" in
merge | squash)
    exit 0
    ;;
esac

NAME="$(git config user.name)" || exit 0
EMAIL="$(git config user.email)" || exit 0
if [ -z "$NAME" ] || [ -z "$EMAIL" ]; then
    exit 0
fi

SIGNOFF="Signed-off-by: $NAME <$EMAIL>"

# Already signed off by this identity (e.g. --signoff, --amend of a
# signed commit, or a re-edit)? Nothing to do. Other people's trailers
# (cherry-picks) are preserved untouched — we only ever append ours.
if grep -qsF "$SIGNOFF" "$COMMIT_MSG_FILE"; then
    exit 0
fi

# interpret-trailers places the trailer before the commented-out status
# block git appends to the message file; fall back to a plain append if
# it ever fails so the commit is still signed.
if git interpret-trailers --in-place --trailer "$SIGNOFF" "$COMMIT_MSG_FILE" 2>/dev/null; then
    exit 0
fi
printf '\n%s\n' "$SIGNOFF" >>"$COMMIT_MSG_FILE"
`
}
