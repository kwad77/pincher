// SPDX-License-Identifier: MIT

package db

import (
	"testing"
	"time"
)

// #1154: when two pincher processes of the same schema_version share a
// DB (multi-Claude-session dogfood, orphan watcher surviving a binary
// swap, `pincher index` CLI run interleaving with an MCP server), the
// schema-version monotonic guard alone wasn't enough — the older
// binary's watcher kept stamping its binary_version back over the
// newer one's writes because the schema_version equality let the CASE
// branch through. Read-then-compare in Go preserves the newer
// binary_version when an older writer races a newer one.
//
// Tests follow the table-from-the-start shape: positive (older can't
// downgrade), negative (newer overwrites older), control (same
// version idempotent), edge (dev / unparseable values).

// Positive: older binary_version cannot downgrade a newer one even
// when both share the same schema_version. The exact repro shape
// from the issue body: 0.58.0-44-g91e9c0f vs 0.58.0-10-gdeb797d.
func TestUpsertProjectMeta_OlderBinaryDoesNotDowngrade(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	pid := "proj-1154-downgrade"
	now := time.Now()

	// Newer binary stamps first.
	if err := s.UpsertProjectMeta(Project{
		ID:            pid,
		Path:          "/tmp/" + pid,
		Name:          pid,
		IndexedAt:     now,
		BinaryVersion: "0.58.0-44-g91e9c0f",
	}); err != nil {
		t.Fatalf("newer stamp: %v", err)
	}

	// Older orphan tries to overwrite — must be rejected.
	if err := s.UpsertProjectMeta(Project{
		ID:            pid,
		Path:          "/tmp/" + pid,
		Name:          pid,
		IndexedAt:     now.Add(10 * time.Second),
		BinaryVersion: "0.58.0-10-gdeb797d",
	}); err != nil {
		t.Fatalf("older stamp attempt: %v", err)
	}

	got, err := s.GetProject(pid)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if got.BinaryVersion != "0.58.0-44-g91e9c0f" {
		t.Errorf("older binary downgraded the row: got %q, want %q (the newer value should be preserved)",
			got.BinaryVersion, "0.58.0-44-g91e9c0f")
	}
}

// Negative: newer binary_version correctly overwrites older. This is
// the normal forward-progress path and must not be blocked by the
// #1154 guard.
func TestUpsertProjectMeta_NewerBinaryUpgrades(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	pid := "proj-1154-upgrade"
	now := time.Now()

	if err := s.UpsertProjectMeta(Project{
		ID: pid, Path: "/tmp/" + pid, Name: pid,
		IndexedAt: now, BinaryVersion: "0.58.0-10-gdeb797d",
	}); err != nil {
		t.Fatalf("older stamp: %v", err)
	}
	if err := s.UpsertProjectMeta(Project{
		ID: pid, Path: "/tmp/" + pid, Name: pid,
		IndexedAt: now.Add(10 * time.Second), BinaryVersion: "0.58.0-44-g91e9c0f",
	}); err != nil {
		t.Fatalf("newer stamp: %v", err)
	}

	got, err := s.GetProject(pid)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if got.BinaryVersion != "0.58.0-44-g91e9c0f" {
		t.Errorf("newer binary should have upgraded the row: got %q, want %q",
			got.BinaryVersion, "0.58.0-44-g91e9c0f")
	}
}

// Control: identical binary versions are idempotent — re-stamping
// with the same value succeeds (this is the most common case in
// steady-state operation).
func TestUpsertProjectMeta_SameBinaryIdempotent(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	pid := "proj-1154-idempotent"
	bv := "0.58.0-44-g91e9c0f"
	now := time.Now()
	for i := 0; i < 3; i++ {
		if err := s.UpsertProjectMeta(Project{
			ID: pid, Path: "/tmp/" + pid, Name: pid,
			IndexedAt: now.Add(time.Duration(i) * time.Second), BinaryVersion: bv,
		}); err != nil {
			t.Fatalf("stamp %d: %v", i, err)
		}
	}
	got, err := s.GetProject(pid)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if got.BinaryVersion != bv {
		t.Errorf("idempotent stamps mutated the row: got %q, want %q", got.BinaryVersion, bv)
	}
}

// Edge: "dev" (the unstamped go-build sentinel) must never displace a
// real release stamp. Bare `go build` produces version="dev"; if an
// MCP child built that way stamps over a `make build` child's
// real version, every health response permanently misreports drift.
func TestUpsertProjectMeta_DevSentinelNeverDisplacesReleaseStamp(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	pid := "proj-1154-dev"
	now := time.Now()
	if err := s.UpsertProjectMeta(Project{
		ID: pid, Path: "/tmp/" + pid, Name: pid,
		IndexedAt: now, BinaryVersion: "0.58.0",
	}); err != nil {
		t.Fatalf("release stamp: %v", err)
	}
	if err := s.UpsertProjectMeta(Project{
		ID: pid, Path: "/tmp/" + pid, Name: pid,
		IndexedAt: now.Add(1 * time.Second), BinaryVersion: "dev",
	}); err != nil {
		t.Fatalf("dev stamp attempt: %v", err)
	}
	got, err := s.GetProject(pid)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if got.BinaryVersion != "0.58.0" {
		t.Errorf("dev sentinel displaced release stamp: got %q, want %q", got.BinaryVersion, "0.58.0")
	}
}

