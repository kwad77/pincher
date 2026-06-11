// SPDX-License-Identifier: MIT

// Gated differential test (ADR-0008 / #1958 EGDL Stage 7): real-tree-sitter
// TS/TSX extractor vs the regex tier on a real corpus. Routes .tsx through the
// tsx grammar, everything else through typescript. Runs only when
// PINCHER_SPIKE_CORPUS_TS points at a dir of .ts/.tsx files.

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

func TestTreeSitterTS_DiffVsRegex(t *testing.T) {
	corpus := os.Getenv("PINCHER_SPIKE_CORPUS_TS")
	if corpus == "" {
		t.Skip("set PINCHER_SPIKE_CORPUS_TS=<dir of .ts/.tsx files> to run the tree-sitter differential")
	}
	var files []string
	_ = filepath.Walk(corpus, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && (strings.HasSuffix(p, ".ts") || strings.HasSuffix(p, ".tsx")) &&
			!strings.HasSuffix(p, ".d.ts") {
			files = append(files, p)
		}
		return nil
	})
	sort.Strings(files)
	if len(files) == 0 {
		t.Fatalf("no .ts/.tsx under %s", corpus)
	}

	tsx, err := newTSTSExtractor(context.Background(), false)
	if err != nil {
		t.Fatalf("newTSTSExtractor: %v", err)
	}
	tsxx, err := newTSTSExtractor(context.Background(), true)
	if err != nil {
		t.Fatalf("newTSTSExtractor(tsx): %v", err)
	}

	rxByKind, tsByKind := map[string]int{}, map[string]int{}
	rxSet, tsSet := map[string]int{}, map[string]int{}
	var rxImports, tsImports, rxCalls, tsCalls, tsErrFiles int

	for _, f := range files {
		src, _ := os.ReadFile(f)
		rel, _ := filepath.Rel(corpus, f)
		lang := "TypeScript"
		ex := tsx
		if strings.HasSuffix(f, ".tsx") {
			lang, ex = "TSX", tsxx
		}
		_ = lang

		ts, ok := ex.extractChecked(src, rel)
		if !ok {
			// Parse error → the production dispatcher routes this file to the
			// regex tier, so its regex symbols are exactly what ships. Exclude
			// it from BOTH tallies so the agreement % compares like-for-like
			// (ts-clean vs regex-on-the-same-clean-files) instead of penalizing
			// ts for files it never claims.
			tsErrFiles++
			continue
		}

		rx := extractTypeScript(src, rel)
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
	fmt.Fprintf(&b, "\n=== tree-sitter vs regex TS/TSX differential — %s (%d files) ===\n", corpus, len(files))
	fmt.Fprintf(&b, "ts parse-error files (regex fallback): %d / %d (%.1f%%)\n",
		tsErrFiles, len(files), 100*float64(tsErrFiles)/float64(len(files)))
	fmt.Fprintf(&b, "SYMBOLS by kind\n  regex: %v (total %d)\n  ts:    %v (total %d)\n", rxByKind, rxTotal, tsByKind, tsTotal)
	fmt.Fprintf(&b, "(name,kind) identity agreement\n  in both: %d\n  only regex: %d\n  only ts: %d\n", both, onlyRx, onlyTs)
	if rxTotal > 0 {
		fmt.Fprintf(&b, "  agreement: %.1f%% of regex symbols also found by tree-sitter\n", 100*float64(both)/float64(rxTotal))
	}
	fmt.Fprintf(&b, "EDGES\n  IMPORTS regex=%d ts=%d\n  CALLS regex=%d ts=%d\n", rxImports, tsImports, rxCalls, tsCalls)
	fmt.Fprintf(&b, "===========================================================\n")
	t.Log(b.String())
}
