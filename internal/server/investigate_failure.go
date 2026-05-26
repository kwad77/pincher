package server

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// #1391 v0.81: investigate_failure — the bug-hunt composite.
//
// Composite #1 of Phase 4. Replaces the canonical bug-investigation
// sequence (typically ~5 atomic calls):
//
//   search(text=stack_top_frame)        → implicated symbols
//   trace(seed=symbol, direction=in)    → callers up to depth 2
//   changes(scope=branch)               → recent edits in implicated files
//   context(seed=top-ranked symbol)     → source for diff review
//
// Today the agent maintains stack-frame-to-symbol mapping state across
// 4 separate calls. The composite holds that state for one round-trip
// and emits a *ranked* list with per-suspect evidence so the agent can
// triage without re-running individual traces to verify.
//
// Contract per docs/integrations/composite-tool-roadmap.md:
//   - Additive: atomic tools stay callable unchanged.
//   - No internal MCP round-trips: direct SQL + in-process scoring.
//   - Single envelope, single _meta block.
//   - empty_reason mandatory on zero-suspect results.
//   - Idempotent: read-only, no writes to SQLite.

// stackFrameRE matches identifier-shaped tokens that look like symbol
// names in a stack trace. Conservative: anchored on word boundaries,
// requires at least one lowercase letter so we don't pull in random
// CONSTANTS or all-caps acronyms; allows dotted package paths
// (`pkg.Func`), dotted instance methods (`obj.method`), and bare names.
//
// We deliberately do NOT try to parse the full file:line shape — that
// varies wildly across languages and the symbol name is the load-bearing
// signal for BM25 scoring. File-path correlation comes from the search
// hit's own file_path, not the trace frame's.
var stackFrameRE = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*)\b`)

// stackFilePathRE matches file:line patterns commonly emitted by Go /
// Node / Java stack traces. Captures the file path so the recent-
// change overlap step can intersect on stack-implicated files even
// when no symbol-name match landed there.
//
// Patterns matched:
//   - Go runtime: `/path/to/file.go:42`
//   - Node: `at foo (/path/to/file.js:10:5)`
//   - Java: `at com.foo.Bar (Bar.java:42)`
var stackFilePathRE = regexp.MustCompile(`([A-Za-z]?[/\\]?[A-Za-z0-9_./\\-]*\.(?:go|py|js|ts|jsx|tsx|rb|rs|java|cs|kt|swift|php|cpp|c|h|hpp|cc|lua|scala|ex|exs|zig|dart|r)):(\d+)`)

// pythonFilePathRE matches Python's quoted-file traceback shape, which
// doesn't fit the `path:line` model — the file path is quoted and the
// line number arrives later as `, line N`. The composite would
// otherwise miss Python tracebacks despite parsing their function
// names cleanly via stackFrameRE.
//
// Matches:
//
//	File "/app/handler.py", line 42, in process_order
var pythonFilePathRE = regexp.MustCompile(`File\s+"([^"]+\.py)"`)

