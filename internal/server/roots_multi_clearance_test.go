// SPDX-License-Identifier: MIT

package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// #1081 Phase 1 — multi-root iteration + IsBloatTrap clearance.
//
// pickSessionRoot is the broken-out filter detectRoot uses to choose
// which client-advertised root becomes the session root. Audit shape:
// positive (cleared root wins), negative (no cleared root → no selection),
// control (single root behavior unchanged), cross-check (bloat-trap roots
// skipped, parse failures skipped, ordering preserved).

// uri builds an MCP-style file:// URI. On Windows the canonical form
// is file:///C:/path/with/forward/slashes — three slashes after the
// scheme, drive letter at the third character, forward slashes
// throughout (parseFileURI strips the leading slash and converts back).
// On Linux/macOS the path is already rooted at /, so file:// + path
// works directly.
func uri(path string) string {
	// Normalize backslashes for Windows test temp dirs.
	p := filepath.ToSlash(path)
	if len(p) > 0 && p[0] != '/' {
		// Windows: drive-letter path — prepend the third slash.
		return "file:///" + p
	}
	return "file://" + p
}

func roots(uris ...string) []*mcp.Root {
	out := make([]*mcp.Root, len(uris))
	for i, u := range uris {
		out[i] = &mcp.Root{URI: u}
	}
	return out
}

func TestPickSessionRoot_FirstClearedWins(t *testing.T) {
	// Positive shape. Two roots advertised; the second has a project
	// marker, the first is bloat-trap-y (/). The cleared root wins
	// even though it's not at index 0.
	tmp := t.TempDir()
	// Create a project marker so IsBloatTrap(hookMode=true) clears the path.
	writeFile(t, tmp+"/.git", "")

	picked, ok := pickSessionRoot(roots(uri("/"), uri(tmp)))
	if !ok {
		t.Fatal("pickSessionRoot returned no selection; expected the tmp root")
	}
	if picked != tmp {
		t.Errorf("picked %q; expected %q", picked, tmp)
	}
}

func TestPickSessionRoot_AllBloatTraps_NoSelection(t *testing.T) {
	// Negative shape. Every root fails IsBloatTrap (no project marker
	// in the tmpdir, no markers in /). Caller falls back to CWD.
	tmpNoMarker := t.TempDir() // no .git / go.mod / etc.
	_, ok := pickSessionRoot(roots(uri("/"), uri(tmpNoMarker)))
	if ok {
		t.Error("pickSessionRoot returned a selection when all roots were bloat-traps")
	}
}

func TestPickSessionRoot_SingleRootUnchanged(t *testing.T) {
	// Control shape. The pre-#1081 behavior was "use Roots[0]"; with
	// a single cleared root the new logic should behave identically.
	tmp := t.TempDir()
	writeFile(t, tmp+"/go.mod", "module test")

	picked, ok := pickSessionRoot(roots(uri(tmp)))
	if !ok || picked != tmp {
		t.Errorf("single-root behavior changed: picked=%q ok=%v; expected %q true", picked, ok, tmp)
	}
}

func TestPickSessionRoot_NonFileSchemeSkipped(t *testing.T) {
	// Cross-check shape. Roots advertised as non-file:// URIs (e.g.
	// http://, vscode-remote://) are not selectable by pincher — we
	// need a local filesystem path. Skip and continue.
	tmp := t.TempDir()
	writeFile(t, tmp+"/.git", "")

	picked, ok := pickSessionRoot(roots("http://example.com", uri(tmp)))
	if !ok {
		t.Fatal("non-file:// URI should be skipped, file:// URI selected")
	}
	if picked != tmp {
		t.Errorf("picked %q; expected %q (non-file URI should not have been chosen)", picked, tmp)
	}
}

func TestPickSessionRoot_PreservesAdvertisedOrder(t *testing.T) {
	// Cross-check shape. When MULTIPLE roots clear the bloat-trap, the
	// first one in advertised order wins — not last-cleared, not
	// lexicographically-first, just first-advertised-and-cleared.
	// Pins the contract that hosts can express priority via ordering.
	first := t.TempDir()
	second := t.TempDir()
	writeFile(t, first+"/.git", "")
	writeFile(t, second+"/go.mod", "module test")

	picked, ok := pickSessionRoot(roots(uri(first), uri(second)))
	if !ok {
		t.Fatal("pickSessionRoot returned no selection; expected the first cleared root")
	}
	if picked != first {
		t.Errorf("picked %q; expected %q (first in advertised order)", picked, first)
	}
}

// writeFile is a helper that creates a file at the path with the given
// content. Used to drop in project markers (.git / go.mod) so
// IsBloatTrap clears the containing directory in hook mode.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
