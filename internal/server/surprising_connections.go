package server

import (
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
	return packageOfFilePath(id[:i])
}

func packageOfFilePath(file string) string {
	if file == "" || file == "external" {
		return ""
	}
	if strings.HasPrefix(file, "@external") {
		return ""
	}
	slash := strings.LastIndexByte(file, '/')
	backslash := strings.LastIndexByte(file, '\\')
	if backslash > slash {
		slash = backslash
	}
	if slash < 0 {
		return "."
	}
	dir := file[:slash]
	if dir == "" {
		return "."
	}
	if strings.IndexByte(dir, '\\') >= 0 {
		dir = strings.ReplaceAll(dir, "\\", "/")
	}
	return dir
}

type surprisingConnectionsAccumulator struct {
	counts  map[surprisingPackagePair]int
	example map[surprisingPackagePair]surprisingConnectionExample
}

type surprisingPackagePair struct{ from, to string }
type surprisingConnectionExample struct {
	fromID string
	toID   string
}

func newSurprisingConnectionsAccumulator() *surprisingConnectionsAccumulator {
	return &surprisingConnectionsAccumulator{
		counts:  make(map[surprisingPackagePair]int),
		example: make(map[surprisingPackagePair]surprisingConnectionExample),
	}
}

func (a *surprisingConnectionsAccumulator) add(fromID, toID string) {
	fromPkg := packageOfSymbolID(fromID)
	toPkg := packageOfSymbolID(toID)
	if fromPkg == "" || toPkg == "" || fromPkg == toPkg {
		return
	}
	p := surprisingPackagePair{fromPkg, toPkg}
	a.counts[p]++
	if _, seen := a.example[p]; !seen {
		a.example[p] = surprisingConnectionExample{fromID: fromID, toID: toID}
	}
}

func (a *surprisingConnectionsAccumulator) result() []surprisingConnection {
	if a == nil || len(a.counts) == 0 {
		return nil
	}
	out := make([]surprisingConnection, 0, len(a.counts))
	for p, n := range a.counts {
		ex := a.example[p]
		out = append(out, surprisingConnection{
			FromPackage: p.from, ToPackage: p.to, EdgeCount: n,
			ExampleFrom: ex.fromID, ExampleTo: ex.toID,
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

// computeSurprisingConnections ranks directed cross-package CALLS edges
// by rarity. The rarest `surprisingConnectionsCap` package pairs are
// returned, lowest edge count first. Edges within a package, edges
// touching an external/malformed endpoint, and non-CALLS edges are
// excluded.
func computeSurprisingConnections(edges []db.Edge) []surprisingConnection {
	acc := newSurprisingConnectionsAccumulator()
	for _, e := range edges {
		if e.Kind != "CALLS" {
			continue
		}
		acc.add(e.FromID, e.ToID)
	}
	return acc.result()
}
