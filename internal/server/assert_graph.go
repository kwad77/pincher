// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/kwad77/pincher/internal/db"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// assert_graph.go — conclusion-density primitive (sibling of trace's /
// search's count_only). Compute below the envelope is token-free; only
// conclusions pay. Agents kept pulling N caller rows just to conclude
// "nothing violates the rule" — assert_graph evaluates the invariant
// entirely server-side against the edge graph and returns the verdict:
// `{pass, checked}` when passing (a two-token conclusion), plus
// `violations` (capped at 10 `{id, file_path}` rows) when failing.
//
// The kind set is deliberately closed at four — a small catalog the
// rich-error can teach in full (failure-as-pedagogy house style):
//
//	no_callers_outside  every caller of target lives under a scope prefix
//	max_callers         target has at most `limit` direct callers
//	no_calls_to         nothing under scope calls target (layering rule)
//	exists              target resolves to >=1 indexed symbol
//
// Caller-shaped kinds count DIRECT inbound CALLS-family edges
// (CALLS/HTTP_CALLS/ASYNC_CALLS) — depth 1, same family trace defaults
// to. Test files are NOT filtered: an assertion is a statement about
// the whole graph, and a test caller outside the allowed scope is a
// real violation of "no callers outside".

// assertGraphViolationsCap bounds the violations list. The conclusion
// is "it fails, here's where to start" — ten strays is plenty to act
// on; the full count rides in checked + the _meta warning.
const assertGraphViolationsCap = 10

// assertGraphKinds is the closed catalog: kind → one-line contract.
// Rendered into the rich-error on unknown kinds so the failure teaches
// the full surface in one round-trip.
var assertGraphKinds = map[string]string{
	"no_callers_outside": "pass when every direct caller of `target` lives under one of the `scope` path prefixes; violations are the strays",
	"max_callers":        "pass when `target` has at most `limit` direct callers; violations list the callers when over",
	"no_calls_to":        "pass when nothing under the `scope` path prefixes calls `target` (layering rule); violations are the in-scope callers",
	"exists":             "pass when `target` resolves to at least one indexed symbol (exact name first, FTS5 search fallback)",
}

