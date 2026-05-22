package server

import (
	"path"
	"sort"
	"strings"

	"github.com/kwad77/pincher/internal/db"
)

// surprising_connections.go — the `architecture` tool surfaces, beside
// hotspots and entry points, the *rare* cross-package CALLS edges:
// package pairs joined by just one or two calls. A pair with one
// crossing is a fragile or hidden coupling point worth a reviewer's
// eye; a pair with 200 crossings is expected partnership. Ranking by
// rarity makes the architectural smell legible.
//
// Deterministic: computed purely from pincher's AST-derived CALLS
// edges and the file path embedded in each symbol id. No inference.

// surprisingConnectionsCap bounds how many rare pairs `architecture`
// surfaces.
const surprisingConnectionsCap = 10

// surprisingConnection is one rarely-traversed directed package pair.
type surprisingConnection struct {
	FromPackage string `json:"from_package"`
	ToPackage   string `json:"to_package"`
	EdgeCount   int    `json:"edge_count"`
	ExampleFrom string `json:"example_from"`
	ExampleTo   string `json:"example_to"`
}

// packageOfSymbolID returns the package of a symbol id — the directory
// of the file path embedded before the `::`. Root-level files map to
// ".". Returns "" when the id has no file component (external or
// malformed), which the caller treats as "skip".
func packageOfSymbolID(id string) string {
	i := strings.Index(id, "::")
	if i <= 0 {
		return ""
	}
	file := strings.ReplaceAll(id[:i], "\\", "/")
	if file == "" || file == "external" {
		return ""
	}
	return path.Dir(file)
}

// computeSurprisingConnections ranks directed cross-package CALLS edges
// by rarity. The rarest `surprisingConnectionsCap` package pairs are
// returned, lowest edge count first. Edges within a package, edges
// touching an external/malformed endpoint, and non-CALLS edges are
// excluded.
func computeSurprisingConnections(edges []db.Edge) []surprisingConnection {
	type pair struct{ from, to string }
	counts := make(map[pair]int)
	example := make(map[pair]db.Edge)

	for _, e := range edges {
		if e.Kind != "CALLS" {
			continue
		}
		fromPkg := packageOfSymbolID(e.FromID)
		toPkg := packageOfSymbolID(e.ToID)
		if fromPkg == "" || toPkg == "" || fromPkg == toPkg {
			continue
		}
		p := pair{fromPkg, toPkg}
		counts[p]++
		if _, seen := example[p]; !seen {
			example[p] = e
		}
	}

	out := make([]surprisingConnection, 0, len(counts))
	for p, n := range counts {
		out = append(out, surprisingConnection{
			FromPackage: p.from, ToPackage: p.to, EdgeCount: n,
			ExampleFrom: example[p].FromID, ExampleTo: example[p].ToID,
		})
	}
	// Rarest first; ties broken deterministically by package names.
	sort.Slice(out, func(i, j int) bool {
		if out[i].EdgeCount != out[j].EdgeCount {
			return out[i].EdgeCount < out[j].EdgeCount
		}
		if out[i].FromPackage != out[j].FromPackage {
			return out[i].FromPackage < out[j].FromPackage
		}
		return out[i].ToPackage < out[j].ToPackage
	})
	if len(out) > surprisingConnectionsCap {
		out = out[:surprisingConnectionsCap]
	}
	return out
}
