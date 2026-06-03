// SPDX-License-Identifier: MIT

package index

import (
	"context"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// TestIndex_PackageMainCrossFileCALLS pins #487: cross-file
// intra-package CALLS for package=main (the cmd/ subcommand dispatch
// shape — main.go calls runDoctorCLI() defined in doctor.go). The
// previously-existing TestIndex_PendingEdges_PreservesCrossFileCALLS
// uses package=mypkg and passes; this test re-runs the same shape with
// package=main to confirm whether the package name itself is what
// breaks resolution.
func TestIndex_PackageMainCrossFileCALLS(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()

	// main.go: the entry point, calls a sibling function via bare name.
	writeFile(t, dir, "main.go", `package main

func main() {
	runDoctorCLI()
}
`)
	// doctor.go: defines runDoctorCLI in the same package.
	writeFile(t, dir, "doctor.go", `package main

func runDoctorCLI() {}
`)

	if _, err := idx.Index(context.Background(), dir, true); err != nil {
		t.Fatalf("Index: %v", err)
	}

	mainID := db.MakeSymbolID("main.go", "main.main", "Function")
	doctorID := db.MakeSymbolID("doctor.go", "main.runDoctorCLI", "Function")

	edges, _ := store.EdgesFrom(mainID, []string{"CALLS"})
	if len(edges) == 0 {
		t.Fatalf("main → runDoctorCLI CALLS edge missing entirely; got 0 outbound CALLS from %s", mainID)
	}
	found := false
	for _, e := range edges {
		if e.ToID == doctorID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("main → runDoctorCLI CALLS edge missing; %s has %d outbound CALLS but none reach %s:\n%v",
			mainID, len(edges), doctorID, edges)
	}
}

// TestIndex_MultiplePackageMainQNCollision repros the pincher-repo
// shape: multiple `package main` directories under cmd/* and
// internal/supervisor/cmd/*. Every one defines `main.main`, so the
// project has N symbols sharing QN `main.main`. The hypothesis behind
// #487 is that resolveCalls' lookupQN returns the lexicographically
// smallest one (pickCanonical), so a deferred edge from cmd/pinch's
// main → runDoctorCLI gets attributed to cmd/benchcmp's main (the
// lex-smaller path), or fails to write entirely.
func TestIndex_MultiplePackageMainQNCollision(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()

	// Three sibling `package main` directories. cmd/pinch is the one
	// that exercises cross-file CALLS (main.go → doctor.go's
	// runDoctorCLI). The other two have their own main.go to create
	// the QN collision.
	writeFile(t, dir, "cmd/pinch/main.go", `package main

func main() {
	runDoctorCLI()
}
`)
	writeFile(t, dir, "cmd/pinch/doctor.go", `package main

func runDoctorCLI() {}
`)
	writeFile(t, dir, "cmd/benchcmp/main.go", `package main

func main() {}
`)
	writeFile(t, dir, "cmd/probe/main.go", `package main

func main() {}
`)

	if _, err := idx.Index(context.Background(), dir, true); err != nil {
		t.Fatalf("Index: %v", err)
	}

	pinchMainID := db.MakeSymbolID("cmd/pinch/main.go", "main.main", "Function")
	doctorID := db.MakeSymbolID("cmd/pinch/doctor.go", "main.runDoctorCLI", "Function")

	// After #487 fix: the deferred edge must attribute to cmd/pinch's
	// main (the file that produced the candidate), not the
	// lex-smallest match (cmd/benchcmp/main.go::main.main) the
	// project-wide QN lookup would otherwise return.
	pinchEdges, _ := store.EdgesFrom(pinchMainID, []string{"CALLS"})
	if len(pinchEdges) == 0 {
		t.Fatalf("cmd/pinch/main.go::main.main has 0 outbound CALLS — #487 still reproduces (FromFile not honored)")
	}
	found := false
	for _, e := range pinchEdges {
		if e.ToID == doctorID {
			found = true
		}
	}
	if !found {
		t.Errorf("cmd/pinch's main → runDoctorCLI edge missing; edges:\n%v", pinchEdges)
	}

	// Inbound on runDoctorCLI must include exactly cmd/pinch's main
	// (no wrong-attribution from cmd/benchcmp/main).
	inbound, _ := store.EdgesTo(doctorID, []string{"CALLS"})
	if len(inbound) == 0 {
		t.Fatalf("runDoctorCLI has zero inbound CALLS — dead_code would still flag it dead")
	}
	for _, e := range inbound {
		if e.FromID != pinchMainID {
			t.Errorf("runDoctorCLI inbound edge from %s, want %s (cmd/pinch's main)", e.FromID, pinchMainID)
		}
	}
}
