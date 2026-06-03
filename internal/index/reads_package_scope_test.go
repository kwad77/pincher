// SPDX-License-Identifier: MIT

package index

import (
	"context"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// #764: resolveReads' bare-name fallback bound a bare identifier to any
// same-named project symbol, ignoring Go package scope — so every bare
// `version` / `token` read funnelled onto a single project-wide
// Variable. A bare unqualified Go read is *always* same-package; a
// cross-package read is written `pkg.Name` (a selector). The fix has two
// parts: extractGoReads now emits a *qualified* ToName for imported-
// package selectors (so `pkga.Token` resolves via lookupQN), and
// resolveReads scopes the bare-name fallback to the reader's package
// (= source-file directory).
func TestIndex_ReadsEdge_PackageScoped(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()

	// pkga: owns `token` (unexported) and `Token` (exported).
	writeFile(t, dir, "pkga/decl.go",
		"package pkga\n\nvar token = \"secret\"\n\nvar Token = \"public\"\n")
	// pkga: a same-package bare read of `token` — must bind.
	writeFile(t, dir, "pkga/use.go",
		"package pkga\n\nfunc Use() {\n\t_ = token\n}\n")
	// pkgb: a bare `token` that is a *local* — must NOT bind to pkga.token.
	writeFile(t, dir, "pkgb/other.go",
		"package pkgb\n\nfunc Other() {\n\ttoken := 1\n\t_ = token\n}\n")
	// pkgc: a genuine cross-package selector read of pkga.Token — must
	// bind, via the qualified-emit path.
	writeFile(t, dir, "pkgc/reader.go",
		"package pkgc\n\nimport \"example.com/m/pkga\"\n\nfunc Reader() {\n\t_ = pkga.Token\n}\n")

	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("Index: %v", err)
	}

	tokenID := db.MakeSymbolID("pkga/decl.go", "pkga.token", "Variable")
	TokenID := db.MakeSymbolID("pkga/decl.go", "pkga.Token", "Variable")
	useID := db.MakeSymbolID("pkga/use.go", "pkga.Use", "Function")
	otherID := db.MakeSymbolID("pkgb/other.go", "pkgb.Other", "Function")
	readerID := db.MakeSymbolID("pkgc/reader.go", "pkgc.Reader", "Function")

	// 1. Same-package bare read binds.
	inboundToken, err := store.EdgesTo(tokenID, nil)
	if err != nil {
		t.Fatalf("EdgesTo pkga.token: %v", err)
	}
	if !hasEdge(inboundToken, useID, "READS") {
		t.Errorf("expected READS edge Use -> pkga.token (same-package bare read):\n  inbound: %v", inboundToken)
	}
	// 2. Cross-package bare local must NOT bind to pkga.token.
	if hasEdge(inboundToken, otherID, "READS") {
		t.Errorf("pkgb.Other's local `token` false-bound to pkga.token — bare reads must be package-scoped:\n  inbound: %v", inboundToken)
	}

	// 3. Cross-package selector read of an exported var still resolves.
	inboundTokenExp, err := store.EdgesTo(TokenID, nil)
	if err != nil {
		t.Fatalf("EdgesTo pkga.Token: %v", err)
	}
	if !hasEdge(inboundTokenExp, readerID, "READS") {
		t.Errorf("expected READS edge Reader -> pkga.Token (cross-package selector read via qualified emit):\n  inbound: %v", inboundTokenExp)
	}
}
