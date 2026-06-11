// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"fmt"
	"sort"

	"github.com/kwad77/pincher/internal/db"
	"github.com/kwad77/pincher/internal/index"
)

// ChangeTracer is the narrow slice of *index.Indexer that AnalyzeChanges
// needs: an inbound BFS from a changed symbol. Declared as an interface
// so the CLI can hand in a plain index.New(store) and tests can stub the
// trace step without standing up a full indexer.
type ChangeTracer interface {
	TraceByID(ctx context.Context, projectID, symbolID, direction string, maxDepth int, addRisk bool, edgeKinds ...string) ([]index.Hop, error)
}

// ImpactedSymbol is one blast-radius entry, in BFS discovery order.
// ChangedBy names the changed symbol whose trace first reached it.
type ImpactedSymbol struct {
	ID        string
	Name      string
	Kind      string
	FilePath  string
	Risk      string
	ChangedBy string
}

// ImpactedTest is one tests_to_run entry. Overlap = how many distinct
// changed symbols this test reaches; higher overlap = more bang per
// re-run. JSON tags match the MCP `changes` tool's tests_to_run shape.
type ImpactedTest struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	FilePath string `json:"file_path"`
	Overlap  int    `json:"overlap"`
}

// ChangesAnalysis is the result of the shared diff → changed symbols →
// blast radius → impacted tests pipeline.
type ChangesAnalysis struct {
	ChangedFiles   []string
	ChangedSymbols []db.Symbol
	// Impacted is the full (untrimmed) blast radius in BFS discovery
	// order. Callers that need risk-severity ordering sort it themselves
	// (handleChanges does, for its response-budget trim).
	Impacted []ImpactedSymbol
	// TestsToRun is sorted by overlap descending, then test ID ascending
	// for stable output.
	TestsToRun []ImpactedTest
}

// AnalyzeChanges is the diff → changed symbols → blast radius →
// impacted tests core shared by the MCP `changes` tool (handleChanges)
// and the `pincher test-impacted` CLI. Extracted from handleChanges so
// the CLI executes the SAME analysis the agent sees, instead of forking
// the logic.
//
// The returned error is non-nil only when the git diff itself fails
// (bad scope, bad base branch, not a git repo); every later step is
// best-effort, matching handleChanges' historical behaviour.
func AnalyzeChanges(ctx context.Context, store *db.Store, tracer ChangeTracer, projectID, root, scope string, depth int) (*ChangesAnalysis, error) {
	diffOutput, diffErr := runGitDiff(root, scope)
	if diffErr != nil {
		return nil, fmt.Errorf("git diff failed: %v", diffErr)
	}
	changedFiles := parseGitDiffFiles(diffOutput)

	// #502: also fetch the unified diff so per-file hunk ranges can
	// intersect each symbol's [StartLine, EndLine]. Pre-fix, every
	// symbol in any changed file was treated as "changed" — adding
	// one function to a 6000-line file expanded the blast radius BFS
	// to half the codebase. The hunk fetch is best-effort: on error
	// we fall back to the pre-#502 behaviour (all symbols in changed
	// files) so the tool stays usable when git options change shape.
	hunkDiff, hunkErr := runGitDiffHunks(root, scope)
	var hunksByFile map[string][][2]int
	if hunkErr == nil {
		hunksByFile = parseGitDiffHunks(hunkDiff)
	}

	// Find symbols in changed files. When we have hunks for a file,
	// keep only symbols whose line range overlaps an actual edit.
	// When hunks aren't available for a file (untracked content,
	// rename without content change, parse miss), fall back to
	// "all symbols in file" — better to over-report than under-report
	// for the safety-check use case.
	var changedSymbols []db.Symbol
	for _, f := range changedFiles {
		syms, err := store.GetSymbolsForFile(projectID, f)
		if err != nil {
			continue
		}
		hunks, hasHunks := hunksByFile[f]
		if !hasHunks || len(hunks) == 0 {
			changedSymbols = append(changedSymbols, syms...)
			continue
		}
		for _, sym := range syms {
			if symbolOverlapsHunks(sym.StartLine, sym.EndLine, hunks) {
				changedSymbols = append(changedSymbols, sym)
			}
		}
	}

	// BFS trace for blast radius. Use TraceByID so a changed `Run` /
	// `Handler` / `Open` resolves to the *exact* symbol that changed,
	// not whichever same-named symbol the name-based lookup picks first
	// (#5).
	//
	// #247 #4: alongside the impacted-symbol collection, track which
	// test symbols reach each changed symbol — separately from the
	// `seen` dedupe so a test reached via multiple changed symbols gets
	// its overlap counted, not collapsed into the first path. Used to
	// produce the tests_to_run array sorted by overlap descending.
	// #330: pre-allocate as zero-len so a JSON encoding of the field is
	// always [], never null.
	impacted := []ImpactedSymbol{}
	seen := make(map[string]bool)
	testHits := make(map[string]map[string]bool) // test sym ID → set of changed sym IDs that reach it
	testSyms := make(map[string]db.Symbol)       // test sym ID → the symbol (for output projection)
	for _, sym := range changedSymbols {
		hops, err := tracer.TraceByID(ctx, projectID, sym.ID, "inbound", depth, true)
		if err != nil {
			continue
		}
		for _, h := range hops {
			if h.Symbol.IsTest {
				if _, ok := testHits[h.Symbol.ID]; !ok {
					testHits[h.Symbol.ID] = make(map[string]bool)
					testSyms[h.Symbol.ID] = h.Symbol
				}
				testHits[h.Symbol.ID][sym.ID] = true
			}
			if seen[h.Symbol.ID] {
				continue
			}
			seen[h.Symbol.ID] = true
			impacted = append(impacted, ImpactedSymbol{
				ID:        h.Symbol.ID,
				Name:      h.Symbol.Name,
				Kind:      h.Symbol.Kind,
				FilePath:  h.Symbol.FilePath,
				Risk:      h.Risk,
				ChangedBy: sym.Name,
			})
		}
	}

	// Build tests_to_run sorted by overlap descending (then test ID
	// ascending for stable output). Deterministic ordering keeps any
	// snapshot test on this surface stable.
	testsToRun := make([]ImpactedTest, 0, len(testHits))
	for testID, hits := range testHits {
		sym := testSyms[testID]
		testsToRun = append(testsToRun, ImpactedTest{
			ID:       testID,
			Name:     sym.Name,
			FilePath: sym.FilePath,
			Overlap:  len(hits),
		})
	}
	sort.Slice(testsToRun, func(i, j int) bool {
		if testsToRun[i].Overlap != testsToRun[j].Overlap {
			return testsToRun[i].Overlap > testsToRun[j].Overlap
		}
		return testsToRun[i].ID < testsToRun[j].ID
	})

	return &ChangesAnalysis{
		ChangedFiles:   changedFiles,
		ChangedSymbols: changedSymbols,
		Impacted:       impacted,
		TestsToRun:     testsToRun,
	}, nil
}
