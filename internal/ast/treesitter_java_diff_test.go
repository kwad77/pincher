// SPDX-License-Identifier: MIT

// Gated differential test (ADR-0008 / #1958 EGDL Stage 7): real-tree-sitter
// Java extractor vs the regex tier on a real corpus. Measures symbol/edge
// agreement and the ID-churn surface. Runs only when PINCHER_SPIKE_CORPUS_JAVA
// points at a dir of .java files.

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

func TestTreeSitterJava_DiffVsRegex(t *testing.T) {
	corpus := os.Getenv("PINCHER_SPIKE_CORPUS_JAVA")
	if corpus == "" {
		t.Skip("set PINCHER_SPIKE_CORPUS_JAVA=<dir of .java files> to run the tree-sitter differential")
	}
	var files []string
	_ = filepath.Walk(corpus, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(p, ".java") {
			files = append(files, p)
		}
		return nil
	})
	sort.Strings(files)
	if len(files) == 0 {
		t.Fatalf("no .java under %s", corpus)
	}

	tsx, err := newJavaTSExtractor(context.Background())
	if err != nil {
		t.Fatalf("newJavaTSExtractor: %v", err)
	}

	rxByKind := map[string]int{}
	tsByKind := map[string]int{}
	rxSet := map[string]int{}
	tsSet := map[string]int{}
	var rxImports, tsImports, rxCalls, tsCalls, tsErrFiles int

	for _, f := range files {
		src, _ := os.ReadFile(f)
		rel, _ := filepath.Rel(corpus, f)

		rx := extractJava(src, rel)
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

		ts, ok := tsx.extractChecked(src, rel)
		if !ok {
			tsErrFiles++
			continue // dispatcher would fall back to regex; don't count partial
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
		rc := rxSet[k]
		if tc > rc {
			onlyTs += tc - rc
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
	fmt.Fprintf(&b, "\n=== tree-sitter vs regex Java differential — %s (%d files) ===\n", corpus, len(files))
	fmt.Fprintf(&b, "ts parse-error files (regex fallback): %d / %d (%.1f%%)\n",
		tsErrFiles, len(files), 100*float64(tsErrFiles)/float64(len(files)))
	fmt.Fprintf(&b, "SYMBOLS by kind\n  regex: %v (total %d)\n  ts:    %v (total %d)\n", rxByKind, rxTotal, tsByKind, tsTotal)
	fmt.Fprintf(&b, "(name,kind) identity agreement\n")
	fmt.Fprintf(&b, "  in both:    %d\n  only regex: %d\n  only ts:    %d\n", both, onlyRx, onlyTs)
	if rxTotal > 0 {
		fmt.Fprintf(&b, "  agreement:  %.1f%% of regex symbols also found by tree-sitter\n", 100*float64(both)/float64(rxTotal))
	}
	fmt.Fprintf(&b, "EDGES\n  IMPORTS  regex=%d  ts=%d\n", rxImports, tsImports)
	fmt.Fprintf(&b, "  CALLS    regex=%d  ts=%d\n", rxCalls, tsCalls)
	fmt.Fprintf(&b, "===========================================================\n")
	t.Log(b.String())
}
