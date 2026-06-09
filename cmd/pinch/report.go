// SPDX-License-Identifier: MIT

package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// report.go — `pincher report` renders a compact, Pincher-native
// architecture briefing from the existing deterministic index. It is a
// read-only artifact generator: no LLM extraction, no graph recomputation, no
// dashboard dependency. The value is Pincher's provenance + workflow guidance
// in a human-readable form.

type reportOptions struct {
	GeneratedAt time.Time
}

type reportHotspot struct {
	Symbol        db.Symbol
	IncomingCalls int
}

type reportPackagePair struct {
	From string
	To   string
}

type reportRationaleGroup struct {
	Attachment string
	Symbols    []db.Symbol
}

type reportRationaleMap struct {
	Groups       []reportRationaleGroup
	Attached     int
	Unattached   int
	TotalVisible int
}

type reportNextCall struct {
	Tool          string
	Args          string
	Why           string
	ExpectedValue string
}

func runReportCLI(args []string) {
	os.Exit(reportCLI(args, os.Stdout, os.Stderr))
}

func reportCLI(args []string, stdout, stderr io.Writer) int {
	log.SetOutput(io.Discard)

	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	fs.SetOutput(stderr)
	format := fs.String("format", "markdown", "Output format: markdown")
	projectFlag := fs.String("project", "", "Project name, id, or substring (default: the current directory's project)")
	outPath := fs.String("out", "", "Write to this file (default: stdout)")
	dataDir := fs.String("data-dir", "", "Override data directory")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: pincher report [--project NAME|ID|SUBSTR] [--out=FILE]")
		fmt.Fprintln(stderr, "  Generates a Pincher-native markdown architecture report from indexed graph evidence.")
		fmt.Fprintln(stderr, "  The report uses deterministic source provenance and existing _meta-oriented workflow guidance; it does not run LLM extraction.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if strings.ToLower(*format) != "markdown" {
		fmt.Fprintf(stderr, "pincher report: unknown format %q (want markdown)\n", *format)
		return 1
	}

	store, _, err := openProjectStore(*dataDir)
	if err != nil {
		fmt.Fprintf(stderr, "pincher report: %v\n", err)
		return 1
	}
	defer store.Close()

	project, err := resolveExportProject(store, *projectFlag)
	if err != nil {
		fmt.Fprintf(stderr, "pincher report: %v\n", err)
		return 1
	}

	symbols, err := store.ListSymbolsForProject(project.ID)
	if err != nil {
		fmt.Fprintf(stderr, "pincher report: load symbols: %v\n", err)
		return 1
	}
	edges, err := store.ListEdgesForProject(project.ID)
	if err != nil {
		fmt.Fprintf(stderr, "pincher report: load edges: %v\n", err)
		return 1
	}

	out := stdout
	if *outPath != "" {
		f, err := os.Create(*outPath)
		if err != nil {
			fmt.Fprintf(stderr, "pincher report: create %s: %v\n", *outPath, err)
			return 1
		}
		defer f.Close()
		out = f
	}
	if err := writeProjectReportMarkdown(out, project, symbols, edges, reportOptions{GeneratedAt: time.Now().UTC()}); err != nil {
		fmt.Fprintf(stderr, "pincher report: write: %v\n", err)
		return 1
	}
	if *outPath != "" {
		fmt.Fprintf(stderr, "wrote Pincher report for %s to %s\n", project.Name, *outPath)
	}
	return 0
}

