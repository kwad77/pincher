package server

import (
	"context"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #401: cache hit returns the resolved ID without re-querying the
// store. Validates by upserting a project, resolving once (warm),
// then deleting the project's row directly via the store and
// resolving again — second call must return the cached ID even
// though the underlying row is gone, proving cache hit happened.
func TestResolveProjectID_CacheHitSkipsSQL(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)

	pid := "p401"
	if err := store.UpsertProject(db.Project{ID: pid, Path: "/tmp/" + pid, Name: "p401-name", IndexedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	// First call: cache miss, fall through to GetProject.
	got1, err := srv.resolveProjectID("p401-name")
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	if got1 != pid {
		t.Fatalf("first resolve = %q, want %q", got1, pid)
	}

	// Drop the project row directly. If the cache works, the next
	// resolve still returns the cached ID without falling through
	// to the store.
	if err := store.DeleteProject(pid); err != nil {
		t.Fatalf("delete project: %v", err)
	}

	got2, err := srv.resolveProjectID("p401-name")
	if err != nil {
		t.Fatalf("second resolve (cache hit expected): %v", err)
	}
	if got2 != pid {
		t.Errorf("second resolve = %q, want cached %q (cache miss leaked SQL)", got2, pid)
	}
}

// invalidateProjectIDCache clears every entry; subsequent
// resolve falls through to the store again.
func TestInvalidateProjectIDCache_ForcesRefresh(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)

	pid := "p401i"
	store.UpsertProject(db.Project{ID: pid, Path: "/tmp/" + pid, Name: "p401i-name", IndexedAt: time.Now()})

	// Warm the cache.
	_, err := srv.resolveProjectID("p401i-name")
	if err != nil {
		t.Fatal(err)
	}

	// Drop the row + invalidate. Next resolve should fail with
	// "not found" — proving the cache was cleared and the lookup
	// went back to the store.
	store.DeleteProject(pid)
	srv.invalidateProjectIDCache()

	if _, err := srv.resolveProjectID("p401i-name"); err == nil {
		t.Error("expected not-found after invalidate; got nil error")
	}
}

// Cached entries expire after TTL. Simulated via direct injection
// of an expired entry — production TTL is 60s.
func TestProjectIDCache_RespectsTTLExpiry(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)

	pid := "p401t"
	store.UpsertProject(db.Project{ID: pid, Path: "/tmp/" + pid, Name: "p401t-name", IndexedAt: time.Now()})

	// Inject an expired entry directly.
	srv.projectIDCache.Store("p401t-name", &projectIDCacheEntry{
		id:        "stale-id",
		expiresAt: time.Now().Add(-time.Second),
	})

	// Resolve must skip the expired entry and return the real ID.
	got, err := srv.resolveProjectID("p401t-name")
	if err != nil {
		t.Fatal(err)
	}
	if got != pid {
		t.Errorf("expired-cache resolve = %q, want fresh %q", got, pid)
	}
}

func TestProjectIDCache_RespectsSessionContext(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)

	now := time.Now()
	store.UpsertProject(db.Project{ID: "p401s-a", Path: t.TempDir(), Name: "p401s-name", IndexedAt: now.Add(-time.Hour)})
	store.UpsertProject(db.Project{ID: "p401s-b", Path: t.TempDir(), Name: "p401s-name", IndexedAt: now})

	srv.sessionID = "p401s-a"
	got, err := srv.resolveProjectID("p401s-name")
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	if got != "p401s-a" {
		t.Fatalf("first resolve = %q, want p401s-a", got)
	}

	srv.sessionID = "p401s-b"
	got, err = srv.resolveProjectID("p401s-name")
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if got != "p401s-b" {
		t.Errorf("second resolve = %q, want p401s-b (cache must not reuse another session's name collision)", got)
	}
}

// handleIndex invalidates the cache after a successful re-index.
// End-to-end via the handler entry point.
func TestHandleIndex_InvalidatesProjectIDCache(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)

	pid := "p401h"
	store.UpsertProject(db.Project{ID: pid, Path: "/tmp/" + pid, Name: "p401h-name", IndexedAt: time.Now()})

	// Warm the cache.
	if _, err := srv.resolveProjectID("p401h-name"); err != nil {
		t.Fatal(err)
	}

	// Confirm cache populated.
	if _, ok := srv.lookupProjectIDCache("p401h-name"); !ok {
		t.Fatal("cache should have been populated by warm-up resolve")
	}

	// handleIndex on a (real, on-disk) tempdir — failure path is fine
	// for this test; what matters is that the cache is cleared on
	// the way out. Use t.TempDir() so indexer doesn't crash.
	tempDir := t.TempDir()
	_, err := srv.handleIndex(context.Background(), makeReq(map[string]any{
		"path": tempDir,
	}))
	if err != nil {
		t.Fatalf("handleIndex: %v", err)
	}

	if _, ok := srv.lookupProjectIDCache("p401h-name"); ok {
		t.Errorf("cache should have been invalidated by handleIndex; entry still present")
	}
}
