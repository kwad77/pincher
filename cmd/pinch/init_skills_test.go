// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pinit "github.com/kwad77/pincher/internal/init"
)

// claude-skills previews by default: no --write means no filesystem
// mutation, and the output says so plus how to apply.
func TestRunInitSkillsDryRunByDefault(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	var out bytes.Buffer

	if err := runInitSkills(&out, dest, false); err != nil {
		t.Fatalf("runInitSkills: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "would install pincher-loop") {
		t.Errorf("dry-run should say 'would install pincher-loop', got:\n%s", got)
	}
	if !strings.Contains(got, "--write") {
		t.Errorf("dry-run should tell the user about --write, got:\n%s", got)
	}

	entries, err := os.ReadDir(dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("dry-run must not write; dest has %v", entries)
	}
}

func TestRunInitSkillsWriteInstalls(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	var out bytes.Buffer

	if err := runInitSkills(&out, dest, true); err != nil {
		t.Fatalf("runInitSkills: %v", err)
	}
	if !strings.Contains(out.String(), "installed 5 skill(s)") {
		t.Errorf("want 'installed 5 skill(s)', got:\n%s", out.String())
	}

	for _, skill := range []string{"pincher-loop", "pincher-onboard", "pincher-review", "pincher-debug", "pincher-steward"} {
		md := filepath.Join(dest, skill, "SKILL.md")
		if _, err := os.Stat(md); err != nil {
			t.Errorf("%s not installed: %v", md, err)
		}
	}
	// The flagship's references travel with it.
	if _, err := os.Stat(filepath.Join(dest, "pincher-loop", "references", "egdl-stages.md")); err != nil {
		t.Errorf("pincher-loop references not installed: %v", err)
	}

	// Second run: everything unchanged, nothing re-written.
	out.Reset()
	if err := runInitSkills(&out, dest, true); err != nil {
		t.Fatalf("re-run: %v", err)
	}
	if !strings.Contains(out.String(), "installed 0 skill(s)") {
		t.Errorf("idempotent re-run should install 0, got:\n%s", out.String())
	}
}

// The refusal gate surfaces in the CLI output and survives --write.
func TestRunInitSkillsRefusesNewerLocal(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	newer := "---\nname: pincher-loop\nversion: 99.0.0\n---\n\n# mine\n"
	path := filepath.Join(dest, "pincher-loop", "SKILL.md")
	if err := pinit.WriteFileEnsuringDir(path, newer); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runInitSkills(&out, dest, true); err != nil {
		t.Fatalf("runInitSkills: %v", err)
	}
	if !strings.Contains(out.String(), "refused") || !strings.Contains(out.String(), "99.0.0") {
		t.Errorf("newer local copy should be refused with versions named, got:\n%s", out.String())
	}
	if got := pinit.ReadFileIfExists(path); got != newer {
		t.Error("--write must not overwrite a newer local skill")
	}
}