func writeProjectReportMarkdown(w io.Writer, project db.Project, symbols []db.Symbol, edges []db.Edge, opts reportOptions) error {
	generatedAt := opts.GeneratedAt
	if generatedAt.IsZero() {
		generatedAt = time.Now().UTC()
	}

	languages := countSymbolsBy(symbols, func(s db.Symbol) string { return emptyAs(s.Language, "Unknown") })
	nodeKinds := countSymbolsBy(symbols, func(s db.Symbol) string { return emptyAs(s.Kind, "Unknown") })
	edgeKinds := countEdgesBy(edges, func(e db.Edge) string { return emptyAs(e.Kind, "Unknown") })
	entryPoints := reportEntryPoints(symbols, 10)
	hotspots := reportHotspots(symbols, edges, 10)
	rationales := reportRationaleMapFor(symbols, 10)
	surprising := reportSurprisingConnections(edges, 10)

	if _, err := fmt.Fprintf(w, "# Pincher report: %s\n\n", emptyAs(project.Name, project.ID)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Generated: %s\n\n", generatedAt.Format(time.RFC3339)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "## Project\n\n"); err != nil {
		return err
	}
	lines := []string{
		fmt.Sprintf("- ID: `%s`", project.ID),
		fmt.Sprintf("- Path: `%s`", project.Path),
		fmt.Sprintf("- Indexed: %s", project.IndexedAt.UTC().Format(time.RFC3339)),
		fmt.Sprintf("- Binary version: `%s`", emptyAs(project.BinaryVersion, "unknown")),
		fmt.Sprintf("- Files: %d · Symbols: %d · Edges: %d", project.FileCount, len(symbols), len(edges)),
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}

	if err := writeCountSection(w, "Languages", languages, "symbols"); err != nil {
		return err
	}
	if err := writeCountSection(w, "Node kinds", nodeKinds, "symbols"); err != nil {
		return err
	}
	if err := writeCountSection(w, "Edge kinds", edgeKinds, "edges"); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(w, "\n## Entry points\n\n"); err != nil {
		return err
	}
	if len(entryPoints) == 0 {
		if _, err := fmt.Fprintln(w, "- none found in the current index"); err != nil {
			return err
		}
	} else {
		for _, s := range entryPoints {
			if _, err := fmt.Fprintf(w, "- `%s` — `%s:%d`\n", s.Name, s.FilePath, s.StartLine); err != nil {
				return err
			}
		}
	}

	if _, err := fmt.Fprintf(w, "\n## Hotspots\n\n"); err != nil {
		return err
	}
	if len(hotspots) == 0 {
		if _, err := fmt.Fprintln(w, "- none found in the current index"); err != nil {
			return err
		}
	} else {
		for _, h := range hotspots {
			if _, err := fmt.Fprintf(w, "- `%s` %s — `%s` (incoming calls: %d)\n", h.Symbol.Name, h.Symbol.Kind, h.Symbol.FilePath, h.IncomingCalls); err != nil {
				return err
			}
		}
	}

	if _, err := fmt.Fprintf(w, "\n## Rationale / design intent\n\n"); err != nil {
		return err
	}
	if len(rationales.Groups) == 0 {
		if _, err := fmt.Fprintln(w, "- none found in the current index"); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintf(w, "- Attached rationale: %d · unattached/file-level: %d\n", rationales.Attached, rationales.Unattached); err != nil {
			return err
		}
		for _, group := range rationales.Groups {
			if _, err := fmt.Fprintf(w, "- Attachment: `%s` (%d rationale%s)\n", group.Attachment, len(group.Symbols), plural(len(group.Symbols))); err != nil {
				return err
			}
			for _, s := range group.Symbols {
				if _, err := fmt.Fprintf(w, "  - `%s` — `%s:%d` (confidence: %.2f)\n", s.Name, s.FilePath, s.StartLine, s.ExtractionConfidence); err != nil {
					return err
				}
			}
		}
	}

	if _, err := fmt.Fprintf(w, "\n## Surprising connections\n\n"); err != nil {
		return err
	}
	if len(surprising) == 0 {
		if _, err := fmt.Fprintln(w, "- none found in the current index"); err != nil {
			return err
		}
	} else {
		for _, pair := range sortedPackagePairs(surprising) {
			if _, err := fmt.Fprintf(w, "- `%s` → `%s`: %d edge%s\n", pair.From, pair.To, surprising[pair], plural(surprising[pair])); err != nil {
				return err
			}
		}
	}

	if _, err := fmt.Fprintf(w, "\n## Suggested next Pincher calls\n\n"); err != nil {
		return err
	}
	suggestions := reportNextCalls(project, hotspots, rationales)
	for _, suggestion := range suggestions {
		if _, err := fmt.Fprintf(w, "- Tool: `%s`\n", suggestion.Tool); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "  - Args: `%s`\n", suggestion.Args); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "  - Why: %s\n", suggestion.Why); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "  - Expected value: %s\n", suggestion.ExpectedValue); err != nil {
			return err
		}
	}

	_, err := fmt.Fprintf(w, "\n## Provenance\n\nThis report is generated from Pincher's existing symbol and edge index. Missing data is reported as missing rather than inferred.\n")
	return err
}

