// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #1053: `changes scope=staged` (or unstaged) on a clean working tree
// returned 0-everything with NO _meta. Caller couldn't tell "nothing
// changed" from "I asked the wrong scope" — and on a typical workflow
// where the agent stages some files then asks for blast radius, the
// natural recovery (try scope=unstaged or scope=all) was invisible.

func TestHandleChanges_StagedEmpty_PointsAtOtherScopes(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	// Custom repo: commit a file, modify it (unstaged) but DO NOT stage.
	dir := t.TempDir()
	if out, err := runCmd(t, dir, "git", "init"); err != nil {
		t.Skipf("git not available: %v (%s)", err, out)
	}
	runCmd(t, dir, "git", "config", "user.email", "test@test.com")
	runCmd(t, dir, "git", "config", "user.name", "Test")
	target := filepath.Join(dir, "main.go")
	os.WriteFile(target, []byte("package main\nfunc Foo() {}\n"), 0o644)
	runCmd(t, dir, "git", "add", ".")
	runCmd(t, dir, "git", "commit", "-m", "init")
	os.WriteFile(target, []byte("package main\nfunc Foo() { return }\n"), 0o644)
	// Now `scope=unstaged` has 1 file; `scope=staged` has 0.

	store.UpsertProject(db.Project{ID: dir, Path: dir, Name: "empty-staged", IndexedAt: time.Now()})
	srv.sessionID = dir
	srv.sessionRoot = dir

	result, err := srv.handleChanges(context.Background(), makeReq(map[string]any{
		"scope": "staged",
	}))
	if err != nil {
		t.Fatalf("handleChanges: %v", err)
	}
	body := decode(t, result)
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil {
		t.Fatal("_meta missing — expected empty-state diagnosis")
	}
	diagnosis, _ := meta["diagnosis"].(string)
	if !strings.Contains(diagnosis, `scope="staged" is clean`) {
		t.Errorf("expected diagnosis naming staged as clean; got %q", diagnosis)
	}
	if !strings.Contains(diagnosis, "unstaged") {
		t.Errorf("expected diagnosis to mention unstaged (which has changes); got %q", diagnosis)
	}
	steps, _ := meta["next_steps"].([]any)
	sawUnstaged, sawAll := false, false
	for _, st := range steps {
		m, _ := st.(map[string]any)
		args, _ := m["args"].(string)
		switch {
		case strings.Contains(args, `"scope":"unstaged"`):
			sawUnstaged = true
		case strings.Contains(args, `"scope":"all"`):
			sawAll = true
		}
	}
	if !sawUnstaged || !sawAll {
		t.Errorf("expected next_steps to offer unstaged + all scopes; got %v", steps)
	}
}

// All scopes empty — diagnosis should say so and point at base:<branch>.
func TestHandleChanges_AllScopesEmpty_PointsAtBaseScope(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	dir := t.TempDir()
	if out, err := runCmd(t, dir, "git", "init"); err != nil {
		t.Skipf("git not available: %v (%s)", err, out)
	}
	runCmd(t, dir, "git", "config", "user.email", "test@test.com")
	runCmd(t, dir, "git", "config", "user.name", "Test")
	target := filepath.Join(dir, "main.go")
	os.WriteFile(target, []byte("package main\nfunc Foo() {}\n"), 0o644)
	runCmd(t, dir, "git", "add", ".")
	runCmd(t, dir, "git", "commit", "-m", "init")
	// Working tree clean — no modifications at all.

	store.UpsertProject(db.Project{ID: dir, Path: dir, Name: "all-clean", IndexedAt: time.Now()})
	srv.sessionID = dir
	srv.sessionRoot = dir

	result, err := srv.handleChanges(context.Background(), makeReq(map[string]any{
		"scope": "staged",
	}))
	if err != nil {
		t.Fatalf("handleChanges: %v", err)
	}
	body := decode(t, result)
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil {
		t.Fatal("_meta missing — expected empty-state diagnosis")
	}
	diagnosis, _ := meta["diagnosis"].(string)
	if !strings.Contains(diagnosis, "the other scopes (staged/unstaged/all)") ||
		!strings.Contains(diagnosis, `base:<branch>`) {
		t.Errorf("expected diagnosis explaining all-clean + suggesting base scope; got %q", diagnosis)
	}
}

// Control: non-empty scope → no empty-state diagnosis.
func TestHandleChanges_NonEmptyScope_NoEmptyStateDiagnosis(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	repoDir := setupChangesGitRepo(t)
	store.UpsertProject(db.Project{ID: repoDir, Path: repoDir, Name: "nonempty", IndexedAt: time.Now()})
	srv.sessionID = repoDir
	srv.sessionRoot = repoDir

	result, err := srv.handleChanges(context.Background(), makeReq(map[string]any{
		"scope": "unstaged",
	}))
	if err != nil {
		t.Fatalf("handleChanges: %v", err)
	}
	body := decode(t, result)
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil {
		return
	}
	diagnosis, _ := meta["diagnosis"].(string)
	if strings.Contains(diagnosis, "is clean") {
		t.Errorf("non-empty scope must not produce empty-state diagnosis; got %q", diagnosis)
	}
}