// stopwordFrames are tokens commonly appearing in stack traces that
// aren't symbol names. Filtering them out before BM25 prevents the
// composite from chasing low-signal noise — every Go panic includes
// `panic`, `runtime`, `goroutine`, etc., and those words would
// dominate the BM25 scoring otherwise.
var stopwordFrames = map[string]bool{
	"panic":        true,
	"runtime":      true,
	"goroutine":    true,
	"Error":        true,
	"error":        true,
	"Exception":    true,
	"exception":    true,
	"Traceback":    true,
	"traceback":    true,
	"at":           true,
	"in":           true,
	"line":         true,
	"File":         true,
	"file":         true,
	"main":         true, // too generic; only useful when explicit prefix exists
	"init":         true,
	"go":           true,
	"true":         true,
	"false":        true,
	"null":         true,
	"nil":          true,
	"None":         true,
	"undefined":    true,
	"TypeError":    true,
	"ValueError":   true,
	"KeyError":     true,
	"IndexError":   true,
	"NameError":    true,
	"RuntimeError": true,
	// #1787: Go runtime panic-message vocabulary. "index out of range
	// [3] with length 3" / "slice bounds out of range" / "invalid
	// memory address or nil pointer dereference" / "goroutine N
	// [running]" appear verbatim in nearly every Go panic — these
	// English words are never the load-bearing symbol token but BM25-
	// match unrelated symbols whose signature happens to contain them
	// (e.g. `out` matched `runGoInstall(out io.Writer)`).
	"out":         true,
	"range":       true,
	"with":        true,
	"length":      true,
	"bounds":      true,
	"running":     true,
	"created":     true,
	"address":     true,
	"dereference": true,
	// The rest of the Go SIGSEGV panic header. A nil-pointer-dereference
	// panic prints `invalid memory address or nil pointer dereference
	// [signal SIGSEGV: segmentation violation code=0x1 addr=0x0 pc=0x..]`
	// — #1787 caught `address`/`dereference` but the remaining header
	// words leak as identifier-shaped tokens and BM25-match unrelated
	// symbols (`violation` matched a workflow-lint Class; `signal` /
	// `memory` match broadly). This is the single most common Go crash
	// shape, so the whole header vocabulary has to go.
	"invalid":      true,
	"memory":       true,
	"pointer":      true,
	"signal":       true,
	"SIGSEGV":      true,
	"sigsegv":      true,
	"SIGABRT":      true,
	"SIGBUS":       true,
	"segmentation": true,
	"violation":    true,
	"code":         true,
	"addr":         true,
	// #1787: VCS-host / import-path host segments. Every Go stack frame
	// is fully qualified (`github.com/<org>/<repo>/...`); these host
	// tokens appear in every frame and are never symbol names. Interior
	// path segments (org / repo / `internal`) are dropped structurally
	// by the slash-delimited filter in parseStackFrames.
	"github":    true,
	"gitlab":    true,
	"bitbucket": true,
	"golang":    true,
	"gopkg":     true,
	// #1880: Go testing package framework vocabulary. Every Go test
	// failure emits `--- FAIL: TestX`, `--- PASS: TestX`, `=== RUN`,
	// `FAIL\t<pkg>\t<seconds>s`, `exit status N`. These tokens are
	// shape-mandatory output, not domain identifiers, and they
	// BM25-match unrelated symbols whose names happen to be common
	// English verbs (`compare` matched cmd/benchcmp.compare from
	// "fetching origin to compare..."; `fetch` matched
	// install.sh::install.fetch from "git fetch"; `FAIL` matched
	// scoop-manifest_test.fail). Tokens like `compare` / `fetch` /
	// `check` / `update` are too risky to filter universally (a real
	// failure site could legitimately be named `compare`), so we
	// limit ourselves to the testing-package output vocabulary.
	"FAIL":   true,
	"PASS":   true,
	"SKIP":   true,
	"RUN":    true,
	"PAUSE":  true,
	"status": true,
	"exit":   true,
}

