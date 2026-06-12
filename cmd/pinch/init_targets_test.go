// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pinit "github.com/kwad77/pincher/internal/init"
)

// CLI orchestration tests for runInitTarget. The pure target writers
// + path resolvers + detection live in internal/init and are tested
// directly there. This file covers the CLI's runInitTarget wrapper:
// dry-run vs write, --global handling for unsupported targets, and
// the AlwaysGlobal continue case.

// withCWD switches into dir for the duration of the test, restoring on
// cleanup. CLI tests need this because runInitTarget resolves paths
// relative to cwd.
func withCWD(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

// withHome overrides HOME / USERPROFILE so UserHomeDir() returns dir.
func withHome(t *testing.T, dir string) {
	t.Helper()
	if v, ok := os.LookupEnv("HOME"); ok {
		t.Setenv("HOME", v)
	}
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
}

func TestRunInitTarget_WritesAndIsIdempotent(t *testing.T) {
	tmp := t.TempDir()
	withCWD(t, tmp)

	var buf strings.Builder
	if err := runInitTarget(&buf, pinit.CursorTarget, tmp, false, false, pinit.PolicyMarkdown); err != nil {
		t.Fatalf("first write: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(tmp, ".cursor", "rules", "pincher.mdc"))
	if err != nil {
		t.Fatalf("expected file written: %v", err)
	}
	if !strings.HasPrefix(string(got), "---\n") {
		t.Error("expected MDC frontmatter on first write")
	}
	first := string(got)

	if err := runInitTarget(&buf, pinit.CursorTarget, tmp, false, false, pinit.PolicyMarkdown); err != nil {
		t.Fatalf("second write: %v", err)
	}
	got2, _ := os.ReadFile(filepath.Join(tmp, ".cursor", "rules", "pincher.mdc"))
	if string(got2) != first {
		t.Error("re-running runInitTarget changed file contents (not idempotent)")
	}
}

func TestRunInitTarget_DryRunDoesNotWrite(t *testing.T) {
	tmp := t.TempDir()
	withCWD(t, tmp)

	var buf strings.Builder
	if err := runInitTarget(&buf, pinit.WindsurfTarget, tmp, false, true, pinit.PolicyMarkdown); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, ".windsurf", "rules", "pincher.md")); !os.IsNotExist(err) {
		t.Errorf("dry run should NOT create file; stat err=%v", err)
	}
	// #803: present-tense verb — "would write", not "would wrote".
	if !strings.Contains(buf.String(), "would write") {
		t.Errorf("expected 'would write' in dry-run output, got: %s", buf.String())
	}
}

func TestRunInitTarget_GlobalIgnoredForUnsupportedTargets(t *testing.T) {
	tmp := t.TempDir()
	withCWD(t, tmp)
	withHome(t, tmp)

	var buf strings.Builder
	// windsurf has SupportsGlobal=false; passing global=true should
	// silently fall back to project-local rather than erroring.
	if err := runInitTarget(&buf, pinit.WindsurfTarget, tmp, true, false, pinit.PolicyMarkdown); err != nil {
		t.Fatalf("windsurf with --global should not error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, ".windsurf", "rules", "pincher.md")); err != nil {
		t.Errorf("expected project-local windsurf file: %v", err)
	}
}

func TestRunInitTarget_ContinueAlwaysGlobal(t *testing.T) {
	tmp := t.TempDir()
	withHome(t, tmp)

	var buf strings.Builder
	if err := runInitTarget(&buf, pinit.ContinueTarget, tmp, false, false, pinit.PolicyMarkdown); err != nil {
		t.Fatalf("continue: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(tmp, ".continue", "config.json"))
	if err != nil {
		t.Fatalf("expected ~/.continue/config.json: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(got, &cfg); err != nil {
		t.Fatalf("written config.json is not valid JSON: %v", err)
	}
	if _, ok := cfg["systemMessage"]; !ok {
		t.Error("expected systemMessage key")
	}
}
