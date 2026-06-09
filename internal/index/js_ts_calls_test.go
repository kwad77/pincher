// SPDX-License-Identifier: MIT

package index

import (
	"context"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// Web-code graph hardening: JS/TS bare function calls across files should not
// leave the persisted graph at zero CALLS. The extractor emits a regex-tier
// CALLS candidate for run() -> loadUser(); the resolver must bind that
// candidate to the unique project-local function even though it lives in a
// sibling module. This is the pragmatic heuristic fallback that makes trace /
// dead_code useful for typical web repositories before a full import-aware JS
// resolver exists.
func TestIndex_TypeScriptBareCallsResolveAcrossFiles(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()

	writeFile(t, dir, "src/api.ts", `export function loadUser() {
	return { id: 1 }
}
`)
	writeFile(t, dir, "src/app.ts", `import { loadUser } from './api'

export function run() {
	return loadUser()
}
`)

	if _, err := idx.Index(context.Background(), dir, true); err != nil {
		t.Fatalf("Index: %v", err)
	}
	projectID := db.ProjectIDFromPath(dir)

	runSyms, err := store.GetSymbolsByName(projectID, "run", 10)
	if err != nil || len(runSyms) == 0 {
		t.Fatalf("expected run symbol, got %d (err=%v)", len(runSyms), err)
	}
	loadSyms, err := store.GetSymbolsByName(projectID, "loadUser", 10)
	if err != nil || len(loadSyms) == 0 {
		t.Fatalf("expected loadUser symbol, got %d (err=%v)", len(loadSyms), err)
	}

	edges, err := store.EdgesFrom(runSyms[0].ID, []string{"CALLS"})
	if err != nil {
		t.Fatalf("EdgesFrom: %v", err)
	}
	for _, e := range edges {
		if e.ToID == loadSyms[0].ID {
			return
		}
	}
	t.Fatalf("expected CALLS edge run→loadUser across TS files; run=%q loadUser=%q edges=%+v", runSyms[0].ID, loadSyms[0].ID, edges)
}

func TestIndex_JavaScriptBareCallsResolveAcrossFiles(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()

	writeFile(t, dir, "lib/helper.js", `export function formatName(name) {
	return name.trim()
}
`)
	writeFile(t, dir, "app.js", `import { formatName } from './lib/helper.js'

export function render(name) {
	return formatName(name)
}
`)

	if _, err := idx.Index(context.Background(), dir, true); err != nil {
		t.Fatalf("Index: %v", err)
	}
	projectID := db.ProjectIDFromPath(dir)

	renderSyms, err := store.GetSymbolsByName(projectID, "render", 10)
	if err != nil || len(renderSyms) == 0 {
		t.Fatalf("expected render symbol, got %d (err=%v)", len(renderSyms), err)
	}
	formatSyms, err := store.GetSymbolsByName(projectID, "formatName", 10)
	if err != nil || len(formatSyms) == 0 {
		t.Fatalf("expected formatName symbol, got %d (err=%v)", len(formatSyms), err)
	}

	edges, err := store.EdgesFrom(renderSyms[0].ID, []string{"CALLS"})
	if err != nil {
		t.Fatalf("EdgesFrom: %v", err)
	}
	for _, e := range edges {
		if e.ToID == formatSyms[0].ID {
			return
		}
	}
	t.Fatalf("expected CALLS edge render→formatName across JS files; render=%q formatName=%q edges=%+v", renderSyms[0].ID, formatSyms[0].ID, edges)
}