// parseStackFrames extracts candidate symbol names and file paths from
// a raw error/stack-trace string. Returns names ranked by frequency
// (more occurrences = stronger signal). Names that appear ONLY inside
// stopword set are filtered. The file paths return ALL matches in
// trace order (top-of-stack first) since recency-of-frame is the
// strongest implication signal.
func parseStackFrames(errorText string) (names []string, files []string) {
	if errorText == "" {
		return nil, nil
	}

	// Count occurrences so we rank by how often a name appears in the
	// stack. A function that appears 5 times in the trace is more
	// likely the failure site than one that appears once.
	nameCounts := map[string]int{}
	for _, idx := range stackFrameRE.FindAllStringSubmatchIndex(errorText, -1) {
		// idx = [fullStart, fullEnd, group1Start, group1End].
		tok := errorText[idx[2]:idx[3]]
		// #1787: drop slash-delimited path segments. A Go stack frame is
		// `host/org/repo/internal/pkg.Func` — the org / repo / internal
		// segments sit between slashes and are never symbol names, yet
		// they appear in every frame and BM25-match unrelated symbols
		// (the repo name `pincher` matched a Homebrew Ruby class). A
		// token followed by `/` is a path prefix; a token with `/` on
		// BOTH sides is an interior segment — drop both. The trailing
		// `pkg.Func` token is preceded by `/` but followed by `.`/`(`,
		// so it survives.
		var before, after byte
		if idx[2] > 0 {
			before = errorText[idx[2]-1]
		}
		if idx[3] < len(errorText) {
			after = errorText[idx[3]]
		}
		// A token immediately followed by '-' is a segment of a
		// hyphenated path component (`pincher-repo`, `my-service`) — never
		// a Go/Python identifier, since neither language allows '-' in a
		// name. The slash-delimited drop misses these: the hyphen sits
		// where the next slash would, so a local checkout path like
		// `/home/me/pincher-repo/internal/...` leaks `pincher`, which then
		// BM25-matches an unrelated symbol (the Homebrew `Pincher` class).
		if after == '/' || after == '-' || (before == '/' && after == '/') {
			continue
		}
		if stopwordFrames[tok] {
			continue
		}
		// Skip pure-numeric remainders and very short tokens — too
		// noisy. Single-character names are valid Go but never load-
		// bearing in a stack trace context.
		if len(tok) < 3 {
			continue
		}
		// If the token has a dot, also count each component — `auth.Login`
		// should contribute to both `auth.Login` and `Login` so BM25
		// finds the symbol whether it was extracted as a qualified or
		// short name. The dotted form gets the higher weight.
		nameCounts[tok]++
		if dotIdx := strings.LastIndex(tok, "."); dotIdx >= 0 && dotIdx+1 < len(tok) {
			short := tok[dotIdx+1:]
			if !stopwordFrames[short] && len(short) >= 3 {
				nameCounts[short]++
			}
		}
	}

	// Rank by count desc, then by length desc (longer = more specific).
	type ranked struct {
		Name  string
		Count int
	}
	var ordered []ranked
	for n, c := range nameCounts {
		ordered = append(ordered, ranked{n, c})
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Count != ordered[j].Count {
			return ordered[i].Count > ordered[j].Count
		}
		return len(ordered[i].Name) > len(ordered[j].Name)
	})
	for _, r := range ordered {
		names = append(names, r.Name)
	}

	// File paths in trace order. Run both regexes so we catch Python's
	// quoted-shape traces alongside the standard `path:line` shape.
	seenFiles := map[string]bool{}
	appendFile := func(fp string) {
		fp = strings.ReplaceAll(fp, "\\", "/")
		if seenFiles[fp] {
			return
		}
		seenFiles[fp] = true
		files = append(files, fp)
	}
	for _, m := range stackFilePathRE.FindAllStringSubmatch(errorText, -1) {
		appendFile(m[1])
	}
	for _, m := range pythonFilePathRE.FindAllStringSubmatch(errorText, -1) {
		appendFile(m[1])
	}
	return names, files
}

// suspectRow is one ranked entry in the response. Score is 0.0-1.0
// normalised; evidence enumerates which signals fired. The agent
// reads evidence to decide whether to trust the rank or pivot.
type suspectRow struct {
	SymbolID         string   `json:"symbol_id"`
	Name             string   `json:"name"`
	QualifiedName    string   `json:"qualified_name"`
	Kind             string   `json:"kind"`
	FilePath         string   `json:"file_path"`
	StartLine        int      `json:"start_line"`
	EndLine          int      `json:"end_line"`
	Signature        string   `json:"signature,omitempty"`
	Score            float64  `json:"score"`
	Evidence         []string `json:"evidence"`
	StackFrameMatch  string   `json:"stack_frame_match,omitempty"`
	CallerFanIn      int      `json:"caller_fan_in"`
	RecentChangeFile bool     `json:"recent_change_file"`
}