func reportNextCalls(project db.Project, hotspots []reportHotspot, rationales reportRationaleMap) []reportNextCall {
	projectID := emptyAs(project.ID, project.Path)
	suggestions := make([]reportNextCall, 0, 4)

	if len(hotspots) > 0 {
		top := hotspots[0].Symbol
		suggestions = append(suggestions,
			reportNextCall{
				Tool:          "mcp_pincher_context",
				Args:          fmt.Sprintf(`{"project":"%s","id":"%s"}`, projectID, top.ID),
				Why:           "inspect the top hotspot before editing it.",
				ExpectedValue: "reduces risky raw reads and grounds edits in symbol provenance.",
			},
			reportNextCall{
				Tool:          "mcp_pincher_trace",
				Args:          fmt.Sprintf(`{"project":"%s","id":"%s","direction":"inbound"}`, projectID, top.ID),
				Why:           "map callers for the highest-incoming hotspot before behavior changes.",
				ExpectedValue: "exposes blast-radius risk for planning and routing escalation.",
			},
		)
	}

	if rationale := firstRationale(rationales); rationale.ID != "" {
		suggestions = append(suggestions, reportNextCall{
			Tool:          "mcp_pincher_search",
			Args:          fmt.Sprintf(`{"project":"%s","query":"%s"}`, projectID, rationale.Name),
			Why:           "follow rationale/design-intent evidence back into indexed symbols.",
			ExpectedValue: "keeps design intent visible instead of relying on prose-only memory.",
		})
	}

	suggestions = append(suggestions, reportNextCall{
		Tool:          "mcp_pincher_changes",
		Args:          fmt.Sprintf(`{"project":"%s","scope":"all"}`, projectID),
		Why:           "run before finalizing edits to map changed-symbol blast radius.",
		ExpectedValue: "turns the report into an execution loop with measurable impact checks.",
	})
	return suggestions
}

func firstRationale(rationales reportRationaleMap) db.Symbol {
	for _, group := range rationales.Groups {
		if len(group.Symbols) > 0 {
			return group.Symbols[0]
		}
	}
	return db.Symbol{}
}

func writeCountSection(w io.Writer, title string, counts map[string]int, noun string) error {
	if _, err := fmt.Fprintf(w, "\n## %s\n\n", title); err != nil {
		return err
	}
	if len(counts) == 0 {
		_, err := fmt.Fprintln(w, "- none found in the current index")
		return err
	}
	for _, kv := range sortedCounts(counts) {
		if _, err := fmt.Fprintf(w, "- %s: %d %s\n", kv.Key, kv.Value, noun); err != nil {
			return err
		}
	}
	return nil
}

type reportCount struct {
	Key   string
	Value int
}

func sortedCounts(counts map[string]int) []reportCount {
	out := make([]reportCount, 0, len(counts))
	for k, v := range counts {
		out = append(out, reportCount{k, v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Value != out[j].Value {
			return out[i].Value > out[j].Value
		}
		return out[i].Key < out[j].Key
	})
	return out
}

func countSymbolsBy(symbols []db.Symbol, keyFn func(db.Symbol) string) map[string]int {
	counts := make(map[string]int)
	for _, s := range symbols {
		counts[keyFn(s)]++
	}
	return counts
}

func countEdgesBy(edges []db.Edge, keyFn func(db.Edge) string) map[string]int {
	counts := make(map[string]int)
	for _, e := range edges {
		counts[keyFn(e)]++
	}
	return counts
}

func reportEntryPoints(symbols []db.Symbol, limit int) []db.Symbol {
	out := make([]db.Symbol, 0)
	for _, s := range symbols {
		if s.IsEntryPoint {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].FilePath != out[j].FilePath {
			return out[i].FilePath < out[j].FilePath
		}
		return out[i].StartLine < out[j].StartLine
	})
	return capSymbols(out, limit)
}

func reportRationales(symbols []db.Symbol, limit int) []db.Symbol {
	out := make([]db.Symbol, 0)
	for _, s := range symbols {
		if s.Kind == "Rationale" {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].FilePath != out[j].FilePath {
			return out[i].FilePath < out[j].FilePath
		}
		return out[i].StartLine < out[j].StartLine
	})
	return capSymbols(out, limit)
}

func reportRationaleMapFor(symbols []db.Symbol, limit int) reportRationaleMap {
	rationales := reportRationales(symbols, limit)
	byAttachment := make(map[string][]db.Symbol)
	out := reportRationaleMap{TotalVisible: len(rationales)}
	for _, s := range rationales {
		attachment := strings.TrimSpace(s.Parent)
		if attachment == "" {
			attachment = "unattached/file-level"
			out.Unattached++
		} else {
			out.Attached++
		}
		byAttachment[attachment] = append(byAttachment[attachment], s)
	}
	attachments := make([]string, 0, len(byAttachment))
	for attachment := range byAttachment {
		attachments = append(attachments, attachment)
	}
	sort.Slice(attachments, func(i, j int) bool {
		if attachments[i] == "unattached/file-level" {
			return false
		}
		if attachments[j] == "unattached/file-level" {
			return true
		}
		return attachments[i] < attachments[j]
	})
	for _, attachment := range attachments {
		group := reportRationaleGroup{Attachment: attachment, Symbols: byAttachment[attachment]}
		sort.Slice(group.Symbols, func(i, j int) bool {
			if group.Symbols[i].FilePath != group.Symbols[j].FilePath {
				return group.Symbols[i].FilePath < group.Symbols[j].FilePath
			}
			return group.Symbols[i].StartLine < group.Symbols[j].StartLine
		})
		out.Groups = append(out.Groups, group)
	}
	return out
}

