// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/kwad77/pincher/internal/db"
	"github.com/kwad77/pincher/internal/server"
	"github.com/zeebo/xxh3"
)

// matchIndexedFile resolves an absolute path to (relPath, fileBytes,
// projectID) by walking the indexed projects in priority order
// (longest path prefix wins, so nested projects route correctly).
// Returns ok=false when the path is outside every indexed project,
// when there's no file_hashes row for it, or when the file size
// can't be stat'd. Best-effort — failures fall through to
// pass-through, not blocking the agent.
func matchIndexedFile(store *db.Store, absPath string) (relPath string, fileBytes int64, projectID string, ok bool) {
	clean := filepath.Clean(absPath)
	projects, err := store.ListProjects()
	if err != nil {
		return "", 0, "", false
	}
	// Sort by descending path length so nested projects (e.g. a
	// pincher-repo/ inside ClaudeCode/) win the prefix match.
	type sized struct {
		p   db.Project
		len int
	}
	scored := make([]sized, 0, len(projects))
	for _, p := range projects {
		scored = append(scored, sized{p, len(p.Path)})
	}
	// Simple selection sort — N is small (typically < 20).
	for i := 0; i < len(scored); i++ {
		max := i
		for j := i + 1; j < len(scored); j++ {
			if scored[j].len > scored[max].len {
				max = j
			}
		}
		scored[i], scored[max] = scored[max], scored[i]
	}
	for _, s := range scored {
		base := filepath.Clean(s.p.Path)
		if !strings.HasPrefix(clean, base+string(filepath.Separator)) && clean != base {
			continue
		}
		rel, err := filepath.Rel(base, clean)
		if err != nil {
			continue
		}
		// Pincher stores forward slashes in file_path on every OS.
		relUnix := filepath.ToSlash(rel)
		// Confirm the file is actually indexed (file_hashes row).
		if !store.IsFileIndexed(s.p.ID, relUnix) {
			continue
		}
		fi, err := os.Stat(clean)
		if err != nil {
			return "", 0, "", false
		}
		return relUnix, fi.Size(), s.p.ID, true
	}
	return "", 0, "", false
}

// runHookCheckCLI is the PreToolUse decision shim invoked by Claude
// Code's `.claude/settings.json` (#625). Reads a tool-call shape from
// stdin, returns a Claude-Code-hook-spec response on stdout:
//
//   - `{"continue": true}` — pass through (silent on success path)
//   - `{"continue": false, "stopReason": "...", "systemMessage": "..."}`
//     — block with feedback (the agent gets the suggested redirect)
//
// Latency budget: < 50ms. Path lookup is a single SQLite point query.
// Telemetry write is best-effort and never blocks the decision.
//
// Decision logic for Read:
//   - pass through if path not indexed
//   - pass through if file size below expected-savings threshold
//   - pass through if offset already used (agent narrowed to a position
//     context can't reproduce)
//   - pass through if symbol count < 5 (config blobs)
//   - otherwise redirect to `context id=<best> lite=true max_tokens=N`
//     where N is a budget derived from the Read call's own limit param
//     when present (limit lines × ~12 tokens/line, floor 400) or from
//     Read's implicit 2000-line page otherwise — a redirected giant
//     file can't blow the window any harder than the Read it replaced
//
// Decision logic for Grep (#630):
//   - redirect when pattern is a single CamelCase / dotted identifier
//     on an indexed project
//   - pass through on regex / quoted phrase / multi-file glob shapes
//
// Decision logic for Glob:
//   - advisory hint when the glob targets code files inside an
//     indexed project; the suggested tool resolves against the active
//     toolset (#2011): onboard_module under PINCHER_TOOLSET=full,
//     search (advertised in both modes) under the core default
//   - pass through when path is absent, outside every indexed
//     project, or the pattern isn't code-shaped
//
// Decision logic for Task (router-loop §A2, advise_route):
//   - one-time recruitment advisory when a pincher-router install is
//     detected (ladder rungs 1–2, no network) and this is at least
//     the third Task event this session; once per session
//   - pass through (silently) everywhere else — always advisory
func runHookCheckCLI(args []string) {
	fs := flag.NewFlagSet("hook-check", flag.ExitOnError)
	dataDir := fs.String("data-dir", "", "Override data directory (default: platform-appropriate)")
	debug := fs.Bool("debug", false, "Print decision rationale to stderr")
	fs.Parse(args)

	// Read tool-call JSON from stdin.
	rawIn, err := io.ReadAll(os.Stdin)
	if err != nil {
		emitPassThrough(*debug, "stdin read error: "+err.Error())
		return
	}
	var input hookCheckInput
	if err := json.Unmarshal(rawIn, &input); err != nil {
		emitPassThrough(*debug, "input not valid JSON: "+err.Error())
		return
	}

	dir := *dataDir
	if dir == "" {
		var err error
		dir, err = db.DataDir()
		if err != nil {
			emitPassThrough(*debug, "data dir resolve: "+err.Error())
			return
		}
	}
	store, err := db.Open(dir)
	if err != nil {
		emitPassThrough(*debug, "db open: "+err.Error())
		return
	}
	defer store.Close()

	// PreCompact (precompact-hook): event routing stays inside
	// hook-check so init registers ONE command for both events. The
	// summarizer is told what the ledger already holds, so it can drop
	// checkpointed payloads to pointers instead of reproducing them.
	if input.HookEventName == "PreCompact" {
		decision := decidePreCompact(store, input, *debug)
		logPreCompactInvocation(store, input, decision)
		emitPreCompactResponse(decision)
		return
	}

	decision := decideHook(store, input, *debug)
	logHookDecision(store, input, decision)
	emitHookResponse(decision)
}