func (s *Server) handleInvestigateFailure(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	start, tool, args := beginCall(req)
	// #1579 v0.82: composite cancellation contract. Entry-point check.
	if err := ctx.Err(); err != nil {
		return s.errResultRich("investigate_failure: ctx canceled before parsing", nil), nil
	}

	errorText := str(args, "error_text")
	if strings.TrimSpace(errorText) == "" {
		// Failure-as-pedagogy: caller didn't pass the only required
		// input. Show the canonical shape so they don't have to guess.
		return s.errResultRich(
			"investigate_failure requires `error_text` — paste the stack trace or error string from the failure",
			[]map[string]string{
				{"tool": "investigate_failure", "args": `{"error_text":"panic: nil pointer deref\n  at auth.Login (login.go:42)\n  at server.handleRequest (server.go:117)"}`,
					"why": "paste the actual trace; the composite parses frames and ranks suspects"},
				{"tool": "context_for_task", "args": `{"task":"what calls auth.Login"}`,
					"why": "if you don't have a stack trace yet, the task-driven composite searches by description"},
			},
		), nil
	}

	projectID, errRes := s.mustProject(args)
	if errRes != nil {
		return errRes, nil
	}

	maxSuspects := intArg(args, "max_suspects", 5)
	if maxSuspects <= 0 {
		maxSuspects = 5
	}
	if maxSuspects > 20 {
		maxSuspects = 20
	}
	traceDepth := intArg(args, "trace_depth", 2)
	if traceDepth <= 0 {
		traceDepth = 2
	}
	if traceDepth > 4 {
		traceDepth = 4
	}

	// ── Step 1: parse stack frames ───────────────────────────────────────
	frameNames, frameFiles := parseStackFrames(errorText)

	if len(frameNames) == 0 && len(frameFiles) == 0 {
		// Nothing extractable. Stamp empty + diagnosis pointing to
		// the parser's heuristic so the caller knows what shape works.
		meta := map[string]any{}
		// #1578: target_not_resolved is the correct code — the parser
		// couldn't extract anything from this input shape, so the
		// "input was wrong" recovery shape applies (try context_for_task
		// for prose). NoResultsInCorpus would imply the data is missing,
		// which is the wrong diagnosis here.
		stampEmpty(meta, EmptyReasonTargetNotResolved,
			"error_text contained no identifier-shaped tokens or file:line patterns — the parser looks for `\\b[A-Za-z_]\\w+\\b` names and `file.ext:NN` paths. Either the input is plain prose (try `context_for_task` with a task description) or the language's trace format is unusual (paste a raw trace, not pre-processed output).")
		meta["next_steps"] = []map[string]string{
			{"tool": "context_for_task", "args": fmt.Sprintf(`{"task":%q}`, truncateForArgs(errorText, 80)),
				"why": "task-driven composite handles freeform prose better than stack-frame parsing"},
		}
		data := map[string]any{
			"error_text":         truncateForOutput(errorText, 500),
			"implicated_symbols": []suspectRow{},
			"callers":            []hopRowFailure{},
			"recent_changes":     []changeRowFailure{},
			"rank":               []suspectRow{},
			"frames_parsed":      map[string]any{"names": []string{}, "files": []string{}},
			"_meta":              meta,
		}
		return s.jsonResultWithMeta(data, start, tool, args, 0), nil
	}

	// ── Step 2: search each frame name; aggregate hits ──────────────────
	// For each parsed name (in rank order), BM25 against the code corpus.
	// Collect into a map keyed by symbol_id so multi-frame hits aggregate.
	// Stop accumulating once we have 3× maxSuspects raw candidates — the
	// rank step will narrow.
	type candidate struct {
		ID            string
		Name          string
		QualifiedName string
		Kind          string
		FilePath      string
		StartLine     int
		EndLine       int
		Signature     string
		// rawBM25 is the highest BM25 score this candidate hit across
		// all frame searches. We don't sum across frames — that would
		// over-weight symbols whose name happens to appear in stopword-
		// adjacent contexts.
		rawBM25      float64
		frameMatches map[string]bool // which frame tokens matched this candidate
	}
	candidates := map[string]*candidate{}

	maxRaw := maxSuspects * 3
	// #1751: search every parsed frame name, bounded by NAME COUNT — not
	// by accumulated-candidate count. The old `len(candidates) >= maxRaw`
	// break filled its budget on high-frequency package-path tokens
	// (`internal`, `index`, `com` — repeated once per Go frame, so they
	// out-rank function names that appear once) and broke before the
	// once-mentioned crash functions (`resolveCalls`, `Index`) were ever
	// searched. A real trace carries well under maxFrameSearches distinct
	// tokens; the cap only guards a pathological input.
	const maxFrameSearches = 60
	for i, name := range frameNames {
		// #1595: per-iteration cancellation check. N frame names × one
		// SQL search each; without this the loop runs to completion
		// after the client cancels.
		if err := ctx.Err(); err != nil {
			break
		}
		if i >= maxFrameSearches {
			break
		}
		// SearchSymbolsByCorpus does BM25 over the code corpus; we
		// pass an empty language/kind filter and the maxRaw cap.
		results, err := s.store.SearchSymbolsByCorpus(projectID, name, "", "", "code", maxRaw)
		if err != nil {
			continue
		}
		for _, r := range results {
			// Filter to callable kinds — the composite's job is bug-hunt,
			// so dead Section/Setting hits aren't useful.
			switch r.Symbol.Kind {
			case "Function", "Method", "Class", "Interface", "Type":
			default:
				continue
			}
			c, ok := candidates[r.Symbol.ID]
			if !ok {
				c = &candidate{
					ID:            r.Symbol.ID,
					Name:          r.Symbol.Name,
					QualifiedName: r.Symbol.QualifiedName,
					Kind:          r.Symbol.Kind,
					FilePath:      r.Symbol.FilePath,
					StartLine:     r.Symbol.StartLine,
					EndLine:       r.Symbol.EndLine,
					Signature:     r.Symbol.Signature,
					frameMatches:  map[string]bool{},
				}
				candidates[r.Symbol.ID] = c
			}
			if r.Score > c.rawBM25 {
				c.rawBM25 = r.Score
			}
			c.frameMatches[name] = true
		}
	}

	// ── Step 3: recent-change overlap ───────────────────────────────────
	// Run one git-diff intersection scoped to ALL changed files in the
	// working tree; cache the resulting set so the per-candidate evidence
	// loop below can mark `recent_change_file` cheaply.
	changedFiles := map[string]bool{}
	if proj, err := s.store.GetProject(projectID); err == nil && proj != nil {
		if raw, derr := runGitDiff(proj.Path, "all"); derr == nil && raw != "" {
			for _, fp := range parseGitDiffFiles(raw) {
				normalised := strings.ReplaceAll(fp, "\\", "/")
				changedFiles[normalised] = true
			}
		}
	}

	// Stack-implicated files (parsed from the trace) also count as
	// "recent change" if they appear in the diff. We union them in.
	stackFileSet := map[string]bool{}
	for _, fp := range frameFiles {
		stackFileSet[fp] = true
	}

	// ── Step 4: per-candidate fan-in count + score assembly ─────────────
	// Caller fan-in is a real cost (one TraceViaCTEScoped per candidate
	// at depth=2). We cap to the top maxSuspects*2 by raw BM25 so the
	// trace work is bounded.
	type scored struct {
		c              *candidate
		fanIn          int
		recentChange   bool
		stackFileMatch bool
	}
	var ranked []scored
	{
		// Pre-sort so the most-implicated candidates survive the
		// traceCap truncation below.
		var byBM25 []*candidate
		for _, c := range candidates {
			byBM25 = append(byBM25, c)
		}
		// #1831: a candidate whose NAME is literally a stack frame token
		// (the crash site itself) must outrank loose BM25 hits and never
		// be truncated out. rawBM25 is a max across heterogeneous
		// per-token queries — scores from a `resolveCalls` query and an
		// `Indexer` query are not comparable — so a raw-BM25-only sort
		// let ~50 loose `*Indexer` signature matches fill the cap and
		// evict the exact crash-frame symbol BEFORE the Step-5
		// exact-name score boost (#1751) ever ran. Rank exact-frame-name
		// matches first.
		isExactFrameMatch := func(c *candidate) bool {
			for tok := range c.frameMatches {
				if strings.EqualFold(tok, c.Name) {
					return true
				}
			}
			return false
		}
		sort.Slice(byBM25, func(i, j int) bool {
			ei, ej := isExactFrameMatch(byBM25[i]), isExactFrameMatch(byBM25[j])
			if ei != ej {
				return ei // exact crash-frame matches first — never truncated out
			}
			if byBM25[i].rawBM25 != byBM25[j].rawBM25 {
				return byBM25[i].rawBM25 > byBM25[j].rawBM25
			}
			// Tiebreak: more frame matches wins.
			return len(byBM25[i].frameMatches) > len(byBM25[j].frameMatches)
		})

		traceCap := maxSuspects * 2
		if len(byBM25) > traceCap {
			byBM25 = byBM25[:traceCap]
		}
		for _, c := range byBM25 {
			// #1579: per-iteration cancellation check.
			if err := ctx.Err(); err != nil {
				break
			}
			fan := 0
			if hops, err := s.store.TraceViaCTEScoped(projectID, c.ID, "inbound", []string{"CALLS"}, traceDepth); err == nil {
				for _, h := range hops {
					if h.SymbolID == c.ID {
						continue
					}
					fan++
				}
			}
			normalised := strings.ReplaceAll(c.FilePath, "\\", "/")
			ranked = append(ranked, scored{
				c:              c,
				fanIn:          fan,
				recentChange:   changedFiles[normalised],
				stackFileMatch: stackFileSet[normalised],
			})
		}
	}

	// ── Step 5: composite scoring ───────────────────────────────────────
	// Score in [0.0, 1.0]. Weighted sum of three signals + saturating
	// adjustments:
	//
	//   stack_frame_match           +0.45  (any frame token matched the symbol)
	//   stack_file_match            +0.20  (symbol's file appears in trace)
	//   recent_change_file          +0.20  (symbol's file is in git diff)
	//   multi_frame_match           +0.10  (more than one distinct frame token)
	//   caller_fan_in (log-scaled)  +0.05  (more callers = more impact)
	//
	// These weights are empirical — adjusted to favour the canonical
	// "stack trace says `Foo`, file changed yesterday" suspect over a
	// peripheral hit. Caller fan-in is the smallest weight because it
	// correlates with "is this function commonly used" rather than "is
	// this function the failure site" — but it's still useful as a
	// tiebreaker.
	suspects := make([]suspectRow, 0, len(ranked))
	for _, r := range ranked {
		var (
			score    float64
			evidence []string
		)
		// Frame-match: always fires for entries in `ranked` since
		// they were seeded by a frame search. #1751: an EXACT-name
		// match (the symbol is literally named by a trace frame) is
		// the strongest implication signal and must outrank a loose
		// BM25 hit — the receiver-type token `Indexer` BM25-matches
		// every `*Indexer` method, so a flat score let an unrelated
		// hub method (`New`, fan-in 500) edge out the real crash site.
		if len(r.c.frameMatches) > 0 {
			evidence = append(evidence, "stack_frame_match")
			exactName := false
			for tok := range r.c.frameMatches {
				if strings.EqualFold(tok, r.c.Name) {
					exactName = true
					break
				}
			}
			if exactName {
				score += 0.45
				evidence = append(evidence, "stack_frame_exact_name_match")
			} else {
				// Loose match — the token appears in the symbol's
				// signature or qualified name but isn't its name.
				score += 0.20
			}
		}
		if len(r.c.frameMatches) > 1 {
			score += 0.10
			evidence = append(evidence, "multi_frame_match")
		}
		if r.stackFileMatch {
			score += 0.20
			evidence = append(evidence, "file_appears_in_trace")
		}
		if r.recentChange {
			score += 0.20
			evidence = append(evidence, "modified_in_working_tree")
		}
		// Log-scaled caller fan-in. Above ~20 callers we saturate at
		// the full +0.05 — beyond that, the symbol's a hub function
		// and fan-in stops being discriminating.
		if r.fanIn > 0 {
			adj := 0.05
			if r.fanIn < 20 {
				adj = 0.05 * (float64(r.fanIn) / 20.0)
			}
			score += adj
			evidence = append(evidence, fmt.Sprintf("caller_fan_in=%d", r.fanIn))
		}
		// Pick the strongest frame-match for the suspect's
		// stack_frame_match field. Iteration order over a map is
		// random in Go; sort for determinism so test output is
		// stable.
		var matched []string
		for f := range r.c.frameMatches {
			matched = append(matched, f)
		}
		sort.Slice(matched, func(i, j int) bool { return len(matched[i]) > len(matched[j]) })
		stackMatch := ""
		if len(matched) > 0 {
			stackMatch = matched[0]
		}
		// Clamp.
		if score > 1.0 {
			score = 1.0
		}
		suspects = append(suspects, suspectRow{
			SymbolID:         r.c.ID,
			Name:             r.c.Name,
			QualifiedName:    r.c.QualifiedName,
			Kind:             r.c.Kind,
			FilePath:         r.c.FilePath,
			StartLine:        r.c.StartLine,
			EndLine:          r.c.EndLine,
			Signature:        r.c.Signature,
			Score:            score,
			Evidence:         evidence,
			StackFrameMatch:  stackMatch,
			CallerFanIn:      r.fanIn,
			RecentChangeFile: r.recentChange,
		})
	}

	// Final sort by score desc; truncate to maxSuspects.
	sort.Slice(suspects, func(i, j int) bool {
		if suspects[i].Score != suspects[j].Score {
			return suspects[i].Score > suspects[j].Score
		}
		// Tiebreak: more evidence items wins (richer signal).
		return len(suspects[i].Evidence) > len(suspects[j].Evidence)
	})
	if len(suspects) > maxSuspects {
		suspects = suspects[:maxSuspects]
	}

	// ── Step 6: callers union across the top suspects ───────────────────
	// Each top suspect's depth-2 inbound callers, de-duped by id.
	callers := []hopRowFailure{}
	type callerHitFailure struct {
		viaSuspect string
		depth      int
		viaKind    string
	}
	seenCaller := map[string]callerHitFailure{}
	callerOrder := []string{}
	for _, sus := range suspects {
		// #1595: per-iteration cancellation check. Each iteration is a
		// BFS trace + N GetSymbol calls; bail between suspects on cancel.
		if err := ctx.Err(); err != nil {
			break
		}
		hops, err := s.store.TraceViaCTEScoped(projectID, sus.SymbolID, "inbound", []string{"CALLS"}, traceDepth)
		if err != nil {
			continue
		}
		for _, h := range hops {
			if h.SymbolID == sus.SymbolID {
				continue
			}
			if prev, ok := seenCaller[h.SymbolID]; ok && prev.depth <= h.Depth {
				continue
			}
			if _, ok := seenCaller[h.SymbolID]; !ok {
				callerOrder = append(callerOrder, h.SymbolID)
			}
			seenCaller[h.SymbolID] = callerHitFailure{viaSuspect: sus.SymbolID, depth: h.Depth, viaKind: h.ViaKind}
		}
	}

	// Batch-load caller metadata after tracing. The previous loop performed
	// one scoped symbol lookup per unique caller before applying the response
	// cap; panic traces naming hubs made that extra query fan-out noticeable.
	callerIDs := make([]string, 0, len(callerOrder))
	callerIDs = append(callerIDs, callerOrder...)
	callerSyms, err := s.store.GetSymbolsByIDs(projectID, callerIDs)
	if err != nil {
		callerSyms = nil
	}
	for _, id := range callerOrder {
		sym := callerSyms[id]
		if sym == nil {
			continue
		}
		hit := seenCaller[id]
		callers = append(callers, hopRowFailure{
			ViaSuspect:    hit.viaSuspect,
			ID:            sym.ID,
			Name:          sym.Name,
			QualifiedName: sym.QualifiedName,
			Kind:          sym.Kind,
			FilePath:      sym.FilePath,
			Depth:         hit.depth,
			ViaKind:       hit.viaKind,
		})
	}

	// Bound the callers union for transport. A hot suspect — a hub
	// symbol named in the trace — has hundreds of depth-2 inbound
	// callers; the uncapped union blew the envelope past the MCP
	// token limit (#1723, same class as the plan_change blast-radius
	// cap #1727). The loop above visits suspects in rank order, so a
	// head truncation keeps the highest-ranked suspects' callers.
	const maxCallers = 50
	callersTotal := len(callers)
	callersTruncated := false
	if len(callers) > maxCallers {
		callers = callers[:maxCallers]
		callersTruncated = true
	}

	// ── Step 7: recent_changes intersection with implicated files ───────
	implicatedFiles := map[string]bool{}
	for _, sus := range suspects {
		implicatedFiles[strings.ReplaceAll(sus.FilePath, "\\", "/")] = true
	}
	for f := range stackFileSet {
		implicatedFiles[f] = true
	}
	recentChanges := []changeRowFailure{}
	for fp := range changedFiles {
		if implicatedFiles[fp] {
			recentChanges = append(recentChanges, changeRowFailure{FilePath: fp})
		}
	}
	// Deterministic order for tests.
	sort.Slice(recentChanges, func(i, j int) bool { return recentChanges[i].FilePath < recentChanges[j].FilePath })

	// ── Step 8: envelope ────────────────────────────────────────────────
	meta := map[string]any{}
	if len(suspects) == 0 {
		stampEmpty(meta, EmptyReasonNoResultsInCorpus,
			fmt.Sprintf("parsed %d frame name(s) and %d file path(s) from error_text, but none resolved to a callable symbol in project %q — either the failure is in code that wasn't indexed, or the symbol names in the trace are mangled (compiled binary vs source)", len(frameNames), len(frameFiles), projectID))
		// #1570: build the next_steps array conditional on what we
		// actually parsed. The original always emitted a `search` step
		// with firstNonEmpty(frameNames, "") — when only frameFiles
		// were parsed (e.g. a Python trace whose path regex captured a
		// path but no identifier name), the hint became
		// `{"query":""}` which handleSearch then rejects, breaking the
		// failure-as-pedagogy chain.
		nextSteps := []map[string]string{}
		if len(frameNames) > 0 {
			nextSteps = append(nextSteps, map[string]string{
				"tool": "search", "args": fmt.Sprintf(`{"query":%q}`, frameNames[0]),
				"why": "drop kind=callable; widen the search to confirm extraction coverage",
			})
		} else if len(frameFiles) > 0 {
			// Only file paths parsed — search by file basename minus
			// extension. Better than nothing; lets the agent confirm
			// whether the file is indexed at all.
			base := path.Base(frameFiles[0])
			if dot := strings.LastIndex(base, "."); dot > 0 {
				base = base[:dot]
			}
			if base != "" {
				nextSteps = append(nextSteps, map[string]string{
					"tool": "search", "args": fmt.Sprintf(`{"query":%q}`, base),
					"why": "trace had file paths but no identifier names; searching by file basename surfaces nearby symbols",
				})
			}
		}
		nextSteps = append(nextSteps, map[string]string{
			"tool": "doctor", "args": `{}`,
			"why": "check for extraction failures in the implicated language",
		})
		meta["next_steps"] = nextSteps
	} else {
		// Steering: top suspect → context call; second-ranked suspect →
		// trace outbound (to verify the call graph the composite saw is
		// what the agent expects).
		nextSteps := []map[string]string{
			{"tool": "context", "args": fmt.Sprintf(`{"id":%q}`, suspects[0].SymbolID),
				"why": "read the top-ranked suspect's source for diff review"},
		}
		if len(suspects) > 1 {
			nextSteps = append(nextSteps, map[string]string{
				"tool": "trace", "args": fmt.Sprintf(`{"id":%q,"direction":"out"}`, suspects[0].SymbolID),
				"why": "verify what the suspect calls — confirms the suspect's callees match the trace's next-frame names",
			})
		}
		if len(callers) > 0 {
			nextSteps = append(nextSteps, map[string]string{
				"tool": "context", "args": fmt.Sprintf(`{"id":%q}`, callers[0].ID),
				"why": "read a caller's source to see how the suspect was invoked",
			})
		}
		meta["next_steps"] = nextSteps
	}

	data := map[string]any{
		"error_text":         truncateForOutput(errorText, 500),
		"implicated_symbols": suspects,
		"callers":            callers,
		// callers_total is the full pre-truncation union size;
		// callers_truncated flags that `callers` was capped for
		// transport (the count survives, the list does not).
		"callers_total":     callersTotal,
		"callers_truncated": callersTruncated,
		"recent_changes":    recentChanges,
		"rank":              suspects, // alias for the roadmap's contract; same data
		"frames_parsed": map[string]any{
			"names": frameNames,
			"files": frameFiles,
		},
	}
	if _, ok := meta["empty_reason"]; ok {
		data["_meta"] = meta
	} else {
		// Merge next_steps into the envelope meta via the standard path.
		// jsonResultWithMeta builds the meta object; we pass next_steps
		// in via the args bag... actually we just embed in data._meta
		// since the standard envelope merges.
		data["_meta"] = meta
	}
	return s.jsonResultWithMeta(data, start, tool, args, 0), nil
}

// hopRowFailure and changeRowFailure mirror the shapes used in
// context_for_task but live here so the composite stays self-contained.
type hopRowFailure struct {
	ViaSuspect    string `json:"via_suspect"`
	ID            string `json:"id"`
	Name          string `json:"name"`
	QualifiedName string `json:"qualified_name"`
	Kind          string `json:"kind"`
	FilePath      string `json:"file_path"`
	Depth         int    `json:"depth"`
	ViaKind       string `json:"via_kind"`
}

type changeRowFailure struct {
	FilePath string `json:"file_path"`
}

func truncateForArgs(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func truncateForOutput(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...[truncated]"
}