// assertGraphCatalogSteps renders the full kind catalog as next_steps —
// the rich-error payload for unknown/missing kinds. Deterministic order.
func assertGraphCatalogSteps() []map[string]string {
	exampleArgs := map[string]string{
		"no_callers_outside": `{"kind":"no_callers_outside","target":"db.Open","scope":"cmd/,internal/server/"}`,
		"max_callers":        `{"kind":"max_callers","target":"db.Open","limit":5}`,
		"no_calls_to":        `{"kind":"no_calls_to","target":"db.Open","scope":"internal/extract/"}`,
		"exists":             `{"kind":"exists","target":"handleSearch"}`,
	}
	kinds := make([]string, 0, len(assertGraphKinds))
	for k := range assertGraphKinds {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	steps := make([]map[string]string, 0, len(kinds))
	for _, k := range kinds {
		steps = append(steps, map[string]string{
			"tool": "assert_graph",
			"args": exampleArgs[k],
			"why":  assertGraphKinds[k],
		})
	}
	return steps
}

// assertCaller is one direct caller of the assertion target.
type assertCaller struct {
	ID       string
	FilePath string
}

// parseAssertScope splits the comma-separated scope arg into cleaned
// path prefixes. Empty/whitespace entries and leading "./" are dropped
// so 'cmd/, ./internal/server/' behaves as the caller means.
func parseAssertScope(scope string) []string {
	var prefixes []string
	for _, p := range strings.Split(scope, ",") {
		p = strings.TrimSpace(p)
		p = strings.TrimPrefix(p, "./")
		if p != "" {
			prefixes = append(prefixes, p)
		}
	}
	return prefixes
}

// underAnyPrefix reports whether path lives under any of the prefixes.
// A prefix without a trailing slash matches as a path SEGMENT prefix
// ("cmd" matches "cmd/main.go" but not "cmdx/main.go") or the exact
// file itself.
func underAnyPrefix(path string, prefixes []string) bool {
	path = strings.TrimPrefix(path, "./")
	for _, p := range prefixes {
		if strings.HasSuffix(p, "/") {
			if strings.HasPrefix(path, p) {
				return true
			}
			continue
		}
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}

// resolveAssertTarget resolves the assertion target to one symbol.
// IDs ('::' present) resolve scoped; short names go through the same
// candidate ranking trace uses (callable kinds preferred over Modules)
// so "db.Open"-style targets land on the function, not a same-named
// module. Returns a rich-error result (not an error) on miss, matching
// the house not-found shape.
func (s *Server) resolveAssertTarget(projectID, target string) (*db.Symbol, *mcp.CallToolResult) {
	if strings.Contains(target, "::") {
		sym, err := s.store.GetSymbolScoped(projectID, target)
		if err == nil && sym != nil {
			return sym, nil
		}
		return nil, s.errResultRich(
			fmt.Sprintf("assert_graph: symbol id %q not found in this project", target),
			[]map[string]string{
				{"tool": "search", "args": fmt.Sprintf(`{"query":%q}`, shortNameFromID(target)),
					"why": "id resolution failed — search by short name to find the current id"},
				{"tool": "list", "args": "{}",
					"why": "if no project matches, the right project may not be indexed"},
			})
	}
	candidates, err := s.store.GetSymbolsByName(projectID, target, 50)
	if err != nil {
		return nil, errResult(fmt.Sprintf("assert_graph lookup: %v", err))
	}
	// Dotted targets ("db.Open") are qualified names, not short names —
	// look up by the last segment and keep only candidates whose
	// qualified_name actually carries the qualifier. No loose fallback:
	// silently asserting about pkga.Open when the caller wrote pkgb.Open
	// would be confidently wrong.
	if len(candidates) == 0 && strings.Contains(target, ".") {
		short := target[strings.LastIndex(target, ".")+1:]
		if byShort, shortErr := s.store.GetSymbolsByName(projectID, short, 50); shortErr == nil {
			for _, c := range byShort {
				if c.QualifiedName == target || strings.HasSuffix(c.QualifiedName, "."+target) {
					candidates = append(candidates, c)
				}
			}
		}
	}
	if len(candidates) == 0 {
		return nil, s.errResultRich(
			fmt.Sprintf("assert_graph: symbol %q not found in project", target),
			[]map[string]string{
				{"tool": "search", "args": fmt.Sprintf(`{"query":%q}`, target),
					"why": "name resolution failed — search to find similar / case-correct matches, then re-assert with the exact id"},
				{"tool": "assert_graph", "args": fmt.Sprintf(`{"kind":"exists","target":%q}`, target),
					"why": "or downgrade to an existence check if presence is the actual question"},
			})
	}
	sortTraceCandidates(candidates)
	return &candidates[0], nil
}

// directCallersOf returns the deduplicated direct (depth-1) inbound
// CALLS-family callers of a symbol, sorted by id for deterministic
// violations output. File paths come from the caller symbol row when
// it resolves; the id's path prefix is the fallback (an edge can
// outlive its from-symbol row mid-reindex).
func (s *Server) directCallersOf(projectID, targetID string) ([]assertCaller, error) {
	edges, err := s.store.EdgesToScoped(projectID, targetID, []string{"CALLS", "HTTP_CALLS", "ASYNC_CALLS"})
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	callers := make([]assertCaller, 0, len(edges))
	for _, e := range edges {
		if e.FromID == "" || seen[e.FromID] {
			continue
		}
		seen[e.FromID] = true
		filePath := ""
		if sym, symErr := s.store.GetSymbolScoped(projectID, e.FromID); symErr == nil && sym != nil {
			filePath = sym.FilePath
		}
		if filePath == "" {
			// Stable-ID format is '{file_path}::{qualified_name}#{kind}'.
			if i := strings.Index(e.FromID, "::"); i > 0 {
				filePath = e.FromID[:i]
			}
		}
		callers = append(callers, assertCaller{ID: e.FromID, FilePath: filePath})
	}
	sort.Slice(callers, func(i, j int) bool { return callers[i].ID < callers[j].ID })
	return callers, nil
}

func (s *Server) handleAssertGraph(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	start, tool, args := beginCall(req)
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	kind := str(args, "kind")
	target := strings.TrimSpace(str(args, "target"))

	// Unknown (or missing) kind → the full catalog. The kind set is
	// closed at exactly four; the error is the documentation.
	if _, known := assertGraphKinds[kind]; !known {
		msg := fmt.Sprintf("assert_graph: unknown kind %q — the catalog is exactly: exists, max_callers, no_callers_outside, no_calls_to (see next_steps for each contract)", kind)
		if kind == "" {
			msg = "assert_graph requires `kind` — the catalog is exactly: exists, max_callers, no_callers_outside, no_calls_to (see next_steps for each contract)"
		}
		return s.errResultRich(msg, assertGraphCatalogSteps()), nil
	}
	if target == "" {
		return s.errResultRich(
			"assert_graph requires `target` — a stable symbol id ('{file_path}::{qualified_name}#{kind}') or a short name",
			assertGraphCatalogSteps()), nil
	}

	projectID, errRes := s.mustProject(args)
	if errRes != nil {
		return errRes, nil
	}

	// Per-kind argument contracts, validated before any graph work.
	scopePrefixes := parseAssertScope(str(args, "scope"))
	if (kind == "no_callers_outside" || kind == "no_calls_to") && len(scopePrefixes) == 0 {
		return s.errResultRich(
			fmt.Sprintf("assert_graph kind=%q requires `scope` — comma-separated path prefix(es), e.g. \"cmd/,internal/server/\"", kind),
			[]map[string]string{
				{"tool": "assert_graph", "args": fmt.Sprintf(`{"kind":%q,"target":%q,"scope":"cmd/,internal/server/"}`, kind, target),
					"why": assertGraphKinds[kind]},
			}), nil
	}
	limit := intArg(args, "limit", -1)
	if kind == "max_callers" && limit < 0 {
		return s.errResultRich(
			"assert_graph kind=\"max_callers\" requires `limit` — the maximum number of direct callers allowed (inclusive, >= 0)",
			[]map[string]string{
				{"tool": "assert_graph", "args": fmt.Sprintf(`{"kind":"max_callers","target":%q,"limit":5}`, target),
					"why": assertGraphKinds["max_callers"]},
			}), nil
	}

	// exists — presence check, no edge traversal. Exact-name lookup
	// first (cheap, index-backed); FTS5 search fallback covers phrase
	// targets like "loop_checkpoints handler".
	if kind == "exists" {
		matches, err := s.store.GetSymbolsByName(projectID, target, assertGraphViolationsCap)
		if err != nil {
			return errResult(fmt.Sprintf("assert_graph: %v", err)), nil
		}
		checked := len(matches)
		if checked == 0 {
			results, searchErr := s.store.SearchSymbolsByCorpus(projectID, sanitizeFTS5Query(target), "", "", "", assertGraphViolationsCap)
			if searchErr == nil {
				checked = len(results)
			}
		}
		data := map[string]any{
			"kind":    kind,
			"target":  target,
			"pass":    checked > 0,
			"checked": checked,
			"_meta":   map[string]any{},
		}
		if checked == 0 {
			data["_meta"] = map[string]any{
				"next_steps": []map[string]string{
					{"tool": "search", "args": fmt.Sprintf(`{"query":%q}`, target+"*"),
						"why": "nothing matched exactly — a prefix search surfaces near-misses (rename? typo?)"},
				},
			}
		}
		return s.jsonResultWithMeta(data, start, tool, args, 0), nil
	}

	// Caller-shaped kinds share one store path: resolve the target,
	// list its direct inbound CALLS-family callers, classify.
	sym, errRes2 := s.resolveAssertTarget(projectID, target)
	if errRes2 != nil {
		return errRes2, nil
	}
	callers, err := s.directCallersOf(projectID, sym.ID)
	if err != nil {
		return errResult(fmt.Sprintf("assert_graph: %v", err)), nil
	}

	var violations []assertCaller
	pass := false
	switch kind {
	case "no_callers_outside":
		for _, c := range callers {
			if !underAnyPrefix(c.FilePath, scopePrefixes) {
				violations = append(violations, c)
			}
		}
		pass = len(violations) == 0
	case "no_calls_to":
		for _, c := range callers {
			if underAnyPrefix(c.FilePath, scopePrefixes) {
				violations = append(violations, c)
			}
		}
		pass = len(violations) == 0
	case "max_callers":
		pass = len(callers) <= limit
		if !pass {
			violations = callers
		}
	}

	meta := map[string]any{}
	data := map[string]any{
		"kind":    kind,
		"target":  sym.ID,
		"pass":    pass,
		"checked": len(callers),
		"_meta":   meta,
	}
	if !pass {
		totalViolations := len(violations)
		if totalViolations > assertGraphViolationsCap {
			violations = violations[:assertGraphViolationsCap]
			meta["warnings"] = []string{fmt.Sprintf(
				"violations trimmed to %d of %d — `checked` carries the full caller count", assertGraphViolationsCap, totalViolations)}
		}
		rows := make([]map[string]any, 0, len(violations))
		for _, v := range violations {
			rows = append(rows, map[string]any{"id": v.ID, "file_path": v.FilePath})
		}
		data["violations"] = rows
		data["violations_total"] = totalViolations
	}
	return s.jsonResultWithMeta(data, start, tool, args, 0), nil
}