// hookCheckInput mirrors Claude Code's hook payload shape. Only the
// fields we read are declared; the rest are ignored. PreToolUse events
// carry tool_name/tool_input; PreCompact events (precompact-hook) carry
// hook_event_name="PreCompact" + cwd and no tool fields — the same
// struct decodes both, with absent fields zero-valued.
type hookCheckInput struct {
	HookEventName string         `json:"hook_event_name"`
	ToolName      string         `json:"tool_name"`
	ToolInput     map[string]any `json:"tool_input"`
	SessionID     string         `json:"session_id"`
	CWD           string         `json:"cwd"`
}

type hookDecision struct {
	Continue       bool
	StopReason     string
	SystemMessage  string
	Decision       string // "pass_through" | "redirect"
	SuggestedTool  string
	SuggestedArgs  string
	FilePathParsed string
	FileBytes      int64
	// EstTokensServed / BaselineTokens (hook-redirect-v2) feed the
	// per-redirect savings telemetry: estimated cost of the suggested
	// `context lite=true` call vs the estimated cost of the Read it
	// replaces (stat-ed file size, capped by the Read's own limit).
	// Zero on pass-through.
	EstTokensServed int64
	BaselineTokens  int64
}

// envelopeEstimate is the floor cost of a pincher response post-#622/#623.
// Lite-mode context (#623): ~150B of always-on _meta + ~50B id/source
// keys. Round up to leave headroom for the next_steps emission paths.
const envelopeEstimate = 400

// minExpectedSavings is the threshold below which the hook passes
// through silently. Raised from 3200 → 16384 in #1656 v0.86: the
// 4 KB floor fired too often in live use, and any file under 16 KB
// is small enough that the agent reading it directly costs less
// than the hook chrome + re-issue overhead even in the redirect
// case. Tuned against the v0.86 dogfood session where Read on
// every non-trivial file generated a hint with no user benefit.
const minExpectedSavingsBytes = 16384

