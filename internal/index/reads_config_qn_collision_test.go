// SPDX-License-Identifier: MIT

package index

import (
	"context"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// #731: a bare Go identifier read (`version`) collides with a *qualified
// name* from a config file. JSON config files have a top-level `version`
// key whose QN is literally "version", while the Go package var's QN is
// `app.version`. `lookupQN("version")` matched the cross-language JSON
// Setting, which suppressed the same-language name fallback that would
// have found the Go Variable — and then the #436 language-mismatch guard
// dropped the edge entirely. Net effect: the READS edge to the Go
// Variable went missing whenever a same-named config key existed
// (`version`, `name`, `description` — universal in package.json).
func TestIndex_ReadsEdge_BareNameCollidesWithConfigQN(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "app/app.go",
		"package app\n\nvar version = \"dev\"\n\nfunc Print() {\n\t_ = version\n}\n")
	// package.json's top-level `version` key extracts as a Setting whose
	// QN is exactly "version" — the cross-language collision source.
	writeFile(t, dir, "package.json",
		"{\n  \"name\": \"demo\",\n  \"version\": \"1.0.0\"\n}\n")

	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("Index: %v", err)
	}

	printID := db.MakeSymbolID("app/app.go", "app.Print", "Function")
	versionVarID := db.MakeSymbolID("app/app.go", "app.version", "Variable")

	inbound, err := store.EdgesTo(versionVarID, nil)
	if err != nil {
		t.Fatalf("EdgesTo app.version: %v", err)
	}
	if !hasEdge(inbound, printID, "READS") {
		t.Errorf("expected READS edge Print -> app.version; the bare `version` read "+
			"collided with package.json's `version` key QN and was dropped:\n  inbound: %v", inbound)
	}
}