// Edge: still updates path/name/indexed_at even when the binary
// downgrade is rejected. The non-version metadata MUST flow through —
// only the version-specific field is clamped. Pre-fix the orphan
// stamping path/name was useful (catches a project rename); breaking
// that to clamp the version would over-correct.
func TestUpsertProjectMeta_DowngradeStillUpdatesOtherFields(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	pid := "proj-1154-otherfields"
	now := time.Now()
	if err := s.UpsertProjectMeta(Project{
		ID: pid, Path: "/old/path", Name: "old-name",
		IndexedAt: now, BinaryVersion: "0.58.0-44-g91e9c0f",
	}); err != nil {
		t.Fatalf("newer stamp: %v", err)
	}
	freshTs := now.Add(60 * time.Second)
	if err := s.UpsertProjectMeta(Project{
		ID: pid, Path: "/new/path", Name: "new-name",
		IndexedAt: freshTs, BinaryVersion: "0.58.0-10-gdeb797d",
	}); err != nil {
		t.Fatalf("older stamp: %v", err)
	}
	got, err := s.GetProject(pid)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if got.BinaryVersion != "0.58.0-44-g91e9c0f" {
		t.Errorf("binary_version downgraded: got %q", got.BinaryVersion)
	}
	if got.Path != "/new/path" {
		t.Errorf("path didn't update when binary downgrade was rejected: got %q", got.Path)
	}
	if got.Name != "new-name" {
		t.Errorf("name didn't update when binary downgrade was rejected: got %q", got.Name)
	}
	if got.IndexedAt.Unix() != freshTs.Unix() {
		t.Errorf("indexed_at didn't update: got %v, want %v", got.IndexedAt, freshTs)
	}
}

// Cross-check: CompareBinaryVersion table-driven probe. Asserts the
// comparator handles every shape the storage layer might see. Avoids
// brittle exact-byte assertions by testing the relative order.
func TestCompareBinaryVersion_TableDriven(t *testing.T) {
	type cmp struct {
		a, b string
		want int // -1, 0, +1
	}
	cases := []cmp{
		// Same value.
		{"0.58.0", "0.58.0", 0},
		{"0.58.0-44-g91e9c0f", "0.58.0-44-g91e9c0f", 0},

		// MAJOR.MINOR.PATCH ordering.
		{"0.57.0", "0.58.0", -1},
		{"0.58.0", "0.57.0", 1},
		{"0.58.0", "0.58.1", -1},
		{"0.58.1", "0.58.0", 1},
		{"1.0.0", "0.99.0", 1},

		// Commit-count tiebreak on same release tag.
		{"0.58.0-10-gdeb797d", "0.58.0-44-g91e9c0f", -1},
		{"0.58.0-44-g91e9c0f", "0.58.0-10-gdeb797d", 1},

		// Clean tag vs dev build of same tag — clean tag is treated as
		// commit-count 0 (the smaller), dev build with N>0 is greater.
		{"0.58.0", "0.58.0-1-gabcdef", -1},
		{"0.58.0-1-gabcdef", "0.58.0", 1},

		// Leading-v stripping.
		{"v0.58.0", "0.58.0", 0},
		{"v0.57.0", "v0.58.0", -1},

		// dev sentinel is always smallest non-equal.
		{"dev", "0.58.0", -1},
		{"0.58.0", "dev", 1},
		{"dev", "dev", 0},
	}
	for _, c := range cases {
		got := CompareBinaryVersion(c.a, c.b)
		// Normalize to sign for comparison — comparator may return any
		// negative / positive integer; spec is -1 / 0 / 1.
		sign := func(n int) int {
			switch {
			case n < 0:
				return -1
			case n > 0:
				return 1
			}
			return 0
		}
		if sign(got) != c.want {
			t.Errorf("CompareBinaryVersion(%q, %q) = %d, want %d",
				c.a, c.b, got, c.want)
		}
	}
}

// Cross-check: unparseable input falls back to string compare and
// doesn't panic. A real-world weird value (manual hex stamp, garbage
// from a corrupt row, etc.) should produce a deterministic answer
// rather than crashing the indexer's project-meta write.
func TestCompareBinaryVersion_UnparseableFallsBackToString(t *testing.T) {
	// Should not panic on weird inputs. Result spec is: deterministic
	// (matches string compare's direction), never panics.
	type cmp struct{ a, b string }
	cases := []cmp{
		{"garbage", "more-garbage"},
		{"", ""},
		{"", "0.58.0"},
		{"0.58.0", ""},
		{"0.58.x-not-a-number", "0.58.0"},
	}
	for _, c := range cases {
		// Should return without panicking.
		_ = CompareBinaryVersion(c.a, c.b)
	}
}