// Budgeted-redirect tuning (hook-redirect-v2). When the hook suggests
// `context lite=true`, it attaches a max_tokens cap so the redirect can
// never cost more window than the Read it replaces:
//
//   - tokensPerLineHeuristic: ~12 tokens per source line. Derived from
//     the benchmark corpus average (≈48 bytes/line ÷ 4 bytes/token).
//   - budgetFloorTokens: never cap below 400 — under that, the lite
//     envelope itself dominates and the truncation marker eats the
//     payload.
//   - readDefaultLimitLines: native Read truncates at 2000 lines when
//     no limit is passed; the default budget mirrors that implicit page
//     so an uncapped redirect matches Read's own worst case.
const (
	tokensPerLineHeuristic = 12
	budgetFloorTokens      = 400
	readDefaultLimitLines  = 2000
)

// repeatReadHashMaxBytes bounds the repeat-read content check: hashing
// requires reading the whole file, and the hook has a <50ms latency
// budget. Files above 4 MiB skip the unchanged-content line rather
// than risk a slow decision.
const repeatReadHashMaxBytes = 4 << 20

// approxTokensFromBytes mirrors db.ApproxTokens's fast path (bytes/4)
// without needing the string in memory.
func approxTokensFromBytes(n int64) int64 {
	return (n + 3) / 4
}

// hookIntArg coerces a JSON tool-input value to a positive int.
// Claude Code sends numbers as float64; tests may pass int.
func hookIntArg(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		if n > 0 {
			return int(n), true
		}
	case int:
		if n > 0 {
			return n, true
		}
	}
	return 0, false
}

