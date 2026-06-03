// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
	"github.com/kwad77/pincher/internal/index"
)

// #719: when the supervisor respawns the inner onto a swapped binary,
// the index on disk was still built by the OLD binary. driftReindexNeeded
// is the decision: session project's BinaryVersion != running version.

func TestDriftReindexNeeded(t *testing.T) {
	cases := []struct {
		name          string
		serverVersion string
		projectExists bool
		projectVer    string
		wantDrift     bool
	}{
		{"no project in db", "0.56.0", false, "", false},
		{"matching version", "0.56.0", true, "0.56.0", false},
		{"unstamped project", "0.56.0", true, "", false},
		{"drifted version", "0.56.0", true, "0.55.0", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, store, _ := newTestServer(t)
			srv.version = c.serverVersion
			dir := t.TempDir()
			srv.setRoot(dir)
			if c.projectExists {
				if err := store.UpsertProject(db.Project{
					ID:            srv.sessionID,
					Path:          dir,
					Name:          db.ProjectNameFromPath(dir),
					BinaryVersion: c.projectVer,
				}); err != nil {
					t.Fatalf("seed project: %v", err)
				}
			}
			_, drifted := srv.driftReindexNeeded()
			if drifted != c.wantDrift {
				t.Errorf("driftReindexNeeded() drifted = %v, want %v", drifted, c.wantDrift)
			}
		})
	}
}

// maybeReindexOnDrift must actually kick off the background re-index
// when drift is detected — the re-index re-stamps the project at the
// running binary version, so the drift converges away.
func TestMaybeReindexOnDrift_ConvergesVersion(t *testing.T) {
	srv, store, _ := newTestServer(t)
	srv.version = "0.56.0-new"
	srv.indexer.SetBinaryVersion("0.56.0-new")

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module probe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package probe\n\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv.setRoot(dir)

	// Seed the project as if indexed by an OLD binary.
	if err := store.UpsertProject(db.Project{
		ID:            srv.sessionID,
		Path:          dir,
		Name:          db.ProjectNameFromPath(dir),
		BinaryVersion: "0.55.0-old",
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	srv.maybeReindexOnDrift()

	// The background re-index should re-stamp BinaryVersion to the
	// running version. Poll up to a generous window.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		p, err := store.GetProject(srv.sessionID)
		if err == nil && p != nil && p.BinaryVersion == "0.56.0-new" {
			return // converged — re-index ran
		}
		time.Sleep(20 * time.Millisecond)
	}
	p, _ := store.GetProject(srv.sessionID)
	got := ""
	if p != nil {
		got = p.BinaryVersion
	}
	t.Errorf("drift did not converge: BinaryVersion = %q, want %q (background re-index never ran)", got, "0.56.0-new")
}

// #1889: the server-level drift refresh must not pass force=true. The
// indexer already owns the binary-drift decision and can suppress full
// re-extraction for invalidatesNothing migrations or narrow it for
// language-scoped invalidations. A forced call here bypasses that gate
// and turns every MCP reconnect into a potential full-project writer
// collision.
func TestMaybeReindexOnDrift_UsesIncrementalPass(t *testing.T) {
	srv, store, _ := newTestServer(t)
	srv.version = "0.94.0"

	dir := t.TempDir()
	srv.setRoot(dir)
	if err := store.UpsertProject(db.Project{
		ID:            srv.sessionID,
		Path:          dir,
		Name:          db.ProjectNameFromPath(dir),
		BinaryVersion: "0.93.0",
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	type call struct {
		path  string
		force bool
	}
	calls := make(chan call, 1)
	srv.driftIndex = func(_ context.Context, path string, force bool) (*index.IndexResult, error) {
		calls <- call{path: path, force: force}
		return &index.IndexResult{}, nil
	}

	srv.maybeReindexOnDrift()

	select {
	case got := <-calls:
		if got.path != dir {
			t.Errorf("drift index path = %q, want %q", got.path, dir)
		}
		if got.force {
			t.Fatal("drift refresh called Index with force=true; want incremental pass so indexer drift gate remains effective")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("drift refresh did not invoke indexer")
	}
}

func TestMaybeReindexOnDrift_SkipsHealthCheckProbe(t *testing.T) {
	t.Setenv("PINCHER_HEALTH_CHECK", "1")

	srv, store, _ := newTestServer(t)
	srv.version = "0.94.0"

	dir := t.TempDir()
	srv.setRoot(dir)
	if err := store.UpsertProject(db.Project{
		ID:            srv.sessionID,
		Path:          dir,
		Name:          db.ProjectNameFromPath(dir),
		BinaryVersion: "0.93.0",
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	calls := make(chan struct{}, 1)
	srv.driftIndex = func(context.Context, string, bool) (*index.IndexResult, error) {
		calls <- struct{}{}
		return &index.IndexResult{}, nil
	}

	srv.maybeReindexOnDrift()

	select {
	case <-calls:
		t.Fatal("health-check probe triggered background drift reindex; liveness probes must not write the live DB")
	case <-time.After(100 * time.Millisecond):
		// Good: no background writer started.
	}
}
