// SPDX-License-Identifier: MIT

// Gated differential test (ADR-0008 Phase 2): real-tree-sitter PHP extractor
// vs the regex tier on a real corpus. Tallies regex and tree-sitter over the
// SAME clean-parse files so error-file regex symbols can't masquerade as a
// tree-sitter regression (the #1967 lesson). Runs only when
// PINCHER_SPIKE_CORPUS_PHP points at a dir of .php files.

package ast

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestTreeSitterPHP_DiffVsRegex(t *testing.T) {
	corpus := os.Getenv("PINCHER_SPIKE_CORPUS_PHP")
	if corpus == "" {
		t.Skip("set PINCHER_SPIKE_CORPUS_PHP=<dir of .php files> to run the tree-sitter differential")
	}
	var files []string
	_ = filepath.Walk(corpus, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(p, ".php") {
			files = append(files, p)
		}
		return nil
	})
	sort.Strings(files)
	if len(files) == 0 {
		t.Fatalf("no .php under %s", corpus)
	}

	tsx, err := newPHPTSExtractor(context.Background())
	if err != nil {
		t.Fatalf("newPHPTSExtractor: %v", err)
	}

	rxByKind, tsByKind := map[string]int{}, map[string]int{}
	rxSet, tsSet := map[string]int{}, map[string]int{}
	var rxImports, tsImports, rxCalls, tsCalls, tsErrFiles int

	for _, f := range files {
		src, _ := os.ReadFile(f)
		rel, _ := filepath.Rel(corpus, f)

		ts, ok := tsx.extractChecked(src, rel)
		if !ok {
			tsErrFiles++
			continue // dispatcher routes this file to regex; exclude from both tallies
		}
		rx := extractPHP(src, rel)
		for _, s := range rx.Symbols {
			rxByKind[s.Kind]++
			rxSet[symKey(s.Name, s.Kind)]++
		}
		for _, e := range rx.Edges {
			if e.Kind == "IMPORTS" {
				rxImports++
			} else if e.Kind == "CALLS" {
				rxCalls++
			}
		}
		for _, s := range ts.Symbols {
			tsByKind[s.Kind]++
			tsSet[symKey(s.Name, s.Kind)]++
		}
		for _, e := range ts.Edges {
			if e.Kind == "IMPORTS" {
				tsImports++
			} else if e.Kind == "CALLS" {
				tsCalls++
			}
		}
	}

	both, onlyRx, onlyTs := 0, 0, 0
	for k, rc := range rxSet {
		tc := tsSet[k]
		m := rc
		if tc < m {
			m = tc
		}
		both += m
		if rc > tc {
			onlyRx += rc - tc
		}
	}
	for k, tc := range tsSet {
		if tc > rxSet[k] {
			onlyTs += tc - rxSet[k]
		}
	}
	rxTotal, tsTotal := 0, 0
	for _, v := range rxByKind {
		rxTotal += v
	}
	for _, v := range tsByKind {
		tsTotal += v
	}

	var b strings.Builder
	fmt.Fprintf(&b, "\n=== tree-sitter vs regex PHP differential — %s (%d files) ===\n", corpus, len(files))
	fmt.Fprintf(&b, "ts parse-error files (regex fallback): %d / %d (%.1f%%)\n",
		tsErrFiles, len(files), 100*float64(tsErrFiles)/float64(len(files)))
	fmt.Fprintf(&b, "SYMBOLS by kind\n  regex: %v (total %d)\n  ts:    %v (total %d)\n", rxByKind, rxTotal, tsByKind, tsTotal)
	fmt.Fprintf(&b, "(name,kind) identity agreement\n  in both: %d\n  only regex: %d\n  only ts: %d\n", both, onlyRx, onlyTs)
	if rxTotal > 0 {
		fmt.Fprintf(&b, "  agreement: %.1f%% of regex symbols also found by tree-sitter\n", 100*float64(both)/float64(rxTotal))
	}
	fmt.Fprintf(&b, "EDGES\n  IMPORTS regex=%d ts=%d (ts adds use-declarations the regex tier never captured)\n  CALLS regex=%d ts=%d\n", rxImports, tsImports, rxCalls, tsCalls)
	fmt.Fprintf(&b, "===========================================================\n")
	t.Log(b.String())
}