// identifierPattern matches single CamelCase / camelCase / dotted /
// :: -qualified identifiers. Used for Grep redirect detection (#630):
// only patterns that look like a single symbol name get rewritten to
// search; regexes / phrases pass through.
var identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$|^[A-Za-z_][A-Za-z0-9_]*\.[A-Za-z_][A-Za-z0-9_]*$|^[A-Za-z_][A-Za-z0-9_]*::[A-Za-z_][A-Za-z0-9_]*$`)

// regexMetacharPattern catches obvious regex shapes — presence of any
// of these chars means the user wrote a regex, not an identifier.
var regexMetacharPattern = regexp.MustCompile(`[\[\]{}().*+?|^$\\]`)

func decideHook(store *db.Store, in hookCheckInput, debug bool) hookDecision {
	switch in.ToolName {
	case "Read":
		return decideReadHook(store, in, debug)
	case "Grep":
		return decideGrepHook(store, in, debug)
	case "Glob":
		return decideGlobHook(store, in, debug)
	case "Task":
		return decideTaskSpawn(store, in, debug)
	default:
		return hookDecision{Continue: true, Decision: "pass_through"}
	}
}

func decideReadHook(store *db.Store, in hookCheckInput, debug bool) hookDecision {
	path, _ := in.ToolInput["file_path"].(string)
	if path == "" {
		return debugPass(debug, "no file_path", hookDecision{FilePathParsed: ""})
	}
	// Offset already used → agent narrowed to a byte position that
	// `context` can't reproduce; don't override.
	if _, hasOffset := in.ToolInput["offset"]; hasOffset {
		return debugPass(debug, "offset already set", hookDecision{FilePathParsed: path})
	}
	// limit (without offset) no longer forces pass-through
	// (hook-redirect-v2): the agent capped its own read from the top of
	// the file, which `context lite=true` can honor via max_tokens. The
	// limit instead derives the redirect's token budget below.
	limitLines := 0
	if v, hasLimit := in.ToolInput["limit"]; hasLimit {
		if n, ok := hookIntArg(v); ok {
			limitLines = n
		} else {
			// Unparseable limit — preserve the legacy narrowing
			// pass-through rather than guess at the agent's intent.
			return debugPass(debug, "limit set but not a positive number", hookDecision{FilePathParsed: path})
		}
	}

	// #1646 v0.86: test files pass through. Test files are commonly
	// edited by hand (Read → Edit), and the `context` redirect's
	// "same retrieval, ~80% smaller payload" promise is a bad trade
	// when the agent needs the literal byte content for an Edit. The
	// hook's job is to redirect navigation reads, not editing reads;
	// test files are the most common false positive because they're
	// often small enough that the agent reads them whole.
	if isTestFile(path) {
		return debugPass(debug, "test file exempted",
			hookDecision{FilePathParsed: path})
	}

	// #1656 v0.86: prose / planning files pass through. `context
	// lite=true` on Markdown, plain text, or RST returns no useful
	// symbols — the redirect would be active misinformation. Same
	// for explicitly-marked planning / scratch directories which
	// often contain Markdown but live outside `docs/`.
	if isProseFile(path) {
		return debugPass(debug, "prose / planning file exempted",
			hookDecision{FilePathParsed: path})
	}

	relPath, fileBytes, projectID, ok := matchIndexedFile(store, path)
	if !ok {
		// #2014: the dominant production case (725/726 audited
		// invocations) — the path is outside every indexed root, so no
		// redirect is possible. When the path sits inside an UNINDEXED
		// git repo that the session keeps reading, recruit the index
		// with a one-time advisory instead of passing through silently
		// forever. Still advisory-only: every branch continues.
		return decideUnindexedRead(store, in, path, debug)
	}

	// Tiny files: Read wins on tokens. Threshold matches the lite-mode
	// envelope floor — if the saving isn't bigger than the envelope,
	// don't redirect.
	if fileBytes < int64(envelopeEstimate+minExpectedSavingsBytes) {
		return debugPass(debug, fmt.Sprintf("file too small (%d bytes)", fileBytes),
			hookDecision{FilePathParsed: path, FileBytes: fileBytes})
	}

	// Symbol count: configs / generated files might be large but have
	// few symbols — context wouldn't help.
	symCount, err := store.CountSymbolsInFile(projectID, relPath)
	if err != nil || symCount < 5 {
		return debugPass(debug, fmt.Sprintf("low symbol count (%d)", symCount),
			hookDecision{FilePathParsed: path, FileBytes: fileBytes})
	}

	// Best-fit symbol: largest by source span. The agent likely wants
	// the file's main entry point.
	bestID, bestSpan, err := store.LargestSymbolInFile(projectID, relPath)
	if err != nil || bestID == "" {
		return debugPass(debug, "no resolvable symbol id",
			hookDecision{FilePathParsed: path, FileBytes: fileBytes})
	}

	// Budgeted redirect (hook-redirect-v2): cap the suggested context
	// call so it can't blow the window any harder than the Read it
	// replaces. With an explicit limit: limit × ~12 tokens/line, floor
	// 400. Without: mirror Read's implicit 2000-line page.
	budget := readDefaultLimitLines * tokensPerLineHeuristic
	if limitLines > 0 {
		budget = limitLines * tokensPerLineHeuristic
		if budget < budgetFloorTokens {
			budget = budgetFloorTokens
		}
	}

	// Savings telemetry: estimated cost of the suggested lite call
	// (largest-symbol span + envelope, never over budget) vs the
	// realistic baseline — what the Read would actually have returned
	// (stat-ed file size, capped by the Read's own limit; the 400-token
	// floor deliberately does NOT apply to the baseline).
	estServed := approxTokensFromBytes(bestSpan) + envelopeEstimate
	if estServed > int64(budget) {
		estServed = int64(budget)
	}
	baseline := approxTokensFromBytes(fileBytes)
	if limitLines > 0 {
		if maxRead := int64(limitLines * tokensPerLineHeuristic); baseline > maxRead {
			baseline = maxRead
		}
	}

	args := fmt.Sprintf(`{"id":"%s","lite":true,"max_tokens":%d}`, bestID, budget)
	// #1654 v0.86: advisory-only mode. Blocking the Read breaks
	// Edit-prep workflows (Edit requires a prior Read) and prose
	// reads where `context lite=true` returns nothing useful. The
	// hook's signal — "this file is indexed, consider context
	// next time" — is delivered via systemMessage on a passing
	// decision instead of a stopReason on a blocked one. The
	// `Decision: "redirect_advisory"` value preserves the
	// telemetry counter so we can still measure how often the
	// hook would have fired.
	msg := fmt.Sprintf(
		"Pincher hint: this file is indexed (%d bytes). For navigation, `context id=%s lite=true max_tokens=%d` is ~80%% cheaper and capped at the budget. (Hook is advisory since v0.86 — Read passes through to support Edit-prep workflows.)",
		fileBytes, bestID, budget,
	)
	// Repeat-read awareness (hook-redirect-v2): if this exact file was
	// already served this session and its content hash still matches
	// what the index recorded, one short line teaches the agent that a
	// re-read returns identical bytes (and that the #655 diff-context
	// path — opt-in via PINCHER_DIFF_CONTEXT=1 — answers it for ~0
	// tokens). Best-effort: any miss in the chain just omits the line.
	if in.SessionID != "" &&
		store.HookFileSeenInSession(in.SessionID, path) &&
		fileBytes <= repeatReadHashMaxBytes &&
		hookFileUnchanged(store, projectID, relPath, path) {
		msg += " Repeat read: already served this session, content unchanged — a re-read returns identical bytes."
	}
	return hookDecision{
		Continue:        true,
		SystemMessage:   msg,
		Decision:        "redirect_advisory",
		SuggestedTool:   "context",
		SuggestedArgs:   args,
		FilePathParsed:  path,
		FileBytes:       fileBytes,
		EstTokensServed: estServed,
		BaselineTokens:  baseline,
	}
}

