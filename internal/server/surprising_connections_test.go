// SPDX-License-Identifier: MIT

package server

import (
	"strings"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

func TestPackageOfSymbolID(t *testing.T) {
	cases := []struct{ in, want string }{
		{"internal/db/db.go::db.Open#Function", "internal/db"},
		{"main.go::main.main#Function", "."},
		{"internal\\server\\server.go::server.New#Function", "internal/server"},
		{"external::fmt.Println#Function", ""},
		{"@external/pathlib.Path::pathlib.Path#Module", ""},
		{"no-separator", ""},
	}
	for _, c := range cases {
		if got := packageOfSymbolID(c.in); got != c.want {
			t.Errorf("packageOfSymbolID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestComputeSurprisingConnections(t *testing.T) {
	// pkgA→pkgB joined by 3 calls (expected), pkgA→pkgC by 1 (surprising),
	// pkgD→pkgB by 1 (surprising). Plus noise that must be excluded.
	edges := []db.Edge{
		// pkgA → pkgB ×3
		{FromID: "pkgA/a.go::a.F1#Function", ToID: "pkgB/b.go::b.G1#Function", Kind: "CALLS"},
		{FromID: "pkgA/a.go::a.F2#Function", ToID: "pkgB/b.go::b.G2#Function", Kind: "CALLS"},
		{FromID: "pkgA/a.go::a.F3#Function", ToID: "pkgB/b.go::b.G3#Function", Kind: "CALLS"},
		// pkgA → pkgC ×1  (surprising)
		{FromID: "pkgA/a.go::a.F4#Function", ToID: "pkgC/c.go::c.H1#Function", Kind: "CALLS"},
		// pkgD → pkgB ×1  (surprising)
		{FromID: "pkgD/d.go::d.K1#Function", ToID: "pkgB/b.go::b.G4#Function", Kind: "CALLS"},
		// noise: same-package edge — excluded
		{FromID: "pkgA/a.go::a.F5#Function", ToID: "pkgA/a.go::a.F6#Function", Kind: "CALLS"},
		// noise: non-CALLS cross-package — excluded
		{FromID: "pkgA/a.go::a.F7#Function", ToID: "pkgB/b.go::b.G5#Function", Kind: "IMPORTS"},
		// noise: external endpoint — excluded
		{FromID: "pkgA/a.go::a.F8#Function", ToID: "external::fmt.Println#Function", Kind: "CALLS"},
	}

	got := computeSurprisingConnections(edges, false)
	if len(got) != 3 {
		t.Fatalf("expected 3 cross-package pairs; got %d: %+v", len(got), got)
	}
	// Rarest first: the two count-1 pairs precede the count-3 pair.
	if got[0].EdgeCount != 1 || got[1].EdgeCount != 1 {
		t.Errorf("rarest pairs must rank first; got counts %d, %d, %d",
			got[0].EdgeCount, got[1].EdgeCount, got[2].EdgeCount)
	}
	if got[2].EdgeCount != 3 || got[2].FromPackage != "pkgA" || got[2].ToPackage != "pkgB" {
		t.Errorf("last pair should be pkgA→pkgB ×3; got %+v", got[2])
	}
	// Example endpoints are populated.
	if got[0].ExampleFrom == "" || got[0].ExampleTo == "" {
		t.Errorf("example endpoints not populated: %+v", got[0])
	}
}

func TestComputeSurprisingConnections_CapsOutput(t *testing.T) {
	var edges []db.Edge
	// 25 distinct single-edge package pairs — more than the cap.
	for i := 0; i < 25; i++ {
		p := string(rune('a' + i))
		edges = append(edges, db.Edge{
			FromID: "src" + p + "/x.go::x.A#Function",
			ToID:   "dst" + p + "/y.go::y.B#Function",
			Kind:   "CALLS",
		})
	}
	got := computeSurprisingConnections(edges, false)
	if len(got) > surprisingConnectionsCap {
		t.Errorf("output not capped: got %d, cap %d", len(got), surprisingConnectionsCap)
	}
}

func TestComputeSurprisingConnections_Empty(t *testing.T) {
	if got := computeSurprisingConnections(nil, false); len(got) != 0 {
		t.Errorf("nil edges should yield no connections; got %+v", got)
	}
}

func TestComputeSurprisingConnections_FiltersTestsAndFixtures(t *testing.T) {
	edges := []db.Edge{
		{
			FromID: "internal/app/app.go::app.Run#Function",
			ToID:   "internal/db/db.go::db.Open#Function",
			Kind:   "CALLS",
		},
		{
			FromID: "internal/app/app_test.go::app.TestRun#Function",
			ToID:   "internal/testutil/testutil.go::testutil.Open#Function",
			Kind:   "CALLS",
		},
		{
			FromID: "internal/app/app.go::app.FromProdToTest#Function",
			ToID:   "internal/testutil/testutil_test.go::testutil.Helper#Function",
			Kind:   "CALLS",
		},
		{
			FromID: "testdata/corpus/app.go::fixture.Run#Function",
			ToID:   "internal/db/db.go::db.OpenFixture#Function",
			Kind:   "CALLS",
		},
	}

	got := computeSurprisingConnections(edges, false)
	if len(got) != 1 {
		t.Fatalf("default should keep only production non-fixture pair; got %+v", got)
	}
	if got[0].FromPackage != "internal/app" || got[0].ToPackage != "internal/db" {
		t.Fatalf("default pair = %+v, want internal/app -> internal/db", got[0])
	}

	got = computeSurprisingConnections(edges, true)
	for _, c := range got {
		if strings.Contains(c.ExampleFrom, "testdata/") || strings.Contains(c.ExampleTo, "testdata/") {
			t.Fatalf("fixture edge leaked even with includeTests=true: %+v", c)
		}
	}
	if len(got) != 2 {
		t.Fatalf("includeTests=true should keep production and test package pairs but not fixture; got %+v", got)
	}
}
