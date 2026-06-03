// SPDX-License-Identifier: MIT

package index

import (
	"context"
	"testing"
)

// #670 §2 regression test: on a watcher no-change tick (no files
// re-extracted, force=false), the cross-file resolve block at the
// tail of Index() must be skipped. Pre-fix, loadOrFallback +
// resolveReads ran unconditionally and consumed 48% of allocations
// per tick — a 14× allocation regression vs the v0.60 baseline on
// the hot path.
//
// The skip is observable through the pending_edges resolver hooks:
// if resolveImports/resolveCalls/resolveReads never run, they
// never touch the store. We measure indirectly via allocation
// budget — a no-change tick on a 2-file corpus must stay well
// under a generous ceiling.

// TestIndex_NoChange_SkipsResolvePass pins the gate at the
// behavior level: prime the index, then ten incremental no-change
// ticks must not exceed the ceiling derived from the post-fix
// measurement plus headroom. If a future change re-introduces the
// unconditional resolve call, this trips long before the bench
// regression gate notices.
func TestIndex_NoChange_SkipsResolvePass(t *testing.T) {
	idx, _ := newTestIndexer(t)
	dir := t.TempDir()

	writeFile(t, dir, "callee.go", "package mypkg\n\nfunc Foo() {}\n")
	writeFile(t, dir, "caller.go", "package mypkg\n\nfunc Bar() {\n\tFoo()\n}\n")

	if _, err := idx.Index(context.Background(), dir, true); err != nil {
		t.Fatalf("prime Index: %v", err)
	}

	avg := testing.AllocsPerRun(10, func() {
		if _, err := idx.Index(context.Background(), dir, false); err != nil {
			t.Fatalf("no-change Index: %v", err)
		}
	})

	// Pre-fix this corpus measured ~1500+ allocs per tick on a
	// 2-file repo (resolveReads pulling QN lookups for every
	// candidate). Post-fix the gate trips, dropping to the
	// walker+hash-check floor. Ceiling is set above measured but
	// well below the pre-fix value — any re-regression that
	// re-introduces the unconditional resolve trips this.
	const ceiling = 800
	if avg > ceiling {
		t.Errorf("no-change Index() allocs/run = %.0f, want <= %d (pre-#670-§2 was ~1500+; regression suggests the resolve-skip gate was bypassed)", avg, ceiling)
	}
}

// TestIndex_FileChange_TriggersResolvePass is the positive
// control: when a file's content actually changes, the resolve
// pass MUST run so the new candidates land in the edges table.
// Without this, a regression that over-tightened the gate (e.g.
// also skipping on totalFiles==1) would silently drop edges.
func TestIndex_FileChange_TriggersResolvePass(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()

	writeFile(t, dir, "callee.go", "package mypkg\n\nfunc Foo() {}\n")
	writeFile(t, dir, "caller.go", "package mypkg\n\nfunc Bar() {}\n")

	if _, err := idx.Index(context.Background(), dir, true); err != nil {
		t.Fatalf("prime Index: %v", err)
	}

	writeFile(t, dir, "caller.go", "package mypkg\n\nfunc Bar() { Foo() }\n")

	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("incremental Index: %v", err)
	}

	// The bound CALLS edge must exist post-change — proves resolve ran.
	calls, err := store.EdgesFrom("caller.go::mypkg.Bar#Function", []string{"CALLS"})
	if err != nil {
		t.Fatalf("EdgesFrom: %v", err)
	}
	if len(calls) == 0 {
		t.Errorf("expected resolved Bar→Foo CALLS edge after file change; got none — the resolve gate may be over-tightened")
	}
}