// hookFileUnchanged reports whether the on-disk content of absPath
// still matches the hash the indexer recorded for (projectID, relPath).
// Same hash format the indexer writes: fmt.Sprintf("%x", xxh3.Hash(b)).
// Best-effort: unreadable file or missing stored hash → false.
func hookFileUnchanged(store *db.Store, projectID, relPath, absPath string) bool {
	stored := store.GetFileHash(projectID, relPath)
	if stored == "" {
		return false
	}
	b, err := os.ReadFile(absPath)
	if err != nil {
		return false
	}
	return fmt.Sprintf("%x", xxh3.Hash(b)) == stored
}

func decideGrepHook(store *db.Store, in hookCheckInput, debug bool) hookDecision {
	pattern, _ := in.ToolInput["pattern"].(string)
	if pattern == "" {
		return debugPass(debug, "no pattern", hookDecision{})
	}
	// Order matters: identifier check first because qualified ids
	// (`pkg.Bar`, `Class::method`) contain chars that the regex
	// metachar set treats as regex (`.`, `:`). If a pattern fits the
	// identifier shape exactly, it's not a regex regardless of which
	// chars appear inside it.
	if identifierPattern.MatchString(pattern) {
		// Fall through to redirect.
	} else if regexMetacharPattern.MatchString(pattern) {
		return debugPass(debug, "regex metachars in pattern", hookDecision{})
	} else if strings.Contains(pattern, " ") {
		return debugPass(debug, "phrase pattern", hookDecision{})
	} else {
		return debugPass(debug, "pattern not identifier-shaped", hookDecision{})
	}
	// Project gate: only useful if SOME project is indexed.
	projects, err := store.ListProjects()
	if err != nil || len(projects) == 0 {
		return debugPass(debug, "no indexed projects", hookDecision{})
	}

	args := fmt.Sprintf(`{"query":"%s"}`, pattern)
	// #1656 v0.86: Grep redirect is advisory, matching the Read path
	// (#1654). Blocking Grep broke the same Edit-prep loop (agent
	// runs Grep to confirm a string exists before editing; block
	// forces a search detour that may not surface the literal
	// match). Hint via systemMessage, pass through.
	msg := fmt.Sprintf(
		"Pincher hint: `%s` looks like an identifier — `search query=\"%s\"` gives BM25-ranked hits with snippets, often more useful than unranked grep matches. (Hook is advisory since v0.86 — Grep passes through.)",
		pattern, pattern,
	)
	return hookDecision{
		Continue:      true,
		SystemMessage: msg,
		Decision:      "redirect_advisory",
		SuggestedTool: "search",
		SuggestedArgs: args,
	}
}

