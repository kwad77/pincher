// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// Glob support in hook-check: advisory hint (never block) when a glob
// targets code files inside an indexed project; silent pass-through
// otherwise. Mirrors the v0.86 advisory posture of Read (#1654) and
// Grep (#1656).

func TestDecideHook_Glob_CodePatternInIndexedDir_Advisory(t *testing.T) {
	// Pin the toolset so the assertion is deterministic regardless of
	// the developer's environment (#2011): under the core default the
	// advisory recommends `search`, which is advertised in both modes.
	t.Setenv("PINCHER_TOOLSET", "core")
	store := newHookTestStore(t)
	projectDir := t.TempDir()
	indexLargeFakeFile(t, store, projectDir, "internal/server/server.go", 50000)

	in := hookCheckInput{
		ToolName: "Glob",
		ToolInput: map[string]any{
			"pattern": "**/*.go",
			"path":    projectDir,
		},
	}
	d := decideHook(store, in, false)
	if !d.Continue {
		t.Fatalf("advisory mode must NEVER block; got %+v", d)
	}
	if d.Decision != "redirect_advisory" {
		t.Errorf("decision = %q, want redirect_advisory", d.Decision)
	}
	if d.SuggestedTool != "search" {
		t.Errorf("suggested tool = %q, want search (core-toolset default, #2011)", d.SuggestedTool)
	}
	if !strings.Contains(d.SystemMessage, "`search`") {
		t.Errorf("system message should name the suggested tool; got %q", d.SystemMessage)
	}
	if d.StopReason != "" {
		t.Errorf("advisory mode must not set StopReason; got %q", d.StopReason)
	}
}

func TestDecideHook_Glob_SubdirOfIndexedProject_Advisory(t *testing.T) {
	store := newHookTestStore(t)
	projectDir := t.TempDir()
	indexLargeFakeFile(t, store, projectDir, "internal/server/server.go", 50000)

	in := hookCheckInput{
		ToolName: "Glob",
		ToolInput: map[string]any{
			"pattern": "*.go",
			"path":    filepath.Join(projectDir, "internal"),
		},
	}
	d := decideHook(store, in, false)
	if d.Decision != "redirect_advisory" {
		t.Errorf("subdir of indexed project should get the hint; got %+v", d)
	}
}

func TestDecideHook_Glob_NoPath_PassesThrough(t *testing.T) {
	// Without `path` the glob runs in the agent's cwd, which the hook
	// can't see — must pass through silently rather than guess.
	store := newHookTestStore(t)
	projectDir := t.TempDir()
	indexLargeFakeFile(t, store, projectDir, "f.go", 50000)

	in := hookCheckInput{
		ToolName:  "Glob",
		ToolInput: map[string]any{"pattern": "**/*.go"},
	}
	d := decideHook(store, in, false)
	if !d.Continue || d.Decision != "pass_through" {
		t.Errorf("pathless glob should silently pass through; got %+v", d)
	}
	if d.SystemMessage != "" {
		t.Errorf("pathless glob must not emit a hint; got %q", d.SystemMessage)
	}
}

func TestDecideHook_Glob_NonCodePattern_PassesThrough(t *testing.T) {
	store := newHookTestStore(t)
	projectDir := t.TempDir()
	indexLargeFakeFile(t, store, projectDir, "f.go", 50000)

	for _, pattern := range []string{"**/*.md", "**/*.yaml", "**/*", "LICENSE*"} {
		t.Run(pattern, func(t *testing.T) {
			in := hookCheckInput{
				ToolName: "Glob",
				ToolInput: map[string]any{
					"pattern": pattern,
					"path":    projectDir,
				},
			}
			d := decideHook(store, in, false)
			if !d.Continue || d.SystemMessage != "" {
				t.Errorf("non-code glob %q should silently pass through; got %+v", pattern, d)
			}
		})
	}
}

func TestDecideHook_Glob_UnindexedDir_PassesThrough(t *testing.T) {
	store := newHookTestStore(t)
	in := hookCheckInput{
		ToolName: "Glob",
		ToolInput: map[string]any{
			"pattern": "**/*.go",
			"path":    "/nowhere/at/all",
		},
	}
	d := decideHook(store, in, false)
	if !d.Continue || d.SystemMessage != "" {
		t.Errorf("glob outside indexed projects should silently pass through; got %+v", d)
	}
}

// Read-only regression coverage: the CLI must produce a real decision
// (proving the DB was actually read, not error-pass-through'd) while a
// separate write-mode Store holds the writer connection — the
// configuration that previously made every hook call fail open with
// SQLITE_BUSY, because db.Open ran migrations on the hook's hot path.
func TestRunHookCheckCLI_ReadOnly_DecidesWhileWriterOpen(t *testing.T) {
	dataDir := t.TempDir()
	store, err := db.Open(dataDir)
	if err != nil {
		t.Fatalf("seed open: %v", err)
	}
	defer store.Close() // writer stays open for the CLI run below

	projectDir := t.TempDir()
	relPath := "internal/server/server.go"
	indexLargeFakeFile(t, store, projectDir, relPath, 50000)

	in, _ := os.CreateTemp(t.TempDir(), "stdin")
	in.WriteString(`{"tool_name":"Read","tool_input":{"file_path":"` +
		filepath.ToSlash(filepath.Join(projectDir, relPath)) + `"}}`)
	in.Close()
	stdinFile, _ := os.Open(in.Name())
	defer stdinFile.Close()

	outFile, _ := os.CreateTemp(t.TempDir(), "stdout")
	defer outFile.Close()

	origStdin, origStdout := os.Stdin, os.Stdout
	os.Stdin = stdinFile
	os.Stdout = outFile
	defer func() { os.Stdin = origStdin; os.Stdout = origStdout }()

	runHookCheckCLI([]string{"--data-dir", dataDir})

	outFile.Sync()
	body, _ := os.ReadFile(outFile.Name())
	got := strings.TrimSpace(string(body))
	if !strings.Contains(got, `"continue":true`) {
		t.Fatalf("hook must never block; got %q", got)
	}
	// The advisory hint is the proof the DB was read: a db-open
	// failure degrades to a silent pass-through with no systemMessage.
	if !strings.Contains(got, "systemMessage") {
		t.Errorf("expected advisory hint (DB read in ro mode while writer open); got silent pass-through: %q", got)
	}
}