func capSymbols(symbols []db.Symbol, limit int) []db.Symbol {
	if limit > 0 && len(symbols) > limit {
		return symbols[:limit]
	}
	return symbols
}

func reportHotspots(symbols []db.Symbol, edges []db.Edge, limit int) []reportHotspot {
	byID := make(map[string]db.Symbol, len(symbols))
	for _, s := range symbols {
		byID[s.ID] = s
	}
	incoming := make(map[string]int)
	for _, e := range edges {
		if e.Kind == "CALLS" {
			incoming[e.ToID]++
		}
	}
	out := make([]reportHotspot, 0, len(incoming))
	for id, n := range incoming {
		if s, ok := byID[id]; ok && reportHotspotKind(s.Kind) && !reportTestOrFixturePath(s.FilePath) {
			out = append(out, reportHotspot{Symbol: s, IncomingCalls: n})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IncomingCalls != out[j].IncomingCalls {
			return out[i].IncomingCalls > out[j].IncomingCalls
		}
		if out[i].Symbol.FilePath != out[j].Symbol.FilePath {
			return out[i].Symbol.FilePath < out[j].Symbol.FilePath
		}
		return out[i].Symbol.Name < out[j].Symbol.Name
	})
	if limit > 0 && len(out) > limit {
		return out[:limit]
	}
	return out
}

func reportHotspotKind(kind string) bool {
	switch kind {
	case "Function", "Method", "Class", "Interface", "Type", "Module":
		return true
	}
	return false
}

func reportTestOrFixturePath(filePath string) bool {
	low := strings.ToLower(filepath.ToSlash(filePath))
	base := low
	if i := strings.LastIndex(low, "/"); i >= 0 {
		base = low[i+1:]
	}
	for _, marker := range []string{"/testdata/", "/tests/", "/test/", "/fixtures/", "/__fixtures__/"} {
		if strings.Contains(low, marker) {
			return true
		}
	}
	if strings.HasPrefix(low, "testdata/") || strings.HasPrefix(low, "tests/") || strings.HasPrefix(low, "fixtures/") {
		return true
	}
	if strings.HasSuffix(base, "_test.go") || strings.HasPrefix(base, "test_") || strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") {
		return true
	}
	return false
}

func reportSurprisingConnections(edges []db.Edge, limit int) map[reportPackagePair]int {
	counts := make(map[reportPackagePair]int)
	for _, e := range edges {
		from := reportPackageOfSymbolID(e.FromID)
		to := reportPackageOfSymbolID(e.ToID)
		if from == "" || to == "" || from == to {
			continue
		}
		counts[reportPackagePair{From: from, To: to}]++
	}
	if limit <= 0 || len(counts) <= limit {
		return counts
	}
	trimmed := make(map[reportPackagePair]int, limit)
	for _, pair := range sortedPackagePairs(counts)[:limit] {
		trimmed[pair] = counts[pair]
	}
	return trimmed
}

func sortedPackagePairs(counts map[reportPackagePair]int) []reportPackagePair {
	out := make([]reportPackagePair, 0, len(counts))
	for pair := range counts {
		out = append(out, pair)
	}
	sort.Slice(out, func(i, j int) bool {
		if counts[out[i]] != counts[out[j]] {
			return counts[out[i]] < counts[out[j]]
		}
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		return out[i].To < out[j].To
	})
	return out
}

func reportPackageOfSymbolID(id string) string {
	filePath := id
	if i := strings.Index(id, "::"); i >= 0 {
		filePath = id[:i]
	}
	filePath = filepath.ToSlash(filePath)
	if i := strings.LastIndex(filePath, "/"); i >= 0 {
		return filePath[:i]
	}
	if ext := filepath.Ext(filePath); ext != "" {
		return strings.TrimSuffix(filePath, ext)
	}
	return filePath
}

func emptyAs(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