// codeGlobExtensions are the file extensions for which a Glob pattern
// counts as "hunting for code" — the case where pincher's structural
// tools beat raw file listing. Conservative: an absent or unknown
// extension passes through.
var codeGlobExtensions = map[string]bool{
	".go": true, ".py": true, ".rs": true, ".rb": true,
	".ts": true, ".tsx": true, ".js": true, ".jsx": true,
	".java": true, ".kt": true, ".swift": true, ".cs": true,
	".php": true, ".c": true, ".h": true,
	".cpp": true, ".hpp": true, ".cc": true, ".hh": true,
}

// globTargetsCode reports whether a glob pattern's extension names a
// code file pincher indexes (`**/*.go`, `src/**/*.ts`, ...).
func globTargetsCode(pattern string) bool {
	return codeGlobExtensions[strings.ToLower(filepath.Ext(pattern))]
}

// matchIndexedDir resolves a directory to the indexed project that
// contains it — longest path prefix wins, mirroring matchIndexedFile's
// routing rule, but without requiring a file_hashes row (globs name
// directories, not indexed files). ok=false when the dir is outside
// every indexed project.
func matchIndexedDir(store *db.Store, dir string) (projectID string, ok bool) {
	clean := filepath.Clean(dir)
	projects, err := store.ListProjects()
	if err != nil {
		return "", false
	}
	bestLen := -1
	for _, p := range projects {
		base := filepath.Clean(p.Path)
		if clean != base && !strings.HasPrefix(clean, base+string(filepath.Separator)) {
			continue
		}
		if len(base) > bestLen {
			projectID, bestLen = p.ID, len(base)
		}
	}
	return projectID, bestLen >= 0
}

// decideGlobHook handles Glob PreToolUse calls. Glob is a
// file-discovery tool, so the pincher equivalent is structural —
// onboard_module for orientation, search for symbol lookup — rather
// than a byte-level redirect. Advisory-only, matching the v0.86
// Read/Grep posture (#1654/#1656): never block, hint when the glob
// targets code files inside an indexed project. A Glob with no `path`
// passes through silently: the hook doesn't know the agent's cwd, so
// it can't tell whether the glob lands in an indexed project.
func decideGlobHook(store *db.Store, in hookCheckInput, debug bool) hookDecision {
	pattern, _ := in.ToolInput["pattern"].(string)
	if pattern == "" {
		return debugPass(debug, "no glob pattern", hookDecision{})
	}
	dir, _ := in.ToolInput["path"].(string)
	if dir == "" {
		return debugPass(debug, "no path on glob (cwd unknown to hook)", hookDecision{})
	}
	if !globTargetsCode(pattern) {
		return debugPass(debug, "glob pattern not code-shaped", hookDecision{})
	}
	if _, ok := matchIndexedDir(store, dir); !ok {
		return debugPass(debug, "glob path not in any indexed project", hookDecision{})
	}

	// #2011: resolve the recommendation against the active toolset.
	// Under the v1.6 core-toolset default, onboard_module is not on
	// the session's tools/list and a tools/call against it returns
	// -32602 — an advisory must never recommend a tool the agent
	// cannot call. The hook is a subprocess and can only read its OWN
	// $PINCHER_TOOLSET; normally that matches the server's (both
	// inherit the user environment), but they can diverge when the
	// server was launched with an explicit --toolset/env override the
	// hook doesn't share. The mismatch case is asymmetric-safe by
	// construction: only a hook env that canonically says "full"
	// restores the onboard_module suggestion, and the fallback
	// recommendation (`search`) is advertised in BOTH modes — so the
	// worst divergence outcome is a core-safe hint on a full server,
	// never an uncallable one.
	if server.ToolAdvertised("onboard_module") {
		args := fmt.Sprintf(`{"directory":"%s"}`, dir)
		msg := fmt.Sprintf(
			"Pincher hint: %s is indexed — `onboard_module directory=\"%s\"` maps entry points, exports and consumers in one envelope, and `search` finds symbols by name with BM25 ranking. (Hook is advisory — Glob passes through.)",
			dir, dir,
		)
		return hookDecision{
			Continue:      true,
			SystemMessage: msg,
			Decision:      "redirect_advisory",
			SuggestedTool: "onboard_module",
			SuggestedArgs: args,
		}
	}
	msg := fmt.Sprintf(
		"Pincher hint: %s is indexed — `search` finds symbols by name with BM25 ranking, and `context` expands a hit to full source plus imports. (Module-level orientation via `onboard_module` is outside the core toolset: reach it as a `batch` sub-query, or restart the server with PINCHER_TOOLSET=full. Hook is advisory — Glob passes through.)",
		dir,
	)
	return hookDecision{
		Continue:      true,
		SystemMessage: msg,
		Decision:      "redirect_advisory",
		SuggestedTool: "search",
		SuggestedArgs: `{"query":"<symbol or keyword>"}`,
	}
}

