package server

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kwad77/pincher/internal/db"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// #1391 v0.82: plan_change — pre-edit blast-radius composite.
//
// Composite #2 of Phase 4. Replaces the canonical pre-edit
// confirmation sequence:
//
//   changes(unstaged=true)              → modified
//   trace(seed=changed_symbol, dir=in)  → callers up to depth 2
//   context(seed=top_caller)            → read call sites
//   adr(action=list)                    → check stored architectural
//                                          decisions for related rules
//
// Input: a file path or a symbol id. The composite resolves the
// target, traces inbound callers at depth 1 (CRITICAL) and depth 2
// (HIGH), partitions them by package boundary + test-file status,
// and looks up ADRs whose key or value mentions the target's
// directory/package. Emits a high-blast-radius warning when the
// depth-1 caller count exceeds a threshold so the agent can suggest
// a staged refactor before the edit.
//
// Contract per docs/integrations/composite-tool-roadmap.md:
//   - Additive: atomic tools stay callable unchanged.
//   - No internal MCP round-trips.
//   - Single envelope, single _meta block.
//   - empty_reason mandatory on zero-suspect / zero-target results.
//   - Idempotent: read-only.

// blastRow is one entry in the depth_1 / depth_2 caller lists.
type blastRow struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	QualifiedName string `json:"qualified_name"`
	Kind          string `json:"kind"`
	FilePath      string `json:"file_path"`
	StartLine     int    `json:"start_line"`
	Depth         int    `json:"depth"`
	ViaKind       string `json:"via_kind"`
	IsTest        bool   `json:"is_test"`
	CrossPackage  bool   `json:"cross_package"`
}

// adrMatch is one ADR record surfaced because its key or value
// mentions a keyword from the target's package or directory.
type adrMatch struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Why   string `json:"why"`
}

// targetSummary describes the resolved target — either a single
// symbol (when input was an id or single-symbol search hit) or a
// file with its symbols (when input was a file path).
type targetSummary struct {
	File                     string     `json:"file,omitempty"`
	SymbolsAffected          []blastRow `json:"symbols_affected"`
	SymbolsAffectedTotal     int        `json:"symbols_affected_total"`
	SymbolsAffectedTruncated bool       `json:"symbols_affected_truncated"`
	ResolutionPath           string     `json:"resolution_path"` // "symbol_id" | "file_path" | "name_search"
}

// maxAffectedSymbols bounds the file-path branch's symbol enumeration.
// A `plan_change` against a large file (server.go has hundreds of
// callable symbols) otherwise enumerated every one into
// target.symbols_affected AND fired an inbound trace per symbol — the
// #1727 cap bounded blast_radius but missed this list, so the envelope
// still blew the MCP token limit (90 KB on server.go) and the trace
// loop ran tens of seconds. symbols_affected_total keeps the true
// count; symbols_affected_truncated flags the cut.
const maxAffectedSymbols = 50

// maxRelatedADRs bounds the advisory ADR list. A file-shaped target in
// a package with many session-log ADRs matched dozens of full ADR
// bodies — a secondary contributor to the #1727 oversize envelope.
const maxRelatedADRs = 20

