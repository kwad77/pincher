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
//   - pass through if offset/limit already used (agent narrowed)
//   - pass through if symbol count < 5 (config blobs)
//   - otherwise redirect to `context id=<best> lite=true`
//
// Decision logic for Grep (#630):
//   - redirect when pattern is a single CamelCase / dotted identifier
//     on an indexed project
//   - pass through on regex / quoted phrase / multi-file glob shapes
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

	decision := decideHook(store, input, *debug)
	logHookDecision(store, input, decision)
	emitHookResponse(decision)
}

// hookCheckInput mirrors Claude Code's PreToolUse hook payload shape.
// Only the fields we read are declared; the rest are ignored.
type hookCheckInput struct {
	ToolName  string         `json:"tool_name"`
	ToolInput map[string]any `json:"tool_input"`
	SessionID string         `json:"session_id"`
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
	default:
		return hookDecision{Continue: true, Decision: "pass_through"}
	}
}

func decideReadHook(store *db.Store, in hookCheckInput, debug bool) hookDecision {
	path, _ := in.ToolInput["file_path"].(string)
	if path == "" {
		return debugPass(debug, "no file_path", hookDecision{FilePathParsed: ""})
	}
	// Offset/limit already used → agent narrowed; don't override.
	if _, hasOffset := in.ToolInput["offset"]; hasOffset {
		return debugPass(debug, "offset already set", hookDecision{FilePathParsed: path})
	}
	if _, hasLimit := in.ToolInput["limit"]; hasLimit {
		return debugPass(debug, "limit already set", hookDecision{FilePathParsed: path})
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
		return debugPass(debug, "not in any indexed project", hookDecision{FilePathParsed: path})
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
	bestID, err := store.LargestSymbolInFile(projectID, relPath)
	if err != nil || bestID == "" {
		return debugPass(debug, "no resolvable symbol id",
			hookDecision{FilePathParsed: path, FileBytes: fileBytes})
	}

	args := fmt.Sprintf(`{"id":"%s","lite":true}`, bestID)
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
		"Pincher hint: this file is indexed (%d bytes). For navigation, `context id=%s lite=true` is ~80%% cheaper. (Hook is advisory since v0.86 — Read passes through to support Edit-prep workflows.)",
		fileBytes, bestID,
	)
	return hookDecision{
		Continue:       true,
		SystemMessage:  msg,
		Decision:       "redirect_advisory",
		SuggestedTool:  "context",
		SuggestedArgs:  args,
		FilePathParsed: path,
		FileBytes:      fileBytes,
	}
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
//             whose tests live in same file — those won't match)
//   - JS/TS:  *.test.js / *.test.ts / *.test.jsx / *.test.tsx /
//             *.spec.js / *.spec.ts / *.spec.jsx / *.spec.tsx
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
		TS:            time.Now().UnixNano(),
		SessionID:     in.SessionID,
		ToolName:      in.ToolName,
		FilePath:      d.FilePathParsed,
		FileBytes:     d.FileBytes,
		Decision:      d.Decision,
		SuggestedTool: d.SuggestedTool,
		SuggestedArgs: d.SuggestedArgs,
	})
}
