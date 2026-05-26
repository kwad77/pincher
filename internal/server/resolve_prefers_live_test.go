package server

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// When multiple projects share a name (typical after a repo gets
// moved on disk — the stale row hangs around until prune_dead),
// resolveProjectID used to return the first match in ListProjects
// order regardless of whether the path was still alive on disk.
// A stale `D:\…\pincher` (0 symbols, dir gone) routinely
// out-resolved the live `D:\ClaudeCode\pincher-repo`, and downstream
// tools returned silently empty results. Now: live-on-disk wins
// over dead-on-disk.

func TestResolveProjectID_PrefersLiveOverDead(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)

	liveDir := t.TempDir()
	deadDir := filepath.Join(t.TempDir(), "gone")
	// deadDir intentionally not created — os.Stat must fail.

	// Insert the DEAD one first so the naive "first match wins" path
	// would have returned it. The fix has to flip that ordering.
	store.UpsertProject(db.Project{
		ID: "id-dead", Path: deadDir, Name: "twin", IndexedAt: time.Now().AddDate(0, 0, -7),
	})
	store.UpsertProject(db.Project{
		ID: "id-live", Path: liveDir, Name: "twin", IndexedAt: time.Now(),
	})

	got, err := srv.resolveProjectID("twin")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "id-live" {
		t.Errorf("resolveProjectID(\"twin\") = %q, want id-live (live-on-disk should win over dead)", got)
	}
}

// When only a dead-on-disk match exists, the resolver still returns
// it (so callers don't break) but logs the warning for ops. The
// behavior on dead-only is unchanged from the pre-fix shape; the
// regression to defend against is "first match wins regardless of
// liveness."
func TestResolveProjectID_DeadOnlyStillResolves(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)

	deadDir := filepath.Join(t.TempDir(), "gone")
	store.UpsertProject(db.Project{
		ID: "id-dead-only", Path: deadDir, Name: "lonely", IndexedAt: time.Now(),
	})

	got, err := srv.resolveProjectID("lonely")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "id-dead-only" {
		t.Errorf("resolveProjectID(\"lonely\") = %q, want id-dead-only", got)
	}
}

func TestResolveProjectID_LiveCollisionPrefersSessionProject(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)

	olderLive := t.TempDir()
	sessionLive := t.TempDir()
	store.UpsertProject(db.Project{
		ID: "id-older-live", Path: olderLive, Name: "twin-live", IndexedAt: time.Now().AddDate(0, 0, -7),
	})
	store.UpsertProject(db.Project{
		ID: "id-session-live", Path: sessionLive, Name: "twin-live", IndexedAt: time.Now().AddDate(0, 0, -1),
	})
	srv.sessionID = "id-session-live"

	got, err := srv.resolveProjectID("twin-live")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "id-session-live" {
		t.Errorf("resolveProjectID(\"twin-live\") = %q, want id-session-live (session project should win live name collisions)", got)
	}
}

func TestResolveProjectID_LiveCollisionPrefersNewestIndexed(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)

	oldLive := t.TempDir()
	newLive := t.TempDir()
	store.UpsertProject(db.Project{
		ID: "id-old-live", Path: oldLive, Name: "twin-newest", IndexedAt: time.Now().AddDate(0, 0, -7),
	})
	store.UpsertProject(db.Project{
		ID: "id-new-live", Path: newLive, Name: "twin-newest", IndexedAt: time.Now(),
	})

	got, err := srv.resolveProjectID("twin-newest")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "id-new-live" {
		t.Errorf("resolveProjectID(\"twin-newest\") = %q, want id-new-live (newest indexed live project should win same-name collisions)", got)
	}
}