func (s *Server) handlePlanChange(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	start, tool, args := beginCall(req)
	// #1579 v0.82: composite cancellation contract. Entry-point check.
	if err := ctx.Err(); err != nil {
		return s.errResultRich("plan_change: ctx canceled before resolution", nil), nil
	}

	target := str(args, "target")
	if strings.TrimSpace(target) == "" {
		return s.errResultRich(
			"plan_change requires `target` — pass either a file path (e.g. `internal/auth/login.go`) or a symbol id (e.g. `internal/auth/login.go::auth.Login#Function`)",
			[]map[string]string{
				{"tool": "plan_change", "args": `{"target":"internal/auth/login.go"}`,
					"why": "file-path target — composite resolves every symbol in the file and traces all of them"},
				{"tool": "plan_change", "args": `{"target":"internal/auth/login.go::auth.Login#Function"}`,
					"why": "symbol-id target — traces just this symbol's callers"},
				{"tool": "plan_change", "args": `{"target":"Login"}`,
					"why": "name-search target — BM25 finds the symbol; useful when you don't have the full id handy"},
			},
		), nil
	}

	projectID, errRes := s.mustProject(args)
	if errRes != nil {
		return errRes, nil
	}

	depth := intArg(args, "depth", 2)
	if depth <= 0 {
		depth = 2
	}
	if depth > 4 {
		depth = 4
	}

	// ── Step 1: resolve the target ──────────────────────────────────────
	// Three shapes accepted (in priority order):
	//   1. Symbol id: contains "::" — direct lookup via GetSymbolScoped.
	//   2. File path: ends with a recognized extension OR matches a
	//      known indexed file — enumerate via GetSymbolsForFile.
	//   3. Free-form name: BM25 search across the code corpus; take the
	//      top callable hit as the target.
	resolved := targetSummary{
		SymbolsAffected: []blastRow{},
	}

	switch {
	case strings.Contains(target, "::"):
		// Symbol-id shape.
		resolved.ResolutionPath = "symbol_id"
		sym, err := s.store.GetSymbolScoped(projectID, target)
		if err == nil && sym != nil {
			resolved.File = sym.FilePath
			resolved.SymbolsAffected = append(resolved.SymbolsAffected, blastRow{
				ID:            sym.ID,
				Name:          sym.Name,
				QualifiedName: sym.QualifiedName,
				Kind:          sym.Kind,
				FilePath:      sym.FilePath,
				StartLine:     sym.StartLine,
			})
		}
	case looksLikeFilePath(target):
		// File-path shape.
		resolved.ResolutionPath = "file_path"
		resolved.File = target
		syms, err := s.store.GetSymbolsForFile(projectID, target)
		if err == nil {
			for _, sym := range syms {
				// Filter to callable kinds — Settings/Sections rarely
				// have callers and would dominate the blast radius
				// with non-actionable hits.
				switch sym.Kind {
				case "Function", "Method", "Class", "Interface", "Type":
					resolved.SymbolsAffectedTotal++
					// Cap the enumerated list — see maxAffectedSymbols.
					if len(resolved.SymbolsAffected) >= maxAffectedSymbols {
						resolved.SymbolsAffectedTruncated = true
						continue
					}
					resolved.SymbolsAffected = append(resolved.SymbolsAffected, blastRow{
						ID:            sym.ID,
						Name:          sym.Name,
						QualifiedName: sym.QualifiedName,
						Kind:          sym.Kind,
						FilePath:      sym.FilePath,
						StartLine:     sym.StartLine,
					})
				}
			}
		}
		// #1577: a bare filename with an extension (e.g. "charge.go")
		// matches looksLikeFilePath but never resolves via GetSymbolsForFile
		// because the symbols table holds the FULL path. The original code
		// fell through to the empty-suspects branch with diagnosis "target
		// did not resolve" — silent-confidently-wrong. Now: if the file-
		// path resolution found nothing AND the target has no slash, strip
		// the extension and fall back to BM25 so a bare name still resolves.
		// A real root-level file like `main.go` still resolves via the file
		// branch above (because it really IS the indexed path).
		if len(resolved.SymbolsAffected) == 0 && !strings.ContainsAny(target, "/\\") {
			resolved.ResolutionPath = "name_search_after_bare_filename"
			// Strip extension to give BM25 a clean identifier token.
			// `charge.go` → `charge`; FTS5 tokenisation matches `Charge`
			// via prefix/case-fold.
			bm25Query := target
			if dot := strings.LastIndex(bm25Query, "."); dot > 0 {
				bm25Query = bm25Query[:dot]
			}
			results, err := s.store.SearchSymbolsByCorpus(projectID, bm25Query, "", "", "code", 5)
			if err == nil {
				for _, r := range results {
					switch r.Symbol.Kind {
					case "Function", "Method", "Class", "Interface", "Type":
						resolved.SymbolsAffected = append(resolved.SymbolsAffected, blastRow{
							ID:            r.Symbol.ID,
							Name:          r.Symbol.Name,
							QualifiedName: r.Symbol.QualifiedName,
							Kind:          r.Symbol.Kind,
							FilePath:      r.Symbol.FilePath,
							StartLine:     r.Symbol.StartLine,
						})
						if resolved.File == target {
							resolved.File = r.Symbol.FilePath
						}
					}
					if len(resolved.SymbolsAffected) >= 3 {
						break
					}
				}
			}
		}
	default:
		// Free-form name — BM25 search.
		resolved.ResolutionPath = "name_search"
		results, err := s.store.SearchSymbolsByCorpus(projectID, target, "", "", "code", 5)
		if err == nil {
			for _, r := range results {
				switch r.Symbol.Kind {
				case "Function", "Method", "Class", "Interface", "Type":
					resolved.SymbolsAffected = append(resolved.SymbolsAffected, blastRow{
						ID:            r.Symbol.ID,
						Name:          r.Symbol.Name,
						QualifiedName: r.Symbol.QualifiedName,
						Kind:          r.Symbol.Kind,
						FilePath:      r.Symbol.FilePath,
						StartLine:     r.Symbol.StartLine,
					})
					if resolved.File == "" {
						resolved.File = r.Symbol.FilePath
					}
				}
				if len(resolved.SymbolsAffected) >= 3 {
					break
				}
			}
		}
	}

	// The id / name-search branches cap their own list small (1 / ≤3)
	// and don't set the total explicitly — backfill it so the field is
	// honest for every resolution path, not just file_path.
	if resolved.SymbolsAffectedTotal == 0 {
		resolved.SymbolsAffectedTotal = len(resolved.SymbolsAffected)
	}

	if len(resolved.SymbolsAffected) == 0 {
		meta := map[string]any{}
		// #1578 v0.82: target_not_resolved is the correct code here —
		// the input shape passed our resolution heuristic but didn't
		// match any indexed symbol. Distinct from no_results_in_corpus
		// (data isn't there); this is "your input was wrong, here's how
		// to fix it" — different recovery shape.
		stampEmpty(meta, EmptyReasonTargetNotResolved,
			fmt.Sprintf("target %q did not resolve in project %q — either the file/symbol is not indexed, or the resolution heuristic picked the wrong shape (id needs `::`, file needs extension, name falls through to BM25 search). Try a more specific target.", target, projectID))
		meta["next_steps"] = []map[string]string{
			{"tool": "search", "args": fmt.Sprintf(`{"query":%q}`, target),
				"why": "confirm what BM25 surfaces; if 0 hits, the symbol isn't indexed"},
			{"tool": "list", "args": `{}`,
				"why": "verify the right project is scoped"},
		}
		data := map[string]any{
			"target":       resolved,
			"blast_radius": emptyBlastRadius(),
			"related_adrs": []adrMatch{},
			"_meta":        meta,
		}
		return s.jsonResultWithMeta(data, start, tool, args, 0), nil
	}

	// ── Step 2: trace inbound callers per affected symbol ───────────────
	// Each call is depth-bounded; we de-dupe across affected symbols by
	// caller id, keeping the MIN depth across paths (closest impact).
	type callerHit struct {
		depth   int
		viaKind string
	}
	seen := map[string]callerHit{} // caller id -> closest recorded hit

	targetPackage := packageOfFile(resolved.File)

	for _, sym := range resolved.SymbolsAffected {
		// #1579: per-iteration cancellation check.
		if err := ctx.Err(); err != nil {
			break
		}
		hops, err := s.store.TraceViaCTEScoped(projectID, sym.ID, "inbound", []string{"CALLS"}, depth)
		if err != nil {
			continue
		}
		for _, h := range hops {
			if h.SymbolID == sym.ID {
				continue
			}
			// De-dupe — keep the closest path across all affected symbols.
			if prev, ok := seen[h.SymbolID]; ok && prev.depth <= h.Depth {
				continue
			}
			seen[h.SymbolID] = callerHit{depth: h.Depth, viaKind: h.ViaKind}
		}
	}

	// Batch-load caller metadata after tracing. The old path did one
	// GetSymbolScoped per hop; a file target can trace 50 affected symbols,
	// so this collapses the hot-path metadata load into one SQL round trip.
	callerIDs := make([]string, 0, len(seen))
	for id := range seen {
		callerIDs = append(callerIDs, id)
	}
	sort.Strings(callerIDs)
	callerSyms, err := s.store.GetSymbolsByIDs(projectID, callerIDs)
	if err != nil {
		callerSyms = map[string]*db.Symbol{}
	}
	depth1 := []blastRow{}
	depth2plus := []blastRow{}
	for _, id := range callerIDs {
		callerSym := callerSyms[id]
		if callerSym == nil {
			continue
		}
		hit := seen[id]
		callerPkg := packageOfFile(callerSym.FilePath)
		row := blastRow{
			ID:            callerSym.ID,
			Name:          callerSym.Name,
			QualifiedName: callerSym.QualifiedName,
			Kind:          callerSym.Kind,
			FilePath:      callerSym.FilePath,
			StartLine:     callerSym.StartLine,
			Depth:         hit.depth,
			ViaKind:       hit.viaKind,
			IsTest:        isTestFile(callerSym.FilePath),
			CrossPackage:  callerPkg != targetPackage && targetPackage != "" && callerPkg != "",
		}
		if hit.depth == 1 {
			depth1 = append(depth1, row)
		} else {
			depth2plus = append(depth2plus, row)
		}
	}

	// Stable ordering for deterministic tests + readability.
	sort.Slice(depth1, func(i, j int) bool { return depth1[i].QualifiedName < depth1[j].QualifiedName })
	sort.Slice(depth2plus, func(i, j int) bool {
		if depth2plus[i].Depth != depth2plus[j].Depth {
			return depth2plus[i].Depth < depth2plus[j].Depth
		}
		return depth2plus[i].QualifiedName < depth2plus[j].QualifiedName
	})

	// Test files crossing the call graph — surfaced separately so the
	// agent knows what to run before pushing the edit. Sourced from
	// BOTH depth1 and depth2plus.
	testFiles := map[string]bool{}
	for _, c := range depth1 {
		if c.IsTest {
			testFiles[c.FilePath] = true
		}
	}
	for _, c := range depth2plus {
		if c.IsTest {
			testFiles[c.FilePath] = true
		}
	}
	testFilesList := make([]string, 0, len(testFiles))
	for f := range testFiles {
		testFilesList = append(testFilesList, f)
	}
	sort.Strings(testFilesList)

	// Cross-package callers — explicit list so the agent can see
	// boundary crossings at a glance instead of grepping the
	// depth_1 list for `cross_package: true`.
	crossPackage := []blastRow{}
	for _, c := range depth1 {
		if c.CrossPackage {
			crossPackage = append(crossPackage, c)
		}
	}

	// ── Step 3: ADR keyword overlap ─────────────────────────────────────
	// Look up every ADR for the project; surface those whose key OR value
	// mentions the target's package, directory, or any affected symbol's
	// short name. Conservative case-insensitive substring match — ADRs
	// are operator-curated so false positives are bounded.
	relatedADRs := []adrMatch{}
	adrs, err := s.store.ListADRs(projectID)
	if err == nil && len(adrs) > 0 {
		// Build the keyword set: package, directory components, and
		// each affected symbol's name + qualified name.
		keywords := map[string]string{}
		if targetPackage != "" {
			keywords[strings.ToLower(targetPackage)] = "package match"
		}
		dirParts := strings.Split(filepath.ToSlash(filepath.Dir(resolved.File)), "/")
		for _, p := range dirParts {
			if len(p) >= 4 && p != "internal" && p != "cmd" && p != "pkg" && p != "src" {
				keywords[strings.ToLower(p)] = "directory match"
			}
		}
		for _, sym := range resolved.SymbolsAffected {
			if len(sym.Name) >= 4 {
				keywords[strings.ToLower(sym.Name)] = "symbol-name match"
			}
		}
		// Deterministic iteration order over the ADR map.
		var keys []string
		for k := range adrs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if len(relatedADRs) >= maxRelatedADRs {
				break
			}
			v := adrs[k]
			combined := strings.ToLower(k + " " + v)
			for kw, why := range keywords {
				if strings.Contains(combined, kw) {
					relatedADRs = append(relatedADRs, adrMatch{Key: k, Value: v, Why: why + ": " + kw})
					break // one match per ADR; first wins
				}
			}
		}
	}

	// Bound the caller lists for transport. plan_change against a file
	// full of hotspots (an indexer with Index/New, a server with its
	// handler set) traces thousands of unique inbound callers; the
	// uncapped envelope blew past the MCP token limit (#1727,
	// same class as #1723). The summary keeps the TRUE counts; the
	// rendered lists are truncated, with blast_radius.truncated set so
	// the consumer knows the lists are shorter than the counts. Fixed
	// cap — no new tool-contract parameter (v1.0 surface freeze).
	// #1788: 50 rows × 3 lists is ~45 KB of blastRow JSON on a hub
	// symbol — `plan_change db.Open` hit 61 KB and exceeded the MCP
	// per-call token cap, so the agent got nothing usable on exactly
	// the symbols it most needs to plan around. The summary block keeps
	// the true counts; the per-row lists past ~25 add no decision value
	// once `blast_radius_high` has already fired. test_files_intersecting
	// was entirely uncapped — a hub is exercised by hundreds of test
	// files.
	const maxBlastRows = 25
	depth1Total := len(depth1)
	depth2Total := len(depth2plus)
	crossPkgTotal := len(crossPackage)
	testFilesTotal := len(testFilesList)
	blastTruncated := false
	if len(depth1) > maxBlastRows {
		depth1 = depth1[:maxBlastRows]
		blastTruncated = true
	}
	if len(depth2plus) > maxBlastRows {
		depth2plus = depth2plus[:maxBlastRows]
		blastTruncated = true
	}
	if len(crossPackage) > maxBlastRows {
		crossPackage = crossPackage[:maxBlastRows]
		blastTruncated = true
	}
	if len(testFilesList) > maxBlastRows {
		testFilesList = testFilesList[:maxBlastRows]
		blastTruncated = true
	}

	// ── Step 4: assemble envelope + warnings ────────────────────────────
	const highBlastThreshold = 14 // per the roadmap's example
	meta := map[string]any{}
	var warnings []map[string]any
	if depth1Total > highBlastThreshold {
		warnings = append(warnings, map[string]any{
			"code":                  "blast_radius_high",
			"severity":              "warning",
			"message":               fmt.Sprintf("depth-1 caller count is %d (threshold %d) — consider a staged refactor or interface-shim before the edit", depth1Total, highBlastThreshold),
			"depth_1_caller_count":  depth1Total,
			"threshold":             highBlastThreshold,
			"cross_package_callers": crossPkgTotal,
			"suggestion":            "consider staged refactor",
		})
	}
	if len(warnings) > 0 {
		meta["warnings_v2"] = warnings
	}
	meta["next_steps"] = []map[string]string{
		{"tool": "context", "args": fmt.Sprintf(`{"id":%q}`, resolved.SymbolsAffected[0].ID),
			"why": "read the target's source before making the edit"},
		{"tool": "changes", "args": `{"scope":"unstaged"}`,
			"why": "check what's already modified in the working tree before adding more"},
	}

	data := map[string]any{
		"target": resolved,
		"blast_radius": map[string]any{
			"depth_1_callers":         depth1,
			"depth_2_callers":         depth2plus,
			"cross_package":           crossPackage,
			"test_files_intersecting": testFilesList,
			"summary": map[string]any{
				"depth_1_count":       depth1Total,
				"depth_2_count":       depth2Total,
				"cross_package_count": crossPkgTotal,
				"test_file_count":     testFilesTotal,
			},
			// True when depth_*_callers / cross_package lists were
			// truncated to maxBlastRows — the *_count fields above
			// remain the full totals.
			"truncated": blastTruncated,
		},
		"related_adrs": relatedADRs,
		"_meta":        meta,
	}
	return s.jsonResultWithMeta(data, start, tool, args, 0), nil
}

