package index

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestWatch_SerializesPerProjectIndex pins the #1496 contract: Watch's
// per-tick reindex calls Index() serially across projects, NOT in a
// per-project goroutine fan-out. Pre-fix, the watcher grabbed every
// project's cross-process lockfile simultaneously after a schema-
// migration auto-restart, blocking user `force=true` calls against any
// project until the slowest reindex finished.
//
// We instrument via SetEventHook (the existing index_started /
// index_complete callback hook used by /v1/events SSE). A counter
// tracks how many Index() calls are concurrently mid-flight. Under
// the serialised contract the maximum observed concurrency is 1.
func TestWatch_SerializesPerProjectIndex(t *testing.T) {
	idx, _ := newTestIndexer(t)
	idx.watchInterval = 50 * time.Millisecond

	// Two projects with one file each. Touched immediately so both
	// look changed to the watcher's mtime scan.
	dirA := t.TempDir()
	dirB := t.TempDir()
	writeFile(t, dirA, "a.go", "package a\nfunc A() {}\n")
	writeFile(t, dirB, "b.go", "package b\nfunc B() {}\n")
	if _, err := idx.Index(context.Background(), dirA, false); err != nil {
		t.Fatalf("prime A: %v", err)
	}
	if _, err := idx.Index(context.Background(), dirB, false); err != nil {
		t.Fatalf("prime B: %v", err)
	}
	// Mtime > prior IndexedAt so changedFiles returns non-empty.
	time.Sleep(20 * time.Millisecond)
	writeFile(t, dirA, "a.go", "package a\nfunc A() {}\nfunc A2() {}\n")
	writeFile(t, dirB, "b.go", "package b\nfunc B() {}\nfunc B2() {}\n")

	// Instrument concurrency from the indexer's actual active map. The event
	// stream only emits index_complete on success, so a cancelled/error return
	// can otherwise look like a leaked in-flight index in this test even after
	// Index has removed itself from idx.active.
	var maxActive atomic.Int32
	var startCount atomic.Int32
	var seenStartedFor sync.Map // projectID → struct{}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	idx.SetEventHook(func(eventType string, payload map[string]any) {
		switch eventType {
		case "index_started":
			idx.mu.Lock()
			cur := int32(len(idx.active))
			idx.mu.Unlock()
			for {
				m := maxActive.Load()
				if cur <= m || maxActive.CompareAndSwap(m, cur) {
					break
				}
			}
			if pid, ok := payload["project_id"].(string); ok {
				seenStartedFor.Store(pid, struct{}{})
			}
			if startCount.Add(1) >= 2 {
				cancel()
			}
		}
	})

	done := make(chan struct{})
	go func() {
		idx.Watch(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Watch did not return after serial reindexes")
	}

	// Wait for any in-flight Index goroutines to settle (they can
	// outlast Watch's return — same belt-and-suspenders the existing
	// TestWatch_TriggersReindex uses).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !idx.anyActive() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Both projects must have been reindexed by Watch.
	startedCount := 0
	seenStartedFor.Range(func(_, _ any) bool { startedCount++; return true })
	if startedCount < 2 {
		t.Errorf("Watch reindexed %d projects, want at least 2 (A + B). Hook events: %d active peak", startedCount, maxActive.Load())
	}

	// The serialise contract: never more than one in-flight at a time.
	if got := maxActive.Load(); got > 1 {
		t.Errorf("Watch ran %d Index calls concurrently; want max 1 (serial). #1496 regression — Watch is fanning out goroutines again.", got)
	}
}

// TestWatch_SingleProjectStillReindexes is the control: with only one
// project changed, the serialised path must still reindex it. Guards
// against an over-eager refactor that accidentally drops projects
// when no goroutine fan-out is in play.
func TestWatch_SingleProjectStillReindexes(t *testing.T) {
	idx, _ := newTestIndexer(t)
	idx.watchInterval = 50 * time.Millisecond
	dir := t.TempDir()
	writeFile(t, dir, "x.go", "package x\nfunc X() {}\n")
	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("prime: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	writeFile(t, dir, "x.go", "package x\nfunc X() {}\nfunc X2() {}\n")

	var startCount atomic.Int32
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	idx.SetEventHook(func(eventType string, _ map[string]any) {
		switch eventType {
		case "index_started":
			startCount.Add(1)
		case "index_complete":
			cancel()
		}
	})

	done := make(chan struct{})
	go func() {
		idx.Watch(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Watch did not return after single-project reindex")
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !idx.anyActive() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if startCount.Load() < 1 {
		t.Errorf("Watch never re-indexed the single changed project (dir=%s)", filepath.Base(dir))
	}
}
