// SPDX-License-Identifier: MIT

package index

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

// #1613 v0.85: per-stage resolve-pass observability. Each of the four
// resolvers (IMPORTS / CALLS / READS / USES_VAR) emits a single
// `pincher.resolve.summary` slog.Info line carrying kind / pending_in /
// resolved_out / dropped / duration_ms. The line lands only when
// pending_in > 0 — quiet on no-op ticks so healthy projects don't
// spam logs.
//
// This test seeds a small Go project with at least one in-project
// IMPORTS + CALLS edge, captures slog output during indexing, and
// verifies the expected summary lines fire with the expected fields.
// Pins the shape so a future refactor that drops a field surfaces in
// PR review.

func TestResolveObservability_EmitsPerStageSummary_1613(t *testing.T) {
	idx, _ := newTestIndexer(t)
	dir := t.TempDir()

	// Minimal Go project: pkg b's func is called from pkg a.
	writeFile(t, dir, "go.mod", "module example.com/probe\n\ngo 1.21\n")
	writeFile(t, dir, "b/b.go", `package b

func Helper() int { return 42 }
`)
	writeFile(t, dir, "a/a.go", `package a

import "example.com/probe/b"

func Use() int { return b.Helper() }
`)

	// Capture slog output during Index().
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	if _, err := idx.Index(context.Background(), dir, true); err != nil {
		t.Fatalf("Index: %v", err)
	}

	out := buf.String()

	// Both IMPORTS and CALLS should have fired with pending_in > 0 on
	// this corpus. USES_VAR + READS are correctly absent (no ansible
	// fixtures, no go package-var reads in this minimal sample).
	for _, kind := range []string{"IMPORTS", "CALLS"} {
		needle := "pincher.resolve.summary"
		if !strings.Contains(out, needle) {
			t.Fatalf("expected slog output to contain %q; got:\n%s", needle, out)
		}
		if !strings.Contains(out, "kind="+kind) {
			t.Errorf("expected `pincher.resolve.summary` line for kind=%s; got:\n%s", kind, out)
		}
	}

	// Required fields on the summary line — pin the shape so a
	// refactor that drops a field fails loud rather than silently
	// regressing the observability surface.
	for _, field := range []string{"pending_in=", "resolved_out=", "dropped=", "duration_ms="} {
		if !strings.Contains(out, field) {
			t.Errorf("`pincher.resolve.summary` line missing field %q; got:\n%s", field, out)
		}
	}

	// #1613 v0.85 follow-up: per-language extraction breakdown must
	// also fire for a non-empty corpus. Pin the line shape + at least
	// one lang_1_* set so a future refactor that drops the
	// per-language detail surfaces in PR review.
	if !strings.Contains(out, "pincher.index.extraction.by_language") {
		t.Errorf("expected pincher.index.extraction.by_language line for non-empty corpus; got:\n%s", out)
	}
	for _, field := range []string{"lang_1_name=", "lang_1_ms=", "lang_1_files=", "total_languages="} {
		if !strings.Contains(out, field) {
			t.Errorf("`pincher.index.extraction.by_language` line missing field %q; got:\n%s", field, out)
		}
	}
}

// Negative: on an empty project (no symbols, no pending edges), the
// resolve wrapper short-circuits per-stage when pending_in==0 — no
// summary line lands. Keeps healthy/empty projects quiet so the line
// becomes a real signal when it does appear.
func TestResolveObservability_QuietOnEmptyProject_1613(t *testing.T) {
	idx, _ := newTestIndexer(t)
	dir := t.TempDir()

	// Empty dir — no files to index. The walker yields nothing,
	// extraction emits nothing, the resolve block runs but every
	// allXxx slice is empty.
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	if _, err := idx.Index(context.Background(), dir, true); err != nil {
		t.Fatalf("Index: %v", err)
	}

	if strings.Contains(buf.String(), "pincher.resolve.summary") {
		t.Errorf("expected no `pincher.resolve.summary` line on empty project (quiet on no-op); got:\n%s", buf.String())
	}
}
