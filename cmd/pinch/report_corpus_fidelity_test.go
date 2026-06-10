// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
	"github.com/kwad77/pincher/internal/index"
)

// indexedAtRE matches the report's "Indexed: <RFC3339>" line. The
// timestamp comes from project.IndexedAt, which is set by the indexer
// at index time and therefore drifts every run. Normalise it to a
// stable placeholder so the snapshot is reproducible.
var indexedAtRE = regexp.MustCompile(`- Indexed: \d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z`)

// updateReportCorpus regenerates the testdata/corpus/<name>.report.md
// golden files. Run with:
//   go test ./cmd/pinch -run TestReportCorpus -update-report-corpus
// then inspect the diff and commit the golden files alongside the change.
var updateReportCorpus = flag.Bool("update-report-corpus", false,
	"regenerate testdata/corpus/<name>.report.md golden snapshots")

// TestReportCorpus_FidelityAcrossLanguages snapshots the `pincher report`
// markdown output against a fixed set of synthetic corpora so format drift
// and cross-language regressions surface in CI before they reach a release.
//
// The pincher self-index has the most extraction coverage but pincher is a
// Go project — TestWriteProjectReportMarkdown already exercises that path
// on hand-rolled fixtures. This test extends the gate across the language
// profiles the v1.2 report path is supposed to serve:
//
//   - python-app  (Python AST extraction; classes/methods)
//   - go-project  (Go AST extraction; functions/methods)
//   - node-monorepo (JavaScript regex+AST extraction; modules/functions)
//
// Each subtest:
//  1. Indexes the corpus into a fresh temp store.
//  2. Loads the persisted project + symbols + edges.
//  3. Renders the report with a fixed GeneratedAt so the output is
//     timestamp-stable.
//  4. Normalises absolute temp-paths to a stable placeholder.
//  5. Compares against the committed `testdata/corpus/<name>.report.md`.
//
// Snapshot drift is intentionally brittle: when extraction, hotspot
// scoring, rationale grouping, or surprising-connection ordering changes,
// the diff IS the rationale. Regenerate with -update-report-corpus and
// commit the new golden alongside the implementation change.
func TestReportCorpus_FidelityAcrossLanguages(t *testing.T) {
	corpora := []string{
		"python-app",
		"go-project",
		"node-monorepo",
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	root := findRepoRoot(wd)
	if root == "" {
		t.Fatalf("findRepoRoot from %s returned empty — repo root not found", wd)
	}

	for _, name := range corpora {
		t.Run(name, func(t *testing.T) {
			corpusDir := filepath.Join(root, "testdata", "corpus", name)
			if _, err := os.Stat(corpusDir); err != nil {
				t.Skipf("corpus dir %s missing: %v", corpusDir, err)
			}

			dataDir := t.TempDir()
			store, err := db.Open(dataDir)
			if err != nil {
				t.Fatalf("db.Open: %v", err)
			}
			defer store.Close()

			res, err := index.New(store).Index(context.Background(), corpusDir, false)
			if err != nil {
				t.Fatalf("Index(%s): %v", name, err)
			}
			projectPtr, err := store.GetProject(res.ProjectID)
			if err != nil || projectPtr == nil {
				t.Fatalf("GetProject(%s): %v", res.ProjectID, err)
			}
			project := *projectPtr

			symbols, err := store.ListSymbolsForProject(project.ID)
			if err != nil {
				t.Fatalf("ListSymbolsForProject: %v", err)
			}
			edges, err := store.ListEdgesForProject(project.ID)
			if err != nil {
				t.Fatalf("ListEdgesForProject: %v", err)
			}

			var buf bytes.Buffer
			opts := reportOptions{
				GeneratedAt: time.Unix(1700000100, 0).UTC(),
			}
			if err := writeProjectReportMarkdown(&buf, project, symbols, edges, opts); err != nil {
				t.Fatalf("writeProjectReportMarkdown: %v", err)
			}

			got := normaliseCorpusReport(buf.String(), project, name)

			goldenPath := filepath.Join(root, "testdata", "corpus", name+".report.md")
			if *updateReportCorpus {
				if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
					t.Fatalf("write golden %s: %v", goldenPath, err)
				}
				t.Logf("regenerated %s (%d bytes)", goldenPath, len(got))
				return
			}

			wantBytes, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read golden %s: %v\n\nIf this is the first run after adding the test, regenerate with:\n  go test ./cmd/pinch -run TestReportCorpus -update-report-corpus",
					goldenPath, err)
			}
			// Normalise line endings before comparing — Windows git
			// checkouts convert LF to CRLF by default, which would otherwise
			// make every byte-identical golden look "drifted" because of
			// invisible \r characters. The visual diff in the failure
			// message would show identical lines, masking the cause.
			gotN := strings.ReplaceAll(got, "\r\n", "\n")
			wantN := strings.ReplaceAll(string(wantBytes), "\r\n", "\n")
			if gotN != wantN {
				t.Errorf("%s report drift — review the diff and re-run with -update-report-corpus to refresh\n%s", name, diffLeadingLines(gotN, wantN, 40))
			}
		})
	}
}

// normaliseCorpusReport replaces machine-specific paths, IDs, and the
// per-run IndexedAt timestamp with stable placeholders so the golden
// file is portable across the TempDir hierarchy and CI runners.
//
// project.ID and project.Path can diverge on macOS (where `/tmp` is a
// symlink to `/private/tmp` so `os.Getwd()` resolves further than
// `t.TempDir()` produces) and on Windows (drive letters, separators).
// We unconditionally replace both with the same stable marker so the
// golden file looks identical regardless of platform — the project's
// "identity" in the report is the corpus name, not the absolute path.
func normaliseCorpusReport(s string, project db.Project, corpusName string) string {
	stable := "/corpus/" + corpusName
	if project.Path != "" {
		s = strings.ReplaceAll(s, project.Path, stable)
	}
	if project.ID != "" && project.ID != project.Path {
		s = strings.ReplaceAll(s, project.ID, stable)
	}
	s = indexedAtRE.ReplaceAllString(s, "- Indexed: 2026-01-01T00:00:00Z")
	return s
}

// diffLeadingLines returns a compact head-of-diff view so the failure
// message stays readable when the report drifts by hundreds of lines.
func diffLeadingLines(got, want string, n int) string {
	gotLines := strings.SplitN(got, "\n", n+1)
	wantLines := strings.SplitN(want, "\n", n+1)
	limit := n
	if len(gotLines) < limit {
		limit = len(gotLines)
	}
	if len(wantLines) < limit {
		limit = len(wantLines)
	}
	for i := 0; i < limit; i++ {
		if gotLines[i] != wantLines[i] {
			return fmt.Sprintf("--- first divergence at line %d ---\ngot : %s\nwant: %s\n--- (truncated; rerun with -v + diff the temp file for full context) ---\n",
				i+1, gotLines[i], wantLines[i])
		}
	}
	if len(gotLines) != len(wantLines) {
		return fmt.Sprintf("got/want match the first %d lines but differ in length (got=%d, want=%d)\n", limit, len(gotLines), len(wantLines))
	}
	return fmt.Sprintf("drift after first %d lines (rerun with -v to capture full output)\n", n)
}

// (findRepoRoot lives in cmd/pinch/update.go — reused here so the test
// participates in the same repo-root detection convention.)