// looksLikeFilePath returns true when the target string ends with a
// recognized source extension OR contains a directory separator. The
// heuristic is conservative — we'd rather fall through to BM25 search
// on an ambiguous input than treat it as a file and miss everything.
func looksLikeFilePath(s string) bool {
	if strings.ContainsAny(s, "/\\") {
		return true
	}
	known := []string{
		".go", ".py", ".js", ".ts", ".jsx", ".tsx", ".rb", ".rs",
		".java", ".cs", ".kt", ".swift", ".php", ".cpp", ".c", ".h",
		".hpp", ".cc", ".lua", ".scala", ".ex", ".exs", ".zig",
		".dart", ".r", ".yaml", ".yml", ".json", ".toml", ".hcl",
		".tf", ".md", ".markdown", ".sh", ".bash", ".sql", ".html",
		".xml",
	}
	lower := strings.ToLower(s)
	for _, ext := range known {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// packageOfFile returns the immediate parent directory of a file
// path — used as a coarse "same package" signal for cross-package
// classification. We do NOT parse the file to read the actual
// `package` declaration; the path-based heuristic is cheap and
// correct for the typical Go/Python/Java layout where one
// directory = one package/module.
//
// Backslashes are normalised to forward slashes BEFORE any path
// operation so the function returns the same value on Linux,
// macOS, and Windows. filepath.Dir is OS-specific and returns "."
// for backslash-only paths on non-Windows, which would silently
// drop the cross_package signal for paths that arrived from a
// Windows client through the indexer.
func packageOfFile(fp string) string {
	if fp == "" {
		return ""
	}
	normalised := strings.ReplaceAll(fp, `\`, "/")
	dir := path.Dir(normalised)
	if dir == "." || dir == "/" {
		return ""
	}
	parts := strings.Split(dir, "/")
	return parts[len(parts)-1]
}

// plan_change reuses the existing isTestFile helper from server.go
// rather than maintaining its own copy — that helper has wider
// language coverage (covers __tests__/ dirs, tests/ prefix, _spec.rb,
// .test.mjs/cjs, etc.) and is already the canonical source of truth
// for test-file classification across the codebase.

func emptyBlastRadius() map[string]any {
	return map[string]any{
		"depth_1_callers":         []blastRow{},
		"depth_2_callers":         []blastRow{},
		"cross_package":           []blastRow{},
		"test_files_intersecting": []string{},
		"summary": map[string]any{
			"depth_1_count":       0,
			"depth_2_count":       0,
			"cross_package_count": 0,
			"test_file_count":     0,
		},
		"truncated": false,
	}
}