// isTestFile reports whether a path looks like a test/spec source file
// using cross-language naming conventions. The hook's redirect to
// `context lite=true` is unhelpful when the agent is about to Edit the
// file — losing the literal byte content forces a follow-up Read that
// defeats the exemption. Recognized conventions (case-insensitive on
// the trailing segment):
//
//   - Go:     *_test.go
//   - Python: *_test.py / test_*.py
//   - Rust:   *_test.rs (also covers internal #[cfg(test)] modules
//     whose tests live in same file — those won't match)
//   - JS/TS:  *.test.js / *.test.ts / *.test.jsx / *.test.tsx /
//     *.spec.js / *.spec.ts / *.spec.jsx / *.spec.tsx
//   - Ruby:   *_spec.rb / *_test.rb
//   - Java:   *Test.java / *Tests.java / *IT.java
//   - Swift:  *Test.swift / *Tests.swift / *Spec.swift
//   - Kotlin: *Test.kt / *Tests.kt
//   - C#:     *Tests.cs / *Test.cs
//   - PHP:    *Test.php
//
// Also matches paths under common test directories regardless of file
// extension: `tests/`, `test/`, `__tests__/`, `spec/`, `e2e/`, `it/`.
// These cover Python `tests/`, JS `__tests__/`, Ruby `spec/`,
// Cypress `e2e/`, and similar.
//
// Designed to err on the side of MORE pass-through (false negatives on
// the hook). A non-test file matching the pattern is a tiny correctness
// loss (agent reads bytes pincher could have summarized); a real test
// file blocked here is a UX bug that compounds across every edit cycle.
func isTestFile(path string) bool {
	if path == "" {
		return false
	}
	clean := filepath.ToSlash(path)
	lower := strings.ToLower(clean)

	// Directory-segment match — matches a `/tests/`, `/test/`,
	// `/__tests__/`, `/spec/`, `/e2e/`, or `/it/` anywhere in the
	// path. Surrounded by slashes to avoid matching `testing.go` etc.
	for _, seg := range []string{"/tests/", "/test/", "/__tests__/", "/spec/", "/e2e/", "/it/"} {
		if strings.Contains(lower, seg) {
			return true
		}
	}
	// Match basename patterns. Use the lowered basename so case
	// variants (`*Test.java`, `*test.go`) both match.
	base := filepath.Base(lower)

	// Suffix patterns (`_test.go` etc).
	for _, suffix := range []string{
		"_test.go", "_test.py", "_test.rs", "_test.rb",
		"_spec.rb",
		".test.js", ".test.jsx", ".test.ts", ".test.tsx", ".test.mjs",
		".spec.js", ".spec.jsx", ".spec.ts", ".spec.tsx", ".spec.mjs",
		"test.java", "tests.java", "it.java",
		"test.swift", "tests.swift", "spec.swift",
		"test.kt", "tests.kt",
		"tests.cs", "test.cs",
		"test.php",
	} {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	// Prefix patterns (`test_*.py`).
	if strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py") {
		return true
	}
	return false
}

