// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kwad77/pincher/internal/db"
	"github.com/kwad77/pincher/internal/index"
)

// #2014: recruit the index. The live telemetry audit (726 hook
// invocations across 110 sessions) found the PreToolUse hook had a 100%
// pass-through rate — 725/726 events targeted files outside every
// indexed root, so the redirect mechanism never had an index to redirect
// TO. When a session repeatedly Reads code files inside a git repo that
// pincher has never indexed, the highest-value advisory isn't "use
// context instead of Read" (impossible — no index), it's "index this
// repo so the code-graph tools can answer at all".
//
// Persistence choice: the hook is a short-lived subprocess with no state
// of its own, but it already writes one hook_invocations row per
// invocation (best-effort, via the daemon DB it opens anyway). Those
// rows carry (session_id, tool_name, file_path) — exactly the per-
// (session, repo-root) counter the threshold needs, for free. So the
// "N≥3 this session" check is a read over the rows previous invocations
// already wrote (prefix match on file_path under the repo root), and
// the "never again for that (session, root)" check is the existence of
// a prior advise_index row whose file_path IS the root. No new table,
// no migration, no cross-process file locking — the simplest mechanism
// the existing architecture supports. Best-effort like all hook
// telemetry: if a log write was lost, the worst case is the advisory
// firing one event late or (after a lost advisory row) twice.
//
// Telemetry: the advisory row uses Decision "advise_index" —
// deliberately distinct from "redirect_advisory" so it never pollutes
// the redirect conversion/override metrics (all of which filter on
// decision IN ('redirect','redirect_advisory')). Its file_path column
// holds the REPO ROOT (not the triggering file): that is the once-per-
// root suppression key, and it makes the take-rate measurable with a
// join — an advise_index row whose file_path later appears in
// projects.path (or as a prefix of one) is a taken advisory.
// took_recommendation is left to NULL on purpose: the expected take is
// a `pincher index` CLI run, which the MCP session-call joiner cannot
// observe, and a false "saw it and rejected it" would be worse than no
// resolution.

// adviseIndexThreshold is the per-(session, repo-root) count of
// code-file Read events at which the one-time advisory fires. 3 — one
// Read can be incidental, two can be a quick peek; three Reads into
// the same unindexed repo in one session is a working session that an
// index would have served.
const adviseIndexThreshold = 3

// gitRepoRoot walks up from absPath's parent directory looking for a
// .git entry and returns the first directory that has one. Lstat (not
// Stat) on purpose: .git is a directory in a normal clone but a FILE
// in worktrees and submodules — both mark a repo root. ok=false when
// no ancestor has a .git (non-repo trees never get the advisory).
func gitRepoRoot(absPath string) (root string, ok bool) {
	dir := filepath.Dir(filepath.Clean(absPath))
	for {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// isCodeFilePath reports whether the path's extension names a code file
// pincher indexes — same conservative extension set the Glob hook uses.
// Prose, config and unknown extensions never trigger the advisory (and
// most prose was already exempted before the unindexed branch anyway).
func isCodeFilePath(path string) bool {
	return codeGlobExtensions[strings.ToLower(filepath.Ext(path))]
}

// decideUnindexedRead handles the Read branch where matchIndexedFile
// found no indexed project for the path — production telemetry says
// this is 99.9% of hook traffic. Emits the one-time advise_index
// advisory when ALL of:
//
//   - the session is identified (no session id → can't honor
//     once-per-session, so never advise);
//   - the path is a code file (extension gate);
//   - an ancestor directory has a .git entry (non-repo dirs: never);
//   - that repo root is not an indexed root and not inside one (the
//     file may simply be new/excluded in an indexed repo — nothing to
//     recruit there);
//   - the root is not a bloat trap (#1991 lesson: never advise
//     indexing $HOME, the filesystem root, or the OS temp dir —
//     index.IsBloatTrap in hook mode, shared with the CLI and the MCP
//     index handler);
//   - no advise_index row exists yet for (session, root);
//   - this is at least the adviseIndexThreshold-th Read of a code
//     file under that root this session (prior rows + this event).
//
// Advisory-only by construction: every return has Continue=true; the
// advisory differs from plain pass-through only in the systemMessage
// and the telemetry row.
func decideUnindexedRead(store *db.Store, in hookCheckInput, path string, debug bool) hookDecision {
	base := hookDecision{FilePathParsed: path}
	if in.SessionID == "" {
		return debugPass(debug, "not in any indexed project (no session id)", base)
	}
	if !isCodeFilePath(path) {
		return debugPass(debug, "not in any indexed project (not a code file)", base)
	}
	root, ok := gitRepoRoot(path)
	if !ok {
		return debugPass(debug, "not in any indexed project (no enclosing git repo)", base)
	}
	if _, indexed := matchIndexedDir(store, root); indexed {
		// The repo IS covered by an indexed root — this file just has
		// no file_hashes row yet (new or excluded file).
		return debugPass(debug, "file not indexed but repo root is covered", base)
	}
	if trap, reason := index.IsBloatTrap(root, true); trap {
		return debugPass(debug, "unindexed repo refused, bloat trap: "+reason, base)
	}
	if store.HookIndexAdvisedForRoot(in.SessionID, root) {
		return debugPass(debug, "index advisory already emitted for this root this session", base)
	}
	// Prior code-file Reads under this root this session, from the rows
	// earlier invocations logged. +1 for the current event, which is
	// not logged yet when the decision runs.
	prior := 0
	for _, p := range store.HookReadPathsUnderRoot(in.SessionID, root) {
		if isCodeFilePath(p) {
			prior++
		}
	}
	if prior+1 < adviseIndexThreshold {
		return debugPass(debug, fmt.Sprintf("unindexed repo below advisory threshold (%d/%d)", prior+1, adviseIndexThreshold), base)
	}

	msg := fmt.Sprintf(
		"Pincher hint: %s is a git repo that isn't indexed, so pincher's code-graph tools (search/context/trace) can't answer here. `pincher index %s` enables them. (One-time advisory — Read passes through.)",
		root, root,
	)
	return hookDecision{
		Continue:      true,
		SystemMessage: msg,
		Decision:      "advise_index",
		SuggestedTool: "index",
		SuggestedArgs: fmt.Sprintf(`{"path":%q}`, root),
		// The advisory row's file_path is the REPO ROOT, not the
		// triggering file: it's the exact-match suppression key for
		// HookIndexAdvisedForRoot and the join key for the take-rate
		// query (advise_index roots vs projects.path).
		FilePathParsed: root,
	}
}