// isProseFile reports whether a path looks like prose / planning /
// docs content where `context lite=true` returns no useful symbols.
// The redirect would be active misinformation on these files —
// pincher indexes Markdown sections by heading, but lite-mode
// context strips the body, leaving the agent with just heading
// strings. Recognized:
//
//   - Markdown:  *.md / *.markdown / *.mdx
//   - RST:       *.rst
//   - Plain:     *.txt
//   - AsciiDoc:  *.adoc / *.asciidoc
//
// Plus path-segment match on directories that conventionally hold
// prose / planning artifacts: `.planning/`, `docs/`, `doc/`,
// `notes/`. Inside those directories every file passes through
// regardless of extension (the agent is reading documentation,
// not code).
//
// Errs toward MORE pass-through. A code file falsely flagged here
// is a tiny correctness loss; a prose file blocked is the v0.86
// dogfood-found friction we're shipping #1656 to eliminate.
func isProseFile(path string) bool {
	if path == "" {
		return false
	}
	clean := filepath.ToSlash(path)
	lower := strings.ToLower(clean)

	// Directory-segment match — try both embedded (`/docs/`) and
	// leading (`docs/`) variants so we catch paths regardless of
	// whether they're absolute or relative to a repo root.
	for _, seg := range []string{
		".planning/", ".planning-",
		"docs/", "doc/",
		"notes/", "note/",
	} {
		if strings.HasPrefix(lower, seg) {
			return true
		}
		if strings.Contains(lower, "/"+seg) {
			return true
		}
	}
	// .planning-foo.md at repo root: catches the basename variant
	// when the planning prefix appears mid-path or at root.
	if strings.HasPrefix(filepath.Base(lower), ".planning-") {
		return true
	}

	base := filepath.Base(lower)
	for _, suffix := range []string{
		".md", ".markdown", ".mdx",
		".rst",
		".txt",
		".adoc", ".asciidoc",
	} {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	return false
}

func emitHookResponse(d hookDecision) {
	resp := map[string]any{"continue": d.Continue}
	if d.StopReason != "" {
		resp["stopReason"] = d.StopReason
	}
	// #1654 v0.86: emit systemMessage on both pass-through and
	// blocking decisions. Advisory-mode redirects use
	// continue:true + systemMessage so the agent sees the nudge
	// without the Read being interrupted.
	if d.SystemMessage != "" {
		resp["systemMessage"] = d.SystemMessage
	}
	out, _ := json.Marshal(resp)
	os.Stdout.Write(out)
	os.Stdout.Write([]byte("\n"))
}

func emitPassThrough(debug bool, reason string) {
	if debug {
		fmt.Fprintln(os.Stderr, "pincher hook-check: pass through —", reason)
	}
	out, _ := json.Marshal(map[string]any{"continue": true})
	os.Stdout.Write(out)
	os.Stdout.Write([]byte("\n"))
}

func debugPass(debug bool, reason string, d hookDecision) hookDecision {
	if debug {
		fmt.Fprintln(os.Stderr, "pincher hook-check: pass through —", reason)
	}
	d.Continue = true
	d.Decision = "pass_through"
	return d
}

func logHookDecision(store *db.Store, in hookCheckInput, d hookDecision) {
	// Best-effort. Don't block the hook decision on a failed insert.
	_ = store.LogHookInvocation(db.HookInvocation{
		TS:              time.Now().UnixNano(),
		SessionID:       in.SessionID,
		ToolName:        in.ToolName,
		FilePath:        d.FilePathParsed,
		FileBytes:       d.FileBytes,
		Decision:        d.Decision,
		SuggestedTool:   d.SuggestedTool,
		SuggestedArgs:   d.SuggestedArgs,
		EstTokensServed: d.EstTokensServed,
		BaselineTokens:  d.BaselineTokens,
	})
}
